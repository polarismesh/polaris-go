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

package common

import (
	"context"
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/algorithm/rand"
	"github.com/polarismesh/polaris-go/pkg/clock"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang/protobuf/jsonpb"
	"github.com/modern-go/reflect2"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/network"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"

	"github.com/golang/protobuf/ptypes/wrappers"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/pkg/plugin/serverconnector"
)

const (
	//需要发往服务端的请求跟踪标识
	headerRequestID = "request-id"
	//失败时的最大超时时间
	maxConnTimeout = 100 * time.Millisecond
	//任务重试间隔
	taskRetryInterval = 200 * time.Millisecond
	//接收线程获取连接的间隔
	receiveConnInterval = 1 * time.Second
	//发送者任务线程轮询时间间隔
	syncInterval = 500 * time.Millisecond
	//间隔多久打印任务队列信息
	logInterval = 5 * time.Minute
	////请求discover服务的任务转化为以自身为cluster的数量最大值为2，一个实例，一个路由
	maxDiscoverClusterNum = 2
)

var (
	mu sync.Mutex
)

//Connector cl5服务端代理，使用GRPC协议对接
type DiscoverConnector struct {
	*common.RunContext
	ServiceConnector      *plugin.PluginBase
	connectionIdleTimeout time.Duration
	messageTimeout        time.Duration
	//普通任务队列
	taskChannel chan *clientTask
	//高优先级重试任务队列，只会在系统服务未ready时候会往队列塞值
	retryPriorityTaskChannel chan model.ServiceEventKey
	//定时轮询任务集合
	updateTaskSet *sync.Map
	//连接管理器
	connManager network.ConnectionManager
	valueCtx    model.ValueContext
	//通过本地缓存加载成功的系统服务
	cachedServerServices []model.ServiceEventKey
	//discover服务的命名空间和服务名
	discoverKey            model.ServiceKey
	discoverInstancesReady bool
	discoverRoutingReady   bool
	//请求discover服务的任务转化为以自身为cluster的数量
	discoverClusterNum int
	queueSize          int32
	//创建具体调度客户端的逻辑
	createClient DiscoverClientCreator
	scalableRand *rand.ScalableRand
}

//任务对象，用于在connector协程中做轮转处理
type clientTask struct {
	updateTask *serviceUpdateTask
	op         taskOp
}

//Init 初始化插件
func (g *DiscoverConnector) Init(ctx *plugin.InitContext, createClient DiscoverClientCreator) {
	ctxConfig := ctx.Config
	g.RunContext = common.NewRunContext()
	g.scalableRand = rand.NewScalableRand()
	g.discoverKey.Namespace = ctxConfig.GetGlobal().GetSystem().GetDiscoverCluster().GetNamespace()
	g.discoverKey.Service = ctxConfig.GetGlobal().GetSystem().GetDiscoverCluster().GetService()
	g.valueCtx = ctx.ValueCtx
	g.queueSize = ctxConfig.GetGlobal().GetServerConnector().GetRequestQueueSize()
	g.connectionIdleTimeout = ctxConfig.GetGlobal().GetServerConnector().GetConnectionIdleTimeout()
	g.messageTimeout = ctxConfig.GetGlobal().GetServerConnector().GetMessageTimeout()
	g.connManager = ctx.ConnManager
	g.createClient = createClient
	for _, cachedSvc := range g.cachedServerServices {
		g.connManager.UpdateServers(cachedSvc)
	}
}

//初始化connector调度主协程
func (g *DiscoverConnector) StartUpdateRoutines() {
	g.updateTaskSet = &sync.Map{}
	g.taskChannel = make(chan *clientTask, g.queueSize)
	g.retryPriorityTaskChannel = make(chan model.ServiceEventKey, g.queueSize)
	go g.doSend()
	go g.doRetry()
	go g.doLog()
}

//将存储在原子变量里面的时间转化为string
func atomicTimeToString(v atomic.Value) string {
	timeValue := v.Load()
	if reflect2.IsNil(timeValue) {
		return "<nil>"
	}
	return timeValue.(time.Time).Format("2006-01-02 15:04:05")
}

//定时打印任务队列状态的协程
func (g *DiscoverConnector) doLog() {
	logLoop := time.NewTicker(logInterval)
	defer logLoop.Stop()
	for {
		select {
		case <-g.Done():
			log.GetBaseLogger().Infof("doLog routine of grpc connector has benn terminated")
			return
		case <-logLoop.C:
			g.updateTaskSet.Range(func(k, v interface{}) bool {
				task := v.(*serviceUpdateTask)
				if task.needLog() {
					log.GetNetworkLogger().Infof("%s, doLog: task %s, msgSendTime %v, lastUpdateTime %v,"+
						" requestsSent %d, successUpdates %d", g.ServiceConnector.GetSDKContextID(), task,
						atomicTimeToString(task.msgSendTime), atomicTimeToString(task.lastUpdateTime),
						atomic.LoadUint64(&task.totalRequests), atomic.LoadUint64(&task.successUpdates))
				}
				return true
			})
		}
	}
}

//用于recv协程通知send协程关于链路故障的问题
type ClientFailEvent struct {
	connID uint32
}

//定时线程进行重试检查，防止send线程高负载
func (g *DiscoverConnector) doRetry() {
	retryLoop := time.NewTicker(taskRetryInterval / 2)
	defer retryLoop.Stop()
	for {
		select {
		case <-g.Done():
			log.GetBaseLogger().Infof("doRetry routine of grpc connector has benn terminated")
			return
		case svcEventKey := <-g.retryPriorityTaskChannel:
			log.GetBaseLogger().Infof("retry: start add priority task %s", svcEventKey)
			if taskValue, ok := g.updateTaskSet.Load(svcEventKey); ok {
				priorityTask := taskValue.(*serviceUpdateTask)
				g.scheduleRetry(priorityTask)
			}
		case <-retryLoop.C:
			g.updateTaskSet.Range(func(key, value interface{}) bool {
				task := value.(*serviceUpdateTask)
				g.scheduleRetry(task)
				return true
			})
		}
	}
}

//执行任务重试调度
func (g *DiscoverConnector) scheduleRetry(task *serviceUpdateTask) {
	task.retryLock.Lock()
	defer task.retryLock.Unlock()
	if atomic.CompareAndSwapUint32(&task.longRun, retryTask, firstTask) {
		log.GetBaseLogger().Infof("retry: start schedule task %s", task.ServiceEventKey)
		select {
		case <-g.Done():
			log.GetBaseLogger().Infof("%s, context done, exit retry", g.ServiceConnector.GetSDKContextID())
			return
		case <-task.retryDeadline:
			g.addFirstTask(task)
			log.GetBaseLogger().Infof("retry: success schedule task %s", task.ServiceEventKey)
		}
	}
}

//执行异步更新及数据获取主流程
func (g *DiscoverConnector) doSend() {
	updateTicker := time.NewTicker(syncInterval)
	defer func() {
		updateTicker.Stop()
	}()
	var streamingClient *StreamingClient
	for {
		select {
		case <-g.Done():
			if nil != streamingClient {
				//如果刚好连接切换，还没有执行到clearIdleClient，旧连接可能还是活跃的，关闭连接避免泄露
				streamingClient.CloseStream(true)
			}
			log.GetBaseLogger().Infof("doSend routine of grpc connector has benn terminated")
			return
		case clientTask := <-g.taskChannel:
			streamingClient = g.onClientTask(streamingClient, clientTask)
		case <-updateTicker.C:
			if nil != streamingClient {
				allTaskTimeout := g.clearTimeoutClient(streamingClient)
				hasSwitchedClient := g.clearSwitchedClient(streamingClient)
				g.clearIdleClient(streamingClient, hasSwitchedClient || allTaskTimeout)
			}
			g.updateTaskSet.Range(func(k, v interface{}) bool {
				task := v.(*serviceUpdateTask)
				//首先进行状态判断，判断状态是否属于长稳运行任务
				if atomic.LoadUint32(&task.longRun) != longRunning || !task.needUpdate() {
					return true
				}
				log.GetBaseLogger().Debugf(
					"start to update task %s, update interval %v", task.ServiceEventKey, task.updateInterval)
				streamingClient = g.processUpdateTask(streamingClient, task)
				if len(g.taskChannel) > 0 {
					log.GetBaseLogger().Infof("firstTask received, now breakthrough updateTasks")
					return false
				}
				return true
			})
		}
	}
}

//重试更新任务
func (g *DiscoverConnector) retryUpdateTask(updateTask *serviceUpdateTask, err error, notReady bool) {
	updateTask.retryLock.Lock()
	defer updateTask.retryLock.Unlock()
	if atomic.CompareAndSwapUint32(&updateTask.longRun, firstTask, retryTask) {
		log.GetBaseLogger().Warnf("retry: task %s for error %v", updateTask.ServiceEventKey, err)
		if notReady {
			//如果是等待首次连接的，则缩短重试间隔
			updateTask.retryDeadline = time.After(clock.TimeStep())
		} else {
			updateTask.retryDeadline = time.After(taskRetryInterval)
		}
		g.updateTaskSet.Store(updateTask.ServiceEventKey, updateTask)
		if updateTask.targetCluster == config.BuiltinCluster {
			g.retryPriorityTaskChannel <- updateTask.ServiceEventKey
		}
	} else {
		log.GetBaseLogger().Warnf(
			"skip retry: not first task %s for error %v", updateTask.ServiceEventKey, err)
		updateTask.lastUpdateTime.Store(time.Now())
	}
}

//最大的消息打印大小，超过该大小的消息则不打印到日志中
const maxLogMsgSize = 4 * 1024 * 1024

//打印应答消息
func logDiscoverResponse(resp *namingpb.DiscoverResponse, connection *network.Connection) {
	if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		svcKey := model.ServiceEventKey{
			ServiceKey: model.ServiceKey{
				Namespace: resp.GetService().GetNamespace().GetValue(),
				Service:   resp.GetService().GetName().GetValue(),
			},
			Type: pb.GetEventType(resp.Type),
		}
		jsonMarshaler := &jsonpb.Marshaler{}
		respJson, _ := jsonMarshaler.MarshalToString(resp)
		if len(respJson) <= maxLogMsgSize {
			log.GetBaseLogger().Debugf("received response from %s(%s), service %s: \n%v",
				connection.ConnID, connection.Address, svcKey, respJson)
		} else {
			log.GetBaseLogger().Debugf("received response from %s(%s), service %s: message size exceed %v",
				connection.ConnID, connection.Address, svcKey, maxLogMsgSize)
		}
	}
}

//服务发现应答转为事件，从应答里面获取调用discover的返回码
func discoverResponseToEvent(resp *namingpb.DiscoverResponse,
	svcEventKey model.ServiceEventKey, connection *network.Connection) (*serverconnector.ServiceEvent, model.ErrCode) {
	svcEvent := &serverconnector.ServiceEvent{ServiceEventKey: svcEventKey}
	retCode := resp.GetCode().GetValue()
	errInfo := resp.GetInfo().GetValue()
	svcCode := pb.ConvertServerErrorToRpcError(retCode)
	if model.IsSuccessResultCode(retCode) {
		svcEvent.Value = resp
	} else if retCode == namingpb.NotFoundResource || retCode == namingpb.NotFoundService {
		log.GetBaseLogger().Errorf("server error received, code %v, info: %s", retCode, errInfo)
		svcEvent.Error = model.NewSDKError(model.ErrCodeServiceNotFound, nil,
			"service %s not found", svcEvent.ServiceEventKey)
	} else if retCode == namingpb.NotFoundMeshConfig {
		log.GetBaseLogger().Errorf("server error received, code %v, info: %s", retCode, errInfo)
		svcEvent.Error = model.NewSDKError(model.ErrCodeMeshConfigNotFound, nil,
			"meshconfig %s not found", svcEvent.ServiceEventKey)
	} else {
		log.GetBaseLogger().Errorf("server error received, code %v, info: %s", retCode, errInfo)
		svcEvent.Error = model.NewServerSDKError(retCode,
			errInfo, nil, "server error from %s: %s", connection.ConnID.Address, errInfo)
	}
	return svcEvent, svcCode
}

//将任务加入调度列表
func (g *DiscoverConnector) addUpdateTaskSet(updateTask *serviceUpdateTask) {
	if atomic.CompareAndSwapUint32(&updateTask.longRun, firstTask, longRunning) {
		log.GetBaseLogger().Infof("serviceEvent %s update has been scheduled, interval %v",
			updateTask.ServiceEventKey, updateTask.updateInterval)
		g.updateTaskSet.Store(updateTask.ServiceEventKey, updateTask)
	}
}

//上报调用结果
func (g *DiscoverConnector) reportCallStatus(
	curClient *StreamingClient, updateTask *serviceUpdateTask, errCode int32, isSuccess bool) {
	consumeTime := GetUpdateTaskRequestTime(updateTask)
	if !isSuccess {
		g.connManager.ReportFail(curClient.connection.ConnID, errCode, consumeTime)
	} else {
		g.connManager.ReportSuccess(curClient.connection.ConnID, errCode, consumeTime)
	}
}

//处理添加服务实例更新任务
func (g *DiscoverConnector) onAddListener(
	streamingClient *StreamingClient, updateTask *serviceUpdateTask) *StreamingClient {
	return g.processUpdateTask(streamingClient, updateTask)
}

//处理删除服务实例更新任务
func (g *DiscoverConnector) onDelListener(taskKey *model.ServiceEventKey) {
	//log.GetBaseLogger().Infof("serviceEvent %s update has been cancelled", *taskKey)
	log.GetBaseLogger().Infof("%s, onDelListener: task %s removed from updateTaskSet",
		g.ServiceConnector.GetSDKContextID(), *taskKey)
	g.updateTaskSet.Delete(*taskKey)
}

//处理消费者任务
func (g *DiscoverConnector) onClientTask(streamingClient *StreamingClient, clientTask *clientTask) *StreamingClient {
	switch clientTask.op {
	case opAddListener:
		streamingClient = g.onAddListener(streamingClient, clientTask.updateTask)
	case opDelListener:
		g.onDelListener(&clientTask.updateTask.ServiceEventKey)
	}
	return streamingClient
}

//流式客户端，带连接
type StreamingClient struct {
	//所属的discoverConnector
	connector *DiscoverConnector
	//用于确保原子关闭
	once sync.Once
	//连接是否可用
	endStream uint32
	//在doSend协程，是否发现了streamingClient的错误，如超时
	hasError uint32
	//实际的连接信息
	connection     *network.Connection
	reqID          string
	discoverClient DiscoverClient
	//互斥锁，用于守护任务队列
	mutex        sync.Mutex
	pendingTasks map[model.ServiceEventKey]*serviceUpdateTask
	//最后一次更新时间，存放的是*time.Time
	lastRecvTime atomic.Value

	// WithTimeout Context return cancel()
	cancel context.CancelFunc
}

//关闭流并释放连接
func (s *StreamingClient) CloseStream(closeSend bool) bool {
	endStreamOk := atomic.CompareAndSwapUint32(&s.endStream, 0, 1)
	if endStreamOk {
		//进行closeStream操作
		if closeSend {
			log.GetNetworkLogger().Debugf(
				"%s, connection %s(%s) reqID %s start to closeSend",
				s.connector.ServiceConnector.GetSDKContextID(), s.connection.ConnID, s.reqID, s.connection.Address)
			if err := s.discoverClient.CloseSend(); nil != err {
				//这里一般不会出现错误，只是为了处理告警
				log.GetNetworkLogger().Warnf("%s, fail to doCloseSend, error is %v",
					s.connector.ServiceConnector.GetSDKContextID(), err)
			}
		}
		log.GetNetworkLogger().Debugf("%s, cancel stream %s, connection %s",
			s.connector.ServiceConnector.GetSDKContextID(), s.reqID, s.connection.ConnID)
		s.connection.Release(OpKeyDiscover)
		if s.cancel != nil {
			s.cancel()
		}
	}
	return endStreamOk
}

//获取最后一次更新时间
func (s *StreamingClient) getLastRecvTime() *time.Time {
	lastRecvTimeValue := s.lastRecvTime.Load()
	lastRecvTime := lastRecvTimeValue.(time.Time)
	return &lastRecvTime
}

//获取回调函数
func (s *StreamingClient) getSvcUpdateTasks(key *model.ServiceEventKey) (tasks []*serviceUpdateTask) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if nil != key {
		if task, ok := s.pendingTasks[*key]; ok {
			tasks = append(tasks, task)
			delete(s.pendingTasks, *key)
		}
	} else {
		for _, task := range s.pendingTasks {
			tasks = append(tasks, task)
		}
		s.pendingTasks = nil
	}
	return tasks
}

//设置流关闭
//func (s *StreamingClient) markEndStream() bool {
//	endStreamOk := atomic.CompareAndSwapUint32(&s.endStream, 0, 1)
//	if endStreamOk {
//		log.GetBaseLogger().Debugf("stream %s mark end", s.reqID)
//	}
//	return endStreamOk
//}

//设置流关闭
func (s *StreamingClient) IsEndStream() bool {
	return atomic.LoadUint32(&s.endStream) > 0
}

//校验Recv收到的错误和应答，看看这次请求是否成功，需要怎么上报discover的实例状态
//返回值：report，是否需要上报失败；code，上报的返回码；discoverErr，转化成SDKError，不为空说明这次Recv失败了
func (s *StreamingClient) checkErrorReport(grpcErr error, resp *namingpb.DiscoverResponse) (report bool, code int32,
	discoverErr model.SDKError) {
	//如果接收的时候出现了错误，那么根据错误进行判断
	if grpcErr != nil {
		closeBySelf := s.CloseStream(false)
		discoverErr = model.NewSDKError(model.ErrCodeInvalidResponse, grpcErr,
			"invalid response from %s(%s), reqID %s",
			s.connection.ConnID, s.connection.Address, s.reqID)
		//由doSend关闭了连接
		if !closeBySelf {
			//如果doSend发现有错误，allTaskTimeout或者idle，那么上报超时错误
			if atomic.LoadUint32(&s.hasError) == 1 {
				return true, int32(model.ErrorCodeRpcTimeout), discoverErr
			} else {
				//如果doSend没发现错误，也进行了CloseStream，那么就是进行了切换连接，不进行上报
				return false, 0, discoverErr
			}
		}
		//如果receiveAndNotify由于错误关闭了streamingClient，那么就是由于Recv过程中出现了错误，
		//现在统一返回ErrCodeInvalidServerResponse，表示discover没有返回正常的数据
		return true, int32(model.ErrCodeInvalidServerResponse), discoverErr
	}
	msgErr := pb.ValidateMessage(nil, resp)
	if nil != msgErr {
		s.CloseStream(false)
		discoverErr = model.NewSDKError(model.ErrCodeInvalidResponse, grpcErr,
			"invalid response from %s(%s), reqID %s",
			s.connection.ConnID, s.connection.Address, s.reqID)
		return true, msgErr.(*pb.DiscoverError).Code, discoverErr
	}
	return false, 0, nil
}

//使用streamClient进行收包，并更新服务信息
func (s *StreamingClient) receiveAndNotify() {
	log.GetBaseLogger().Infof("%s, receiveAndNotify of streamingClient %s start to receive message",
		s.connector.ServiceConnector.GetSDKContextID(), s.reqID)
	for {
		resp, grpcErr := s.discoverClient.Recv()
		s.lastRecvTime.Store(time.Now())
		if nil != grpcErr {
			log.GetNetworkLogger().Errorf("%s, receiveAndNotify: fail to receive message from connection %s,"+
				" streamingClient %s, err %v",
				s.connector.ServiceConnector.GetSDKContextID(), s.connection, s.reqID, grpcErr)
		}
		report, code, discoverErr := s.checkErrorReport(grpcErr, resp)
		//处理与discover连接出现的问题
		if nil != discoverErr {
			//grpc请求有问题，上报connection down
			s.connector.connManager.ReportConnectionDown(s.connection.ConnID)
			updateTasks := s.getSvcUpdateTasks(nil)
			if len(updateTasks) > 0 {
				for _, updateTask := range updateTasks {
					if report {
						s.connector.reportCallStatus(s, updateTask, code, false)
					}
					s.connector.retryUpdateTask(updateTask, discoverErr, false)
				}
			}
			//出现了错误，退出收包协程
			log.GetBaseLogger().Infof("%s, receiveAndNotify of streamClient %s terminated",
				s.connector.ServiceConnector.GetSDKContextID(), s.reqID)
			return
		}
		logDiscoverResponse(resp, s.connection)
		//触发回调
		svcKey := &model.ServiceEventKey{
			ServiceKey: model.ServiceKey{
				Namespace: resp.GetService().GetNamespace().GetValue(),
				Service:   resp.GetService().GetName().GetValue(),
			},
			Type: pb.GetEventType(resp.Type),
		}
		//获取服务的回调列表
		tasks := s.getSvcUpdateTasks(svcKey)
		if len(tasks) > 0 {
			//执行正常回调操作
			updateTask := tasks[0]
			updateTask.lastUpdateTime.Store(time.Now())
			atomic.AddUint64(&updateTask.successUpdates, 1)
			//g.reportCallStatus(curClient, updateTask, nil, true)
			//触发回调事件
			svcEvent, discoverCode := discoverResponseToEvent(resp, updateTask.ServiceEventKey, s.connection)
			//没有返回grpc错误，返回的消息是合法且不是返回了500错误，认为这次调用成功了
			s.connector.connManager.ReportSuccess(s.connection.ConnID, int32(discoverCode), GetUpdateTaskRequestTime(updateTask))
			svcDeleted := updateTask.handler.OnServiceUpdate(svcEvent)
			if !svcDeleted {
				//服务如果没有被删除，则添加后续轮询
				s.connector.addUpdateTaskSet(updateTask)
			}
		} else {
			log.GetBaseLogger().Errorf("%s, can not get task %s from streamingClient %s pendingTask",
				s.connector.ServiceConnector.GetSDKContextID(), svcKey, s.reqID)
		}
	}
}

//检查链接是否可用，返回false代表链接不可用，需要新建连接
//连接如果可用，则把task加入连接回调列表
func (g *DiscoverConnector) checkStreamingClientAvailable(
	streamingClient *StreamingClient, task *serviceUpdateTask) bool {
	if nil == streamingClient {
		return false
	}
	if streamingClient.IsEndStream() {
		return false
	}
	streamingClient.mutex.Lock()
	defer streamingClient.mutex.Unlock()
	if nil == streamingClient.pendingTasks {
		//队列已经清空，证明stream已经出问题，无需继续发送
		return false
	}
	taskType := atomic.LoadUint32(&task.longRun)
	if taskType == firstTask || taskType == retryTask {
		log.GetNetworkLogger().Infof("%s, checkStreamingClientAvailable: "+
			"add first or retry task %s to streamingClient %s pendingTasks",
			g.ServiceConnector.GetSDKContextID(), task, streamingClient.reqID)
	}
	origTask, ok := streamingClient.pendingTasks[task.ServiceEventKey]
	if ok {
		log.GetBaseLogger().Errorf("%s, checkStreamingClientAvailable:"+
			" add exist task %s to client %s, whose msgSendTime is %s, lastUpdateTime is %s",
			g.ServiceConnector.GetSDKContextID(), origTask, streamingClient.reqID,
			atomicTimeToString(origTask.msgSendTime), atomicTimeToString(origTask.lastUpdateTime))
	}
	streamingClient.pendingTasks[task.ServiceEventKey] = task
	return true
	//
	//var available = false
	//if !streamingClient.isEndStream() {
	//	streamingClient.mutex.Lock()
	//	if !streamingClient.isEndStream() {
	//		taskType := atomic.LoadUint32(&task.longRun)
	//		if taskType == firstTask || taskType == retryTask {
	//			log.GetNetworkLogger().Infof("%s, checkStreamingClientAvailable: "+
	//				"add first or retry task %s to streamingClient %s pendingTasks",
	//				g.ServiceConnector.GetSDKContextID(), task, streamingClient.reqID)
	//		}
	//		origTask, ok := streamingClient.pendingTasks[task.ServiceEventKey]
	//		if ok {
	//			log.GetBaseLogger().Errorf("%s, checkStreamingClientAvailable:"+
	//				" add exist task %s to client %s, whose msgSendTime is %s, lastUpdateTime is %s",
	//				g.ServiceConnector.GetSDKContextID(), origTask, streamingClient.reqID,
	//				atomicTimeToString(origTask.msgSendTime), atomicTimeToString(origTask.lastUpdateTime))
	//		}
	//		streamingClient.pendingTasks[task.ServiceEventKey] = task
	//		available = true
	//	}
	//	streamingClient.mutex.Unlock()
	//}
	//return available
}

//创建新的客户端数据流
func (g *DiscoverConnector) newStream(task *serviceUpdateTask) (streamingClient *StreamingClient, err error) {
	//构造新的streamingClient
	streamingClient = &StreamingClient{
		pendingTasks: make(map[model.ServiceEventKey]*serviceUpdateTask, 0),
		connector:    g,
	}
	streamingClient.connection, err = g.connManager.GetConnection(OpKeyDiscover, task.targetCluster)
	taskType := atomic.LoadUint32(&task.longRun)
	if nil != err {
		log.GetNetworkLogger().Errorf("%s, newStream: fail to get connection of %s, err %v",
			g.ServiceConnector.GetSDKContextID(), task.targetCluster, err)
		goto finally
	}
	streamingClient.reqID = NextDiscoverReqID()
	streamingClient.discoverClient, streamingClient.cancel, err = g.createClient(streamingClient.reqID,
		streamingClient.connection, 0)
	if nil != err {
		log.GetNetworkLogger().Errorf("%s, newStream: fail to get streaming client from %s, reqID %s, err %v",
			g.ServiceConnector.GetSDKContextID(), streamingClient.connection, streamingClient.reqID, err)
		goto finally
	}
	log.GetNetworkLogger().Infof("%s, stream %s created, connection %s, timeout %v", g.ServiceConnector.GetSDKContextID(),
		streamingClient.reqID, streamingClient.connection.ConnID, g.connectionIdleTimeout)
	if taskType == firstTask || taskType == retryTask {
		log.GetNetworkLogger().Infof("%s, newStream: add first or retry task %s to new streamingClient %s pendingTasks",
			g.ServiceConnector.GetSDKContextID(), task, streamingClient.reqID)
	}
	//这里面还没有发给接收线程，都是单线程操作，所以不用加锁
	streamingClient.pendingTasks[task.ServiceEventKey] = task
	streamingClient.lastRecvTime.Store(time.Now())
	//启动streamingClient的接收协程
	go streamingClient.receiveAndNotify()
finally:
	if nil != err {
		if nil != streamingClient.connection {
			g.connManager.ReportConnectionDown(streamingClient.connection.ConnID)
			streamingClient.connection.Release(OpKeyDiscover)
		}
		sdkErr := model.NewSDKError(model.ErrCodeNetworkError, err,
			"fail to GetInstances for %v, reqID %s", task.ServiceEventKey, streamingClient.reqID)
		g.retryUpdateTask(task, sdkErr, false)
		return nil, sdkErr
	}
	return streamingClient, nil
}

//是否需要关闭流
func (g *DiscoverConnector) needCloseSend(lastRecvTime time.Time) bool {
	if !lastRecvTime.Add(g.connectionIdleTimeout).Before(time.Now()) {
		return false
	}
	return true
}

//获取超时任务列表
func (s *StreamingClient) allTaskTimeout(msgTimeout time.Duration) bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if len(s.pendingTasks) == 0 {
		return false
	}
	var allTaskTimeout = true
	//是否至少检查过一个任务
	var taskChecked bool
	now := time.Now()
	for _, task := range s.pendingTasks {
		msgSendTimeValue := task.msgSendTime.Load()
		if reflect2.IsNil(msgSendTimeValue) {
			//未发送请求不予处理
			continue
		}
		msgSendTime := msgSendTimeValue.(time.Time)
		taskChecked = true
		msgPassTime := now.Sub(msgSendTime)
		if msgPassTime <= msgTimeout {
			allTaskTimeout = false
		} else {
			//有个任务超时了，进行上报并打印日志
			s.connector.connManager.ReportFail(s.connection.ConnID, int32(model.ErrorCodeRpcTimeout), msgPassTime)
			log.GetNetworkLogger().Infof("%s, allTaskTimeout: task %s timeout, sendTime %v, now %v",
				s.connector.ServiceConnector.GetSDKContextID(), task, msgSendTime, now)
		}
	}
	//如果一个任务都没有，则不做超时判断
	if !taskChecked {
		allTaskTimeout = false
	}
	lastRecvTime := s.getLastRecvTime()
	if allTaskTimeout && time.Since(*lastRecvTime) > msgTimeout {
		log.GetBaseLogger().Infof("%s, allTaskTimeout: timeout on connection %s, connection lastRecv time is %v",
			s.connector.ServiceConnector.GetSDKContextID(), s.connection.ConnID, *lastRecvTime)
		return true
	}
	return false
}

//处理已经发生切换的streamClient
func (g *DiscoverConnector) clearSwitchedClient(client *StreamingClient) bool {
	if !network.IsAvailableConnection(client.connection) {
		log.GetNetworkLogger().Infof("%s, client connection %s has switched",
			g.ServiceConnector.GetSDKContextID(), client.connection.ConnID)
		return true
	}
	return false
}

//通知并清理所有的超时请求
func (g *DiscoverConnector) clearTimeoutClient(client *StreamingClient) bool {
	isTimeout := client.allTaskTimeout(g.messageTimeout)
	if !isTimeout {
		return false
	}
	//进行closeStream操作
	log.GetBaseLogger().Warnf(
		"connection %s(%s) reqID %s has pending tasks after timeout %v, start to terminate",
		client.connection.ConnID, client.reqID, client.connection.Address, g.messageTimeout)
	atomic.StoreUint32(&client.hasError, 1)
	g.connManager.ReportConnectionDown(client.connection.ConnID)
	return true
}

//清理空闲连接，当连接超过一段时间内没有任何收包，则会被清理
func (g *DiscoverConnector) clearIdleClient(client *StreamingClient, forceClear bool) {
	//检查是否超时
	lastRecvTime := client.getLastRecvTime()
	needClose := g.needCloseSend(*lastRecvTime)
	if needClose {
		atomic.StoreUint32(&client.hasError, 1)
	}
	if !forceClear && !needClose {
		return
	}
	//进行closeStream操作
	client.CloseStream(true)
}

//异步处理发现事件
func (g *DiscoverConnector) asyncUpdateTask(
	streamingClient *StreamingClient, task *serviceUpdateTask) *StreamingClient {
	//服务发现请求是否已经准备可以处理
	//需要获取discover集群完毕，以及地域信息获取完毕
	var notReadyErr error
	if !g.connManager.IsReady() {
		notReadyErr = fmt.Errorf("discover is not ready")
	} else if !g.valueCtx.GetCurrentLocation().IsLocationInitialized() {
		notReadyErr = fmt.Errorf("location info is not inited")
	}
	if nil != notReadyErr {
		g.retryUpdateTask(task, notReadyErr, true)
		return streamingClient
	}
	var curTime = time.Now()
	var err error
	var request = task.toDiscoverRequest()
	if !g.checkStreamingClientAvailable(streamingClient, task) {
		streamingClient = nil
	}
	if nil == streamingClient {
		//构造新的streamingClient
		streamingClient, err = g.newStream(task)
		if nil != err {
			log.GetBaseLogger().Errorf("fail to create stream for service %v, error is %+v", task.ServiceEventKey, err)
			return nil
		}
	}
	log.GetBaseLogger().Debugf("send request(id=%s) to %s for service %v",
		streamingClient.reqID, streamingClient.connection.Address, task.ServiceEventKey)
	task.msgSendTime.Store(curTime)
	taskType := atomic.LoadUint32(&task.longRun)
	if taskType == firstTask || taskType == retryTask {
		log.GetNetworkLogger().Infof("%s, asyncUpdateTask: start to send request for %s from streamingClient %s",
			g.ServiceConnector.GetSDKContextID(), task, streamingClient.reqID)
	}
	atomic.AddUint64(&task.totalRequests, 1)
	err = streamingClient.discoverClient.Send(request)
	if nil != err {
		//由receive协程来处理该错误的连接
		log.GetNetworkLogger().Errorf("%s, asyncUpdateTask: fail to send request for service %s from "+
			"streamingClient %s, error is %+v", g.ServiceConnector.GetSDKContextID(), task, streamingClient.reqID, err)
	}
	return streamingClient
}

//处理更新任务
func (g *DiscoverConnector) processUpdateTask(
	streamingClient *StreamingClient, task *serviceUpdateTask) *StreamingClient {
	if !task.needUpdate() {
		//未到更新时间
		return streamingClient
	}
	log.GetBaseLogger().Debugf("start to process task %s", task.ServiceEventKey)
	if task.targetCluster == config.BuiltinCluster {
		svcDeleted, err := g.syncUpdateTask(task)
		if nil != err {
			g.retryUpdateTask(task, err, true)
			return streamingClient
		}
		task.targetCluster = config.DiscoverCluster
		if !svcDeleted {
			g.addUpdateTaskSet(task)
		}
		return streamingClient
	}
	return g.asyncUpdateTask(streamingClient, task)
}

//Destroy 销毁插件，可用于释放资源
func (g *DiscoverConnector) Destroy() error {
	g.RunContext.Destroy()
	return nil
}

const (
	//首次执行的任务
	firstTask uint32 = iota
	//重试执行的任务
	retryTask
	//长稳调度的任务
	longRunning
)

//将longRun状态转化为string
var longRunMap = map[uint32]string{
	firstTask:   "firstTask",
	retryTask:   "retryTask",
	longRunning: "longRunning",
}

//serviceUpdateTask 服务更新任务
type serviceUpdateTask struct {
	model.ServiceEventKey
	//标识已经在长期运行的任务
	longRun        uint32
	updateInterval time.Duration
	//发起服务的发现的目标cluster
	targetCluster  config.ClusterType
	handler        serverconnector.EventHandler
	msgSendTime    atomic.Value
	lastUpdateTime atomic.Value
	totalRequests  uint64
	successUpdates uint64
	//到达某个时间点进行重试
	retryDeadline <-chan time.Time
	//已经准备好重试前的准备动作
	retryLock *sync.Mutex
}

//将一个更新任务格式化为string
func (s *serviceUpdateTask) String() string {
	return fmt.Sprintf("{namespace: \"%s\", service: \"%s\", event: %v, longRun: %s}",
		s.Namespace, s.Service, s.Type, longRunMap[atomic.LoadUint32(&s.longRun)])
}

//needUpdate 返回当前任务是否到达了更新间隔
func (s *serviceUpdateTask) needUpdate() bool {
	if atomic.LoadUint32(&s.longRun) == firstTask {
		return true
	}
	lastUpdateTimeValue := s.lastUpdateTime.Load()
	if reflect2.IsNil(lastUpdateTimeValue) {
		return true
	}
	curTime := time.Now()
	updateTime := lastUpdateTimeValue.(time.Time).Add(s.updateInterval)
	return !curTime.Before(updateTime)
}

//needLog 是否需要将当前任务定时打印到日志
func (s *serviceUpdateTask) needLog() bool {
	lastUpdateTimeValue := s.lastUpdateTime.Load()
	if reflect2.IsNil(lastUpdateTimeValue) {
		return true
	}
	curTime := time.Now()
	//如果在三倍的更新时间之内都没有更新的话，打印一次日志
	updateTime := lastUpdateTimeValue.(time.Time).Add(3 * s.updateInterval)
	return !curTime.Before(updateTime)
}

//转换为服务发现的请求对象
func (s *serviceUpdateTask) toDiscoverRequest() *namingpb.DiscoverRequest {
	var request = &namingpb.DiscoverRequest{
		Type: pb.GetProtoRequestType(s.Type),
		Service: &namingpb.Service{
			Name:      &wrappers.StringValue{Value: s.Service},
			Namespace: &wrappers.StringValue{Value: s.Namespace},
			Revision:  &wrappers.StringValue{Value: s.handler.GetRevision()},
			Business:  &wrappers.StringValue{Value: s.handler.GetBusiness()},
		},
		//网格请求
		MeshConfig: s.handler.GetMeshConfig(),
		Mesh: &namingpb.Mesh{
			Id: &wrappers.StringValue{Value: s.Service},
		},
	}
	if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		reqJson, _ := (&jsonpb.Marshaler{}).MarshalToString(request)
		log.GetBaseLogger().Debugf(
			"discover request to send is %s", reqJson)
	}
	return request
}

//RegisterServiceHandler 注册服务监听器
//异常场景：当key不合法或者sdk已经退出过程中，则返回error
func (g *DiscoverConnector) RegisterServiceHandler(svcEventHandler *serverconnector.ServiceEventHandler) error {
	updateTask := &serviceUpdateTask{
		handler:       svcEventHandler.Handler,
		targetCluster: svcEventHandler.TargetCluster,
		longRun:       firstTask,
		retryLock:     &sync.Mutex{},
	}
	updateTask.Service = svcEventHandler.Service
	updateTask.Namespace = svcEventHandler.Namespace
	updateTask.Type = svcEventHandler.Type
	//增加随机秒数[0~3)，为了让更新不要聚集
	mu.Lock()
	diffSecond := g.scalableRand.Intn(3)
	mu.Unlock()
	updateTask.updateInterval = svcEventHandler.RefreshInterval + (time.Duration(diffSecond) * time.Second)
	log.GetBaseLogger().Debugf(
		"register update task for service %s, update interval %v", updateTask.ServiceEventKey, updateTask.updateInterval)
	return g.addFirstTask(updateTask)
}

//往队列插入任务
func (g *DiscoverConnector) addFirstTask(updateTask *serviceUpdateTask) error {
	task := &clientTask{updateTask: updateTask, op: opAddListener}
	log.GetBaseLogger().Infof("%s, addFirstTask: start to add first task for %s",
		g.ServiceConnector.GetSDKContextID(), updateTask)
	select {
	case <-g.Done():
		return model.NewSDKError(model.ErrCodeInvalidStateError, nil,
			"RegisterServiceHandler: serverConnector has been destroyed")
	case g.taskChannel <- task:
		//这里先用同步来塞，到时测试下性能，不行的话就改成异步塞
		log.GetBaseLogger().Infof("%s, addFirstTask: finish add first task for %s",
			g.ServiceConnector.GetSDKContextID(), updateTask)
	}
	return nil
}

//DeRegisterEventHandler 反注册事件监听器
//异常场景：当sdk已经退出过程中，则返回error
func (g *DiscoverConnector) DeRegisterServiceHandler(key *model.ServiceEventKey) error {
	updateTask := &serviceUpdateTask{}
	updateTask.Service = key.Service
	updateTask.Namespace = key.Namespace
	updateTask.Type = key.Type
	task := &clientTask{
		updateTask: updateTask,
		op:         opDelListener}
	log.GetBaseLogger().Infof("%s, DeRegisterServiceHandler: start to add deregister task %s",
		g.ServiceConnector.GetSDKContextID(), updateTask)
	select {
	case <-g.Done():
		return model.NewSDKError(model.ErrCodeInvalidStateError, nil,
			"DeRegisterServiceHandler: serverConnector has been destroyed")
	case g.taskChannel <- task:
		//这里先用同步来塞，到时测试下性能，不行的话就改成异步塞
		log.GetBaseLogger().Infof("%s, DeRegisterServiceHandler: finish add deregister task %s",
			g.ServiceConnector.GetSDKContextID(), updateTask)
	}
	return nil
}

// 更新服务端地址
// 异常场景：当地址列表为空，或者地址全部连接失败，则返回error，调用者需进行重试
func (g *DiscoverConnector) UpdateServers(key *model.ServiceEventKey) error {
	if nil != g.connManager {
		g.connManager.UpdateServers(*key)
	} else {
		g.cachedServerServices = append(g.cachedServerServices, *key)
	}
	return nil
}

//同步进行服务或规则发现
func (g *DiscoverConnector) syncUpdateTask(task *serviceUpdateTask) (bool, error) {
	var curTime = time.Now()
	//获取服务发现server连接
	connection, err := g.connManager.GetConnection(OpKeyDiscover, task.targetCluster)
	if nil != err {
		return false, err
	}
	defer connection.Release(OpKeyDiscover)
	reqID := NextDiscoverReqID()
	discoverClient, cancel, err := g.createClient(reqID, connection, g.messageTimeout)
	if cancel != nil {
		defer cancel()
	}
	if nil != err {
		return false, err
	}
	log.GetBaseLogger().Debugf("sync stream %s created, connection %s, timeout %v",
		reqID, connection.ConnID, g.connectionIdleTimeout)
	var request = task.toDiscoverRequest()
	task.msgSendTime.Store(curTime)
	atomic.AddUint64(&task.totalRequests, 1)
	err = discoverClient.Send(request)
	if nil != err {
		log.GetBaseLogger().Errorf(
			"fail to send request for service %v, error is %+v", task.ServiceEventKey, err)
		return false, err
	}
	resp, err := discoverClient.Recv()
	var sdkErr model.SDKError
	if nil != err {
		sdkErr = model.NewSDKError(model.ErrCodeNetworkError, err,
			"error while receiving from %s(%s), reqID %s",
			connection.ConnID, connection.Address, reqID)
	} else {
		err = pb.ValidateMessage(nil, resp)
		if nil != err {
			sdkErr = model.NewSDKError(model.ErrCodeInvalidResponse, err,
				"invalid response from %s(%s), reqID %s",
				connection.ConnID, connection.Address, reqID)
		}
	}
	if nil != sdkErr {
		return false, sdkErr
	}
	//打印应答报文
	logDiscoverResponse(resp, connection)
	svcEvent, _ := discoverResponseToEvent(resp, task.ServiceEventKey, connection)
	atomic.AddUint64(&task.successUpdates, 1)
	return task.handler.OnServiceUpdate(svcEvent), nil
}
