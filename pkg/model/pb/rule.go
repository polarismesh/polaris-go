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
	"sync/atomic"

	"github.com/golang/protobuf/proto"
	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
)

// ServiceRuleAssistant 助手接口.
type ServiceRuleAssistant interface {
	// ParseRuleValue 解析出具体的规则值
	ParseRuleValue(resp *apiservice.DiscoverResponse) (proto.Message, string)
	// SetDefault 设置默认值
	SetDefault(message proto.Message)
	// Validate 规则校验
	Validate(message proto.Message, cache model.RuleCache) error
}

var eventTypeToAssistant = map[model.EventType]ServiceRuleAssistant{
	model.EventRouting:         &RoutingAssistant{},
	model.EventRateLimiting:    &RateLimitingAssistant{},
	model.EventCircuitBreaker:  &CircuitBreakAssistant{},
	model.EventFaultDetect:     &FaultDetectAssistant{},
	model.EventNearbyRouteRule: &NearbyRoutingAssistant{},
}

// ServiceRuleInProto 路由规则配置对象.
type ServiceRuleInProto struct {
	*model.ServiceKey
	initialized bool
	notExists   bool
	revision    string
	hashValue   uint64
	ruleValue   proto.Message
	ruleCache   model.RuleCache
	eventType   model.EventType
	assistant   ServiceRuleAssistant
	CacheLoaded int32
	// 规则的校验错误缓存
	validateError error
}

// NewServiceRuleInProto 创建路由规则配置对象.
func NewServiceRuleInProto(resp *apiservice.DiscoverResponse) *ServiceRuleInProto {
	value := NewServiceRuleInProtoWithInitializeStatus(resp, true)
	if nil == resp {
		value.initialized = false
	}
	return value
}

// NewServiceRuleInProtoWithInitializeStatus 创建路由规则配置对象.
func NewServiceRuleInProtoWithInitializeStatus(resp *apiservice.DiscoverResponse, initialized bool) *ServiceRuleInProto {
	value := &ServiceRuleInProto{}
	if nil == resp {
		value.initialized = initialized
		return value
	}
	value.ServiceKey = &model.ServiceKey{
		Namespace: resp.Service.Namespace.GetValue(),
		Service:   resp.Service.Name.GetValue(),
	}
	value.initialized = initialized
	if resp.GetCode().GetValue() == uint32(apimodel.Code_NotFoundResource) {
		value.notExists = true
	}
	value.eventType = GetEventType(resp.GetType())
	value.assistant = eventTypeToAssistant[value.eventType]
	value.ruleValue, value.revision = value.assistant.ParseRuleValue(resp)
	if len(value.revision) > 0 {
		var err error
		value.hashValue, err = model.GetCrc64Hash(value.revision)
		if err != nil {
			log.GetBaseLogger().Errorf("fail to calc crc64 hash for rule %s, type %v: %v",
				value.ServiceKey, value.eventType, err)
		}
	}
	value.ruleCache = model.NewRuleCache()
	return value
}

// IsCacheLoaded pb的值是否从缓存文件中加载.
func (s *ServiceRuleInProto) IsCacheLoaded() bool {
	return atomic.LoadInt32(&s.CacheLoaded) > 0
}

// ValidateAndBuildCache 校验路由规则，以及构建正则表达式缓存.
func (s *ServiceRuleInProto) ValidateAndBuildCache() error {
	s.assistant.SetDefault(s.ruleValue)
	if err := s.assistant.Validate(s.ruleValue, s.ruleCache); err != nil {
		// 缓存规则解释失败异常
		s.validateError = err
		return err
	}
	return nil
}

// GetNamespace 获取命名空间.
func (s *ServiceRuleInProto) GetNamespace() string {
	if s.initialized {
		return s.Namespace
	}
	return ""
}

// GetService 获取服务名.
func (s *ServiceRuleInProto) GetService() string {
	if s.initialized {
		return s.Service
	}
	return ""
}

// GetValue 获取通用规则值.
func (s *ServiceRuleInProto) GetValue() interface{} {
	return s.ruleValue
}

// GetType 获取规则类型.
func (s *ServiceRuleInProto) GetType() model.EventType {
	return s.eventType
}

// IsInitialized 缓存是否已经初始化.
func (s *ServiceRuleInProto) IsInitialized() bool {
	return s.initialized
}

// IsNotExists 规则是否存在
func (s *ServiceRuleInProto) IsNotExists() bool {
	return s.notExists
}

// GetRevision 缓存版本号，标识缓存是否更新.
func (s *ServiceRuleInProto) GetRevision() string {
	return s.revision
}

// GetHashValue 获取数据的hash值
func (s *ServiceRuleInProto) GetHashValue() uint64 {
	return s.hashValue
}

// GetRuleCache 获取规则缓存信息.
func (s *ServiceRuleInProto) GetRuleCache() model.RuleCache {
	return s.ruleCache
}

// GetValidateError 获取规则校验错误.
func (s *ServiceRuleInProto) GetValidateError() error {
	return s.validateError
}
