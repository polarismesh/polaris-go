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

package inmemory

import (
	"fmt"
	"github.com/golang/protobuf/proto"
	"github.com/modern-go/reflect2"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/local"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/loadbalancer"
	"github.com/polarismesh/polaris-go/pkg/plugin/localregistry"
	"github.com/polarismesh/polaris-go/pkg/plugin/serverconnector"
	"github.com/polarismesh/polaris-go/pkg/plugin/servicerouter"
	lrplug "github.com/polarismesh/polaris-go/plugin/localregistry/common"
	"sync/atomic"

	"sync"
	"time"
)

const (
	name               = "inmemory"
	emptyReplaceHolder = "<empty>"
)

var (
	emptyInstance   = pb.NewServiceInstancesInProto(nil, nil, nil, nil)
	emptyRule       = pb.NewServiceRuleInProto(nil)
	emptyMeshConfig = pb.NewMeshConfigInProto(nil)
	emptyMesh       = pb.NewMeshInProto(nil)
)

var (
	//查询对象池
	svcEventPool = &sync.Pool{}
)

//LocalCache 基于内存的本地缓存策略
type LocalCache struct {
	*plugin.PluginBase
	*common.RunContext
	//这个锁的只有在服务新增或者删除时候触发，频率较小
	servicesMutex *sync.RWMutex
	services      model.HashSet

	serviceMap *sync.Map

	connector              serverconnector.ServerConnector
	serviceRefreshInterval time.Duration
	serviceExpireTime      time.Duration
	persistDir             string
	persistTasks           *sync.Map
	persistTaskChan        chan struct{}
	cachePersistHandler    *lrplug.CachePersistHandler
	eventToCacheHandlers   map[model.EventType]CacheHandlers
	//系统服务集合，用于比对本地缓存
	serverServicesSet map[model.ServiceKey]clusterAndInterval
	//全局配置
	globalConfig config.Configuration
	globalCtx    model.ValueContext
	//插件工厂
	plugins plugin.Supplier
	//主流程engine
	engine model.Engine
	//服务到服务级插件映射
	svcToPluginValues map[model.ServiceKey]*pb.SvcPluginValues
	//命名空间到服务级插件映射，比如针对Polaris命名空间下的服务，都使用元数据路由
	namespaceToPluginValues map[string]*pb.SvcPluginValues
	//首次拉取是否使用缓存文件
	startUseFileCache bool
	//缓存文件的有效时间
	cacheFromPersistAvailableInterval time.Duration
}

//系统服务集群及刷新间隔信息
type clusterAndInterval struct {
	clsType  config.ClusterType
	interval time.Duration
}

//Type 插件类型
func (g *LocalCache) Type() common.Type {
	return common.TypeLocalRegistry
}

//Name 插件名，一个类型下插件名唯一
func (g *LocalCache) Name() string {
	return name
}

// destroy
func (g *LocalCache) Destroy() error {
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

////构建单个系统服务信息
//func (g *LocalCache) buildServerService(cluster config.ServerClusterConfig, clsType config.ClusterType) {
//	svcKey := config.ServiceClusterToServiceKey(cluster)
//	g.serverServicesSet[svcKey] = clusterAndInterval{
//		clsType:  clsType,
//		interval: cluster.GetRefreshInterval(),
//	}
//}

//构建系统服务集合
func (g *LocalCache) buildServerServiceSet(clsTypeToConfig map[config.ClusterType]config.ClusterService) {
	g.serverServicesSet = make(map[model.ServiceKey]clusterAndInterval, 0)
	for clsType, clsConfig := range clsTypeToConfig {
		g.serverServicesSet[clsConfig.ServiceKey] = clusterAndInterval{
			clsType:  clsType,
			interval: clsConfig.ClusterConfig.GetRefreshInterval(),
		}
	}
}

//Init 初始化插件
func (g *LocalCache) Init(ctx *plugin.InitContext) error {
	g.RunContext = common.NewRunContext()
	g.PluginBase = plugin.NewPluginBase(ctx)
	protocol := ctx.Config.GetGlobal().GetServerConnector().GetProtocol()
	connectorPlugin, err := ctx.Plugins.GetPlugin(common.TypeServerConnector, protocol)
	if nil != err {
		return err
	}
	g.globalConfig = ctx.Config
	g.services = model.HashSet{}
	g.servicesMutex = &sync.RWMutex{}
	g.serviceRefreshInterval = ctx.Config.GetConsumer().GetLocalCache().GetServiceRefreshInterval()
	g.serviceExpireTime = ctx.Config.GetConsumer().GetLocalCache().GetServiceExpireTime()
	g.persistDir = model.ReplaceHomeVar(ctx.Config.GetConsumer().GetLocalCache().GetPersistDir())
	log.GetBaseLogger().Infof("LocalCache Real persistDir:%s", g.persistDir)
	g.persistTasks = &sync.Map{}
	g.persistTaskChan = make(chan struct{}, 1)
	g.connector = connectorPlugin.(serverconnector.ServerConnector)
	g.serviceMap = &sync.Map{}
	g.eventToCacheHandlers = make(map[model.EventType]CacheHandlers, 0)
	g.eventToCacheHandlers[model.EventInstances] = g.newServiceCacheHandler()
	g.eventToCacheHandlers[model.EventRouting] = g.newRuleCacheHandler()
	g.eventToCacheHandlers[model.EventRateLimiting] = g.newRateLimitCacheHandler()
	//mesh
	g.eventToCacheHandlers[model.EventMeshConfig] = g.newMeshConfigHandler()
	g.eventToCacheHandlers[model.EventMesh] = g.newMeshHandler()
	//批量服务
	g.eventToCacheHandlers[model.EventServices] = g.newServicesHandler()
	g.cachePersistHandler, err = lrplug.NewCachePersistHandler(
		g.persistDir,
		ctx.Config.GetConsumer().GetLocalCache().GetPersistMaxWriteRetry(),
		ctx.Config.GetConsumer().GetLocalCache().GetPersistMaxReadRetry(),
		ctx.Config.GetConsumer().GetLocalCache().GetPersistRetryInterval())
	g.cacheFromPersistAvailableInterval = ctx.Config.GetConsumer().GetLocalCache().GetPersistAvailableInterval()
	if nil != err {
		return err
	}
	g.plugins = ctx.Plugins
	g.globalCtx = ctx.ValueCtx
	clsTypeToSvcConfigs := config.GetServerServices(ctx.Config)
	g.svcToPluginValues = make(map[model.ServiceKey]*pb.SvcPluginValues, len(clsTypeToSvcConfigs))
	for clsType, svcConfig := range clsTypeToSvcConfigs {
		g.svcToPluginValues[svcConfig.ServiceKey] = g.toPluginValues(clsType)
	}
	g.namespaceToPluginValues = make(map[string]*pb.SvcPluginValues)
	g.namespaceToPluginValues[config.ServerNamespace] = g.toNamespacePluginValues()
	g.buildServerServiceSet(clsTypeToSvcConfigs)
	g.startUseFileCache = ctx.Config.GetConsumer().GetLocalCache().GetStartUseFileCache()
	return nil
}

//打印有问题的cacheObject
func (g *LocalCache) logServiceMap() {
	logTicker := time.NewTicker(5 * time.Minute)
	defer logTicker.Stop()
	for {
		select {
		case <-g.Done():
			log.GetBaseLogger().Infof("logServiceMap of inmemory localRegistry has been terminated")
			return
		case <-logTicker.C:
			g.serviceMap.Range(func(k, v interface{}) bool {
				svcKey := k.(model.ServiceEventKey)
				cacheObj := v.(*CacheObject)
				if reflect2.IsNil(cacheObj.value.Load()) {
					log.GetBaseLogger().Warnf("%s, logServiceMap: %s cacheObject has nil value, createTime, %v,"+
						" hasRegistered, %d", g.GetSDKContextID(), svcKey, cacheObj.createTime,
						atomic.LoadUint32(&cacheObj.hasRegistered))
				}
				return true
			})
		}
	}
}

//启动插件
func (g *LocalCache) Start() error {
	g.loadCacheFromFiles()
	go g.eliminateExpiredCache()
	go g.logServiceMap()
	return nil
}

//GetInstances 获取服务实例列表
func (g *LocalCache) GetInstances(svcKey *model.ServiceKey, includeCache bool,
	isInternalRequest bool) model.ServiceInstances {
	eventKey := poolGetSvcEventKey(svcKey, model.EventInstances)
	value, ok := g.serviceMap.Load(*eventKey)
	poolPutSvcEventKey(eventKey)
	if !ok {
		return emptyInstance
	}
	cacheObj := value.(*CacheObject)
	instances := g.getInstances(cacheObj, isInternalRequest)
	if nil == instances {
		return emptyInstance
	}

	if atomic.LoadUint32(&cacheObj.hasRemoteUpdated) > 0 {
		return instances
	}

	if includeCache {
		return instances
	}

	if g.startUseFileCache && atomic.LoadUint32(&cacheObj.cachePersistentAvailable) > 0 {
		return instances
	}

	//如果该对象没有经过connector的更新，并且includeCache和isInternalRequest都为false，不返回缓存的值，返回emptyInstance
	//if !includeCache && !isInternalRequest && atomic.LoadUint32(&cacheObj.hasRemoteUpdated) == 0 {
	//	return emptyInstance
	//	//_, isSystemSvc := g.serverServicesSet[*svcKey]
	//	//if !isSystemSvc {
	//	//	log.GetBaseLogger().Debugf("reject request for %s, because not remote updated\n", eventKey)
	//	//	return emptyInstance
	//	//}
	//	//log.GetBaseLogger().Debugf("return not remote updated system service %s", eventKey)
	//}
	return emptyInstance
}

//获取服务实例列表
func (g *LocalCache) getInstances(cacheObject *CacheObject, isInternalRequest bool) *pb.ServiceInstancesInProto {
	value := cacheObject.LoadValue(!isInternalRequest)
	if nil == value {
		return nil
	}
	return value.(*pb.ServiceInstancesInProto)
}

//增加服务名
func (g *LocalCache) addServiceToSet(svcKey *model.ServiceEventKey) {
	if svcKey.Type == model.EventInstances {
		g.servicesMutex.Lock()
		defer g.servicesMutex.Unlock()
		g.services.Add(svcKey.ServiceKey)
	}
}

//删除服务名
func (g *LocalCache) deleteServiceFromSet(svcKey *model.ServiceEventKey) {
	g.servicesMutex.Lock()
	defer g.servicesMutex.Unlock()
	g.services.Delete(svcKey.ServiceKey)
}

//删除服务信息，包括从注销监听和删除本地缓存信息
func (g *LocalCache) deleteService(svcKey *model.ServiceEventKey, oldValue interface{}) {
	//log.GetBaseLogger().Infof("service %s has been cleared", *svcKey)
	log.GetBaseLogger().Infof("%s, deregister %s", g.GetSDKContextID(), svcKey)
	g.connector.DeRegisterServiceHandler(svcKey)
	g.serviceMap.Delete(*svcKey)
	g.deleteServiceFromSet(svcKey)
	svcCacheFile := lrplug.ServiceEventKeyToFileName(*svcKey)
	g.persistTasks.Store(svcCacheFile, &persistTask{
		op:       deleteCache,
		protoMsg: nil,
	})
}

//服务实例是否已经更新
func compareServiceInstances(instValue interface{}, newValue proto.Message) CachedStatus {
	var oldRevision string
	var oldInstances model.ServiceInstances
	var oldInstancesCount = 0
	var resp = newValue.(*namingpb.DiscoverResponse)
	//判断server的错误码，是否未变更
	if resp.GetCode().GetValue() == namingpb.DataNoChange {
		if reflect2.IsNil(instValue) {
			return CacheEmptyButNoData
		}
		return CacheNotChanged
	}
	var newRevision = resp.GetService().GetRevision().GetValue()
	if len(newRevision) == 0 {
		log.GetBaseLogger().Warnf("empty revision from remote query instances"+
			", service is %s::%s", resp.GetService().GetNamespace().GetValue(), resp.GetService().GetName().GetValue())
	}
	var status CachedStatus
	if reflect2.IsNil(instValue) {
		oldRevision = emptyReplaceHolder
		status = CacheNotExists
		goto finally
	}
	oldInstances = instValue.(model.ServiceInstances)
	oldRevision = oldInstances.GetRevision()
	oldInstancesCount = len(oldInstances.GetInstances())
	if oldRevision != newRevision {
		status = CacheChanged
		goto finally
	}
	status = CacheNotChanged
finally:
	if status != CacheNotChanged {
		log.GetBaseLogger().Infof(
			"service instances %s::%s has updated, compare status %s, "+
				"old revision is %s, old instances count is %d, new revision is %s, new instances count is %d",
			resp.GetService().GetNamespace().GetValue(), resp.GetService().GetName().GetValue(), status,
			oldRevision, oldInstancesCount, newRevision, len(resp.Instances))
	} else {
		log.GetBaseLogger().Debugf(
			"service instances %s::%s is not updated, compare status %s, "+
				"old revision is %s, old instances count is %d, new revision is %s, new instances count is %d",
			resp.GetService().GetNamespace().GetValue(), resp.GetService().GetName().GetValue(), status,
			oldRevision, oldInstancesCount, newRevision, len(resp.Instances))
	}
	return status
}

//创建默认的实例本地信息
func (g *LocalCache) CreateDefaultInstanceLocalValue(instId string) local.InstanceLocalValue {
	newLocalValue := local.NewInstanceLocalValue()
	eventHandlers := g.plugins.GetEventSubscribers(common.OnInstanceLocalValueCreated)
	if len(eventHandlers) == 0 {
		return newLocalValue
	}
	event := &common.PluginEvent{
		EventType: common.OnInstanceLocalValueCreated, EventObject: newLocalValue}
	for _, handler := range eventHandlers {
		handler.Callback(event)
	}
	return newLocalValue
}

//PB对象转服务实例对象
func (g *LocalCache) messageToServiceInstances(cachedValue interface{}, value proto.Message,
	svcLocalValue local.ServiceLocalValue, cacheLoaded bool) model.RegistryValue {
	respInProto := value.(*namingpb.DiscoverResponse)
	svcKey := model.ServiceKey{
		Service:   respInProto.GetService().GetName().GetValue(),
		Namespace: respInProto.GetService().GetNamespace().GetValue(),
	}
	var pluginValues *pb.SvcPluginValues
	var ok bool
	pluginValues, ok = g.svcToPluginValues[svcKey]
	if !ok {
		pluginValues, ok = g.namespaceToPluginValues[svcKey.Namespace]
	}
	if nil == pluginValues {
		pluginValues = &pb.SvcPluginValues{}
	}
	var createLocalValueFunc = g.CreateDefaultInstanceLocalValue
	if !reflect2.IsNil(cachedValue) {
		svcInsts := cachedValue.(*pb.ServiceInstancesInProto)
		createLocalValueFunc = func(instId string) local.InstanceLocalValue {
			localValue := svcInsts.GetInstanceLocalValue(instId)
			if nil != localValue {
				return localValue
			}
			newLocalValue := g.CreateDefaultInstanceLocalValue("")
			return newLocalValue
		}
	}
	svcInstances := pb.NewServiceInstancesInProto(respInProto, createLocalValueFunc, pluginValues, svcLocalValue)
	if cacheLoaded {
		svcInstances.CacheLoaded = 1
	}
	return svcInstances
}

//转换为北极星命名空间下的插件链
func (g *LocalCache) toNamespacePluginValues() *pb.SvcPluginValues {
	values := &pb.SvcPluginValues{}
	for _, router := range config.DefaultPolarisServicesRouterChain {
		routePlugin, err := g.plugins.GetPlugin(common.TypeServiceRouter, router)
		if nil != err {
			log.GetBaseLogger().Errorf("fail to lookup plugin %s, error %v", router, err)
			continue
		}
		if nil == values.Routers {
			values.Routers = &servicerouter.RouterChain{}
		}
		values.Routers.Chain = append(values.Routers.Chain, routePlugin.(servicerouter.ServiceRouter))
	}
	return values
}

//转换为服务级插件链
func (g *LocalCache) toPluginValues(clsType config.ClusterType) *pb.SvcPluginValues {
	values := &pb.SvcPluginValues{}
	for _, router := range config.DefaultServerServiceRouterChain {
		routePlugin, err := g.plugins.GetPlugin(common.TypeServiceRouter, router)
		if nil != err {
			log.GetBaseLogger().Errorf("fail to lookup plugin %s, error %v", router, err)
			continue
		}
		if nil == values.Routers {
			values.Routers = &servicerouter.RouterChain{}
		}
		values.Routers.Chain = append(values.Routers.Chain, routePlugin.(servicerouter.ServiceRouter))
	}
	if lbStr, ok := config.DefaultServerServiceToLoadBalancer[clsType]; ok {
		lbPlugin, err := g.plugins.GetPlugin(common.TypeLoadBalancer, lbStr)
		if nil != err {
			log.GetBaseLogger().Errorf("fail to lookup plugin %s, error %v", lbStr, err)
			return values
		}
		values.Loadbalancer = lbPlugin.(loadbalancer.LoadBalancer)
	}
	return values
}

//实例更新后的处理动作
func (g *LocalCache) postServiceInstanceUpdated(
	svcKey *model.ServiceEventKey, cacheValue interface{}, preStatus CachedStatus) {
	//if preStatus == CacheNotExists {
	//	g.addServiceToSet(svcKey)
	//}
}

//创建服务缓存操作回调集合
func (g *LocalCache) newServiceCacheHandler() CacheHandlers {
	return CacheHandlers{
		CompareMessage:      compareServiceInstances,
		MessageToCacheValue: g.messageToServiceInstances,
		PostCacheUpdated:    g.postServiceInstanceUpdated,
		OnEventDeleted:      g.deleteService,
	}
}

//LoadInstances 发起实例查询
func (g *LocalCache) LoadInstances(svcKey *model.ServiceKey) (*common.Notifier, error) {
	log.GetBaseLogger().Debugf("LoadInstances: %s", svcKey)
	svcEvKey := &model.ServiceEventKey{
		ServiceKey: model.ServiceKey{Service: svcKey.Service, Namespace: svcKey.Namespace},
		Type:       model.EventInstances}
	svcEvKey.Type = model.EventInstances
	return g.loadRemoteValue(svcEvKey, g.eventToCacheHandlers[svcEvKey.Type])
}

//loadRemoteValue 通用远程查询逻辑
func (g *LocalCache) loadRemoteValue(svcKey *model.ServiceEventKey, handler CacheHandlers) (*common.Notifier, error) {
	if g.IsDestroyed() {
		return nil, model.NewSDKError(model.ErrCodeInvalidStateError, nil,
			"loadRemoteValue: LocalCache %s has been destroyed", name)
	}

	var actualSvcObject *CacheObject
	value, ok := g.serviceMap.Load(svcKey)
	if !ok {
		svcObject := NewCacheObject(handler, g, svcKey)
		actualValue, _ := g.serviceMap.LoadOrStore(*svcKey, svcObject)
		actualSvcObject = actualValue.(*CacheObject)
	} else {
		actualSvcObject = value.(*CacheObject)
	}

	//如果cas操作失败了，那么说明原本注册就是1，或者为0的时候由另一个协程设置成功了
	//两种情况下都不需要自身再进行注册了
	if !atomic.CompareAndSwapUint32(&actualSvcObject.hasRegistered, 0, 1) {
		return actualSvcObject.GetNotifier(), nil
	}
	//注册了监听后，认为是被用户需要的服务，加入serviceSet
	g.addServiceToSet(svcKey)
	//如果类型为实例，在加入了监听和serviceSet之后，创建ServiceLocalValue
	if svcKey.Type == model.EventInstances {
		createHandlers := g.plugins.GetEventSubscribers(common.OnServiceLocalValueCreated)
		if len(createHandlers) > 0 {
			event := &common.PluginEvent{
				EventType: common.OnServiceLocalValueCreated, EventObject: actualSvcObject.svcLocalValue}
			for _, h := range createHandlers {
				h.Callback(event)
			}
		}
	}
	//该服务下的头一个访问的，因此他发起向connector的监听操作
	svcEventHandler := &serverconnector.ServiceEventHandler{
		ServiceEventKey: svcKey,
		Handler:         actualSvcObject,
	}
	g.enhanceServiceEventHandler(svcEventHandler)
	log.GetBaseLogger().Infof("%s, start to register event handler for %s", g.GetSDKContextID(), svcKey)
	err := g.connector.RegisterServiceHandler(svcEventHandler)
	log.GetBaseLogger().Infof("%s, finish register event handler for %s, err %v", g.GetSDKContextID(), svcKey, err)
	if nil != err {
		//出错了，这时候要清理自己，并通知已经注册的成员
		actualSvcObject.MakeInValid(err.(model.SDKError))
		handler.OnEventDeleted(svcKey, actualSvcObject.LoadValue(false))
		return nil, err
	}
	return actualSvcObject.GetNotifier(), nil
}

//UpdateInstances 批量更新服务实例状态，properties存放的是状态值，当前支持2个key
//对同一个key的更新，请保持线程安全
// 1. ReadyToServe: 故障熔断标识，true or false
// 2. DynamicWeight：动态权重值
func (g *LocalCache) UpdateInstances(svcUpdateReq *localregistry.ServiceUpdateRequest) error {
	_, ok := g.serviceMap.Load(model.ServiceEventKey{
		ServiceKey: svcUpdateReq.ServiceKey,
		Type:       model.EventInstances,
	})
	if !ok {
		return model.NewSDKError(model.ErrCodeAPIInstanceNotFound, nil,
			"UpdateInstances in %s: service %s not found", g.Name(), svcUpdateReq.ServiceKey)
	}
	if g.engine == nil {
		e, _ := g.globalCtx.GetValue(model.ContextKeyEngine)
		g.engine = e.(model.Engine)
	}
	for i := 0; i < len(svcUpdateReq.Properties); i++ {
		//更新实例的本地信息，包括熔断状态、健康检测状态
		var cbStatusUpdated bool
		property := svcUpdateReq.Properties[i]
		instances := g.GetInstances(property.Service, true, true)
		svcInstancesInProto := instances.(*pb.ServiceInstancesInProto)
		localValuesIntf := svcInstancesInProto.GetInstanceLocalValue(property.ID)
		if nil == localValuesIntf {
			log.GetBaseLogger().Warnf(
				"instance %s for service %s has been expired, update ignored", property.ID, *property.Service)
			continue
		}
		localValues := localValuesIntf.(*local.DefaultInstanceLocalValue)
		updateInstance := svcInstancesInProto.GetInstance(property.ID)
		for k, v := range property.Properties {
			switch k {
			case localregistry.PropertyCircuitBreakerStatus:
				preCBStatus := localValues.GetCircuitBreakerStatus()
				nextCBStatus := v.(model.CircuitBreakerStatus)
				localValues.SetCircuitBreakerStatus(nextCBStatus)
				cbStatusUpdated = true
				if (nil != preCBStatus && preCBStatus.GetStatus() == nextCBStatus.GetStatus()) ||
					(nil == preCBStatus && nextCBStatus.GetStatus() == model.Close) {
					cbStatusUpdated = false
				}
				err := g.engine.SyncReportStat(model.CircuitBreakStat,
					&circuitBreakGauge{changeInstance: updateInstance, previousCBStatus: preCBStatus})
				if nil != err {
					log.GetBaseLogger().Errorf("fail to report circuitbreak change, error %v", err)
				}
			case localregistry.PropertyHealthCheckStatus:
				localValues.SetActiveDetectStatus(v.(model.ActiveDetectStatus))
			}
		}
		if cbStatusUpdated {
			svcInstancesInProto.ReloadServiceClusters()
		}
	}
	return nil
}

//获取服务列表
func (g *LocalCache) GetServices() model.HashSet {
	g.servicesMutex.RLock()
	defer g.servicesMutex.RUnlock()
	svcs := model.HashSet{}
	for s := range g.services {
		svcs.Add(s)
	}
	return svcs
}

// 归还池化查询对象
func poolPutSvcEventKey(svcEventKey *model.ServiceEventKey) {
	svcEventPool.Put(svcEventKey)
}

//获取池化查询对象
func poolGetSvcEventKey(svcKey *model.ServiceKey, eventType model.EventType) *model.ServiceEventKey {
	var svcEventKey *model.ServiceEventKey
	value := svcEventPool.Get()
	if reflect2.IsNil(value) {
		svcEventKey = &model.ServiceEventKey{}
	} else {
		svcEventKey = value.(*model.ServiceEventKey)
	}
	svcEventKey.Service = svcKey.Service
	svcEventKey.Namespace = svcKey.Namespace
	svcEventKey.Type = eventType
	return svcEventKey
}

//非阻塞获取配置信息
func (g *LocalCache) GetServiceRouteRule(key *model.ServiceKey, includeCache bool) model.ServiceRule {
	svcEventKey := poolGetSvcEventKey(key, model.EventRouting)
	svcRule := g.GetServiceRule(svcEventKey, includeCache)
	poolPutSvcEventKey(svcEventKey)
	return svcRule
}

//非阻塞获取网格
func (g *LocalCache) GetMesh(key *model.ServiceKey, includeCache bool) model.Mesh {
	svcEventKey := poolGetSvcEventKey(key, model.EventMesh)
	mc := g.GetMeshImp(svcEventKey, includeCache)
	poolPutSvcEventKey(svcEventKey)
	return mc
}

//非阻塞获取网格具体逻辑
func (g *LocalCache) GetMeshImp(svcEventKey *model.ServiceEventKey, includeCache bool) model.Mesh {
	value, ok := g.serviceMap.Load(*svcEventKey)
	if !ok {
		return emptyMesh
	}
	cacheObj := value.(*CacheObject)
	ruleValue := cacheObj.LoadValue(true)
	if reflect2.IsNil(ruleValue) {
		return emptyMesh
	}

	if atomic.LoadUint32(&cacheObj.hasRemoteUpdated) > 0 {
		return ruleValue.(model.Mesh)
	}

	if includeCache {
		return ruleValue.(model.Mesh)
	}

	if g.startUseFileCache && atomic.LoadUint32(&cacheObj.cachePersistentAvailable) > 0 {
		return ruleValue.(model.Mesh)
	}
	return emptyMesh
}

//非阻塞获取网格规则
func (g *LocalCache) GetMeshConfig(key *model.ServiceKey, includeCache bool) model.MeshConfig {
	svcEventKey := poolGetSvcEventKey(key, model.EventMeshConfig)
	mc := g.GetMeshConfigImp(svcEventKey, includeCache)
	poolPutSvcEventKey(svcEventKey)
	return mc
}

//非阻塞获取网格规则具体逻辑
func (g *LocalCache) GetMeshConfigImp(svcEventKey *model.ServiceEventKey, includeCache bool) model.MeshConfig {
	value, ok := g.serviceMap.Load(*svcEventKey)
	if !ok {
		return emptyMeshConfig
	}
	cacheObj := value.(*CacheObject)
	ruleValue := cacheObj.LoadValue(true)
	if reflect2.IsNil(ruleValue) {
		return emptyMeshConfig
	}

	if atomic.LoadUint32(&cacheObj.hasRemoteUpdated) > 0 {
		return ruleValue.(model.MeshConfig)
	}

	if includeCache {
		return ruleValue.(model.MeshConfig)
	}

	if g.startUseFileCache && atomic.LoadUint32(&cacheObj.cachePersistentAvailable) > 0 {
		return ruleValue.(model.MeshConfig)
	}
	return emptyMeshConfig

}

func (g *LocalCache) GetServicesByMeta(key *model.ServiceKey, includeCache bool) model.Services {
	svcEventKey := poolGetSvcEventKey(key, model.EventServices)
	value, ok := g.serviceMap.Load(*svcEventKey)
	if !ok {
		poolPutSvcEventKey(svcEventKey)
		return pb.NewServicesProto(nil)
	}
	cacheObj := value.(*CacheObject)
	ruleValue := cacheObj.LoadValue(true)
	if reflect2.IsNil(ruleValue) {
		poolPutSvcEventKey(svcEventKey)
		return pb.NewServicesProto(nil)
	}
	//如果includeCache为false，并且这个对象没有经过远程更新，那么不返回缓存值
	if !includeCache && atomic.LoadUint32(&cacheObj.hasRemoteUpdated) == 0 {
		poolPutSvcEventKey(svcEventKey)
		return pb.NewServicesProto(nil)
	}
	poolPutSvcEventKey(svcEventKey)
	return ruleValue.(model.Services)
}

//非阻塞获取限流规则
func (g *LocalCache) GetServiceRateLimitRule(key *model.ServiceKey, includeCache bool) model.ServiceRule {
	svcEventKey := poolGetSvcEventKey(key, model.EventRateLimiting)
	svcRule := g.GetServiceRule(svcEventKey, includeCache)
	//fmt.Printf("rateLimit svcRule: %v", svcRule.GetValue())
	poolPutSvcEventKey(svcEventKey)
	return svcRule
}

//非阻塞获取规则信息
func (g *LocalCache) GetServiceRule(svcEventKey *model.ServiceEventKey, includeCache bool) model.ServiceRule {
	value, ok := g.serviceMap.Load(*svcEventKey)
	if !ok {
		return emptyRule
	}
	cacheObj := value.(*CacheObject)
	ruleValue := cacheObj.LoadValue(true)
	if reflect2.IsNil(ruleValue) {
		return emptyRule
	}

	if atomic.LoadUint32(&cacheObj.hasRemoteUpdated) > 0 {
		return ruleValue.(model.ServiceRule)
	}

	if includeCache {
		return ruleValue.(model.ServiceRule)
	}

	if g.startUseFileCache && atomic.LoadUint32(&cacheObj.cachePersistentAvailable) > 0 {
		return ruleValue.(model.ServiceRule)
	}

	//如果includeCache为false，并且这个对象没有经过远程更新，那么不返回缓存值
	//if !includeCache && atomic.LoadUint32(&cacheObj.hasRemoteUpdated) == 0 {
	//	return emptyRule
	//	//_, isSystemSvc := g.serverServicesSet[svcEventKey.ServiceKey]
	//	//if !isSystemSvc {
	//	//	log.GetBaseLogger().Debugf("reject request for %s, because not remote updated\n", svcEventKey)
	//	//	return emptyRule
	//	//}
	//	//log.GetBaseLogger().Debugf("return not remote updated system service %s", svcEventKey)
	//}
	return emptyRule
}

//创建服务路由规则缓存操作回调集合
func (g *LocalCache) newRuleCacheHandler() CacheHandlers {
	return CacheHandlers{
		CompareMessage:      compareServiceRouting,
		MessageToCacheValue: messageToServiceRule,
		OnEventDeleted:      g.deleteRule,
	}
}

//创建限流规则缓存操作回调集合
func (g *LocalCache) newRateLimitCacheHandler() CacheHandlers {
	return CacheHandlers{
		CompareMessage:      compareRateLimitRule,
		MessageToCacheValue: messageToServiceRule,
		OnEventDeleted:      g.deleteRule,
	}
}

//创建网格规则回调
func (g *LocalCache) newMeshConfigHandler() CacheHandlers {
	return CacheHandlers{
		CompareMessage:      compareMeshConfig,
		MessageToCacheValue: messageToMeshConfig,
		OnEventDeleted:      g.deleteRule,
	}
}

//创建网格回调
func (g *LocalCache) newMeshHandler() CacheHandlers {
	return CacheHandlers{
		CompareMessage:      compareMesh,
		MessageToCacheValue: messageToMesh,
		OnEventDeleted:      g.deleteRule,
	}
}

//创建批量服务回调
func (g *LocalCache) newServicesHandler() CacheHandlers {
	return CacheHandlers{
		CompareMessage:      compareServices,
		MessageToCacheValue: messageToServices,
		OnEventDeleted:      g.deleteRule,
	}
}

//删除服务信息，包括从注销监听和删除本地缓存信息
func (g *LocalCache) deleteRule(svcKey *model.ServiceEventKey, oldValue interface{}) {
	log.GetBaseLogger().Infof("%s, deregister %s", g.GetSDKContextID(), svcKey)
	g.connector.DeRegisterServiceHandler(svcKey)
	g.serviceMap.Delete(*svcKey)
	g.deleteServiceFromSet(svcKey)
	cacheFile := lrplug.ServiceEventKeyToFileName(*svcKey)
	g.persistTasks.Store(cacheFile, &persistTask{
		op:       deleteCache,
		protoMsg: nil,
	})
}

//处理当之前缓存值为空的场景
func onOriginalRoutingRuleValueEmpty(newRuleValue *namingpb.Routing) (CachedStatus, string) {
	if nil != newRuleValue {
		return CacheNotExists, newRuleValue.GetRevision().GetValue()
	}
	return CacheNotExists, emptyReplaceHolder
}

//处理当之前缓存值不为空的场景
func onOriginalRoutingRuleValueNotEmpty(oldRevision string, newRuleValue *namingpb.Routing) (CachedStatus, string) {
	if nil != newRuleValue {
		newRevision := newRuleValue.GetRevision().GetValue()
		if newRevision != oldRevision {
			return CacheChanged, newRevision
		}
		return CacheNotChanged, newRevision
	}
	if len(oldRevision) == 0 {
		return CacheNotChanged, emptyReplaceHolder
	}
	return CacheChanged, emptyReplaceHolder
}

//服务路由是否已经更新
func compareServiceRouting(instValue interface{}, newValue proto.Message) CachedStatus {
	var status CachedStatus
	var oldRevision string
	var newRevision string
	var resp = newValue.(*namingpb.DiscoverResponse)
	var routingValue = resp.GetRouting()
	//判断server的错误码，是否未变更
	if resp.GetCode().GetValue() == namingpb.DataNoChange {
		if reflect2.IsNil(instValue) {
			status = CacheEmptyButNoData
		} else {
			status = CacheNotChanged
		}
		goto finally
	}
	if reflect2.IsNil(instValue) {
		oldRevision = emptyReplaceHolder
		status, newRevision = onOriginalRoutingRuleValueEmpty(routingValue)
	} else {
		oldRevision = instValue.(model.ServiceRule).GetRevision()
		status, newRevision = onOriginalRoutingRuleValueNotEmpty(oldRevision, routingValue)
	}
finally:
	if status != CacheNotChanged {
		log.GetBaseLogger().Infof(
			"service routing %s:%s has updated, compare status %s: old revision is %s, new revision is %s",
			resp.GetService().GetNamespace().GetValue(), resp.GetService().GetName().GetValue(),
			status, oldRevision, newRevision)
	} else {
		log.GetBaseLogger().Debugf(
			"service routing %s:%s is not updated, compare status %s: old revision is %s, new revision is %s",
			resp.GetService().GetNamespace().GetValue(), resp.GetService().GetName().GetValue(),
			status, oldRevision, newRevision)
	}
	return status
}

//处理当之前缓存值为空的场景
func onOriginalRateLimitRuleEmpty(newRuleValue *namingpb.RateLimit) (CachedStatus, string) {
	if nil != newRuleValue {
		return CacheNotExists, newRuleValue.GetRevision().GetValue()
	}
	return CacheNotExists, emptyReplaceHolder
}

//处理当之前缓存值不为空的场景
func onOriginalRateLimitRuleNotEmpty(oldRevision string, newRuleValue *namingpb.RateLimit) (CachedStatus, string) {
	if nil != newRuleValue {
		newRevision := newRuleValue.GetRevision().GetValue()
		if newRevision != oldRevision {
			return CacheChanged, newRevision
		}
		return CacheNotChanged, newRevision
	}
	if len(oldRevision) == 0 {
		return CacheNotChanged, emptyReplaceHolder
	}
	return CacheChanged, emptyReplaceHolder
}

func onOriginalMeshConfigEmpty(newRuleValue *namingpb.MeshConfig) (CachedStatus, string) {
	if nil != newRuleValue {
		return CacheNotExists, newRuleValue.GetRevision().GetValue()
	}
	return CacheNotExists, emptyReplaceHolder
}

func onOriginalMeshConfigNotEmpty(oldRevision string, newRuleValue *namingpb.MeshConfig) (CachedStatus, string) {
	if nil != newRuleValue {
		if newRuleValue.GetRevision().GetValue() != oldRevision {
			return CacheChanged, newRuleValue.GetRevision().GetValue()
		}
		return CacheNotChanged, oldRevision
	}
	if len(oldRevision) == 0 {
		return CacheNotChanged, emptyReplaceHolder
	}
	return CacheChanged, emptyReplaceHolder
}

//比较网格规则变化
func compareMeshConfig(instValue interface{}, newValue proto.Message) CachedStatus {
	var status CachedStatus
	var oldRevision string
	var newRevision string
	var resp = newValue.(*namingpb.DiscoverResponse)
	var meshValue = resp.GetMeshconfig()
	log.GetBaseLogger().Debugf("compareMeshConfig", meshValue)
	if meshValue == nil {
		status = CacheNotChanged
		goto finally
	}
	//判断server的错误码，是否未变更
	if resp.GetCode().GetValue() == namingpb.DataNoChange {
		if reflect2.IsNil(instValue) {
			status = CacheEmptyButNoData
		} else {
			status = CacheNotChanged
		}
		goto finally
	}
	if reflect2.IsNil(instValue) {
		oldRevision = emptyReplaceHolder
		status, newRevision = onOriginalMeshConfigEmpty(meshValue)
	} else {
		oldRevision = instValue.(model.MeshConfig).GetRevision()
		status, newRevision = onOriginalMeshConfigNotEmpty(oldRevision, meshValue)
	}
finally:
	if status != CacheNotChanged {
		log.GetBaseLogger().Infof(
			"meshconfig rule %s:%s has updated, compare status %s: old revision is %s, new revision is %s",
			resp.GetService().GetNamespace().GetValue(), resp.GetService().GetName().GetValue(),
			status, oldRevision, newRevision)
	} else {
		log.GetBaseLogger().Debugf(
			"meshconfig rule %s:%s is not updated, compare status %s: old revision is %s, new revision is %s",
			resp.GetService().GetNamespace().GetValue(), resp.GetService().GetName().GetValue(),
			status, oldRevision, newRevision)
	}
	return status
}

//比较网格变化
func compareMesh(instValue interface{}, newValue proto.Message) CachedStatus {
	var status CachedStatus
	var oldRevision string
	var newRevision string
	var resp = newValue.(*namingpb.DiscoverResponse)
	var meshValue = resp.GetMesh()
	log.GetBaseLogger().Debugf("compareMesh", meshValue)
	if meshValue == nil {
		status = CacheNotChanged
		goto finally
	}
	//判断server的错误码，是否未变更
	if resp.GetCode().GetValue() == namingpb.DataNoChange {
		if reflect2.IsNil(instValue) {
			status = CacheEmptyButNoData
		} else {
			status = CacheNotChanged
		}
		goto finally
	}
	if reflect2.IsNil(instValue) {
		oldRevision = emptyReplaceHolder
		status, newRevision = CacheNotExists, meshValue.GetRevision().GetValue()
	} else {
		oldRevision = instValue.(model.Mesh).GetRevision()
		if meshValue.GetRevision().GetValue() != oldRevision {
			status, newRevision = CacheChanged, meshValue.GetRevision().GetValue()
		} else {
			status, newRevision = CacheNotChanged, oldRevision
		}
	}
finally:
	if status != CacheNotChanged {
		log.GetBaseLogger().Infof(
			"mesh %s:%s has updated, compare status %s: old revision is %s, new revision is %s",
			resp.GetService().GetNamespace().GetValue(), resp.GetService().GetName().GetValue(),
			status, oldRevision, newRevision)
	} else {
		log.GetBaseLogger().Debugf(
			"mesh %s:%s is not updated, compare status %s: old revision is %s, new revision is %s",
			resp.GetService().GetNamespace().GetValue(), resp.GetService().GetName().GetValue(),
			status, oldRevision, newRevision)
	}
	return status
}

func onOriginalServicesEmpty(services []*namingpb.Service) (CachedStatus, string) {
	newVersion := pb.GenServicesRevision(services)
	if nil != services && len(services) > 0 {
		return CacheNotExists, newVersion
	}
	return CacheNotExists, emptyReplaceHolder
}

func onOriginalServicesNotEmpty(oldRevision string, services []*namingpb.Service) (CachedStatus, string) {
	newVersion := pb.GenServicesRevision(services)
	if nil != services && len(services) > 0 {
		if newVersion != oldRevision {
			return CacheChanged, newVersion
		}
		return CacheNotChanged, oldRevision
	}
	if len(oldRevision) == 0 {
		return CacheNotChanged, emptyReplaceHolder
	}
	return CacheChanged, emptyReplaceHolder
}

//比较批量获取的服务变化
func compareServices(instValue interface{}, newValue proto.Message) CachedStatus {
	var status CachedStatus
	var oldRevision string
	var newRevision string
	var resp = newValue.(*namingpb.DiscoverResponse)
	var services = resp.GetServices()
	//临时处理
	log.GetBaseLogger().Debugf("compareServices", services)
	if services == nil {
		status = CacheNotChanged
		goto finally
	}
	//判断server的错误码，是否未变更
	if resp.GetCode().GetValue() == namingpb.DataNoChange {
		if reflect2.IsNil(instValue) {
			status = CacheEmptyButNoData
		} else {
			status = CacheNotChanged
		}
		goto finally
	}
	if reflect2.IsNil(instValue) {
		oldRevision = emptyReplaceHolder
		status, newRevision = onOriginalServicesEmpty(services)
	} else {
		oldRevision = instValue.(model.Services).GetRevision()
		status, newRevision = onOriginalServicesNotEmpty(oldRevision, services)
	}
finally:
	if status != CacheNotChanged {
		log.GetBaseLogger().Infof(
			"compareServices rule %s:%s has updated, compare status %s: old revision is %s, new revision is %s",
			resp.GetService().GetNamespace().GetValue(), resp.GetService().GetName().GetValue(),
			status, oldRevision, newRevision)
	} else {
		log.GetBaseLogger().Debugf(
			"compareServices rule %s:%s is not updated, compare status %s: old revision is %s, new revision is %s",
			resp.GetService().GetNamespace().GetValue(), resp.GetService().GetName().GetValue(),
			status, oldRevision, newRevision)
	}
	return status

}

//比较限流规则的变化
func compareRateLimitRule(instValue interface{}, newValue proto.Message) CachedStatus {
	var status CachedStatus
	var oldRevision string
	var newRevision string
	var resp = newValue.(*namingpb.DiscoverResponse)
	var ruleValue = resp.GetRateLimit()
	//判断server的错误码，是否未变更
	if resp.GetCode().GetValue() == namingpb.DataNoChange {
		if reflect2.IsNil(instValue) {
			status = CacheEmptyButNoData
		} else {
			status = CacheNotChanged
		}
		goto finally
	}
	if reflect2.IsNil(instValue) {
		oldRevision = emptyReplaceHolder
		status, newRevision = onOriginalRateLimitRuleEmpty(ruleValue)
	} else {
		oldRevision = instValue.(model.ServiceRule).GetRevision()
		status, newRevision = onOriginalRateLimitRuleNotEmpty(oldRevision, ruleValue)
	}
finally:
	if status != CacheNotChanged {
		log.GetBaseLogger().Infof(
			"ratelimit rule %s:%s has updated, compare status %s: old revision is %s, new revision is %s",
			resp.GetService().GetNamespace().GetValue(), resp.GetService().GetName().GetValue(),
			status, oldRevision, newRevision)
	} else {
		log.GetBaseLogger().Debugf(
			"ratelimit rule %s:%s is not updated, compare status %s: old revision is %s, new revision is %s",
			resp.GetService().GetNamespace().GetValue(), resp.GetService().GetName().GetValue(),
			status, oldRevision, newRevision)
	}
	return status
}

//PB对象转服务实例对象
func messageToServiceRule(cachedValue interface{}, value proto.Message,
	svcLocalValue local.ServiceLocalValue, cacheLoaded bool) model.RegistryValue {
	respInProto := value.(*namingpb.DiscoverResponse)
	svcRule := pb.NewServiceRuleInProto(respInProto)
	if cacheLoaded {
		svcRule.CacheLoaded = 1
	}
	if err := svcRule.ValidateAndBuildCache(); nil != err {
		log.GetBaseLogger().Errorf(
			"fail to validate service rule for service %s, namespace %s, error is %v",
			respInProto.GetService().GetName(), respInProto.GetService().GetNamespace(), err)
	}
	return svcRule
}

func messageToMeshConfig(cachedValue interface{}, value proto.Message,
	svcLocalValue local.ServiceLocalValue, cacheLoaded bool) model.RegistryValue {
	respInProto := value.(*namingpb.DiscoverResponse)
	mc := pb.NewMeshConfigInProto(respInProto)
	if cacheLoaded {
		mc.CacheLoaded = 1
	}
	log.GetBaseLogger().Debugf("messageToMeshConfig", respInProto.Meshconfig, mc, mc.GetValue())
	return mc
}

func messageToMesh(cachedValue interface{}, value proto.Message,
	svcLocalValue local.ServiceLocalValue, cacheLoaded bool) model.RegistryValue {
	respInProto := value.(*namingpb.DiscoverResponse)
	mc := pb.NewMeshInProto(respInProto)
	if cacheLoaded {
		mc.CacheLoaded = 1
	}
	log.GetBaseLogger().Debugf("messageToMesh", respInProto.Mesh, mc, mc.GetValue())
	return mc
}

func messageToServices(cachedValue interface{}, value proto.Message,
	svcLocalValue local.ServiceLocalValue, cacheLoaded bool) model.RegistryValue {
	respInProto := value.(*namingpb.DiscoverResponse)
	mc := pb.NewServicesProto(respInProto)
	if cacheLoaded {
		mc.CacheLoaded = 1
	}
	log.GetBaseLogger().Debugf("messageToServices", respInProto.Meshconfig, mc, mc.GetValue(), mc.GetRevision())
	return mc
}

//非阻塞发起配置加载
func (g *LocalCache) LoadServiceRouteRule(key *model.ServiceKey) (*common.Notifier, error) {
	return g.LoadServiceRule(&model.ServiceEventKey{
		ServiceKey: model.ServiceKey{
			Namespace: key.Namespace,
			Service:   key.Service,
		},
		Type: model.EventRouting,
	})
}

//非阻塞加载网格规则
func (g *LocalCache) LoadMeshConfig(key *model.ServiceKey) (*common.Notifier, error) {
	return g.LoadServiceRule(&model.ServiceEventKey{
		ServiceKey: model.ServiceKey{
			Namespace: key.Namespace,
			Service:   key.Service,
		},
		Type: model.EventMeshConfig,
	})
}

//非阻塞加载网格
func (g *LocalCache) LoadMesh(key *model.ServiceKey) (*common.Notifier, error) {
	return g.LoadServiceRule(&model.ServiceEventKey{
		ServiceKey: model.ServiceKey{
			Namespace: key.Namespace,
			Service:   key.Service,
		},
		Type: model.EventMesh,
	})
}

//非阻塞加载批量服务
func (g *LocalCache) LoadServices(key *model.ServiceKey) (*common.Notifier, error) {
	log.GetBaseLogger().Infof("LoadServices", *key)
	return g.LoadServiceRule(&model.ServiceEventKey{
		ServiceKey: model.ServiceKey{
			Namespace: key.Namespace,
			Service:   key.Service,
		},
		Type: model.EventServices,
	})
}

//非阻塞发起限流规则加载
func (g *LocalCache) LoadServiceRateLimitRule(key *model.ServiceKey) (*common.Notifier, error) {
	return g.LoadServiceRule(&model.ServiceEventKey{
		ServiceKey: model.ServiceKey{
			Namespace: key.Namespace,
			Service:   key.Service,
		},
		Type: model.EventRateLimiting,
	})
}

//非阻塞发起规则加载
func (g *LocalCache) LoadServiceRule(svcEventKey *model.ServiceEventKey) (*common.Notifier, error) {
	log.GetBaseLogger().Debugf("LoadServiceRule: serviceEvent %s", *svcEventKey)
	return g.loadRemoteValue(svcEventKey, g.eventToCacheHandlers[svcEventKey.Type])
}

//从持久化文件中读取缓存
func (g *LocalCache) loadCacheFromFiles() {
	timeNow := time.Now()
	persistedServices := g.cachePersistHandler.LoadPersistedServices()
	for svcKey, message := range persistedServices {
		newSvcKey := &model.ServiceEventKey{
			ServiceKey: svcKey.ServiceKey,
			Type:       svcKey.Type,
		}
		newSvcObj := NewCacheObjectWithInitValue(g.eventToCacheHandlers[newSvcKey.Type], g, newSvcKey, message.Msg)
		if timeNow.Sub(message.FileInfo.ModTime()) <= g.cacheFromPersistAvailableInterval {
			newSvcObj.cachePersistentAvailable = 1
		} else {
			newSvcObj.cachePersistentAvailable = 0
		}
		g.serviceMap.Store(*newSvcKey, newSvcObj)
		log.GetBaseLogger().Infof("cache loaded from files, key: %v, cacheObject: %v",
			newSvcKey, newSvcObj.serviceValueKey)
	}
}

// 补充ServiceEventHandler的特殊字段
func (g *LocalCache) enhanceServiceEventHandler(svcEventHandler *serverconnector.ServiceEventHandler) {
	if clsType, ok := g.serverServicesSet[svcEventHandler.ServiceKey]; ok {
		svcEventHandler.RefreshInterval = clsType.interval
		if clsType.clsType == config.DiscoverCluster {
			svcEventHandler.TargetCluster = config.BuiltinCluster
		} else {
			svcEventHandler.TargetCluster = config.DiscoverCluster
		}
	} else {
		svcEventHandler.RefreshInterval = g.serviceRefreshInterval
		svcEventHandler.TargetCluster = config.DiscoverCluster
	}
}

//淘汰过时缓存
func (g *LocalCache) eliminateExpiredCache() {
	//用于检测服务是否过期的定时器，周期为服务过期时间一半
	checkTime := g.serviceExpireTime / 2
	if checkTime > config.DefaultMaxServiceExpireCheckTime {
		checkTime = config.DefaultMaxServiceExpireCheckTime
	}
	expireTicker := time.NewTicker(checkTime)
	defer expireTicker.Stop()
	//执行缓存文件创建和删除操作的定时器，周期为config.DefaultMinTimingInterval(100ms)
	fileTaskTicker := time.NewTicker(config.DefaultMinTimingInterval)
	defer fileTaskTicker.Stop()
	for {
		select {
		case <-g.Done():
			log.GetBaseLogger().Infof("eliminateExpiredCache of inmemory localRegistry has been terminated")
			return
		case <-expireTicker.C:
			currentTime := g.globalCtx.Now().UnixNano()
			g.serviceMap.Range(func(k, v interface{}) bool {
				cacheObjectValue := v.(*CacheObject)
				svcKey := cacheObjectValue.serviceValueKey.ServiceKey
				if _, ok := g.serverServicesSet[svcKey]; ok {
					//系统服务不淘汰
					return true
				}
				//如果当前时间减去最新访问时间没有超过expireTime，那么不用淘汰，继续检查下一个服务
				lastVisitTime := atomic.LoadInt64(&cacheObjectValue.lastVisitTime)
				diffTime := currentTime - lastVisitTime
				if diffTime < 0 {
					//时间发生倒退，则直接更新最近访问时间
					atomic.CompareAndSwapInt64(&cacheObjectValue.lastVisitTime, lastVisitTime, currentTime)
					return true
				}
				// 该服务被订阅,不能淘汰
				if atomic.LoadUint32(&cacheObjectValue.serviceIsWatched) > 0 {
					log.GetBaseLogger().Debugf("%s serviceIsWatched, can not expire", svcKey.String())
					return true
				}
				if time.Duration(diffTime) < g.serviceExpireTime {
					return true
				}
				svcEvKey := k.(model.ServiceEventKey)
				log.GetBaseLogger().Infof("%s expired, lastVisited: %v, serviceExpireTime：%v",
					cacheObjectValue.serviceValueKey, time.Unix(0, lastVisitTime),
					g.serviceExpireTime)
				oldValue := cacheObjectValue.LoadValue(false)
				g.eventToCacheHandlers[svcEvKey.Type].OnEventDeleted(&svcEvKey, oldValue)
				eventObject := &common.ServiceEventObject{
					SvcEventKey: svcEvKey,
					OldValue:    oldValue,
					NewValue:    nil,
				}
				if svcEvKey.Type == model.EventRateLimiting {
					eventObject.DiffInfo = calcRateLimitDiffInfo(extractRateLimitFromCacheValue(oldValue), nil)
				}
				deleteHandlers := g.plugins.GetEventSubscribers(common.OnServiceDeleted)
				if len(deleteHandlers) > 0 && !reflect2.IsNil(oldValue) {
					event := &common.PluginEvent{
						EventType: common.OnServiceDeleted, EventObject: eventObject}
					for _, handler := range deleteHandlers {
						handler.Callback(event)
					}
				}
				return true
			})
		case <-fileTaskTicker.C:
			g.persistTasks.Range(func(k, v interface{}) bool {
				g.persistTasks.Delete(k)
				cacheFile := k.(string)
				task := v.(*persistTask)
				if addCache == task.op {
					g.cachePersistHandler.SaveMessageToFile(cacheFile, task.protoMsg)
				} else {
					g.cachePersistHandler.DeleteCacheFromFile(cacheFile)
				}
				return true
			})
		}
	}
}

//对PB缓存进行持久化
func (g *LocalCache) PersistMessage(file string, message proto.Message) error {
	g.persistTasks.Store(file, &persistTask{
		op:       addCache,
		protoMsg: message,
	})
	return nil
}

//从文件中加载PB缓存
func (g *LocalCache) LoadPersistedMessage(file string, msg proto.Message) error {
	return g.cachePersistHandler.LoadMessageFromFile(file, msg)
}

//服务订阅
func (g *LocalCache) WatchService(svcEventKey *model.ServiceEventKey) error {
	value, ok := g.serviceMap.Load(*svcEventKey)
	if !ok {
		return model.NewSDKError(model.ErrCodeServiceNotFound, nil,
			fmt.Sprintf("no key in serviceMap %s", svcEventKey.String()))
	}
	cacheObj := value.(*CacheObject)
	atomic.StoreUint32(&cacheObj.serviceIsWatched, 1)
	return nil
}

//init 注册插件
func init() {
	plugin.RegisterPlugin(&LocalCache{})
}
