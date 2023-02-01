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

package flow

import (
	"errors"
	"sync"
	"time"

	"github.com/modern-go/reflect2"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/flow/cbcheck"
	"github.com/polarismesh/polaris-go/pkg/flow/configuration"
	"github.com/polarismesh/polaris-go/pkg/flow/data"
	"github.com/polarismesh/polaris-go/pkg/flow/quota"
	"github.com/polarismesh/polaris-go/pkg/flow/registerstate"
	"github.com/polarismesh/polaris-go/pkg/flow/schedule"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/circuitbreaker"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/configconnector"
	"github.com/polarismesh/polaris-go/pkg/plugin/loadbalancer"
	"github.com/polarismesh/polaris-go/pkg/plugin/localregistry"
	"github.com/polarismesh/polaris-go/pkg/plugin/location"
	statreporter "github.com/polarismesh/polaris-go/pkg/plugin/metrics"
	"github.com/polarismesh/polaris-go/pkg/plugin/serverconnector"
	"github.com/polarismesh/polaris-go/pkg/plugin/servicerouter"
)

// Engine 编排调度引擎，API相关逻辑在这里执行
type Engine struct {
	// 服务端连接器
	connector serverconnector.ServerConnector
	// 服务端连接器
	configConnector configconnector.ConfigConnector
	// 服务本地缓存
	registry localregistry.LocalRegistry
	// 全局配置
	configuration config.Configuration
	// 只做过滤的服务路由插件实例
	filterOnlyRouter servicerouter.ServiceRouter
	// 服务路由责任链
	routerChain *servicerouter.RouterChain
	// 上报插件链
	reporterChain []statreporter.StatReporter
	// 负载均衡器
	loadbalancer loadbalancer.LoadBalancer
	// 限流处理协助辅助类
	flowQuotaAssistant *quota.FlowQuotaAssistant
	// 全局上下文，在reportclient
	globalCtx model.ValueContext
	// 系统服务列表
	serverServices config.ServerServices
	// 插件仓库
	plugins plugin.Supplier
	// 任务调度协程
	taskRoutines []schedule.TaskRoutine
	// 实时熔断任务队列
	rtCircuitBreakChan chan<- *model.PriorityTask
	// 实时熔断公共任务信息
	circuitBreakTask *cbcheck.CircuitBreakCallBack
	// 熔断插件链
	circuitBreakerChain []circuitbreaker.InstanceCircuitBreaker
	// 修改消息订阅插件链
	subscribe *subscribeChannel
	// 配置中心门面类
	configFileService *configuration.ConfigFileService
	// 注册状态管理器
	registerStates *registerstate.RegisterStateManager
}

// InitFlowEngine 初始化flowEngine实例
func InitFlowEngine(flowEngine *Engine, initContext plugin.InitContext) error {
	var err error
	cfg := initContext.Config
	plugins := initContext.Plugins
	globalCtx := initContext.ValueCtx
	flowEngine.configuration = cfg
	flowEngine.plugins = plugins
	// 加载服务端连接器
	flowEngine.connector, err = data.GetServerConnector(cfg, plugins)
	if err != nil {
		return err
	}
	flowEngine.serverServices = config.GetServerServices(cfg)
	// 加载本地缓存插件
	flowEngine.registry, err = data.GetRegistry(cfg, plugins)
	if err != nil {
		return err
	}
	if cfg.GetGlobal().GetStatReporter().IsEnable() {
		flowEngine.reporterChain, err = data.GetStatReporterChain(cfg, plugins)
		if err != nil {
			return err
		}
	}

	// 加载配置中心连接器
	if len(cfg.GetConfigFile().GetConfigConnectorConfig().GetAddresses()) > 0 {
		flowEngine.configConnector, err = data.GetConfigConnector(cfg, plugins)
		if err != nil {
			return err
		}
	}

	// 加载服务路由链插件
	err = flowEngine.LoadFlowRouteChain()
	if err != nil {
		return err
	}
	// 加载全局上下文
	flowEngine.globalCtx = globalCtx
	// 初始化限流缓存
	flowEngine.flowQuotaAssistant = &quota.FlowQuotaAssistant{}
	if err = flowEngine.flowQuotaAssistant.Init(flowEngine, flowEngine.configuration, flowEngine.plugins); err != nil {
		return err
	}
	// 启动健康探测
	when := cfg.GetConsumer().GetHealthCheck().GetWhen()
	disableHealthCheck := when == config.HealthCheckNever
	if !disableHealthCheck {
		if err = flowEngine.addHealthCheckTask(); err != nil {
			return err
		}
	}
	// 加载熔断器插件
	enable := cfg.GetConsumer().GetCircuitBreaker().IsEnable()
	if enable {
		flowEngine.circuitBreakerChain, err = data.GetCircuitBreakers(cfg, plugins)
		if err != nil {
			return err
		}
		flowEngine.rtCircuitBreakChan, flowEngine.circuitBreakTask, err = flowEngine.addPeriodicCircuitBreakTask()
		if err != nil {
			return err
		}
	}
	flowEngine.subscribe = &subscribeChannel{
		registerServices: []model.ServiceKey{},
		eventChannelMap:  make(map[model.ServiceKey]chan model.SubScribeEvent),
	}
	callbackHandler := common.PluginEventHandler{
		Callback: flowEngine.ServiceEventCallback,
	}
	initContext.Plugins.RegisterEventSubscriber(common.OnServiceUpdated, callbackHandler)
	globalCtx.SetValue(model.ContextKeyEngine, flowEngine)

	// 初始化配置中心服务
	if cfg.GetConfigFile().IsEnable() {
		flowEngine.configFileService = configuration.NewConfigFileService(flowEngine.configConnector, flowEngine.configuration)
	}

	// 初始注册状态管理器
	flowEngine.registerStates = registerstate.NewRegisterStateManager(flowEngine.configuration.GetProvider().GetMinRegisterInterval())
	return nil
}

// LoadFlowRouteChain 加载服务路由链插件
func (e *Engine) LoadFlowRouteChain() error {
	var err error
	e.routerChain, err = data.GetServiceRouterChain(e.configuration, e.plugins)
	if err != nil {
		return err
	}
	filterOnlyRouterPlugin, err := e.plugins.GetPlugin(common.TypeServiceRouter, config.DefaultServiceRouterFilterOnly)
	if err != nil {
		return err
	}
	e.filterOnlyRouter = filterOnlyRouterPlugin.(servicerouter.ServiceRouter)
	// 加载负载均衡插件
	e.loadbalancer, err = data.GetLoadBalancer(e.configuration, e.plugins)
	if err != nil {
		return err
	}
	return nil
}

// FlowQuotaAssistant 获取流程辅助类
func (e *Engine) FlowQuotaAssistant() *quota.FlowQuotaAssistant {
	return e.flowQuotaAssistant
}

// PluginSupplier 获取插件工厂
func (e *Engine) PluginSupplier() plugin.Supplier {
	return e.plugins
}

// WatchService watch service
func (e *Engine) WatchService(req *model.WatchServiceRequest) (*model.WatchServiceResponse, error) {
	allInsReq := &model.GetAllInstancesRequest{}
	allInsReq.Namespace = req.Key.Namespace
	allInsReq.Service = req.Key.Service
	allInsRsp, err := e.SyncGetAllInstances(allInsReq)
	if err != nil {
		return nil, err
	}
	ch, err := e.subscribe.WatchService(req.Key)
	if err != nil {
		log.GetBaseLogger().Errorf("watch service %s %s error:%s", req.Key.Namespace, req.Key.Service,
			err.Error())
		return nil, err
	}
	svcEventKey := &model.ServiceEventKey{
		ServiceKey: req.Key,
		Type:       model.EventInstances,
	}
	if err := e.registry.WatchService(svcEventKey); err != nil {
		return nil, err
	}
	watchResp := &model.WatchServiceResponse{}
	watchResp.EventChannel = ch
	watchResp.GetAllInstancesResp = allInsRsp
	return watchResp, nil
}

// GetContext 获取上下文
func (e *Engine) GetContext() model.ValueContext {
	return e.globalCtx
}

// ServiceEventCallback serviceUpdate消息订阅回调
func (e *Engine) ServiceEventCallback(event *common.PluginEvent) error {
	if e.subscribe != nil {
		if err := e.subscribe.DoSubScribe(event); err != nil {
			log.GetBaseLogger().Errorf("subscribePlugin.DoSubScribe error:%s", err.Error())
		}
	}
	return nil
}

// Start 启动引擎
func (e *Engine) Start() error {
	// 获取SDK自身所在地理位置信息
	e.loadLocation()
	// 添加客户端定期上报任务
	clientReportTaskValues, err := e.addClientReportTask()
	if err != nil {
		return err
	}
	// 添加获取系统服务的任务
	serverServiceTaskValues, err := e.addLoadServerServiceTask()
	if err != nil {
		return err
	}
	// 添加上报sdk配置任务
	configReportTaskValues := e.addSDKConfigReportTask()
	// 启动协程
	discoverSvc := e.serverServices.GetClusterService(config.DiscoverCluster)
	if nil != discoverSvc {
		schedule.StartTask(
			taskServerService, serverServiceTaskValues, map[interface{}]model.TaskValue{
				keyDiscoverService: &data.ServiceKeyComparable{SvcKey: discoverSvc.ServiceKey}})
	}
	schedule.StartTask(
		taskClientReport, clientReportTaskValues, map[interface{}]model.TaskValue{
			taskClientReport: &data.AllEqualsComparable{}})
	schedule.StartTask(
		taskConfigReport, configReportTaskValues, map[interface{}]model.TaskValue{
			taskConfigReport: &data.AllEqualsComparable{}})
	return nil
}

// getRouterChain 根据服务获取路由链
func (e *Engine) getRouterChain(svcInstances model.ServiceInstances) *servicerouter.RouterChain {
	svcInstancesProto, ok := svcInstances.(*pb.ServiceInstancesInProto)
	if ok {
		routerChain := svcInstancesProto.GetServiceRouterChain()
		if nil != routerChain {
			return routerChain
		}
	}
	return e.routerChain
}

// getLoadBalancer 根据服务获取负载均衡器
// 优先使用被调配置的负载均衡算法，其次选择用户选择的算法
func (e *Engine) getLoadBalancer(svcInstances model.ServiceInstances, chooseAlgorithm string) (
	loadbalancer.LoadBalancer, error) {
	svcInstancesProto, ok := svcInstances.(*pb.ServiceInstancesInProto)
	if ok {
		svcLoadbalancer := svcInstancesProto.GetServiceLoadbalancer()
		if !reflect2.IsNil(svcLoadbalancer) {
			return svcLoadbalancer, nil
		}
	}
	if chooseAlgorithm == "" {
		return e.loadbalancer, nil
	}
	return data.GetLoadBalancerByLbType(chooseAlgorithm, e.plugins)
}

// Destroy 销毁流程引擎
func (e *Engine) Destroy() error {
	if len(e.taskRoutines) > 0 {
		for _, routine := range e.taskRoutines {
			routine.Destroy()
		}
	}
	if e.flowQuotaAssistant != nil {
		e.flowQuotaAssistant.Destroy()
	}
	if e.configFileService != nil {
		e.configFileService.Destroy()
	}
	e.registerStates.Destroy()
	return nil
}

// SyncReportStat 上报统计数据到统计插件中
func (e *Engine) SyncReportStat(typ model.MetricType, stat model.InstanceGauge) error {
	if !model.ValidMetircType(typ) {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil, "invalid report metric type")
	}
	if len(e.reporterChain) > 0 {
		for _, reporter := range e.reporterChain {
			if err := reporter.ReportStat(typ, stat); err != nil {
				return err
			}
		}
	}
	return nil
}

// reportAPIStat 上报api数据
func (e *Engine) reportAPIStat(result *model.APICallResult) error {
	return e.SyncReportStat(model.SDKAPIStat, result)
}

// reportSvcStat 上报服务数据
func (e *Engine) reportSvcStat(result *model.ServiceCallResult) error {
	return e.SyncReportStat(model.ServiceStat, result)
}

// loadLocation 上报服务数据
func (e *Engine) loadLocation() {
	providerName := e.configuration.GetGlobal().GetLocation().GetProviders()
	if len(providerName) == 0 {
		return
	}
	locationProvider, err := e.plugins.GetPlugin(common.TypeLocationProvider, location.ProviderName)
	if err != nil {
		log.GetBaseLogger().Errorf("get location provider plugin fail, error:%v", err)
		return
	}
	loc, err := locationProvider.(location.Provider).GetLocation()
	if err != nil {
		log.GetBaseLogger().Errorf("location provider get location fail, error:%v", err)
		return
	}

	e.globalCtx.SetCurrentLocation(&model.Location{
		Region: loc.Region,
		Zone:   loc.Zone,
		Campus: loc.Campus,
	}, nil)
}

func pushToBufferChannel(event model.SubScribeEvent, ch chan model.SubScribeEvent) error {
	select {
	case ch <- event:
		return nil
	default:
		return errors.New("buffer full")
	}
}

type subscribeChannel struct {
	registerServices []model.ServiceKey
	eventChannelMap  map[model.ServiceKey]chan model.SubScribeEvent
	lock             sync.RWMutex
}

// DoSubScribe is called when a new subscription is created
func (s *subscribeChannel) DoSubScribe(event *common.PluginEvent) error {
	if event.EventType != common.OnServiceUpdated {
		return nil
	}
	serviceEvent := event.EventObject.(*common.ServiceEventObject)
	if serviceEvent.SvcEventKey.Type != model.EventInstances {
		return nil
	}
	insEvent := &model.InstanceEvent{}
	insEvent.AddEvent = data.CheckAddInstances(serviceEvent)
	insEvent.UpdateEvent = data.CheckUpdateInstances(serviceEvent)
	insEvent.DeleteEvent = data.CheckDeleteInstances(serviceEvent)
	s.lock.RLock()
	channel, ok := s.eventChannelMap[serviceEvent.SvcEventKey.ServiceKey]
	s.lock.RUnlock()
	if !ok {
		log.GetBaseLogger().Debugf("%s %s not watch", serviceEvent.SvcEventKey.ServiceKey.Namespace,
			serviceEvent.SvcEventKey.ServiceKey.Service)
		return nil
	}
	var err error
	for i := 0; i < 2; i++ {
		err = pushToBufferChannel(insEvent, channel)
		if err == nil {
			break
		} else {
			time.Sleep(time.Millisecond * 10)
		}
	}
	if err != nil {
		log.GetBaseLogger().Errorf("DoSubScribe %s %s pushToBufferChannel err:%s",
			serviceEvent.SvcEventKey.ServiceKey.Namespace, serviceEvent.SvcEventKey.ServiceKey.Service, err.Error())
	}
	return err
}

// WatchService is called when a new service is added
func (s *subscribeChannel) WatchService(key model.ServiceKey) (<-chan model.SubScribeEvent, error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	value, ok := s.eventChannelMap[key]
	if !ok {
		ch := make(chan model.SubScribeEvent, 32)
		s.eventChannelMap[key] = ch
		return ch, nil
	}
	return value, nil
}
