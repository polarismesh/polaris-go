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
	"github.com/polarismesh/polaris-go/pkg/clock"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/local"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/serverconnector"
	lrplug "github.com/polarismesh/polaris-go/plugin/localregistry/common"
	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/ptypes/wrappers"
	"github.com/modern-go/reflect2"
	"strings"
	"sync/atomic"
	"time"
)

type persistOpType int

const (
	addCache    persistOpType = 0
	deleteCache persistOpType = 1
)

//持久化任务
type persistTask struct {
	op       persistOpType
	protoMsg proto.Message
}

//缓存状态
type CachedStatus int

const (
	CacheNotExists CachedStatus = iota + 1
	CacheChanged
	CacheNotChanged
	//cache是空的，但是server没有返回data
	CacheEmptyButNoData
)

var (
	CachedStatusToPresent = map[CachedStatus]string{
		CacheNotExists:  "CacheNotExists",
		CacheChanged:    "CacheChanged",
		CacheNotChanged: "CacheNotChanged",
	}
)

//缓存状态ToString
func (c CachedStatus) String() string {
	return CachedStatusToPresent[c]
}

//上报熔断状态变化
type circuitBreakGauge struct {
	model.EmptyInstanceGauge
	changeInstance   model.Instance
	previousCBStatus model.CircuitBreakerStatus
}

//获取变化前的熔断状态
func (cbg *circuitBreakGauge) GetCircuitBreakerStatus() model.CircuitBreakerStatus {
	return cbg.previousCBStatus
}

//获取状态发生改变的实例
func (cbg *circuitBreakGauge) GetCalledInstance() model.Instance {
	return cbg.changeInstance
}

//检测指标是否合法
func (cbg *circuitBreakGauge) Validate() error {
	if !reflect2.IsNil(cbg.changeInstance) {
		return nil
	}
	return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil, "empty change instance")
}

//不同的事件回调函数
type CacheHandlers struct {
	//消息比较，返回比较结果
	CompareMessage func(cacheValue interface{}, newMessage proto.Message) CachedStatus
	//原始消息转换为缓存对象
	MessageToCacheValue func(cacheValue interface{}, newMessage proto.Message,
		svcLocalValue local.ServiceLocalValue, cacheLoaded bool) model.RegistryValue
	//缓存被删除
	OnEventDeleted func(key *model.ServiceEventKey, cacheValue interface{})
	//缓存更新的后续擦欧洲哦
	PostCacheUpdated func(svcKey *model.ServiceEventKey, newCacheValue interface{}, preCacheStatus CachedStatus)
}

//缓存值的管理基类
type CacheObject struct {
	//最后一次访问的时间，初始化时为加入轮询队列的时间
	lastVisitTime   int64
	value           atomic.Value
	serviceValueKey *model.ServiceEventKey
	Handler         CacheHandlers
	registry        *LocalCache
	inValid         uint32
	//服务的localValue，只有当类型为instances才不为空
	svcLocalValue local.ServiceLocalValue
	//创建出来的时间
	createTime time.Time
	notifier   *common.Notifier
	//是否经过远程更新
	hasRemoteUpdated uint32
	//是否已经注册了connector监听
	hasRegistered uint32
	//标记这个服务对象是否已经删除了，防止connector收到多次服务不存在的消息，导致重复删除
	hasDeleted uint32
	//是否已经触发服务新增回调
	hasNotifyServiceAdded uint32
	//在没有经过远程更新的情况下是否直接可用
	cachePersistentAvailable uint32
	//服务是否被订阅
	serviceIsWatched uint32
}

//创建缓存对象
func NewCacheObject(
	handler CacheHandlers, registry *LocalCache, serviceValueKey *model.ServiceEventKey) *CacheObject {
	res := &CacheObject{
		serviceValueKey:  serviceValueKey,
		registry:         registry,
		Handler:          handler,
		inValid:          0,
		notifier:         common.NewNotifier(),
		createTime:       clock.GetClock().Now(),
		lastVisitTime:    clock.GetClock().Now().UnixNano(),
		serviceIsWatched: 0,
	}
	if serviceValueKey.Type == model.EventInstances {
		res.svcLocalValue = local.NewServiceLocalValue()
	}
	return res
}

//创建带初始值的缓存对象
func NewCacheObjectWithInitValue(handler CacheHandlers, registry *LocalCache,
	serviceValueKey *model.ServiceEventKey, message proto.Message) *CacheObject {
	cacheObject := &CacheObject{
		serviceValueKey: serviceValueKey,
		registry:        registry,
		Handler:         handler,
		inValid:         0,
		lastVisitTime:   clock.GetClock().Now().UnixNano(),
	}
	if serviceValueKey.Type == model.EventInstances {
		cacheObject.svcLocalValue = local.NewServiceLocalValue()
	}
	cacheValue := handler.MessageToCacheValue(nil, message, cacheObject.svcLocalValue, true)
	cacheObject.SetValue(cacheValue)
	cacheObject.notifier = common.NewNotifier()
	cacheObject.createTime = clock.GetClock().Now()
	return cacheObject
}

//将本缓存值为不可用，只用于首次请求时，向后端connector监听失败的场景
func (s *CacheObject) MakeInValid(err model.SDKError) {
	if atomic.CompareAndSwapUint32(&s.inValid, 0, 1) {
		s.notifier.Notify(err)
	}
}

//判断缓存是否不可用
func (s *CacheObject) IsInValid() bool {
	return atomic.LoadUint32(&s.inValid) > 0
}

//判断缓存值是否有效
func (s *CacheObject) isValueAvailable() bool {
	if s.IsInValid() {
		return false
	}
	value := s.LoadValue(false)
	if reflect2.IsNil(value) {
		return false
	}
	return true
}

//判断缓存值是否可读取
func (s *CacheObject) LoadValue(updateVisitTime bool) interface{} {
	if updateVisitTime {
		atomic.StoreInt64(&s.lastVisitTime, clock.GetClock().Now().UnixNano())
	}
	value := s.value.Load()
	if reflect2.IsNil(value) {
		return nil
	}
	if atomic.CompareAndSwapUint32(&s.hasNotifyServiceAdded, 0, 1) {
		eventObject := &common.ServiceEventObject{
			SvcEventKey: *s.serviceValueKey,
			OldValue:    nil,
			NewValue:    value,
		}
		//如果是限流规则，计算diffinfo
		if s.serviceValueKey.Type == model.EventRateLimiting {
			eventObject.DiffInfo = calcRateLimitDiffInfo(nil, extractRateLimitFromCacheValue(value))
		}
		if s.serviceValueKey.Type == model.EventMeshConfig {
			eventObject.DiffInfo = s.calcMeshResourceDiffInfo(nil, extractMeshConfigFromCacheValue(value))
		}
		s.notifyServiceAdded(eventObject)
	}
	return value
}

//触发服务新增事件
func (s *CacheObject) notifyServiceAdded(value interface{}) {
	addHandlers := s.registry.plugins.GetEventSubscribers(common.OnServiceAdded)
	if len(addHandlers) > 0 {
		event := &common.PluginEvent{
			EventType: common.OnServiceAdded, EventObject: value}
		for _, handler := range addHandlers {
			handler.Callback(event)
		}
	}
}

//获取通知对象
func (s *CacheObject) GetNotifier() *common.Notifier {
	return s.notifier
}

//服务远程实例更新事件到来后的回调操作
func (s *CacheObject) OnServiceUpdate(event *serverconnector.ServiceEvent) bool {
	err, svcEventKey := event.Error, &event.ServiceEventKey
	//更新标记为，表示该对象已经经过远程更新
	atomic.StoreUint32(&s.hasRemoteUpdated, 1)
	var svcDeleted bool
	if nil != err {
		//收取消息有出错
		instancesValue := s.LoadValue(false)
		//没有服务信息直接删除
		if atomic.CompareAndSwapUint32(&s.hasDeleted, 0, 1) &&
			(model.ErrCodeServiceNotFound == err.ErrorCode() || model.ErrCodeMeshConfigNotFound == err.ErrorCode()) {
			s.Handler.OnEventDeleted(svcEventKey, instancesValue)
			eventObject := &common.ServiceEventObject{SvcEventKey: *svcEventKey, OldValue: instancesValue}
			if svcEventKey.Type == model.EventRateLimiting {
				eventObject.DiffInfo = calcRateLimitDiffInfo(extractRateLimitFromCacheValue(instancesValue), nil)
			}
			if svcEventKey.Type == model.EventMeshConfig {
				eventObject.DiffInfo = s.calcMeshResourceDiffInfo(extractMeshConfigFromCacheValue(instancesValue), nil)
			}
			deleteHandlers := s.registry.plugins.GetEventSubscribers(common.OnServiceDeleted)
			if !reflect2.IsNil(instancesValue) && len(deleteHandlers) > 0 {
				dEvent := &common.PluginEvent{
					EventType: common.OnServiceDeleted, EventObject: eventObject}
				for _, handler := range deleteHandlers {
					handler.Callback(dEvent)
				}
			}
			svcDeleted = true
		} else {
			log.GetBaseLogger().Errorf("OnServiceUpdate: fail to update %s for err %v", *svcEventKey, err)
		}
	} else {
		message := event.Value
		cachedValue := s.value.Load()
		cachedStatus := s.Handler.CompareMessage(cachedValue, message)
		if cachedStatus == CacheChanged || cachedStatus == CacheNotExists {
			log.GetBaseLogger().Infof("OnServiceUpdate: cache %s is pending to update", *svcEventKey)
			svcCacheFile := lrplug.ServiceEventKeyToFileName(*svcEventKey)
			s.registry.PersistMessage(svcCacheFile, message)
			cacheValue := s.Handler.MessageToCacheValue(cachedValue, message, s.svcLocalValue, false)
			s.SetValue(cacheValue)
			postCacheUpdated := s.Handler.PostCacheUpdated
			if nil != postCacheUpdated {
				postCacheUpdated(svcEventKey, cacheValue, cachedStatus)
			}
			eventObject := &common.ServiceEventObject{SvcEventKey: *svcEventKey,
				OldValue: cachedValue, NewValue: cacheValue}
			if svcEventKey.Type == model.EventRateLimiting {
				eventObject.DiffInfo = calcRateLimitDiffInfo(extractRateLimitFromCacheValue(cachedValue),
					extractRateLimitFromCacheValue(cacheValue))
			}
			if svcEventKey.Type == model.EventMeshConfig {
				eventObject.DiffInfo = s.calcMeshResourceDiffInfo(extractMeshConfigFromCacheValue(cachedValue),
					extractMeshConfigFromCacheValue(cacheValue))
			}
			updateHandlers := s.registry.plugins.GetEventSubscribers(common.OnServiceUpdated)
			//更新后的cacheValue不会为空
			if cachedStatus == CacheChanged && len(updateHandlers) > 0 {
				uEvent := &common.PluginEvent{EventType: common.OnServiceUpdated, EventObject: eventObject}
				for _, handler := range updateHandlers {
					handler.Callback(uEvent)
				}
			}
		} else if cachedStatus == CacheEmptyButNoData {
			log.GetBaseLogger().Errorf("%s, OnServiceUpdate: %s is empty, but discover returns no data",
				s.registry.GetSDKContextID(), svcEventKey)
		} else {
			switch event.Type {
			case model.EventInstances:
				atomic.StoreInt32(&cachedValue.(*pb.ServiceInstancesInProto).CacheLoaded, 0)
			case model.EventRouting:
				atomic.StoreInt32(&cachedValue.(*pb.ServiceRuleInProto).CacheLoaded, 0)
			case model.EventMeshConfig:
				atomic.StoreInt32(&cachedValue.(*pb.MeshConfigProto).CacheLoaded, 0)
			case model.EventMesh:
				atomic.StoreInt32(&cachedValue.(*pb.MeshProto).CacheLoaded, 0)
			}
		}
	}
	s.notifier.Notify(err)
	return svcDeleted
}

//从缓存的值中提取namingpb.RateLimit限流规则
func extractRateLimitFromCacheValue(cacheValue interface{}) *namingpb.RateLimit {
	if reflect2.IsNil(cacheValue) {
		return nil
	}
	return cacheValue.(model.ServiceRule).GetValue().(*namingpb.RateLimit)
}

//从缓存的值中提取namingpb.MeshConfig网格规则
func extractMeshConfigFromCacheValue(cacheValue interface{}) *namingpb.MeshConfig {
	if reflect2.IsNil(cacheValue) {
		return nil
	}
	return cacheValue.(model.MeshConfig).GetValue().(*namingpb.MeshConfig)
}

// 计算新旧限流规则的变化信息
func calcRateLimitDiffInfo(oldRule *namingpb.RateLimit, newRule *namingpb.RateLimit) *common.RateLimitDiffInfo {
	updatedRules := make(map[string]*common.RevisionChange)
	deletedRules := make(map[string]string)
	if newRule != nil {
		for _, rule := range newRule.GetRules() {
			updatedRules[rule.GetId().GetValue()] = &common.RevisionChange{
				OldRevision: "",
				NewRevision: rule.GetRevision().GetValue(),
			}
		}
	}
	if oldRule != nil {
		for _, rule := range oldRule.GetRules() {
			newRevision, ok := updatedRules[rule.GetId().GetValue()]
			if !ok {
				deletedRules[rule.GetId().GetValue()] = rule.GetRevision().GetValue()
			} else {
				if newRevision.NewRevision == rule.GetRevision().GetValue() {
					delete(updatedRules, rule.GetId().GetValue())
				} else {
					newRevision.OldRevision = rule.GetRevision().GetValue()
				}
			}
		}
	}
	return &common.RateLimitDiffInfo{
		UpdatedRules: updatedRules,
		DeletedRules: deletedRules,
	}
}

// 计算新旧网格规则的变化信息
func (s *CacheObject) calcMeshResourceDiffInfo(oldResource *namingpb.MeshConfig,
	newResource *namingpb.MeshConfig) *common.MeshResourceDiffInfo {
	updatedResources := make(map[string]*common.RevisionChange)
	deletedResources := make(map[string]string)
	if newResource != nil {
		for _, resource := range newResource.GetResources() {
			updatedResources[resource.GetName().GetValue()] = &common.RevisionChange{
				OldRevision: "",
				NewRevision: resource.GetRevision().GetValue(),
			}
		}
	}
	if oldResource != nil {
		for _, resource := range oldResource.GetResources() {
			newRevision, ok := updatedResources[resource.GetName().GetValue()]
			if !ok {
				deletedResources[resource.GetName().GetValue()] = resource.GetRevision().GetValue()
			} else {
				if newRevision.NewRevision == resource.GetRevision().GetValue() {
					delete(updatedResources, resource.GetName().GetValue())
				} else {
					newRevision.OldRevision = resource.GetRevision().GetValue()
				}
			}
		}
	}
	return &common.MeshResourceDiffInfo{
		MeshID:           s.GetMeshConfig().GetMeshId().GetValue(),
		ResourceType:     s.GetMeshResource(),
		UpdatedResources: updatedResources,
		DeletedResources: deletedResources,
	}
}

//获取服务对象的版本号
func (s *CacheObject) GetRevision() string {
	value := s.LoadValue(false)
	if nil == value {
		return ""
	}
	svcValue := value.(model.RegistryValue)
	return svcValue.GetRevision()
}

//设置缓存对象
func (s *CacheObject) SetValue(cacheValue model.RegistryValue) {
	s.value.Store(cacheValue)
	log.GetBaseLogger().Infof(
		"CacheObject: value for %s is updated, revision %s", *s.serviceValueKey, cacheValue.GetRevision())
}

func (s *CacheObject) GetMeshResource() *namingpb.MeshResource {
	meshinfos := strings.Split(s.serviceValueKey.Service, model.MeshKeySpliter)
	out := &namingpb.MeshResource{}
	if s.serviceValueKey.Type == model.EventMeshConfig && len(meshinfos) ==
		model.MeshKeyLen && meshinfos[0] == model.MeshPrefix {
		out.MeshName = &wrappers.StringValue{Value: meshinfos[1]}
		out.TypeUrl = &wrappers.StringValue{Value: meshinfos[2]}
		out.Revision = &wrappers.StringValue{Value: s.GetRevision()}
		log.GetBaseLogger().Debugf("(s *CacheObject) GetMeshResource", meshinfos, out)
	}
	return out
}

func (s *CacheObject) GetMeshConfig() *namingpb.MeshConfig {
	meshinfos := strings.Split(s.serviceValueKey.Service, model.MeshKeySpliter)
	out := &namingpb.MeshConfig{}
	if s.serviceValueKey.Type == model.EventMeshConfig && len(meshinfos) ==
		model.MeshKeyLen && meshinfos[0] == model.MeshPrefix {
		out.MeshId = &wrappers.StringValue{Value: meshinfos[1]}
		out.TypeUrl = &wrappers.StringValue{Value: meshinfos[2]}
		out.Revision = &wrappers.StringValue{Value: s.GetRevision()}
		log.GetBaseLogger().Infof("(s *CacheObject) GetMeshConfig", meshinfos, out)
	}
	return out
}

func (s *CacheObject) GetBusiness() string {
	if s.serviceValueKey.Type == model.EventServices {
		return s.serviceValueKey.Service
	}
	return ""
}
