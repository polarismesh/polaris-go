/**
 * Tencent is pleased to support the open source community by making polaris-go available.
 *
 * Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
 *
 * Licensed under the BSD 3-Clause License (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * https://opensource.org/licenses/BSD-3-Clause
 *
 * Unless required by applicable law or agreed to in writing, software distributed
 * under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
 * CONDITIONS OF ANY KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations under the License.
 */

package quota

import (
	"context"
	"fmt"
	"github.com/polarismesh/specification/source/go/api/v1/traffic_manage/ratelimiter"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang/protobuf/jsonpb"
	"github.com/modern-go/reflect2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	rlimitV2 "github.com/polarismesh/specification/source/go/api/v1/traffic_manage/ratelimiter"

	limitpb "github.com/polarismesh/polaris-go/pkg/model/pb/metric/v2"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
)

// ResponseCallBack 应答回调函数
type ResponseCallBack interface {
	// OnInitResponse 应答回调函数
	OnInitResponse(counter *ratelimiter.QuotaCounter, duration time.Duration, curTimeMilli int64)
	// OnReportResponse 应答回调函数
	OnReportResponse(counter *ratelimiter.QuotaLeft, duration time.Duration, curTimeMilli int64)
}

// RateLimitMsgSender 限流消息同步器
type RateLimitMsgSender interface {
	// HasInitialized 是否已经初始化
	HasInitialized(svcKey model.ServiceKey, labels string) bool
	// SendInitRequest 发送初始化请求
	SendInitRequest(request *ratelimiter.RateLimitInitRequest, callback ResponseCallBack)
	// SendReportRequest 发送上报请求
	SendReportRequest(request *limitpb.ClientRateLimitReportRequest) error
	// AdjustTime 同步时间
	AdjustTime() int64
}

// AsyncRateLimitConnector 异步限流连接器
type AsyncRateLimitConnector interface {
	// GetMessageSender 初始化限流控制信息
	GetMessageSender(svcKey model.ServiceKey, hashValue uint64) (RateLimitMsgSender, error)
	// Destroy 销毁
	Destroy()
	// StreamCount 流数量
	StreamCount() int
}

// 头信息带给server真实的IP地址
const headerKeyClientIP = "client-ip"

// DurationBaseCallBack 基于时间段的回调结构
type DurationBaseCallBack struct {
	record   *InitializeRecord
	callBack ResponseCallBack
	duration time.Duration
}

// InitializeRecord 初始化记录
type InitializeRecord struct {
	identifier        *CounterIdentifier
	counterSet        *StreamCounterSet
	counterKeys       map[time.Duration]uint32
	callback          ResponseCallBack
	initSendTimeMilli int64
	lastRecvTimeMilli int64
}

// Expired 记录超时
func (ir *InitializeRecord) Expired(nowMilli int64) bool {
	lastRecvMilli := atomic.LoadInt64(&ir.lastRecvTimeMilli)
	lastInitMilli := atomic.LoadInt64(&ir.initSendTimeMilli)
	idleTimeoutMilli := model.ToMilliSeconds(ir.counterSet.asyncConnector.connIdleTimeout)
	return (lastRecvMilli > 0 && nowMilli-lastRecvMilli > idleTimeoutMilli) ||
		(lastRecvMilli == 0 && lastInitMilli > 0 && nowMilli-lastInitMilli > idleTimeoutMilli)
}

// 30s同步一次时间
const (
	syncTimeInterval = 30 * time.Second
)

// StreamCounterSet 同一个节点的counter集合，用于回调
type StreamCounterSet struct {
	// 锁，保证下面2个map同步
	mutex *sync.RWMutex
	// 上一次时间同步间隔
	lastSyncTimeMilli int64
	// 目标节点信息
	HostIdentifier *HostIdentifier
	// 客户端ID
	clientKey uint32
	// 客户端连接
	conn *grpc.ClientConn
	// 限流客户端
	client ratelimiter.RateLimitGRPCV2Client
	// 消息流
	serviceStream ratelimiter.RateLimitGRPCV2_ServiceClient
	// 已发起初始化的窗口，初始化完毕后，value为大于0的值
	initialingWindows map[CounterIdentifier]*InitializeRecord
	// 回调函数
	counters map[uint32]*DurationBaseCallBack
	// 连接器
	asyncConnector *asyncRateLimitConnector
	// 上一次连接失败的时间点
	lastConnectFailTimeMilli int64
	// 创建时间
	createTimeMilli int64
	// 是否已经过期
	expired int32
	// 时间差
	timeDiff int64
}

// NewStreamCounterSet 新建流管理器
func NewStreamCounterSet(asyncConnector *asyncRateLimitConnector, identifier *HostIdentifier) *StreamCounterSet {
	streamCounterSet := &StreamCounterSet{
		mutex:           &sync.RWMutex{},
		asyncConnector:  asyncConnector,
		HostIdentifier:  identifier,
		createTimeMilli: model.CurrentMillisecond(),
	}
	return streamCounterSet
}

// CompareTo 比较两个元素
func (s *StreamCounterSet) CompareTo(value interface{}) int {
	record := value.(*StreamCounterSet)
	if *s.HostIdentifier == *record.HostIdentifier {
		return 0
	}
	return 1
}

// EnsureDeleted 删除前进行检查，返回true才删除，该检查是同步操作
func (s *StreamCounterSet) EnsureDeleted(value interface{}) bool {
	counterSet := value.(*StreamCounterSet)
	result := atomic.LoadInt32(&counterSet.expired) > 0
	return result
}

// HasInitialized 是否已经初始化
func (s *StreamCounterSet) HasInitialized(svcKey model.ServiceKey, labels string) bool {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	identifier := CounterIdentifier{
		service:   svcKey.Service,
		namespace: svcKey.Namespace,
		labels:    labels,
	}
	record, ok := s.initialingWindows[identifier]
	return ok && len(record.counterKeys) > 0
}

// createConnection 创建连接
func (s *StreamCounterSet) createConnection() (*grpc.ClientConn, error) {
	var opts []grpc.DialOption
	opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	opts = append(opts, grpc.WithBlock())
	ctx, cancel := context.WithTimeout(context.Background(), s.asyncConnector.connTimeout)
	defer cancel()
	conn, err := grpc.DialContext(
		ctx, fmt.Sprintf("%s:%d", s.HostIdentifier.host, s.HostIdentifier.port), opts...)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// preInitCheck 初始化操作的前置检查
func (s *StreamCounterSet) preInitCheck(
	counterIdentifier CounterIdentifier, callback ResponseCallBack) ratelimiter.RateLimitGRPCV2_ServiceClient {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if nil == s.conn {
		conn, err := s.createConnection()
		if err != nil {
			log.GetNetworkLogger().Errorf("[RateLimit]fail to connect to %s:%d, err is %v",
				s.HostIdentifier.host, s.HostIdentifier.port, err)
			return nil
		}
		s.conn = conn
	}
	if nil == s.client {
		s.client = ratelimiter.NewRateLimitGRPCV2Client(s.conn)
	}
	if nil == s.serviceStream {
		selfHost := s.asyncConnector.getIPString(s.HostIdentifier.host, s.HostIdentifier.port)
		ctx := createHeaderContext(map[string]string{headerKeyClientIP: selfHost})
		serviceStream, err := s.client.Service(ctx)
		if err != nil {
			log.GetNetworkLogger().Errorf("[RateLimit]fail to create serviceStream to %s:%d, err is %v",
				s.HostIdentifier.host, s.HostIdentifier.port, err)
			s.conn.Close()
			return nil
		}
		s.serviceStream = serviceStream
		go s.processResponse(s.serviceStream)
	}
	if nil == s.initialingWindows {
		s.initialingWindows = make(map[CounterIdentifier]*InitializeRecord)
		s.counters = make(map[uint32]*DurationBaseCallBack)
	}
	record, ok := s.initialingWindows[counterIdentifier]
	curTimeMilli := model.CurrentMillisecond()
	if ok && curTimeMilli-record.initSendTimeMilli < s.asyncConnector.msgTimeout.Milliseconds() {
		// 已经在初始化中
		return nil
	}
	record = &InitializeRecord{
		identifier:        &counterIdentifier,
		counterSet:        s,
		callback:          callback,
		initSendTimeMilli: curTimeMilli,
		counterKeys:       make(map[time.Duration]uint32),
	}
	s.initialingWindows[counterIdentifier] = record
	return s.serviceStream
}

func createHeaderContext(headers map[string]string) context.Context {
	md := metadata.New(headers)

	ctx := context.Background()
	return metadata.NewOutgoingContext(ctx, md)
}

// SendInitRequest 发送初始化请求
func (s *StreamCounterSet) SendInitRequest(initReq *ratelimiter.RateLimitInitRequest, callback ResponseCallBack) {
	counterIdentifier := CounterIdentifier{
		service:   initReq.GetTarget().GetService(),
		namespace: initReq.GetTarget().GetNamespace(),
		labels:    initReq.GetTarget().GetLabels(),
	}
	serviceStream := s.preInitCheck(counterIdentifier, callback)
	if reflect2.IsNil(serviceStream) {
		return
	}
	// 发起初始化
	request := &ratelimiter.RateLimitRequest{
		Cmd:                  ratelimiter.RateLimitCmd_INIT,
		RateLimitInitRequest: initReq,
	}
	if log.GetNetworkLogger().IsLevelEnabled(log.DebugLog) {
		initReqStr, _ := (&jsonpb.Marshaler{}).MarshalToString(initReq)
		log.GetNetworkLogger().Debugf("[RateLimit]Send init request: %s\n", initReqStr)
	}
	if err := serviceStream.Send(request); err != nil {
		log.GetNetworkLogger().Errorf("[RateLimit]fail to send init message to %s:%d, key is %s, err is %v",
			s.HostIdentifier.host, s.HostIdentifier.port, counterIdentifier, err)
	}
}

// checkAndCreateClient 检查并创建客户端
func (s *StreamCounterSet) checkAndCreateClient() (ratelimiter.RateLimitGRPCV2Client, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	curTimeMilli := model.CurrentMillisecond()
	timePassed := curTimeMilli - s.lastConnectFailTimeMilli
	if s.lastConnectFailTimeMilli > 0 &&
		timePassed > 0 && timePassed < model.ToMilliSeconds(s.asyncConnector.reconnectInterval) {
		// 未达到重连的时间间隔
		return nil, fmt.Errorf("reconnect interval should exceed %v", s.asyncConnector.reconnectInterval)
	}
	if nil == s.conn {
		log.GetNetworkLogger().Infof("[RateLimit]createConnection to %s", *s.HostIdentifier)
		s.lastConnectFailTimeMilli = curTimeMilli
		conn, err := s.createConnection()
		if err != nil {
			log.GetNetworkLogger().Errorf("[RateLimit]fail to connect to %s, err is %v",
				*s.HostIdentifier, err)
			return nil, err
		}
		s.lastConnectFailTimeMilli = 0
		s.conn = conn
	}
	if reflect2.IsNil(s.client) {
		s.client = ratelimiter.NewRateLimitGRPCV2Client(s.conn)
	}
	return s.client, nil
}

// Expired 检查是否已经超时
func (s *StreamCounterSet) Expired(nowMilli int64, clearRecords bool) bool {
	if clearRecords {
		s.eliminateExpiredRecords(nowMilli)
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if len(s.initialingWindows) > 0 {
		return false
	}
	idleMilli := model.ToMilliSeconds(s.asyncConnector.connIdleTimeout)
	if s.lastConnectFailTimeMilli > 0 && nowMilli-s.lastConnectFailTimeMilli >= idleMilli {
		return true
	}
	if nowMilli-s.createTimeMilli >= idleMilli {
		return true
	}
	return false
}

// eliminateExpiredRecords 清理超时的记录
func (s *StreamCounterSet) eliminateExpiredRecords(nowMilli int64) {
	s.mutex.RLock()
	records := make(map[CounterIdentifier]*InitializeRecord, len(s.initialingWindows))
	for k, v := range s.initialingWindows {
		records[k] = v
	}
	s.mutex.RUnlock()
	for identifier, record := range records {
		if !record.Expired(nowMilli) {
			continue
		}
		s.mutex.Lock()
		record := s.initialingWindows[identifier]
		if nil != record && record.Expired(nowMilli) {
			delete(s.initialingWindows, identifier)
		}
		s.mutex.Unlock()
	}
}

// AdjustTime 同步时间
func (s *StreamCounterSet) AdjustTime() int64 {
	client, err := s.checkAndCreateClient()
	if err != nil {
		return atomic.LoadInt64(&s.timeDiff)
	}
	lastSyncTimeMilli := atomic.LoadInt64(&s.lastSyncTimeMilli)
	sendTimeMilli := model.CurrentMillisecond()
	if lastSyncTimeMilli > 0 && sendTimeMilli-lastSyncTimeMilli < model.ToMilliSeconds(syncTimeInterval) {
		return atomic.LoadInt64(&s.timeDiff)
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.asyncConnector.msgTimeout)
	defer cancel()
	timeResp, err := client.TimeAdjust(ctx, &ratelimiter.TimeAdjustRequest{})
	atomic.StoreInt64(&s.lastSyncTimeMilli, model.CurrentMillisecond())
	if err != nil {
		log.GetNetworkLogger().Errorf("[RateLimit]fail to send timeAdjust message to %s:%d, key is %s, err is %v",
			s.HostIdentifier.host, s.HostIdentifier.port, err)
		return atomic.LoadInt64(&s.timeDiff)
	}
	serverTimeMill := timeResp.GetServerTimestamp()
	recvClientTimeMilli := model.CurrentMillisecond()
	latency := recvClientTimeMilli - sendTimeMilli
	timeDiff := serverTimeMill + latency/2 - recvClientTimeMilli
	atomic.StoreInt64(&s.timeDiff, timeDiff)
	log.GetNetworkLogger().Infof(
		"[RateLimit]adjust timediff to %s:%d is %v, server time is %d, latency is %d",
		s.HostIdentifier.host, s.HostIdentifier.port, timeDiff, serverTimeMill, latency)
	return timeDiff
}

// closeConnection 关闭连接
func (s *StreamCounterSet) closeConnection() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if nil != s.conn {
		s.conn.Close()
		s.conn = nil
	}
}

// cleanup 清理stream
func (s *StreamCounterSet) cleanup(serviceStream ratelimiter.RateLimitGRPCV2_ServiceClient) {
	s.asyncConnector.dropStreamCounterSet(s, serviceStream)
	s.closeConnection()
}

// code2CommonCode 转为http status
func code2CommonCode(code uint32) int {
	value := int(code / 1000)
	if value < 100 {
		return 0
	}
	return (value / 100) * 100
}

// IsSuccess 是否成功错误码
func IsSuccess(code uint32) bool {
	return code2CommonCode(code) == 200
}

// updateByInitResp 通过初始化应答来更新
func (s *StreamCounterSet) updateByInitResp(identifier CounterIdentifier, initResp *ratelimiter.RateLimitInitResponse) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.clientKey = initResp.GetClientKey()
	record := s.initialingWindows[identifier]
	if nil == record {
		return
	}
	atomic.StoreInt64(&record.lastRecvTimeMilli, model.CurrentMillisecond())
	for _, quotaSum := range initResp.GetCounters() {
		duration := time.Duration(quotaSum.GetDuration()) * time.Second
		record.counterKeys[duration] = quotaSum.GetCounterKey()
		s.counters[quotaSum.GetCounterKey()] = &DurationBaseCallBack{
			record:   record,
			callBack: record.callback,
			duration: duration,
		}
	}
}

// processInitResponse 处理初始化应答
func (s *StreamCounterSet) processInitResponse(initResp *ratelimiter.RateLimitInitResponse) bool {
	target := initResp.GetTarget()
	identifier := CounterIdentifier{
		service:   target.GetService(),
		namespace: target.GetNamespace(),
		labels:    target.GetLabels(),
	}
	if IsSuccess(initResp.GetCode()) {
		// 变更通知map
		s.updateByInitResp(identifier, initResp)
		// 触发回调
		s.mutex.RLock()
		for _, quotaSum := range initResp.GetCounters() {
			counterKey := quotaSum.GetCounterKey()
			if callback, ok := s.counters[counterKey]; ok {
				callback.callBack.OnInitResponse(quotaSum, callback.duration, initResp.GetTimestamp())
			}
		}
		s.mutex.RUnlock()
		return true
	}
	log.GetNetworkLogger().Errorf(
		"[RateLimit]received init response with error, code %d, counter %s", initResp.Code, identifier)
	return false
}

// processReportResponse 处理上报的应答
func (s *StreamCounterSet) processReportResponse(reportRsp *ratelimiter.RateLimitReportResponse) bool {
	if IsSuccess(reportRsp.GetCode()) {
		s.mutex.RLock()
		nowMilli := model.CurrentMillisecond()
		for _, quotaLeft := range reportRsp.GetQuotaLefts() {
			counterKey := quotaLeft.GetCounterKey()
			if callback, ok := s.counters[counterKey]; ok {
				atomic.StoreInt64(&callback.record.lastRecvTimeMilli, nowMilli)
				callback.callBack.OnReportResponse(quotaLeft, callback.duration, reportRsp.GetTimestamp())
			}
		}
		s.mutex.RUnlock()
		return true
	}
	log.GetNetworkLogger().Errorf("[RateLimit]received init response with error, code %d, window %s",
		reportRsp.GetCode(), *s.HostIdentifier)
	return false
}

// processResponse 处理应答消息
func (s *StreamCounterSet) processResponse(serviceStream ratelimiter.RateLimitGRPCV2_ServiceClient) {
	defer s.cleanup(serviceStream)
	for {
		resp, err := serviceStream.Recv()
		if err != nil {
			if err != io.EOF {
				log.GetNetworkLogger().Errorf("[RateLimit]fail to receive message from %s:%d, err is %v",
					s.HostIdentifier.host, s.HostIdentifier.port, err)
			}
			return
		}
		switch resp.Cmd {
		case ratelimiter.RateLimitCmd_INIT:
			initResp := resp.GetRateLimitInitResponse()
			if log.GetNetworkLogger().IsLevelEnabled(log.DebugLog) {
				initRspStr, _ := (&jsonpb.Marshaler{}).MarshalToString(initResp)
				log.GetNetworkLogger().Debugf("[RateLimit]Recv init response: %s\n", initRspStr)
			}
			if !s.processInitResponse(initResp) {
				return
			}
		case ratelimiter.RateLimitCmd_ACQUIRE:
			reportResp := resp.GetRateLimitReportResponse()
			if log.GetNetworkLogger().IsLevelEnabled(log.DebugLog) {
				reportRspStr, _ := (&jsonpb.Marshaler{}).MarshalToString(reportResp)
				log.GetNetworkLogger().Debugf("[RateLimit]Recv report response: %s\n", reportRspStr)
			}
			if !s.processReportResponse(reportResp) {
				return
			}
		}
	}
}

// SendReportRequest 发送上报请求
func (s *StreamCounterSet) SendReportRequest(clientReportReq *limitpb.ClientRateLimitReportRequest) error {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	if reflect2.IsNil(s.serviceStream) {
		return fmt.Errorf("serviceStream is empty")
	}
	if s.clientKey == 0 {
		return fmt.Errorf("clientKey is empty")
	}
	identifier := CounterIdentifier{
		service:   clientReportReq.Service,
		namespace: clientReportReq.Namespace,
		labels:    clientReportReq.Labels,
	}
	record := s.initialingWindows[identifier]
	if nil == record {
		return fmt.Errorf("fail to find initialingWindow, identifier is %s", identifier)
	}
	reportReq := &ratelimiter.RateLimitReportRequest{}
	reportReq.ClientKey = s.clientKey
	// 转换系统时间
	reportReq.Timestamp = clientReportReq.Timestamp
	for duration, sum := range clientReportReq.QuotaUsed {
		counterKey, ok := record.counterKeys[duration]
		if !ok {
			continue
		}
		sum.CounterKey = counterKey
		reportReq.QuotaUses = append(reportReq.QuotaUses, sum)
	}
	// 发起上报调用
	request := &ratelimiter.RateLimitRequest{
		Cmd:                    ratelimiter.RateLimitCmd_ACQUIRE,
		RateLimitReportRequest: reportReq,
	}
	if log.GetNetworkLogger().IsLevelEnabled(log.DebugLog) {
		reportReqStr, _ := (&jsonpb.Marshaler{}).MarshalToString(reportReq)
		log.GetNetworkLogger().Debugf("[RateLimit]Send report request: %s\n", reportReqStr)
	}
	if err := s.serviceStream.Send(request); err != nil {
		log.GetNetworkLogger().Errorf("[RateLimit]fail to send request message to %s:%d, err is %v",
			s.HostIdentifier.host, s.HostIdentifier.port, err)
	}
	return nil
}

// HostIdentifier 节点标识
type HostIdentifier struct {
	host string
	port uint32
}

// ToString输出
func (h HostIdentifier) String() string {
	return fmt.Sprintf("{host: %s, port: %d}", h.host, h.port)
}

// CounterIdentifier 计数器标识
type CounterIdentifier struct {
	service   string
	namespace string
	labels    string
}

// String ToString输出
func (c CounterIdentifier) String() string {
	return fmt.Sprintf("{service: %s, namespace: %s, labels: %s}", c.service, c.namespace, c.labels)
}

// asyncRateLimitConnector 目前只实现了 RateLimit-Acquire的异步 和 metric-report的异步
type asyncRateLimitConnector struct {
	// 读写锁，守护streams列表
	mutex *sync.RWMutex
	// IP端口到Stream的映射，一个IP端口只有一个stream
	streams map[HostIdentifier]*StreamCounterSet
	// 销毁标识
	destroyed bool
	// 全局上下文信息
	valueCtx model.ValueContext
	// 单次加载
	once *sync.Once
	// 获取自身IP的互斥锁
	clientHostMutex *sync.Mutex
	// 自身IP信息
	clientHost string
	// 连接超时时间
	connTimeout time.Duration
	// 消息超时时间
	msgTimeout time.Duration
	// 淘汰清理任务列表
	taskValues model.TaskValues
	// 定时淘汰间隔
	purgeInterval time.Duration
	// 连接释放的空闲时长
	connIdleTimeout time.Duration
	// 重连间隔时间
	reconnectInterval time.Duration
	// 协议
	protocol string
}

// NewAsyncRateLimitConnector .
func NewAsyncRateLimitConnector(valueCtx model.ValueContext, cfg config.Configuration) AsyncRateLimitConnector {
	connTimeout := cfg.GetGlobal().GetServerConnector().GetConnectTimeout()
	msgTimeout := cfg.GetGlobal().GetServerConnector().GetMessageTimeout()
	protocol := cfg.GetGlobal().GetServerConnector().GetProtocol()
	purgeInterval := cfg.GetProvider().GetRateLimit().GetPurgeInterval()
	connIdleTimeout := cfg.GetGlobal().GetServerConnector().GetConnectionIdleTimeout()
	reconnectInterval := cfg.GetGlobal().GetServerConnector().GetReconnectInterval()
	return &asyncRateLimitConnector{
		mutex:             &sync.RWMutex{},
		streams:           make(map[HostIdentifier]*StreamCounterSet),
		valueCtx:          valueCtx,
		connTimeout:       connTimeout,
		msgTimeout:        msgTimeout,
		purgeInterval:     purgeInterval,
		connIdleTimeout:   connIdleTimeout,
		reconnectInterval: reconnectInterval,
		once:              &sync.Once{},
		clientHostMutex:   &sync.Mutex{},
		protocol:          protocol,
	}
}

// dropStreamCounterSet 淘汰流管理器
func (a *asyncRateLimitConnector) dropStreamCounterSet(
	streamCounterSet *StreamCounterSet, serviceStream ratelimiter.RateLimitGRPCV2_ServiceClient) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	existStream := a.streams[*streamCounterSet.HostIdentifier]
	if existStream != streamCounterSet || existStream.serviceStream != serviceStream {
		return
	}
	delete(a.streams, *streamCounterSet.HostIdentifier)
}

// getStreamCounterSet 获取流计数器
func (a *asyncRateLimitConnector) getStreamCounterSet(hostIdentifier HostIdentifier) (*StreamCounterSet, error) {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	if a.destroyed {
		return nil, model.NewSDKError(model.ErrCodeInvalidStateError, nil, "rateLimit connector destroyed")
	}
	return a.streams[hostIdentifier], nil
}

// Process 定时处理过期任务
func (a *asyncRateLimitConnector) Process(
	taskKey interface{}, taskValue interface{}, lastProcessTime time.Time) model.TaskResult {
	counterSet := taskValue.(*StreamCounterSet)
	nowMilli := model.CurrentMillisecond()
	lastProcessMilli := lastProcessTime.UnixNano() / 1e6
	if nowMilli-lastProcessMilli < model.ToMilliSeconds(a.purgeInterval) {
		return model.SKIP
	}

	if !counterSet.Expired(nowMilli, true) {
		return model.CONTINUE
	}
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if counterSet.Expired(nowMilli, false) {
		atomic.StoreInt32(&counterSet.expired, 1)
		delete(a.streams, *counterSet.HostIdentifier)
		counterSet.closeConnection()
		log.GetBaseLogger().Infof("[RateLimit]stream %s expired", *counterSet.HostIdentifier)
		return model.TERMINATE
	}
	return model.CONTINUE
}

// OnTaskEvent 任务事件回调
func (a *asyncRateLimitConnector) OnTaskEvent(event model.TaskEvent) {

}

// StreamCount 获取stream的数量，用于测试
func (a *asyncRateLimitConnector) StreamCount() int {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	return len(a.streams)
}

// GetMessageSender 创建流上下文
func (a *asyncRateLimitConnector) GetMessageSender(
	svcKey model.ServiceKey, hashValue uint64) (RateLimitMsgSender, error) {
	req := &model.GetOneInstanceRequest{}
	req.Service = svcKey.Service
	req.Namespace = svcKey.Namespace
	req.LbPolicy = config.DefaultLoadBalancerMaglev
	req.HashValue = hashValue
	req.Metadata = map[string]string{"protocol": a.protocol}
	engine := a.valueCtx.GetEngine()
	a.once.Do(func() {
		_, taskValues := engine.ScheduleTask(&model.PeriodicTask{
			Name:       "rateLimit-connector-clean",
			CallBack:   a,
			Period:     a.purgeInterval,
			DelayStart: false,
		})
		a.taskValues = taskValues
	})
	instanceResp, err := engine.SyncGetOneInstance(req)
	if err != nil {
		return nil, err
	}
	var hostIdentifier = &HostIdentifier{}
	hostIdentifier.host = instanceResp.GetInstances()[0].GetHost()
	hostIdentifier.port = instanceResp.GetInstances()[0].GetPort()
	var counterSet *StreamCounterSet
	counterSet, err = a.getStreamCounterSet(*hostIdentifier)
	if err != nil {
		return nil, err
	}
	if nil != counterSet {
		return counterSet, nil
	}
	a.mutex.Lock()
	defer a.mutex.Unlock()
	counterSet = a.streams[*hostIdentifier]
	if nil != counterSet {
		return counterSet, nil
	}
	counterSet = NewStreamCounterSet(a, hostIdentifier)
	a.streams[*hostIdentifier] = counterSet
	a.taskValues.AddValue(*hostIdentifier, counterSet)
	return counterSet, nil
}

func (a *asyncRateLimitConnector) getIPString(remoteHost string, remotePort uint32) string {
	a.clientHostMutex.Lock()
	defer a.clientHostMutex.Unlock()
	if len(a.clientHost) > 0 {
		return a.clientHost
	}
	addr := fmt.Sprintf("%s:%d", remoteHost, remotePort)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		log.GetNetworkLogger().Errorf("fail to dial %s to get local host, err is %v", err)
		return ""
	}
	localAddr := conn.LocalAddr().String()
	a.clientHost = strings.Split(localAddr, ":")[0]
	return a.clientHost
}

// Destroy 清理
func (a *asyncRateLimitConnector) Destroy() {
	a.mutex.Lock()
	a.destroyed = true
	streams := a.streams
	a.streams = nil
	a.mutex.Unlock()
	if len(streams) == 0 {
		return
	}
	for _, stream := range streams {
		stream.closeConnection()
	}
}
