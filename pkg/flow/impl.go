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
	"github.com/modern-go/reflect2"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/flow/cbcheck"
	"github.com/polarismesh/polaris-go/pkg/flow/data"
	"github.com/polarismesh/polaris-go/pkg/flow/quota"
	"github.com/polarismesh/polaris-go/pkg/flow/schedule"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/circuitbreaker"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/loadbalancer"
	"github.com/polarismesh/polaris-go/pkg/plugin/localregistry"
	"github.com/polarismesh/polaris-go/pkg/plugin/serverconnector"
	"github.com/polarismesh/polaris-go/pkg/plugin/servicerouter"
	"github.com/polarismesh/polaris-go/pkg/plugin/statreporter"
	"github.com/polarismesh/polaris-go/pkg/plugin/subscribe"
)

/**
 * @brief 编排调度引擎，API相关逻辑在这里执行
 */
type Engine struct {
	//服务端连接器
	connector serverconnector.ServerConnector
	//服务本地缓存
	registry localregistry.LocalRegistry
	//全局配置
	configuration config.Configuration
	//只做过滤的服务路由插件实例
	filterOnlyRouter servicerouter.ServiceRouter
	//服务路由责任链
	routerChain *servicerouter.RouterChain
	//上报插件链
	reporterChain []statreporter.StatReporter
	//负载均衡器
	loadbalancer loadbalancer.LoadBalancer
	//限流处理协助辅助类
	flowQuotaAssistant *quota.FlowQuotaAssistant
	//全局上下文，在reportclient
	globalCtx model.ValueContext
	//系统服务列表
	serverServices config.ServerServices
	//插件仓库
	plugins plugin.Supplier
	//任务调度协程
	taskRoutines []schedule.TaskRoutine
	//实时熔断任务队列
	rtCircuitBreakChan chan<- *model.PriorityTask
	//实时熔断公共任务信息
	circuitBreakTask *cbcheck.CircuitBreakCallBack
	//熔断插件链
	circuitBreakerChain []circuitbreaker.InstanceCircuitBreaker
	//修改消息订阅插件链
	subscribe subscribe.Subscribe
}

//InitFlowEngine 初始化flowEngine实例
func InitFlowEngine(flowEngine *Engine, initContext plugin.InitContext) error {
	var err error
	cfg := initContext.Config
	plugins := initContext.Plugins
	globalCtx := initContext.ValueCtx
	flowEngine.configuration = cfg
	flowEngine.plugins = plugins
	//加载服务端连接器
	flowEngine.connector, err = data.GetServerConnector(cfg, plugins)
	if nil != err {
		return err
	}
	flowEngine.serverServices = config.GetServerServices(cfg)
	//加载本地缓存插件
	flowEngine.registry, err = data.GetRegistry(cfg, plugins)
	if nil != err {
		return err
	}
	if cfg.GetGlobal().GetStatReporter().IsEnable() {
		flowEngine.reporterChain, err = data.GetStatReporterChain(cfg, plugins)
		if nil != err {
			return err
		}
	}
	//加载服务路由链插件
	err = flowEngine.LoadFlowRouteChain()
	if err != nil {
		return err
	}
	//初始化限流缓存
	flowEngine.flowQuotaAssistant = &quota.FlowQuotaAssistant{}
	if err = flowEngine.flowQuotaAssistant.Init(flowEngine, flowEngine.configuration, flowEngine.plugins); nil != err {
		return err
	}
	//加载全局上下文
	flowEngine.globalCtx = globalCtx
	//启动健康探测
	when := cfg.GetConsumer().GetHealthCheck().GetWhen()
	disableHealthCheck := when == config.HealthCheckNever
	if !disableHealthCheck {
		if err = flowEngine.addHealthCheckTask(); nil != err {
			return err
		}
	}
	//加载熔断器插件
	enable := cfg.GetConsumer().GetCircuitBreaker().IsEnable()
	if enable {
		flowEngine.circuitBreakerChain, err = data.GetCircuitBreakers(cfg, plugins)
		if nil != err {
			return err
		}
		flowEngine.rtCircuitBreakChan, flowEngine.circuitBreakTask, err = flowEngine.addPeriodicCircuitBreakTask()
		if nil != err {
			return err
		}
	}
	//加载消息订阅插件
	pluginName := cfg.GetConsumer().GetSubScribe().GetType()
	p, err := flowEngine.plugins.GetPlugin(common.TypeSubScribe, pluginName)
	if err != nil {
		return err
	}
	sP := p.(subscribe.Subscribe)
	flowEngine.subscribe = sP
	callbackHandler := common.PluginEventHandler{
		Callback: flowEngine.ServiceEventCallback,
	}
	initContext.Plugins.RegisterEventSubscriber(common.OnServiceUpdated, callbackHandler)
	globalCtx.SetValue(model.ContextKeyEngine, flowEngine)
	return nil
}

// 加载服务路由链插件
func (e *Engine) LoadFlowRouteChain() error {
	var err error
	e.routerChain, err = data.GetServiceRouterChain(e.configuration, e.plugins)
	if nil != err {
		return err
	}
	filterOnlyRouterPlugin, err := e.plugins.GetPlugin(common.TypeServiceRouter, config.DefaultServiceRouterFilterOnly)
	if nil != err {
		return err
	}
	e.filterOnlyRouter = filterOnlyRouterPlugin.(servicerouter.ServiceRouter)
	//加载负载均衡插件
	e.loadbalancer, err = data.GetLoadBalancer(e.configuration, e.plugins)
	if nil != err {
		return err
	}
	return nil
}

//获取流程辅助类
func (e *Engine) FlowQuotaAssistant() *quota.FlowQuotaAssistant {
	return e.flowQuotaAssistant
}

//获取插件工厂
func (e *Engine) PluginSupplier() plugin.Supplier {
	return e.plugins
}

// watch service
func (e *Engine) WatchService(req *model.WatchServiceRequest) (*model.WatchServiceResponse, error) {
	if e.subscribe != nil {
		allInsReq := &model.GetAllInstancesRequest{}
		allInsReq.Namespace = req.Key.Namespace
		allInsReq.Service = req.Key.Service
		allInsRsp, err := e.SyncGetAllInstances(allInsReq)
		if err != nil {
			return nil, err
		}
		v, err := e.subscribe.WatchService(req.Key)
		if err != nil {
			log.GetBaseLogger().Errorf("watch service %s %s error:%s", req.Key.Namespace, req.Key.Service,
				err.Error())
			return nil, err
		}
		svcEventKey := &model.ServiceEventKey{
			ServiceKey: req.Key,
			Type:       model.EventInstances,
		}
		err = e.registry.WatchService(svcEventKey)
		if err != nil {
			return nil, err
		}
		watchResp := &model.WatchServiceResponse{}
		if e.subscribe.Name() == config.SubscribeLocalChannel {
			watchResp.EventChannel = v.(chan model.SubScribeEvent)
		} else {
			watchResp.EventChannel = nil
		}
		watchResp.GetAllInstancesResp = allInsRsp
		return watchResp, nil
	} else {
		return nil, model.NewSDKError(model.ErrCodeInternalError, nil, "engine subscribe is nil")
	}
}

func (e *Engine) GetContext() model.ValueContext {
	return e.globalCtx
}

//serviceUpdate消息订阅回调
func (e *Engine) ServiceEventCallback(event *common.PluginEvent) error {
	if e.subscribe != nil {
		err := e.subscribe.DoSubScribe(event)
		if err != nil {
			log.GetBaseLogger().Errorf("subscribePlugin.DoSubScribe name:%s error:%s",
				e.subscribe.Name(), err.Error())
		}
	}
	return nil
}

//启动引擎
func (e *Engine) Start() error {
	//添加客户端定期上报任务
	clientReportTaskValues, err := e.addClientReportTask()
	if nil != err {
		return err
	}
	//添加获取系统服务的任务
	serverServiceTaskValues, err := e.addLoadServerServiceTask()
	if nil != err {
		return err
	}
	//添加上报sdk配置任务
	configReportTaskValues := e.addSDKConfigReportTask()
	//启动协程
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

//根据服务获取路由链
func (e *Engine) getRouterChain(svcInstances model.ServiceInstances) *servicerouter.RouterChain {
	svcInstancesProto := svcInstances.(*pb.ServiceInstancesInProto)
	routerChain := svcInstancesProto.GetServiceRouterChain()
	if nil == routerChain {
		return e.routerChain
	}
	return routerChain
}

//根据服务获取负载均衡器
//优先使用被调配置的负载均衡算法，其次选择用户选择的算法
func (e *Engine) getLoadBalancer(svcInstances model.ServiceInstances, chooseAlgorithm string) (
	loadbalancer.LoadBalancer, error) {
	svcInstancesProto := svcInstances.(*pb.ServiceInstancesInProto)
	svcLoadbalancer := svcInstancesProto.GetServiceLoadbalancer()
	if reflect2.IsNil(svcLoadbalancer) {
		if chooseAlgorithm == "" {
			return e.loadbalancer, nil
		} else {
			return data.GetLoadBalancerByLbType(chooseAlgorithm, e.plugins)
		}
	}
	return svcLoadbalancer, nil
}

//Destroy 销毁流程引擎
func (e *Engine) Destroy() error {
	if len(e.taskRoutines) > 0 {
		for _, routine := range e.taskRoutines {
			routine.Destroy()
		}
	}
	if e.flowQuotaAssistant != nil {
		e.flowQuotaAssistant.Destroy()
	}
	return nil
}

//上报统计数据到统计插件中
func (e *Engine) SyncReportStat(typ model.MetricType, stat model.InstanceGauge) error {
	if !model.ValidMetircType(typ) {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil, "invalid report metric type")
	}
	if len(e.reporterChain) > 0 {
		for _, reporter := range e.reporterChain {
			err := reporter.ReportStat(typ, stat)
			if nil != err {
				return err
			}
		}
	}
	return nil
}

//上报api数据
func (e *Engine) reportAPIStat(result *model.APICallResult) error {
	return e.SyncReportStat(model.SDKAPIStat, result)
}

//上报服务数据
func (e *Engine) reportSvcStat(result *model.ServiceCallResult) error {
	return e.SyncReportStat(model.ServiceStat, result)
}
