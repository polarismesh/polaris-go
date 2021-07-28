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

package model

import (
	"bytes"
	"fmt"
	"github.com/modern-go/reflect2"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
)

//用于拼接metadata的样式
const (
	composedMetaSeparator = ","
	NearbyMetadataEnable  = "internal-enable-nearby"
	CanaryMetadataEnable  = "internal-canary"

	CanaryMetaKey = "canary"
)

//位置信息
type Location struct {
	Region string
	Zone   string
	Campus string
}

//位置信息ToString
func (l Location) String() string {
	return fmt.Sprintf("{region: %s, zone: %s, campus: %s}", l.Region, l.Zone, l.Campus)
}

//位置信息是否为空
func (l *Location) IsEmpty() bool {
	return l.Zone == "" || l.Region == "" || l.Campus == ""
}

//集群缓存KEY对象
type ClusterKey struct {
	//组合后的元数据kv
	ComposeMetaValue string
	Location         Location
}

//设置集群
func (k *ClusterKey) setComposeMetaValue(composeMetaValues sort.StringSlice, composedMetaStrCount int) {
	if composedMetaStrCount == 0 {
		k.ComposeMetaValue = ""
		return
	}
	sort.Sort(composeMetaValues)
	buf := bytes.NewBuffer(make([]byte, 0, composedMetaStrCount+len(composeMetaValues)-1))
	for i, composedValue := range composeMetaValues {
		if i > 0 {
			buf.WriteString(composedMetaSeparator)
		}
		buf.WriteString(composedValue)
	}
	k.ComposeMetaValue = buf.String()
}

//ToString
func (k ClusterKey) String() string {
	return fmt.Sprintf("{ComposeMetaValue: %s, Location: %s}", k.ComposeMetaValue, k.Location)
}

//单个结构，针对单个会进行优化
type MetaComposedValue struct {
	metaKey       string
	metaValue     string
	composedValue string
}

//存放集群对象池
var clusterPool = &sync.Pool{}

//路由后的集群信息，包含的是标签以及地域信息
type Cluster struct {
	ClusterKey
	MetaComposedValue
	//集群的元数据信息，key为metaKey, valueKey为metaValue, valueValue为composedValue
	Metadata map[string]map[string]string
	//元数据KV的个数
	MetaCount int
	//是否包含限制路由的实例，全死全活后为true
	HasLimitedInstances bool
	//没有符合就近路由的实例
	MissLocationInstances bool
	//位置信息匹配情况
	LocationMatchInfo string
	//通过服务路由选择出来的集群值
	value *ClusterValue

	//该小集群所基于的服务集群
	clusters ServiceClusters

	//设计cluster对象是否进行复用
	//对于常驻的cluster，以及返回给外部接口的cluster, reuse = false
	reuse bool

	IncludeHalfOpen bool
}

//重置集群对象
func (c *Cluster) reset(clusters ServiceClusters) {
	c.ComposeMetaValue = ""
	c.Location.Region = ""
	c.Location.Campus = ""
	c.Location.Zone = ""
	c.Metadata = nil
	c.HasLimitedInstances = false
	c.MissLocationInstances = false
	c.LocationMatchInfo = ""
	c.value = nil
	c.clusters = clusters
	c.MetaCount = 0
	c.MetaComposedValue.metaKey = ""
	c.MetaComposedValue.metaValue = ""
	c.MetaComposedValue.composedValue = ""
	c.reuse = true
}

//归还对象到池子中
func (c *Cluster) PoolPut() {
	//如果不是复用，则无需回收
	if !c.reuse {
		return
	}
	clusterPool.Put(c)
}

//设置是否需要复用cluster
func (c *Cluster) SetReuse(value bool) {
	c.reuse = value
}

//新建Cluster
func NewCluster(clusters ServiceClusters, cls *Cluster) *Cluster {
	var newCls *Cluster
	clsValue := clusterPool.Get()
	if !reflect2.IsNil(clsValue) {
		newCls = clsValue.(*Cluster)
		newCls.reset(clusters)
	} else {
		newCls = &Cluster{
			clusters: clusters,
			reuse:    true,
		}
	}
	if nil == cls {
		return newCls
	}
	newCls.ClusterKey = cls.ClusterKey
	if len(cls.Metadata) > 0 {
		newCls.Metadata = make(map[string]map[string]string, len(cls.Metadata))
		for k, values := range cls.Metadata {
			nValues := make(map[string]string, len(values))
			for value, composedValue := range values {
				nValues[value] = composedValue
			}
			newCls.Metadata[k] = nValues
		}
		newCls.MetaCount = cls.MetaCount
	} else if cls.MetaCount > 0 {
		newCls.MetaCount = cls.MetaCount
		newCls.MetaComposedValue = cls.MetaComposedValue
	}
	return newCls
}

//集群信息ToString
func (c Cluster) String() string {
	if nil == c.value {
		return fmt.Sprintf(
			"{metadata: %s, location: %s, hasLimitedInstances: %v, value: nil}",
			c.ComposeMetaValue, c.Location, c.HasLimitedInstances)
	}
	return fmt.Sprintf(
		"{metadata: %s, location: %s, hasLimitedInstances: %v, value: %s}",
		c.ComposeMetaValue, c.Location, c.HasLimitedInstances, c.value.String())
}

//开放给插件进行手动元数据的添加
func (c *Cluster) AddMetadata(key string, value string) bool {
	composedValue := buildComposedValue(key, value)
	return c.RuleAddMetadata(key, value, composedValue)
}

//重建组合元数据KV
func (c *Cluster) ReloadComposeMetaValue() {
	switch c.MetaCount {
	case 0:
		c.ComposeMetaValue = ""
	case 1:
		c.ComposeMetaValue = c.MetaComposedValue.composedValue
	default:
		composedMetas := make([]string, c.MetaCount)
		var composedMetaStrCount int
		var index int
		for _, values := range c.Metadata {
			for _, composedValueStr := range values {
				composedMetas[index] = composedValueStr
				index++
				composedMetaStrCount += len(composedValueStr)
			}
		}
		c.setComposeMetaValue(composedMetas, composedMetaStrCount)
	}
	//ClusterKey变更了，因此原来的cluster值要被清理掉
	c.ClearClusterValue()
}

//通过路由规则插件添加复合元数据，不会进行缓存数据的变更
func (c *Cluster) RuleAddMetadata(key string, value string, composedValue string) bool {
	var values map[string]string
	var ok bool
	switch c.MetaCount {
	case 0:
		c.MetaComposedValue.metaKey = key
		c.MetaComposedValue.metaValue = value
		c.MetaComposedValue.composedValue = composedValue
		c.MetaCount++
		return true
	case 1:
		if c.MetaComposedValue.metaKey == key && c.MetaComposedValue.metaValue == value {
			return false
		}
		c.Metadata = make(map[string]map[string]string, 0)
		oneKeyValues := make(map[string]string, 0)
		oneKeyValues[c.MetaComposedValue.metaValue] = c.MetaComposedValue.composedValue
		c.Metadata[c.MetaComposedValue.metaKey] = oneKeyValues
	}
	if values, ok = c.Metadata[key]; !ok {
		values = make(map[string]string, 0)
		c.Metadata[key] = values
	}
	if _, ok = values[value]; !ok {
		values[value] = composedValue
		c.MetaCount++
		return true
	}
	return false
}

//获取完整的服务实例列表
func (c *Cluster) GetAllInstances() ([]Instance, int) {
	allInstances := c.GetClusterValue().GetAllInstanceSet()
	return allInstances.GetRealInstances(), allInstances.TotalWeight()
}

//通过服务实例全量列表获取集群服务实例
func (c *Cluster) GetInstances() ([]Instance, int) {
	instanceSet := c.GetClusterValue().GetInstancesSet(c.HasLimitedInstances, true)
	return instanceSet.GetRealInstances(), instanceSet.TotalWeight()
}

func (c *Cluster) GetInstancesWhenSkipRouteFilter() ([]Instance, int) {
	instanceSet := c.GetClusterValue().GetInstancesSetWhenSkipRouteFilter(c.HasLimitedInstances, true)
	return instanceSet.GetRealInstances(), instanceSet.TotalWeight()
}

//获取父集群
func (c *Cluster) GetClusters() ServiceClusters {
	return c.clusters
}

//清理集群值缓存
func (c *Cluster) ClearClusterValue() {
	c.value = nil
}

//重构建集群缓存
func (c *Cluster) GetClusterValue() *ClusterValue {
	if nil != c.value {
		return c.value
	}
	svcClusters := c.clusters
	c.value = svcClusters.GetClusterInstances(c.ClusterKey)
	if nil == c.value {
		c.value = c.buildCluster()
	}
	return c.value
}

//重构 包含Key但是不匹配 的缓存
func (c *Cluster) GetContainNotMatchMetaKeyClusterValue() *ClusterValue {
	if nil != c.value {
		return c.value
	}
	svcClusters := c.clusters
	c.value = svcClusters.GetContainNotMatchMetaKeyClusterInstances(c.ClusterKey)
	if nil == c.value {
		c.value = c.buildContainNotMatchMetaKeyCluster()
	}
	return c.value
}

//重构 不包含Key 的缓存
func (c *Cluster) GetNotContainMetaKeyClusterValue() *ClusterValue {
	if nil != c.value {
		return c.value
	}
	svcClusters := c.clusters
	c.value = svcClusters.GetNotContainMetaKeyClusterInstances(c.ClusterKey)
	if nil == c.value {
		c.value = c.buildNotContainMetaKeyCluster()
	}
	return c.value
}

//重构 包含Key 的缓存
func (c *Cluster) GetContainMetaKeyClusterValue() *ClusterValue {
	if nil != c.value {
		return c.value
	}
	svcClusters := c.clusters
	c.value = svcClusters.GetContainMetaKeyClusterInstances(c.ClusterKey)
	if nil == c.value {
		c.value = c.buildContainMetaKeyCluster()
	}
	return c.value
}

//根据给定集群构建索引
func (c *Cluster) buildCluster() *ClusterValue {
	clsKey := c.ClusterKey
	clsValue := newClusterValue(&c.ClusterKey, c.clusters)
	//noMetaClsValue := newClusterValue(&c.ClusterKey, c.clusters)
	clsCache := c.clusters.(*clusterCache)
	instances := clsCache.svcInstances.GetInstances()
	for index, inst := range instances {
		if !c.matchMetadata(inst) {
			continue
		}
		if !matchLocation(inst, c.Location) {
			continue
		}
		clsValue.addInstance(index, inst)
	}
	value, _ := clsCache.cacheValues.LoadOrStore(clsKey, clsValue)
	return value.(*ClusterValue)
}

//构建包含key但是不匹配value的索引
func (c *Cluster) buildContainNotMatchMetaKeyCluster() *ClusterValue {
	clsKey := c.ClusterKey
	clsValue := newClusterValue(&c.ClusterKey, c.clusters)
	//noMetaClsValue := newClusterValue(&c.ClusterKey, c.clusters)
	clsCache := c.clusters.(*clusterCache)
	instances := clsCache.svcInstances.GetInstances()
	for index, inst := range instances {
		if !c.containNotMatchMetadata(inst) {
			continue
		}
		if !matchLocation(inst, c.Location) {
			continue
		}
		clsValue.addInstance(index, inst)
	}
	value, _ := clsCache.notMatchMetaKeyCacheValues.LoadOrStore(clsKey, clsValue)
	return value.(*ClusterValue)
}

//构建不包含key的索引
func (c *Cluster) buildNotContainMetaKeyCluster() *ClusterValue {
	clsKey := c.ClusterKey
	clsValue := newClusterValue(&c.ClusterKey, c.clusters)
	//noMetaClsValue := newClusterValue(&c.ClusterKey, c.clusters)
	clsCache := c.clusters.(*clusterCache)
	instances := clsCache.svcInstances.GetInstances()
	for index, inst := range instances {
		if c.MatchContainMetaKeyData(inst) {
			continue
		}
		if !matchLocation(inst, c.Location) {
			continue
		}
		clsValue.addInstance(index, inst)
	}
	value, _ := clsCache.notContainMetaKeyCacheValues.LoadOrStore(clsKey, clsValue)
	return value.(*ClusterValue)
}

//构建包含key的索引
func (c *Cluster) buildContainMetaKeyCluster() *ClusterValue {
	clsKey := c.ClusterKey
	clsValue := newClusterValue(&c.ClusterKey, c.clusters)
	//noMetaClsValue := newClusterValue(&c.ClusterKey, c.clusters)
	clsCache := c.clusters.(*clusterCache)
	instances := clsCache.svcInstances.GetInstances()
	for index, inst := range instances {
		if !c.MatchContainMetaKeyData(inst) {
			continue
		}
		if !matchLocation(inst, c.Location) {
			continue
		}
		clsValue.addInstance(index, inst)
	}
	value, _ := clsCache.containMetaKeyCacheValues.LoadOrStore(clsKey, clsValue)
	return value.(*ClusterValue)
}

//带累加权重的索引
type WeightedIndex struct {
	Index            int
	AccumulateWeight int
}

//权重索引集合
type WeightIndexSlice []WeightedIndex

//备份节点Set
type ReplicateNodes struct {
	//所依附的集群缓存
	SvcClusters ServiceClusters
	//备份节点数
	Count int
	//实例下标
	Indexes []int
	//实例列表缓存
	Instances atomic.Value
}

//获取服务实例
func (r *ReplicateNodes) GetInstances() []Instance {
	instancesValue := r.Instances.Load()
	if !reflect2.IsNil(instancesValue) {
		return instancesValue.([]Instance)
	}
	if len(r.Indexes) == 0 {
		return nil
	}
	svcInstances := r.SvcClusters.GetServiceInstances()
	instances := make([]Instance, 0, len(r.Indexes))
	for _, idx := range r.Indexes {
		instances = append(instances, svcInstances.GetInstances()[idx])
	}
	r.Instances.Store(instances)
	return instances
}

//可供插件自定义的实例选择器
type ExtendedSelector interface {
	//选择实例下标

	Select(criteria interface{}) (int, *ReplicateNodes, error)

	//对应负载均衡插件的名字
	ID() int32
}

//插件自定义的实例选择器base
type SelectorBase struct {
	Id int32
}

func (s *SelectorBase) ID() int32 {
	return s.Id
}

//服务实例集合
type InstanceSet struct {
	clsCache ServiceClusters
	//服务实例下标权重集合
	weightedIndexes WeightIndexSlice
	//缓存的实例对象
	cachedInstances *atomic.Value
	//总权重
	totalWeight int
	//最大权重
	maxWeight int
	//扩展的实例选择器
	selector *sync.Map
	//加锁，防止重复创建selector
	lock sync.Mutex
}

//服务实例集合ToString
func (i *InstanceSet) String() string {
	return fmt.Sprintf(
		"{count: %v, totalWeight: %v, weightedIndexes: %v}", i.Count(), i.totalWeight, i.weightedIndexes)
}

//创建实例集合
func newInstanceSet(clsCache ServiceClusters) *InstanceSet {
	return &InstanceSet{
		clsCache:        clsCache,
		weightedIndexes: WeightIndexSlice{},
		totalWeight:     0,
		cachedInstances: &atomic.Value{},
		selector:        &sync.Map{},
	}
}

//设置selector
func (i *InstanceSet) SetSelector(selector ExtendedSelector) {
	i.selector.Store(selector.ID(), selector)
}

//获取selector
func (i *InstanceSet) GetSelector(id int32) ExtendedSelector {
	value, ok := i.selector.Load(id)
	if !ok {
		return nil
	}
	return value.(ExtendedSelector)
}

//加入实例到实例集合中
func (i *InstanceSet) addInstance(index int, instance Instance) {
	weight := instance.GetWeight()
	if weight > i.maxWeight {
		i.maxWeight = weight
	}
	i.totalWeight += weight
	i.weightedIndexes = append(i.weightedIndexes, WeightedIndex{
		Index:            index,
		AccumulateWeight: i.totalWeight,
	})
}

//获取实例下标集合
func (i *InstanceSet) GetInstances() WeightIndexSlice {
	return i.weightedIndexes
}

//获取实例对象集合
func (i *InstanceSet) GetRealInstances() []Instance {
	value := i.cachedInstances.Load()
	if !reflect2.IsNil(value) {
		return value.([]Instance)
	}
	instances := make([]Instance, 0, i.Count())
	allInstances := i.clsCache.GetServiceInstances().GetInstances()
	weightedInstances := i.weightedIndexes
	for _, weightedInstance := range weightedInstances {
		instances = append(instances, allInstances[weightedInstance.Index])
	}
	i.cachedInstances.Store(instances)
	return instances
}

//实例数
func (i *InstanceSet) Count() int {
	return len(i.weightedIndexes)
}

//总权重
func (i *InstanceSet) TotalWeight() int {
	return i.totalWeight
}

//最大权重
func (i *InstanceSet) MaxWeight() int {
	return i.maxWeight
}

//获取节点累积的权重
func (i *InstanceSet) GetValue(index int) uint64 {
	return uint64(i.weightedIndexes[index].AccumulateWeight)
}

//获取当前服务集群
func (i *InstanceSet) GetServiceClusters() ServiceClusters {
	return i.clsCache
}

//获取互斥锁，用于创建selector时使用，防止重复创建selector
func (i *InstanceSet) GetLock() sync.Locker {
	return &i.lock
}

//集群缓存VALUE对象
type ClusterValue struct {
	clsKey *ClusterKey
	//全量服务实例
	allInstances *InstanceSet
	//可被分配的服务实例（去掉隔离以及权重为0，包含不健康的）== 健康 + 熔断 + 不健康 level:2
	selectableInstances *InstanceSet
	//可以被分的服务实例（去掉隔离以及权重为0，不包含不健康的）==  健康+熔断的 level:1
	selectableInstancesWithoutUnhealthy *InstanceSet
	//健康的服务实例
	//健康的实例，没有任何非正常状态（包括熔断状态close的） level:0-1 GetOneInstance 负载均衡时，若选择的实例为半开且无配额是使用
	healthyInstances *InstanceSet
	//健康以及半开实例，只用于获取全量服务实例场景下使用   level:0
	availableInstances *InstanceSet
}

//缓存值的ToString
func (v ClusterValue) String() string {
	return fmt.Sprintf("{clsKey: %s, all: %s, healthy: %s, available: %s}",
		v.clsKey.String(), v.selectableInstances.String(), v.healthyInstances.String(), v.availableInstances.String())
}

//创建缓存值对象
func newClusterValue(clsKey *ClusterKey, cache ServiceClusters) *ClusterValue {
	return &ClusterValue{
		clsKey:                              clsKey,
		allInstances:                        newInstanceSet(cache),
		selectableInstances:                 newInstanceSet(cache),
		selectableInstancesWithoutUnhealthy: newInstanceSet(cache),
		healthyInstances:                    newInstanceSet(cache),
		availableInstances:                  newInstanceSet(cache),
	}
}

//获取实例集合，根据参数来进行选择返回全量或者健康
func (v *ClusterValue) GetInstancesSet(hasLimitedInstances bool, includeHalfOpen bool) *InstanceSet {
	if hasLimitedInstances {
		if v.selectableInstancesWithoutUnhealthy.totalWeight > 0 {
			return v.selectableInstancesWithoutUnhealthy
		}
		return v.selectableInstances
	}
	if includeHalfOpen {
		return v.availableInstances
	}
	return v.healthyInstances
}

func (v *ClusterValue) GetInstancesSetWhenSkipRouteFilter(hasLimitedInstances bool, includeHalfOpen bool) *InstanceSet {
	if hasLimitedInstances {
		return v.selectableInstances
	}
	if includeHalfOpen {
		return v.availableInstances
	}
	return v.healthyInstances
}

//获取全量服务实例集合
func (v *ClusterValue) GetAllInstanceSet() *InstanceSet {
	return v.allInstances
}

//获取cluster下的服务可分配实例数量
func (v *ClusterValue) Count() int {
	return v.selectableInstances.Count()
}

//往value中添加实例
func (v *ClusterValue) addInstance(index int, instance Instance) {
	v.allInstances.addInstance(index, instance)
	if instance.IsIsolated() || instance.GetWeight() == 0 {
		//被隔离以及权重为0，则完全不加入可分配缓存
		return
	}
	v.selectableInstances.addInstance(index, instance)
	if !instance.IsHealthy() {
		return
	}

	//可选健康服务实例（不包含不健康）
	v.selectableInstancesWithoutUnhealthy.addInstance(index, instance)
	cbStatus := instance.GetCircuitBreakerStatus()
	if (cbStatus != nil) && (cbStatus.GetStatus() == Open) {
		return
	}
	//可选服务实例（不包含熔断）
	v.availableInstances.addInstance(index, instance)
	if (cbStatus != nil) && (cbStatus.GetStatus() != Close) {
		return
	}
	v.healthyInstances.addInstance(index, instance)
}

//集群事件处理器
type ClusterEventHandler struct {
	//在集群值构建完毕后进行调用
	PostClusterValueBuilt func(value *ClusterValue)
}

//集群缓存接口
type ServiceClusters interface {
	//获取就近集群
	GetNearbyCluster(location Location) (*Cluster, int)
	//设置就近集群
	SetNearbyCluster(location Location, cluster *Cluster, matchLevel int)
	//获取缓存值
	GetClusterInstances(cacheKey ClusterKey) *ClusterValue
	//获取 包含指定key但是不匹配value 的ClusterValue
	GetContainNotMatchMetaKeyClusterInstances(cacheKey ClusterKey) *ClusterValue
	//获取不包含指定meta的ClusterValue
	GetNotContainMetaKeyClusterInstances(cacheKey ClusterKey) *ClusterValue
	//获取包含指定meta key的ClusterValue
	GetContainMetaKeyClusterInstances(cacheKey ClusterKey) *ClusterValue
	//构建缓存实例
	AddInstance(instance Instance)
	//服务存在该大区
	HasRegion(region string) bool
	//服务存在该园区
	HasZone(zone string) bool
	//服务存在该机房
	HasCampus(campus string) bool
	//是否开启了就近路由
	IsNearbyEnabled() bool
	//是否开启了金丝雀路由
	IsCanaryEnabled() bool
	//获取实例的标签集合
	GetInstanceMetaValues(location Location, metaKey string) map[string]string
	//获取服务二元组信息
	GetServiceKey() ServiceKey
	//获取所属服务
	GetServiceInstances() ServiceInstances
	//获取扩展的缓存值
	GetExtendedCacheValue(pluginIndex int) interface{}
	//设置扩展的缓存值，需要预初始化好，否则会有并发修改的问题
	SetExtendedCacheValue(pluginIndex int, value interface{})
}

//基于某个地域信息的元数据查询key
type LocationBasedMetaKey struct {
	location Location
	metaKey  string
}

//标签缓存信息
type metaDataInService struct {
	//健康实例中包含的元数据信息
	//map[LocationBasedMetaKey]map[string]string
	metaDataSet sync.Map
}

//服务级别的地域信息
type locationInSvc struct {
	regions HashSet
	zones   HashSet
	campus  HashSet
}

//字符串输出
func (l locationInSvc) String() string {
	return fmt.Sprintf("{regions: %v, zones: %v, campus: %v}", l.regions, l.zones, l.campus)
}

//往locationInSvc中添加实例
func (l *locationInSvc) addInstance(instance Instance) {
	if len(instance.GetRegion()) > 0 {
		l.regions.Add(instance.GetRegion())
	}
	if len(instance.GetZone()) > 0 {
		l.zones.Add(instance.GetZone())
	}
	if len(instance.GetCampus()) > 0 {
		l.campus.Add(instance.GetCampus())
	}
}

//当前客户端的集群
type clientLocationCluster struct {
	Location
	cluster *Cluster
	matchLevel int
}

//集群缓存
type clusterCache struct {
	//当前服务二元组信息
	svcKey ServiceKey
	//是否开启就近路由
	enabledNearby bool
	//是否开启金丝雀路由
	enabledCanary bool
	//与当前客户端就近的集群缓存
	nearbyCluster atomic.Value
	//服务集合
	svcInstances ServiceInstances
	//按服务来提取的元数据索引
	svcLevelMetadata metaDataInService
	//按服务维度来提取的地域总和
	svcLocations locationInSvc
	//缓存值map
	//keyType=ClusterKey, valueType=*ClusterValue
	cacheValues sync.Map

	notMatchMetaKeyCacheValues sync.Map

	notContainMetaKeyCacheValues sync.Map

	containMetaKeyCacheValues sync.Map
	//扩展的缓存索引
	//key为pluginId, value为值
	extendedCacheValues sync.Map
}

//创建集群缓存
func NewServiceClusters(svcInstances ServiceInstances) ServiceClusters {
	var nearbyEnabled bool
	var canaryEnabled bool = false
	svcMetadata := svcInstances.GetMetadata()
	if len(svcMetadata) > 0 {
		enableNearbyMeta, ok := svcMetadata[NearbyMetadataEnable]
		if ok {
			nearbyEnabled, _ = strconv.ParseBool(enableNearbyMeta)
		}
		enableCanaryByMeta, ok := svcMetadata[CanaryMetadataEnable]
		if ok {
			canaryEnabled, _ = strconv.ParseBool(enableCanaryByMeta)
		}
	}
	return &clusterCache{
		svcKey: ServiceKey{
			Namespace: svcInstances.GetNamespace(),
			Service:   svcInstances.GetService(),
		},
		svcInstances: svcInstances,
		svcLocations: locationInSvc{
			regions: HashSet{},
			zones:   HashSet{},
			campus:  HashSet{},
		},
		svcLevelMetadata: metaDataInService{},
		enabledNearby:    nearbyEnabled,
		enabledCanary:    canaryEnabled,
	}
}

//获取就近集群
func (c *clusterCache) GetNearbyCluster(location Location) (*Cluster, int) {
	clusterValue := c.nearbyCluster.Load()
	if reflect2.IsNil(clusterValue) {
		return nil, 0
	}
	clientCluster := clusterValue.(*clientLocationCluster)
	if clientCluster.Location == location {
		return clientCluster.cluster, clientCluster.matchLevel
	}
	return nil, 0
}

//设置就近集群
func (c *clusterCache) SetNearbyCluster(location Location, cluster *Cluster, matchLevel int) {
	nCluster := NewCluster(c, cluster)
	nCluster.value = cluster.GetClusterValue()
	nCluster.HasLimitedInstances = cluster.HasLimitedInstances
	//常驻缓存的cluster，无需进行回收
	nCluster.SetReuse(false)
	nCluster.MissLocationInstances = cluster.MissLocationInstances
	nCluster.LocationMatchInfo = cluster.LocationMatchInfo
	c.nearbyCluster.Store(&clientLocationCluster{
		Location: location,
		cluster:  nCluster,
		matchLevel: matchLevel,
	})
}

//获取服务标识
func (c *clusterCache) GetServiceKey() ServiceKey {
	return c.svcKey
}

//获取服务实例集合
func (c *clusterCache) GetServiceInstances() ServiceInstances {
	return c.svcInstances
}

//服务是否存在该区域
func (c *clusterCache) HasRegion(region string) bool {
	if len(region) == 0 {
		return true
	}
	return c.svcLocations.regions.Contains(region)
}

//获取元数据组合值
func buildComposedValue(metaKey string, value string) string {
	totalLen := len(metaKey) + len(value) + 1
	buf := bytes.NewBuffer(make([]byte, 0, totalLen))
	buf.WriteString(metaKey)
	buf.WriteString(":")
	buf.WriteString(value)
	return buf.String()
}

//获取健康实例的标签值
func (c *clusterCache) GetInstanceMetaValues(location Location, metaKey string) map[string]string {
	var value interface{}
	var exists bool
	locationKey := LocationBasedMetaKey{location: location, metaKey: metaKey}
	value, exists = c.svcLevelMetadata.metaDataSet.Load(locationKey)
	if exists {
		return value.(map[string]string)
	}
	//按需构建指定地域的实例元数据集合
	instances := c.svcInstances.GetInstances()
	metaValueSet := make(map[string]string, 0)
	for _, instance := range instances {
		if !matchLocation(instance, location) {
			continue
		}
		if instance.IsIsolated() || instance.GetWeight() == 0 {
			continue
		}
		metadata := instance.GetMetadata()
		if len(metadata) == 0 {
			continue
		}
		var value string
		var ok bool
		if value, ok = metadata[metaKey]; !ok {
			continue
		}
		metaValueSet[value] = buildComposedValue(metaKey, value)
	}
	value, _ = c.svcLevelMetadata.metaDataSet.LoadOrStore(locationKey, metaValueSet)
	return value.(map[string]string)
}

//服务是否存在该区域
func (c *clusterCache) HasZone(zone string) bool {
	if len(zone) == 0 {
		return true
	}
	return c.svcLocations.zones.Contains(zone)
}

//服务是否存在该区域
func (c *clusterCache) HasCampus(campus string) bool {
	if len(campus) == 0 {
		return true
	}
	return c.svcLocations.campus.Contains(campus)
}

//获取缓存值
func (c *clusterCache) GetClusterInstances(cacheKey ClusterKey) *ClusterValue {
	if value, ok := c.cacheValues.Load(cacheKey); ok {
		return value.(*ClusterValue)
	}
	return nil
}

//获取 包含指定key但是不匹配value 的ClusterValue
func (c *clusterCache) GetContainNotMatchMetaKeyClusterInstances(cacheKey ClusterKey) *ClusterValue {
	if value, ok := c.notMatchMetaKeyCacheValues.Load(cacheKey); ok {
		return value.(*ClusterValue)
	}
	return nil
}

//获取不包含指定meta key的ClusterValue
func (c *clusterCache) GetNotContainMetaKeyClusterInstances(cacheKey ClusterKey) *ClusterValue {
	if value, ok := c.notContainMetaKeyCacheValues.Load(cacheKey); ok {
		return value.(*ClusterValue)
	}
	return nil
}

//获取包含指定meta key的ClusterValue
func (c *clusterCache) GetContainMetaKeyClusterInstances(cacheKey ClusterKey) *ClusterValue {
	if value, ok := c.containMetaKeyCacheValues.Load(cacheKey); ok {
		return value.(*ClusterValue)
	}
	return nil
}

//对服务实例进行缓存设置
func (c *clusterCache) AddInstance(instance Instance) {
	//去除被隔离的实例
	if instance.IsIsolated() || instance.GetWeight() == 0 {
		return
	}
	c.svcLocations.addInstance(instance)
}

//获取服务级别的元数据值
func (c *clusterCache) IsNearbyEnabled() bool {
	return c.enabledNearby
}

func (c *clusterCache) IsCanaryEnabled() bool {
	return c.enabledCanary
}

//获取扩展的缓存值
func (c *clusterCache) GetExtendedCacheValue(pluginIndex int) interface{} {
	var value interface{}
	var ok bool
	if value, ok = c.extendedCacheValues.Load(pluginIndex); !ok {
		return nil
	}
	return value
}

//设置扩展的缓存值，需要预初始化好，否则会有并发修改的问题
func (c *clusterCache) SetExtendedCacheValue(pluginIndex int, value interface{}) {
	c.extendedCacheValues.Store(pluginIndex, value)
}

//标签匹配
func (c *Cluster) matchMetadata(instance Instance) bool {
	if c.MetaCount == 0 {
		return true
	}
	instMetadata := instance.GetMetadata()
	if len(instMetadata) == 0 {
		return false
	}
	if c.MetaCount == 1 {
		if instValue, ok := instMetadata[c.MetaComposedValue.metaKey]; ok {
			return instValue == c.MetaComposedValue.metaValue
		}
		return false
	}
	compareMetadata := c.Metadata
	for compareKey, compareValue := range compareMetadata {
		if instValue, ok := instMetadata[compareKey]; ok {
			if _, ok := compareValue[instValue]; !ok {
				return false
			}
		} else {
			return false
		}
	}
	return true
}

//判断是否包含标签key但是不匹配value
func (c *Cluster) containNotMatchMetadata(instance Instance) bool {
	if c.MetaCount == 0 {
		return false
	}
	instMetadata := instance.GetMetadata()
	if len(instMetadata) == 0 {
		return false
	}
	if c.MetaCount == 1 {
		if instValue, ok := instMetadata[c.MetaComposedValue.metaKey]; ok {
			return instValue != c.MetaComposedValue.metaValue
		}
		return false
	}
	compareMetadata := c.Metadata
	for compareKey, compareValue := range compareMetadata {
		if instValue, ok := instMetadata[compareKey]; ok {
			if _, ok := compareValue[instValue]; !ok {
				return true
			} else {
				return false
			}
		} else {
			return false
		}
	}
	return false
}

//判断是否包含标签key
func (c *Cluster) MatchContainMetaKeyData(instance Instance) bool {
	if c.MetaCount == 0 {
		return false
	}
	instMetadata := instance.GetMetadata()
	if len(instMetadata) == 0 {
		return false
	}
	compareMetadataKey := c.MetaComposedValue.metaKey
	if _, ok := instMetadata[compareMetadataKey]; ok {
		return true
	}
	return false
}

//地域信息匹配
func matchLocation(instance Instance, location Location) bool {
	region := instance.GetRegion()
	zone := instance.GetZone()
	campus := instance.GetCampus()
	if len(location.Region) > 0 && len(region) > 0 && location.Region != region {
		return false
	}
	if len(location.Zone) > 0 && len(zone) > 0 && location.Zone != zone {
		return false
	}
	if len(location.Campus) > 0 && len(campus) > 0 && location.Campus != campus {
		return false
	}
	return true
}
