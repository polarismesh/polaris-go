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
	"sync/atomic"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/modern-go/reflect2"

	"github.com/polarismesh/polaris-go/pkg/clock"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/local"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/serverconnector"
	lrplug "github.com/polarismesh/polaris-go/plugin/localregistry/common"
)

type persistOpType int

const (
	addCache    persistOpType = 0
	deleteCache persistOpType = 1
)

// 持久化任务
type persistTask struct {
	op       persistOpType
	protoMsg proto.Message
}

// CachedStatus 缓存状态
type CachedStatus int

const (
	// CacheAdded 缓存数据从无到有
	CacheAdded CachedStatus = iota + 1
	// CacheChanged 缓存已存在，发生了数据改变
	CacheChanged
	// CacheNotChanged 缓存未改变
	CacheNotChanged
	// CacheEmptyButNotChanged 缓存不存在，但是server返回DataNotChanged
	CacheEmptyButNotChanged
	// CacheDeleted 服务数据已经被删除
	CacheDeleted
)

var (
	// CachedStatusToPresent 将缓存状态转换为present状态
	CachedStatusToPresent = map[CachedStatus]string{
		CacheAdded:              "CacheAdded",
		CacheChanged:            "CacheChanged",
		CacheNotChanged:         "CacheNotChanged",
		CacheEmptyButNotChanged: "CacheEmptyButNotChanged",
		CacheDeleted:            "CacheDeleted",
	}
)

// String 缓存状态ToString
func (c CachedStatus) String() string {
	return CachedStatusToPresent[c]
}

// 上报熔断状态变化
type circuitBreakGauge struct {
	model.EmptyInstanceGauge
	changeInstance   model.Instance
	previousCBStatus model.CircuitBreakerStatus
}

// GetCircuitBreakerStatus 获取变化前的熔断状态
func (cbg *circuitBreakGauge) GetCircuitBreakerStatus() model.CircuitBreakerStatus {
	return cbg.previousCBStatus
}

// GetCalledInstance 获取状态发生改变的实例
func (cbg *circuitBreakGauge) GetCalledInstance() model.Instance {
	return cbg.changeInstance
}

// Validate 检测指标是否合法
func (cbg *circuitBreakGauge) Validate() error {
	if !reflect2.IsNil(cbg.changeInstance) {
		return nil
	}
	return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil, "empty change instance")
}

// CacheHandlers 不同的事件回调函数
type CacheHandlers struct {
	// CompareMessage 消息比较，返回比较结果
	CompareMessage func(cacheValue interface{}, newMessage proto.Message) CachedStatus
	// MessageToCacheValue 原始消息转换为缓存对象
	MessageToCacheValue func(cacheValue interface{}, newMessage proto.Message,
		svcLocalValue local.ServiceLocalValue, cacheLoaded bool) model.RegistryValue
	// OnEventDeleted 缓存被删除
	OnEventDeleted func(key *model.ServiceEventKey, cacheValue interface{})
}

// CacheObject 缓存值的管理基类
type CacheObject struct {
	// 最后一次访问的时间，初始化时为加入轮询队列的时间
	lastVisitTime   int64
	value           atomic.Value
	serviceValueKey *model.ServiceEventKey
	Handler         CacheHandlers
	registry        *LocalCache
	inValid         uint32
	// 服务的localValue，只有当类型为instances才不为空
	svcLocalValue local.ServiceLocalValue
	// 创建出来的时间
	createTime time.Time
	notifier   *common.Notifier
	// 是否经过远程更新
	hasRemoteUpdated uint32
	// 是否已经注册了connector监听
	hasRegistered uint32
	// 标记这个服务对象是否已经删除了，防止connector收到多次服务不存在的消息，导致重复删除
	hasDeleted uint32
	// 在没有经过远程更新的情况下是否直接可用
	cachePersistentAvailable uint32
	// 是否为远程服务端出现错误无法获取数据
	hasRemoteError uint32
}

// NewCacheObject 创建缓存对象
func NewCacheObject(
	handler CacheHandlers, registry *LocalCache, serviceValueKey *model.ServiceEventKey) *CacheObject {
	res := &CacheObject{
		serviceValueKey: serviceValueKey,
		registry:        registry,
		Handler:         handler,
		inValid:         0,
		notifier:        common.NewNotifier(),
		createTime:      clock.GetClock().Now(),
		lastVisitTime:   clock.GetClock().Now().UnixNano(),
	}
	if serviceValueKey.Type == model.EventInstances {
		res.svcLocalValue = local.NewServiceLocalValue()
	}
	return res
}

// NewCacheObjectWithInitValue 创建带初始值的缓存对象
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

// MakeInValid 将本缓存值为不可用，只用于首次请求时，向后端connector监听失败的场景
func (s *CacheObject) MakeInValid(err model.SDKError) {
	if atomic.CompareAndSwapUint32(&s.inValid, 0, 1) {
		s.notifier.Notify(err)
	}
}

// IsInValid 判断缓存是否不可用
func (s *CacheObject) IsInValid() bool {
	return atomic.LoadUint32(&s.inValid) > 0
}

// 判断缓存值是否有效
func (s *CacheObject) isValueAvailable() bool {
	if s.IsInValid() {
		return false
	}
	value := s.LoadValue(false)
	return !reflect2.IsNil(value)
}

// LoadValue 判断缓存值是否可读取
func (s *CacheObject) LoadValue(updateVisitTime bool) interface{} {
	if updateVisitTime {
		atomic.StoreInt64(&s.lastVisitTime, clock.GetClock().Now().UnixNano())
	}
	value := s.value.Load()
	if reflect2.IsNil(value) {
		return nil
	}
	return value
}

// 触发服务新增事件
func (s *CacheObject) notifyServiceAdded(value interface{}) {
	addHandlers := s.registry.plugins.GetEventSubscribers(common.OnServiceAdded)
	if len(addHandlers) > 0 {
		event := &common.PluginEvent{
			EventType: common.OnServiceAdded, EventObject: value}
		for _, handler := range addHandlers {
			_ = handler.Callback(event)
		}
	}
}

// GetNotifier 获取通知对象
func (s *CacheObject) GetNotifier() *common.Notifier {
	return s.notifier
}

func (s *CacheObject) notifyEventHandlers(eventObject *common.ServiceEventObject, status CachedStatus) {
	switch status {
	case CacheAdded:
		addHandlers := s.registry.plugins.GetEventSubscribers(common.OnServiceAdded)
		if len(addHandlers) > 0 {
			uEvent := &common.PluginEvent{EventType: common.OnServiceAdded, EventObject: eventObject}
			for _, handler := range addHandlers {
				_ = handler.Callback(uEvent)
			}
		}
	case CacheChanged:
		updateHandlers := s.registry.plugins.GetEventSubscribers(common.OnServiceUpdated)
		if len(updateHandlers) > 0 {
			uEvent := &common.PluginEvent{EventType: common.OnServiceUpdated, EventObject: eventObject}
			for _, handler := range updateHandlers {
				_ = handler.Callback(uEvent)
			}
		}
	case CacheDeleted:
		deleteHandlers := s.registry.plugins.GetEventSubscribers(common.OnServiceDeleted)
		if len(deleteHandlers) > 0 {
			uEvent := &common.PluginEvent{EventType: common.OnServiceDeleted, EventObject: eventObject}
			for _, handler := range deleteHandlers {
				_ = handler.Callback(uEvent)
			}
		}
	}
}

// OnServiceUpdate 服务远程实例更新事件到来后的回调操作
func (s *CacheObject) OnServiceUpdate(event *serverconnector.ServiceEvent) {
	err, svcEventKey := event.Error, &event.ServiceEventKey
	// 更新标记为，表示该对象已经经过远程更新
	atomic.StoreUint32(&s.hasRemoteUpdated, 1)
	atomic.StoreUint32(&s.hasRemoteError, 0)
	if err != nil {
		log.GetBaseLogger().Errorf("OnServiceUpdate: fail to update %s for err %v", *svcEventKey, err)
		if err.ErrorCode() == model.ErrCodeInvalidServerResponse {
			// 网络错误问题，这里塞入一个空的 value, 避免每次获取都需要等待
			atomic.StoreUint32(&s.hasRemoteError, 1)
		}
	} else {
		message := event.Value
		cachedValue := s.LoadValue(false)
		cachedStatus := s.Handler.CompareMessage(cachedValue, message)
		if reflect2.IsNil(cachedValue) || cachedStatus == CacheChanged || cachedStatus == CacheAdded ||
			cachedStatus == CacheDeleted {
			log.GetBaseLogger().Infof(
				"OnServiceUpdate: cache %s is pending to update, status %s", *svcEventKey, cachedStatus)
			svcCacheFile := lrplug.ServiceEventKeyToFileName(*svcEventKey)
			_ = s.registry.PersistMessage(svcCacheFile, message)
			cacheValue := s.Handler.MessageToCacheValue(cachedValue, message, s.svcLocalValue, false)
			s.SetValue(cacheValue)
			eventObject := &common.ServiceEventObject{SvcEventKey: *svcEventKey,
				OldValue: cachedValue, NewValue: cacheValue}
			s.notifyEventHandlers(eventObject, cachedStatus)
		} else {
			switch event.Type {
			case model.EventInstances:
				atomic.StoreInt32(&cachedValue.(*pb.ServiceInstancesInProto).CacheLoaded, 0)
			case model.EventRouting:
				atomic.StoreInt32(&cachedValue.(*pb.ServiceRuleInProto).CacheLoaded, 0)
			case model.EventNearbyRouteRule:
				atomic.StoreInt32(&cachedValue.(*pb.ServiceRuleInProto).CacheLoaded, 0)
			}
		}
	}
	s.notifier.Notify(err)
}

// GetRevision 获取服务对象的版本号
func (s *CacheObject) GetRevision() string {
	value := s.LoadValue(false)
	if nil == value {
		return ""
	}
	svcValue := value.(model.RegistryValue)
	return svcValue.GetRevision()
}

// SetValue 设置缓存对象
func (s *CacheObject) SetValue(cacheValue model.RegistryValue) {
	s.value.Store(cacheValue)
	log.GetBaseLogger().Infof(
		"CacheObject: value for %s is updated, revision %s", *s.serviceValueKey, cacheValue.GetRevision())
}

// GetBusiness 获取业务类型
func (s *CacheObject) GetBusiness() string {
	if s.serviceValueKey.Type == model.EventServices {
		return s.serviceValueKey.Service
	}
	return ""
}
