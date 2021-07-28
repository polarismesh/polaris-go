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

package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/clock"
	sysconfig "github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/flow/data"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/local"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/network"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/localregistry"
	"github.com/polarismesh/polaris-go/pkg/version"
	"github.com/polarismesh/polaris-go/plugin/statreporter/pb/util"
	monitorpb "github.com/polarismesh/polaris-go/plugin/statreporter/pb/v1"
	gyaml "github.com/ghodss/yaml"
	"github.com/golang/protobuf/ptypes/timestamp"
	"github.com/golang/protobuf/ptypes/wrappers"
	"github.com/google/uuid"
	"github.com/modern-go/reflect2"
	"gopkg.in/yaml.v2"
	"sync"
	"sync/atomic"
	"time"
)

const (
	opReportStat = "ReportStat"
)

//遍历统计数据进行上报
type iterateFunc func(handler *statHandler, registry localregistry.LocalRegistry)

//统计操作handler
type statHandler struct {
	reporter *Stat2MonitorReporter
	//统计类型
	metricType model.MetricType
	//增加统计数据的函数
	addStatFunc addDimensionFunc
	//发送统计数据的函数
	//sendStatFunc sendStatFunc
	//统计窗口
	apiWindow *dimensionRecord
	//遍历统计信息并发送到monitor
	iterateAndSendFunc iterateFunc
}

//获取统计窗口
func (h *statHandler) GetWindow(info model.InstanceGauge) *dimensionRecord {
	if h.metricType == model.SDKAPIStat {
		return h.apiWindow
	}
	instance := info.GetCalledInstance().(*pb.InstanceInProto)
	windowsMapIntf := instance.GetExtendedData(h.reporter.ID())
	windowsMap := windowsMapIntf.(*instanceCallWindows)
	retCode := info.GetRetCodeValue()
	windowInf, loaded := windowsMap.windows.Load(retCode)
	if !loaded {
		//window := metric.NewSliceWindow(h.reporter.Name(), h.reporter.cfg.MetricsNumBuckets*2,
		//	h.reporter.cfg.GetBucketInterval(), numKeySvc, clock.GetClock().Now().UnixNano())
		window := newDimensionRecord(numKeySvc, numKeySvcDelay)
		windowInf, loaded = windowsMap.windows.LoadOrStore(retCode, window)
	}
	atomic.StoreInt64(&windowsMap.updateTime, clock.GetClock().Now().UnixNano())
	return windowInf.(*dimensionRecord)
}

//Stat2FileReporter 打印统计日志到本地文件中
type Stat2MonitorReporter struct {
	*plugin.PluginBase
	*common.RunContext
	cfg               *Config
	hasReportConfig   uint32
	connectionManager network.ConnectionManager
	sdkStatHandler    *statHandler
	svcStatHandler    *statHandler
	//与monitor的连接
	conn       *network.Connection
	cancelFunc context.CancelFunc
	//发送sdk统计数据的stream
	sdkClient monitorpb.GrpcAPI_CollectSDKAPIStatisticsClient
	//发送服务调用统计数据的stream
	svcClient monitorpb.GrpcAPI_CollectServiceStatisticsClient
	sdkToken  model.SDKToken
	//全局上下文
	globalCtx model.ValueContext
	//全局配置marshal的字符串
	globalCfgStr atomic.Value
	//sdk加载的插件
	sdkPlugins string
	//插件工厂
	plugins plugin.Supplier
	//本地缓存插件
	registry localregistry.LocalRegistry
}

//Type 插件类型
func (s *Stat2MonitorReporter) Type() common.Type {
	return common.TypeStatReporter
}

//Name 插件名，一个类型下插件名唯一
func (s *Stat2MonitorReporter) Name() string {
	return "stat2Monitor"
}

//Init 初始化插件
func (s *Stat2MonitorReporter) Init(ctx *plugin.InitContext) error {
	s.RunContext = common.NewRunContext()
	s.globalCtx = ctx.ValueCtx
	s.plugins = ctx.Plugins
	s.PluginBase = plugin.NewPluginBase(ctx)
	cfgValue := ctx.Config.GetGlobal().GetStatReporter().GetPluginConfig(s.Name())
	if cfgValue != nil {
		s.cfg = cfgValue.(*Config)
	}
	ctx.Plugins.RegisterEventSubscriber(common.OnInstanceLocalValueCreated, common.PluginEventHandler{
		Callback: s.generateSliceWindow,
	})
	t, _ := s.globalCtx.GetValue(model.ContextKeyToken)
	s.sdkToken = t.(model.SDKToken)
	s.connectionManager = ctx.ConnManager
	s.sdkStatHandler = &statHandler{
		metricType:         model.SDKAPIStat,
		reporter:           s,
		addStatFunc:        addSDKStatToBucket,
		iterateAndSendFunc: iterateSDKStat,
	}
	//apiArraySize := allIndexSize
	s.sdkStatHandler.apiWindow = newDimensionRecord(allIndexSize, 0)
	//metric.NewSliceWindow("SDKStat",
	//s.cfg.MetricsNumBuckets*2, s.cfg.GetBucketInterval(), apiArraySize, clock.GetClock().Now().UnixNano())
	s.svcStatHandler = &statHandler{
		metricType:         model.ServiceStat,
		reporter:           s,
		addStatFunc:        addSvcStatToBucket,
		iterateAndSendFunc: iterateSvcStat,
	}
	//s.cfg.localCacheName = ctx.Config.GetConsumer().GetLocalCache().GetType()
	if err := s.getLocalRegistryAndConfigInfo(ctx); nil != err {
		return err
	}

	return nil
}

//start 启动定时协程
func (g *Stat2MonitorReporter) Start() error {
	go g.uploadStat()
	return nil
}

// enable
func (g *Stat2MonitorReporter) IsEnable(cfg sysconfig.Configuration) bool {
	if cfg.GetGlobal().GetSystem().GetMode() == model.ModeWithAgent {
		return false
	} else {
		for _, name := range cfg.GetGlobal().GetStatReporter().GetChain() {
			if name == g.Name() {
				return true
			}
		}
	}
	return false
}

// destroy
func (g *Stat2MonitorReporter) Destroy() error {
	err := g.PluginBase.Destroy()
	if err != nil {
		return err
	}
	err = g.RunContext.Destroy()
	if err != nil {
		return err
	}
	return nil
}

//ReportStat 上报统计信息
func (s *Stat2MonitorReporter) ReportStat(t model.MetricType, info model.InstanceGauge) error {
	err := s.handleStat(t, info)
	if err != nil {
		return err
	}
	return nil
}

//处理统计数据，校验正确后添加指标到window
func (s *Stat2MonitorReporter) handleStat(metricType model.MetricType, info model.InstanceGauge) error {
	var handler *statHandler
	switch metricType {
	case model.SDKAPIStat:
		handler = s.sdkStatHandler
	case model.ServiceStat:
		handler = s.svcStatHandler
	case model.SDKCfgStat:
		return s.reportSDKConfig()
	default:
		return nil
	}
	handler.GetWindow(info).AddValue(info, handler.addStatFunc)
	return nil
}

//定期上报统计数据
func (s *Stat2MonitorReporter) uploadStat() {
	t := time.NewTicker(*s.cfg.MetricsReportWindow)
	defer t.Stop()
	for {
		select {
		case <-s.Done():
			log.GetBaseLogger().Infof("uploadStat of statReporter stat_monitor has been terminated")
			return
		case <-t.C:
			//如果registry没有获取到的话，等待下一次
			registry := s.registry
			if nil == registry {
				log.GetBaseLogger().Warnf("registry not ready, wait for next period")
				continue
			}
			now := s.globalCtx.Now()
			log.GetStatReportLogger().Infof("start to upload stat info to monitor")

			//连接monitor
			monitorDeadline := now.Add(*s.cfg.MetricsReportWindow)
			s.connectMonitor(monitorDeadline)

			//遍历两种统计数据的key，并且发送到monitor中
			s.svcStatHandler.iterateAndSendFunc(s.svcStatHandler, registry)
			s.sdkStatHandler.iterateAndSendFunc(s.sdkStatHandler, registry)
			s.resetConnection()
			log.GetStatReportLogger().Infof("finish to upload stat info to monitor")
		}
	}
}

//用于上报的instanceKey
type instanceCallKey struct {
	model.InstanceKey
	resCode int32
}

//遍历sdk统计数据的key，并且进行上报
func iterateSDKStat(handler *statHandler, registry localregistry.LocalRegistry) {
	metrics := handler.GetWindow(nil).getDimension32Values(sdkDimensions)
	//m := []uint32{0}
	apiKey := model.APICallKey{
		APIName:    0,
		RetCode:    0,
		DelayRange: 0,
	}
	log.GetBaseLogger().Debugf("len, %v, upload metrics, %v", len(metrics), metrics)
	for i := 0; i < len(metrics); i++ {
		if metrics[i] > 0 {
			//m[0] = metrics[i]
			apiKey.APIName = model.ApiOperation(reveseIdx[i][apiOperationIdx])
			apiKey.DelayRange = model.ApiDelayRange(reveseIdx[i][delayIdx])
			apiKey.RetCode = model.ErrCodeFromIndex(reveseIdx[i][errcodeIdx])
			handler.reporter.sendSdkStatReq(&apiKey, metrics[i])
			//handler.sendStatFunc(&apiKey, m)
		}
	}
}

//遍历服务
func iterateSvcStat(handler *statHandler, registry localregistry.LocalRegistry) {
	services := registry.GetServices()
	instanceKey := instanceCallKey{}
	for svcValue := range services {
		instanceKey.ServiceKey = svcValue.(model.ServiceKey)
		svc := svcValue.(model.ServiceKey)
		svcInstances := registry.GetInstances(&svc, false, true)
		if !svcInstances.IsInitialized() || len(svcInstances.GetInstances()) == 0 {
			continue
		}
		actualSvcInstances := svcInstances.(*pb.ServiceInstancesInProto)
		for _, inst := range actualSvcInstances.GetInstances() {
			sliceWindowMapInf := actualSvcInstances.GetInstanceLocalValue(inst.GetId()).GetExtendedData(handler.reporter.ID())
			sliceWindowMap := sliceWindowMapInf.(*instanceCallWindows)
			if atomic.LoadInt64(&sliceWindowMap.readTime) > atomic.LoadInt64(&sliceWindowMap.updateTime) {
				continue
			}
			atomic.StoreInt64(&sliceWindowMap.readTime, clock.GetClock().Now().UnixNano())
			sliceWindowMap.windows.Range(func(resCode, sliceWindowIntf interface{}) bool {
				sliceWindow := sliceWindowIntf.(*dimensionRecord)
				if sliceWindow.IsMetricUpdate() {
					sliceWindow.SetLastReadTime()
					resMetrics := sliceWindow.getDimension32Values(svcResIdx)
					delayMetrics := sliceWindow.getDimensions64Values(svcDelayIdx)
					instanceKey.Host = inst.GetHost()
					instanceKey.Port = int(inst.GetPort())
					instanceKey.resCode = resCode.(int32)
					handler.reporter.sendSvcStatReq(&instanceKey, resMetrics, delayMetrics)
					//handler.sendStatFunc(&instanceKey, resMetrics)
				}
				return true
			})
		}
	}
}

//根据统计数据生成sdk统计请求
func (s *Stat2MonitorReporter) sendSdkStatReq(sdkKey *model.APICallKey, metric uint32) {
	//sdkKey := sdkKeyValue.(*model.APICallKey)
	id := "sdk" + uuid.New().String()
	success := true
	result := monitorpb.APIResultType_Success
	if sdkKey.RetCode != model.ErrCodeSuccess {
		success = false
		switch model.GetErrCodeType(sdkKey.RetCode) {
		case model.PolarisError:
			result = monitorpb.APIResultType_PolarisFail
		case model.UserError:
			result = monitorpb.APIResultType_UserFail
		}
	}
	key := &monitorpb.SDKAPIStatisticsKey{
		ClientHost:    &wrappers.StringValue{Value: s.connectionManager.GetClientInfo().GetIPString()},
		SdkApi:        &wrappers.StringValue{Value: sdkKey.APIName.String()},
		ResCode:       &wrappers.StringValue{Value: model.ErrCodeToString(sdkKey.RetCode)},
		Success:       &wrappers.BoolValue{Value: success},
		DelayRange:    &wrappers.StringValue{Value: sdkKey.DelayRange.String()},
		ClientVersion: &wrappers.StringValue{Value: version.Version},
		ClientType:    &wrappers.StringValue{Value: version.ClientType},
		Result:        result,
		Uid:           s.sdkToken.UID,
	}
	req := &monitorpb.SDKAPIStatistics{
		Id:  &wrappers.StringValue{Value: id},
		Key: key,
		Value: &monitorpb.Indicator{
			TotalRequestPerMinute: &wrappers.UInt32Value{
				Value: metric,
			},
		},
	}
	//log2.Printf("send sdk req, %v\n", req)
	log.GetStatLogger().Infof("%s", sdkStatToString(req))
	if nil != s.sdkClient {
		err := s.sdkClient.Send(req)
		if nil != err {
			log.GetStatReportLogger().Errorf("fail to send sdk stat data32, err: %s, monitor server is %s",
				err.Error(), s.conn.ConnID)
		} else {
			resp, err := s.sdkClient.Recv()
			if nil != err || resp.GetId().GetValue() != id || resp.GetCode().GetValue() != monitorpb.ReceiveSuccess {
				log.GetStatReportLogger().Errorf("fail to receive resp for id %v, resp is %v,"+
					" monitor server is %s", id, resp, s.conn.ConnID)
			} else {
				log.GetStatReportLogger().Infof("Success to report sdk stat, resp is %v, monitor server is %s",
					resp, s.conn.ConnID)
			}
		}
	} else {
		log.GetStatReportLogger().Warnf("Skip to report sdk stat to monitor for connection problem, req id: %s",
			req.Id.GetValue())
	}
}

//生成服务调用统计数据请求
func (s *Stat2MonitorReporter) sendSvcStatReq(dimensionKey *instanceCallKey, resMetrics []uint32,
	delayMetrics []int64) {
	//dimensionKey := dimensionKeyValue.(*instanceCallKey)
	for i, idx := range svcResIdx {
		if resMetrics[idx] > 0 {
			var dimIdx int
			var delayIndex int
			if svcSuccess[i] {
				dimIdx = keySvcSuccess
				delayIndex = keySvcSuccessDelay
			} else {
				dimIdx = keySvcFail
				delayIndex = keySvcFailDelay
			}

			id := "svc" + uuid.New().String()
			key := &monitorpb.ServiceStatisticsKey{
				CallerHost: &wrappers.StringValue{Value: s.connectionManager.GetClientInfo().GetIPString()},
				Namespace:  &wrappers.StringValue{Value: dimensionKey.Namespace},
				Service:    &wrappers.StringValue{Value: dimensionKey.Service},
				ResCode:    dimensionKey.resCode,
				InstanceHost: &wrappers.StringValue{
					Value: fmt.Sprintf("%s:%d", dimensionKey.Host, dimensionKey.Port)},
				Success: &wrappers.BoolValue{Value: svcSuccess[i]},
			}

			req := &monitorpb.ServiceStatistics{
				Id:       &wrappers.StringValue{Value: id},
				SdkToken: util.GetPBSDkToken(s.sdkToken),
				Key:      key,
				Value: &monitorpb.ServiceIndicator{
					TotalRequestPerMinute: &wrappers.UInt32Value{Value: resMetrics[dimIdx]},
					TotalDelayPerMinute: &wrappers.UInt64Value{
						//发送的延迟时间以ms为单位
						Value: uint64(time.Duration(delayMetrics[delayIndex]).Nanoseconds() / 1000000),
					},
				},
			}
			if req.SdkToken.Ip == "" {
				req.SdkToken.Ip = s.connectionManager.GetClientInfo().GetIPString()
			}

			log.GetStatLogger().Infof("uid: %s | %s", s.sdkToken.UID, svcStatToString(req))
			if nil != s.svcClient {
				err := s.svcClient.Send(req)
				if nil != err {
					log.GetStatReportLogger().Errorf("fail to send sdk service data32, err: %s, monitor server is %s",
						err.Error(), s.conn.ConnID)
				} else {
					resp, err := s.svcClient.Recv()
					if nil != err || resp.GetId().GetValue() != id || resp.GetCode().GetValue() != monitorpb.ReceiveSuccess {
						log.GetStatReportLogger().Errorf("fail to reveice resp for id %v, resp is %v,"+
							" monitor server is %s", id, resp, s.conn.ConnID)
					} else {
						log.GetStatReportLogger().Infof("Success to report service stat, resp is %v,"+
							" monitor server is %s", resp, s.conn.ConnID)
					}
				}
			} else {
				log.GetStatReportLogger().Warnf("Skip to report service stat to monitor"+
					" for connection problem, req id:%s", req.Id.GetValue())
			}
		}
	}
}

//创建与monitor的连接
func (s *Stat2MonitorReporter) connectMonitor(monitorDeadline time.Time) {
	var err error
	s.conn, err = s.connectionManager.GetConnection(opReportStat, sysconfig.MonitorCluster)
	if nil != err {
		log.GetStatReportLogger().Errorf("fail to connect to monitor, err: %s", err.Error())
		return
	}
	client := monitorpb.NewGrpcAPIClient(network.ToGRPCConn(s.conn.Conn))
	ctx, c := context.WithDeadline(context.Background(), monitorDeadline)
	s.cancelFunc = c
	s.sdkClient, err = client.CollectSDKAPIStatistics(ctx)
	if nil != err {
		log.GetStatReportLogger().Errorf("fail to create stream to report sdk stat, err: %s", err.Error())
	}
	s.svcClient, err = client.CollectServiceStatistics(ctx)
	if nil != err {
		log.GetStatReportLogger().Errorf("fail to create stream to report service stat, err: %s", err.Error())
	}
}

//关闭与monitor的连接
func (s *Stat2MonitorReporter) resetConnection() {
	if nil != s.sdkClient {
		s.sdkClient.CloseSend()
	}

	if nil != s.svcClient {
		s.svcClient.CloseSend()
	}

	if nil != s.cancelFunc {
		s.cancelFunc()
		s.cancelFunc = nil
	}

	if nil != s.conn {
		s.conn.Release(opReportStat)
	}
	s.sdkClient = nil
	s.svcClient = nil
}

//实例的滑窗集合，不同的返回码都有一个对应滑窗
type instanceCallWindows struct {
	windows    *sync.Map
	updateTime int64
	readTime   int64
}

//为实例生成服务调用滑窗
func (s *Stat2MonitorReporter) generateSliceWindow(event *common.PluginEvent) error {
	localValue := event.EventObject.(*local.DefaultInstanceLocalValue)
	localValue.SetExtendedData(s.ID(), &instanceCallWindows{
		windows: &sync.Map{},
	})
	return nil
}

//设置本地缓存插件和全局配置转化的字符串
func (s *Stat2MonitorReporter) getLocalRegistryAndConfigInfo(ctx *plugin.InitContext) error {
	s.registry, _ = data.GetRegistry(ctx.Config, ctx.Plugins)
	yamlByte, _ := yaml.Marshal(ctx.Config)
	cfgByte, err := gyaml.YAMLToJSON(yamlByte)
	if nil != err {
		return model.NewSDKError(model.ErrCodeAPIInvalidConfig, err, "fail to convert yaml-format Config to json")
	}
	s.globalCfgStr.Store(string(cfgByte))
	//s.globalCfgTime.Store(clock.GetClock().Now())
	pluginMap := make(map[string][]string)
	for _, typ := range common.LoadedPluginTypes {
		pluginNames := s.plugins.GetPluginsByType(typ)
		if len(pluginNames) > 0 {
			pluginMap[typ.String()] = pluginNames
		}
	}
	pluginBytes, _ := json.Marshal(pluginMap)
	s.sdkPlugins = string(pluginBytes)
	return nil
}

//上报sdk配置
func (s *Stat2MonitorReporter) reportSDKConfig() error {
	globalCfgStr, takeEffectTime, initFinishTime := s.getConfigInfo()
	if len(globalCfgStr) == 0 {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil, "global Config info not ready")
	}
	//如果discover没有ready，那么首次不要进行上报配置
	if atomic.CompareAndSwapUint32(&s.hasReportConfig, 0, 1) && !s.connectionManager.IsReady() {
		log.GetStatReportLogger().Infof("%s, discover not ready, sleep 1s to wait discover ready to report config",
			s.GetSDKContextID())
		wait := time.After(1 * time.Second)
		select {
		case <-s.Done():
			return model.NewSDKError(model.ErrCodeInvalidStateError, nil, "SDK context has destroyed")
		case <-wait:
			break
		}
		if !s.connectionManager.IsReady() {
			log.GetStatReportLogger().Errorf("%s, after sleep 1s, discover still not ready, skip first report config",
				s.GetSDKContextID())
			return nil
		}
	}
	conn, err := s.connectionManager.GetConnection(opReportStat, sysconfig.MonitorCluster)
	if nil != err {
		log.GetStatReportLogger().Errorf("fail to connect to monitor, err: %s", err.Error())
		return model.NewSDKError(model.ErrCodeNetworkError, err, "fail to connect to polaris.monitor")
	}
	defer conn.Release(opReportStat)
	reportClient := monitorpb.NewGrpcAPIClient(network.ToGRPCConn(conn.Conn))
	//t, _ := s.globalCtx.GetValue(model.ContextKeyToken)
	//sdkToken := t.(model.SDKToken)
	now := clock.GetClock().Now()
	req := &monitorpb.SDKConfig{
		Token:          util.GetPBSDkToken(s.sdkToken),
		Config:         globalCfgStr,
		TakeEffectTime: &timestamp.Timestamp{Seconds: takeEffectTime.Unix(), Nanos: int32(takeEffectTime.Nanosecond())},
		InitFinishTime: &timestamp.Timestamp{Seconds: initFinishTime.Unix(), Nanos: int32(initFinishTime.Nanosecond())},
		ReportTime:     &timestamp.Timestamp{Seconds: now.Unix(), Nanos: int32(now.Nanosecond())},
		Client:         version.ClientType,
		Version:        version.Version,
		Plugins:        s.sdkPlugins,
	}
	if len(s.sdkToken.IP) == 0 {
		req.Token.Ip = s.connectionManager.GetClientInfo().GetIPString()
	}
	location := s.globalCtx.GetCurrentLocation().GetLocation()
	if nil != location {
		req.Location = location.String()
	}
	log.GetStatLogger().Infof("Config of %v, client %s, version %s, location %s plugins %s config: %s",
		req.Token, req.GetClient(), req.GetVersion(), req.GetLocation(), req.GetConfig(), req.GetPlugins())
	ctx, cancel := context.WithDeadline(context.Background(), s.globalCtx.Now().Add(*s.cfg.MetricsReportWindow))
	defer cancel()
	resp, err := reportClient.CollectSDKConfiguration(ctx, req)
	if nil != err || resp.GetId().GetValue() != req.Token.Uid ||
		resp.GetCode().GetValue() != monitorpb.ReceiveSuccess {
		err = model.NewSDKError(model.ErrCodeInvalidResponse, err, "invalid resp from monitor")
		log.GetStatReportLogger().Errorf("fail to reveice resp for Config of  %v, resp is %v, monitor server is %s",
			req.Token, resp, conn.ConnID)
	} else {
		log.GetStatReportLogger().Infof("Success to report sdk Config, resp is %v, monitor server is %s", resp, conn.ConnID)
	}
	return err
}

//获取配置相关信息
func (s *Stat2MonitorReporter) getConfigInfo() (string, time.Time, time.Time) {
	str := s.globalCfgStr.Load()
	var cfgStr string
	if !reflect2.IsNil(str) {
		cfgStr = str.(string)
	}
	var startTime, endTime time.Time
	sdkTime, ok := s.globalCtx.GetValue(model.ContextKeyTakeEffectTime)
	if ok {
		startTime = sdkTime.(time.Time)
	}
	sdkTime, ok = s.globalCtx.GetValue(model.ContextKeyFinishInitTime)
	if ok {
		endTime = sdkTime.(time.Time)
	}
	return cfgStr, startTime, endTime
}

func (s *Stat2MonitorReporter) createSvcLocalValue(event *common.PluginEvent) error {
	lv := event.EventObject.(local.ServiceLocalValue)
	lv.SetServiceDataByPluginId(s.ID(), &sync.Map{})
	return nil
}

//init 注册插件
func init() {
	plugin.RegisterConfigurablePlugin(&Stat2MonitorReporter{}, &Config{})
	//plugin.RegisterPlugin(&Stat2MonitorReporter{})
}
