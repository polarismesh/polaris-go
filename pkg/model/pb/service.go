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

package pb

import (
	"crypto/md5"
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/metric"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/local"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/pkg/plugin/loadbalancer"
	"github.com/polarismesh/polaris-go/pkg/plugin/servicerouter"
	"io"
	"sort"
	"sync/atomic"
)

//服务级插件值
type SvcPluginValues struct {
	Routers      *servicerouter.RouterChain
	Loadbalancer loadbalancer.LoadBalancer
}

//通用的应答
type ServiceInstancesInProto struct {
	service         *namingpb.Service
	instances       []model.Instance
	instancesMap    map[string]model.Instance
	initialized     bool
	svcIDSet        model.HashSet
	totalWeight     int
	revision        string
	clusterCache    atomic.Value
	svcPluginValues *SvcPluginValues
	svcLocalValue   local.ServiceLocalValue
	CacheLoaded     int32
}

//instSlice，[]*namingpb.Instance的别名
type InstSlice []*namingpb.Instance

//instSlice的长度
func (is InstSlice) Len() int {
	return len(is)
}

//instSlice交换元素方法
func (is InstSlice) Swap(i, j int) {
	is[i], is[j] = is[j], is[i]
}

//instSlice元素比较方法
func (is InstSlice) Less(i, j int) bool {
	return is[i].Id.GetValue() < is[j].Id.GetValue()
}

//ServiceInstancesResponse的构造函数
func NewServiceInstancesInProto(resp *namingpb.DiscoverResponse, createLocalValue func(string) local.InstanceLocalValue,
	pluginValues *SvcPluginValues, svcLocalValue local.ServiceLocalValue) *ServiceInstancesInProto {
	//未初始化
	if nil == resp {
		return &ServiceInstancesInProto{}
	}
	//对实例元素按照id进行排序
	sort.Sort(InstSlice(resp.Instances))
	instancesInProto := &ServiceInstancesInProto{
		service:         resp.Service,
		instances:       make([]model.Instance, 0, len(resp.Instances)),
		instancesMap:    make(map[string]model.Instance, len(resp.Instances)),
		initialized:     true,
		svcIDSet:        model.HashSet{},
		svcPluginValues: pluginValues,
		svcLocalValue:   svcLocalValue,
	}
	clusterCache := model.NewServiceClusters(instancesInProto)
	if clusterCache.IsNearbyEnabled() {
		log.GetBaseLogger().Infof("service %s::%s nearby enabled",
			instancesInProto.GetNamespace(), instancesInProto.GetService())
	}
	instancesInProto.clusterCache.Store(clusterCache)
	instancesInProto.revision = resp.GetService().GetRevision().GetValue()
	svcKey := &model.ServiceKey{
		Namespace: resp.GetService().GetNamespace().GetValue(),
		Service:   resp.GetService().GetName().GetValue(),
	}
	if len(resp.Instances) > 0 {
		for _, inst := range resp.Instances {
			instId := inst.GetId().GetValue()
			instanceInProto := NewInstanceInProto(inst, svcKey, createLocalValue(instId))
			instancesInProto.totalWeight += int(inst.GetWeight().GetValue())
			instancesInProto.instances = append(instancesInProto.instances, instanceInProto)
			instancesInProto.svcIDSet.Add(instId)
			instancesInProto.instancesMap[instId] = instanceInProto
			clusterCache.AddInstance(instanceInProto)
		}
	}
	return instancesInProto
}

//pb值是否为从缓存文件中加载的
func (s *ServiceInstancesInProto) IsCacheLoaded() bool {
	return atomic.LoadInt32(&s.CacheLoaded) > 0
}

//重建缓存索引
func (s *ServiceInstancesInProto) ReloadServiceClusters() {
	clusterCache := model.NewServiceClusters(s)
	if len(s.instances) > 0 {
		for _, inst := range s.instances {
			clusterCache.AddInstance(inst.(*InstanceInProto))
		}
	}
	s.clusterCache.Store(clusterCache)
}

//获取缓存索引
func (s *ServiceInstancesInProto) GetServiceClusters() model.ServiceClusters {
	return s.clusterCache.Load().(model.ServiceClusters)
}

//返回实例的id集合
func (s *ServiceInstancesInProto) GetSvcIDSet() model.HashSet {
	return s.svcIDSet
}

//获取配置类型
func (s *ServiceInstancesInProto) GetType() model.EventType {
	return model.EventInstances
}

//获取服务名
func (s *ServiceInstancesInProto) GetService() string {
	if nil == s.service {
		return ""
	}
	return s.service.GetName().GetValue()
}

//获取名字空间
func (s *ServiceInstancesInProto) GetNamespace() string {
	if nil == s.service {
		return ""
	}
	return s.service.GetNamespace().GetValue()
}

//获取元数据信息
func (s *ServiceInstancesInProto) GetMetadata() map[string]string {
	if nil == s.service {
		return map[string]string{}
	}
	return s.service.GetMetadata()
}

//总权重
func (s *ServiceInstancesInProto) GetGetTotalWeight() int {
	return 0
}

//获取服务实例列表
func (s *ServiceInstancesInProto) GetInstances() []model.Instance {
	return s.instances
}

//服务实例列表是否已经加载
func (s *ServiceInstancesInProto) IsInitialized() bool {
	return s.initialized
}

func (s *ServiceInstancesInProto) SetInitialized(isInitialized bool) {
	s.initialized = isInitialized
}

//获取服务的修订版本信息
func (s *ServiceInstancesInProto) GetRevision() string {
	return s.revision
}

//获取所有实例总权重
func (s *ServiceInstancesInProto) GetTotalWeight() int {
	return s.totalWeight
}

//获取服务级的路由链
func (s *ServiceInstancesInProto) GetServiceRouterChain() *servicerouter.RouterChain {
	return s.svcPluginValues.Routers
}

//获取服务级的负载均衡
func (s *ServiceInstancesInProto) GetServiceLoadbalancer() loadbalancer.LoadBalancer {
	return s.svcPluginValues.Loadbalancer
}

// 按实例获取本地可变状态值
func (s *ServiceInstancesInProto) GetInstanceLocalValue(instId string) local.InstanceLocalValue {
	if inst, ok := s.instancesMap[instId]; ok {
		return inst.(*InstanceInProto).GetInstanceLocalValue()
	}
	return nil
}

//通过实例ID获取实例
func (s *ServiceInstancesInProto) GetInstance(instId string) model.Instance {
	return s.instancesMap[instId]
}

//获取服务的localvalue
func (s *ServiceInstancesInProto) GetServiceLocalValue() local.ServiceLocalValue {
	return s.svcLocalValue
}

//通过proto定义来构造实例
type InstanceInProto struct {
	*namingpb.Instance
	instanceKey *model.InstanceKey
	//本地可变记录，只初始化一次，后续都引用该指针
	localValue local.InstanceLocalValue
	//保存单个实例的数组引用
	singleInstances []model.Instance
}

//InstanceInProto的构造函数
func NewInstanceInProto(
	instance *namingpb.Instance, svcKey *model.ServiceKey, localValue local.InstanceLocalValue) *InstanceInProto {
	instInProto := &InstanceInProto{
		Instance:   instance,
		localValue: localValue,
	}
	instInProto.instanceKey = &model.InstanceKey{
		ServiceKey: model.ServiceKey{
			Namespace: svcKey.Namespace,
			Service:   svcKey.Service,
		},
		Host: instance.GetHost().GetValue(),
		Port: int(instance.GetPort().GetValue()),
	}
	instInProto.singleInstances = []model.Instance{instInProto}
	return instInProto
}

//命名空间
func (i *InstanceInProto) GetNamespace() string {
	return i.instanceKey.Namespace
}

//服务名
func (i *InstanceInProto) GetService() string {
	return i.instanceKey.Service
}

//获取实例ID，因为pb3的GetId返回wrappers.StringValue，所以改写
func (i *InstanceInProto) GetId() string {
	return i.Id.GetValue()
}

//获取实例Host，，因为pb3的GetId返回wrappers.StringValue，所以改写
func (i *InstanceInProto) GetHost() string {
	return i.Host.GetValue()
}

//获取实例的vpcId
func (i *InstanceInProto) GetVpcId() string {
	return i.VpcId.GetValue()
}

//获取实例Revision，，因为pb3的GetRevision返回wrappers.StringValue，所以改写
func (i *InstanceInProto) GetRevision() string {
	return i.Revision.GetValue()
}

//获取实例权重，，因为pb3的GetWeight返回wrappers.UInt32Value，所以改写
func (i *InstanceInProto) GetWeight() int {
	return int(i.Weight.GetValue())
}

//获取实例版本，因为pb3的GetVersion返回wrappers.StringValue，所以改写
func (i *InstanceInProto) GetVersion() string {
	return i.Version.GetValue()
}

//获取实例协议，因为pb3的GetProtocol返回wrappers.UInt32Value，所以改写
func (i *InstanceInProto) GetProtocol() string {
	return i.Protocol.GetValue()
}

//获取实例端口，因为pb3的GetPort返回wrappers.UInt32Value，所以改写
func (i *InstanceInProto) GetPort() uint32 {
	return i.Port.GetValue()
}

//获取实例优先级，因为pb3的GetPriority返回wrappers.UInt32Value，所以改写
func (i *InstanceInProto) GetPriority() uint32 {
	return i.Priority.GetValue()
}

//获取逻辑分区
func (i *InstanceInProto) GetLogicSet() string {
	return i.LogicSet.GetValue()
}

//获取插件数据
func (i *InstanceInProto) GetExtendedData(pluginIndex int32) interface{} {
	return i.localValue.GetExtendedData(pluginIndex)
}

//设置插件数据
func (i *InstanceInProto) SetExtendedData(pluginIndex int32, data interface{}) {
	i.localValue.SetExtendedData(pluginIndex, data)
}

//instance dynamic weight
func (i *InstanceInProto) GetCircuitBreakerStatus() model.CircuitBreakerStatus {
	return i.localValue.GetCircuitBreakerStatus()
}

//instance dynamic weight
func (i *InstanceInProto) GetActiveDetectStatus() model.ActiveDetectStatus {
	return i.localValue.GetActiveDetectStatus()
}

//instance health status
func (i *InstanceInProto) IsHealthy() bool {
	return i.GetHealthy().GetValue()
}

//instance is isolated
func (i *InstanceInProto) IsIsolated() bool {
	return i.GetIsolate().GetValue()
}

//whether the health check enabled
func (i *InstanceInProto) IsEnableHealthCheck() bool {
	return nil != i.HealthCheck
}

//instance region
func (i *InstanceInProto) GetRegion() string {
	return i.GetLocation().GetRegion().GetValue()
}

//instance zone
func (i *InstanceInProto) GetZone() string {
	return i.GetLocation().GetZone().GetValue()
}

//instance idc
func (i *InstanceInProto) GetIDC() string {
	return i.GetLocation().GetCampus().GetValue()
}

//instance campus
func (i *InstanceInProto) GetCampus() string {
	return i.GetLocation().GetCampus().GetValue()
}

//获取实例的四元组标识
func (i *InstanceInProto) GetInstanceKey() model.InstanceKey {
	return *i.instanceKey
}

// 获取本地可变状态值
func (i *InstanceInProto) GetInstanceLocalValue() local.InstanceLocalValue {
	return i.localValue
}

//获取统计窗口
func (i *InstanceInProto) GetSliceWindows(pluginIndex int32) []*metric.SliceWindow {
	return i.localValue.GetSliceWindows(pluginIndex)
}

//获取单个实例数组
func (i *InstanceInProto) SingleInstances() []model.Instance {
	return i.singleInstances
}

func GenServicesRevision(services []*namingpb.Service) string {
	h := md5.New()
	for _, svc := range services {
		io.WriteString(h, svc.Namespace.GetValue())
		io.WriteString(h, svc.Name.GetValue())
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

//批量服务
type ServicesProto struct {
	*model.ServiceKey
	initialized bool
	revision    string
	ruleValue   []*namingpb.Service
	ruleCache   model.RuleCache
	eventType   model.EventType
	CacheLoaded int32
}

//新建服务proto
func NewServicesProto(resp *namingpb.DiscoverResponse) *ServicesProto {
	value := &ServicesProto{}
	if nil == resp {
		value.initialized = false
		return value
	}
	value.ServiceKey = &model.ServiceKey{
		Namespace: resp.Service.Namespace.GetValue(),
		Service:   resp.Service.Name.GetValue(),
	}
	value.initialized = true
	value.eventType = GetEventType(resp.GetType())
	value.ruleValue = resp.Services
	value.revision = GenServicesRevision(resp.Services)
	return value
}

//获取事件类型
func (s *ServicesProto) GetType() model.EventType {
	return s.eventType
}

//缓存是否已经初始化
func (s *ServicesProto) IsInitialized() bool {
	return s.initialized
}

//缓存版本号，标识缓存是否更新
func (s *ServicesProto) GetRevision() string {
	return s.revision
}

//获取值
func (s *ServicesProto) GetValue() interface{} {
	return s.ruleValue
}

//获取Namespace
func (s *ServicesProto) GetNamespace() string {
	return s.Namespace
}

//获取Service
func (s *ServicesProto) GetService() string {
	return s.Service
}
