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
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/golang/protobuf/proto"
	"regexp"
	"sync/atomic"
)

//助手接口
type ServiceRuleAssistant interface {
	//解析出具体的规则值
	ParseRuleValue(resp *namingpb.DiscoverResponse) (proto.Message, string)
	//设置默认值
	SetDefault(message proto.Message)
	//规则校验
	Validate(message proto.Message, cache model.RuleCache) error
}

var (
	eventTypeToAssistant = map[model.EventType]ServiceRuleAssistant{
		model.EventRouting:      &RoutingAssistant{},
		model.EventRateLimiting: &RateLimitingAssistant{},
	}
)

//网格规则
type MeshConfigProto struct {
	*model.ServiceKey
	initialized bool
	revision    string
	ruleValue   *namingpb.MeshConfig
	ruleCache   model.RuleCache
	eventType   model.EventType
	CacheLoaded int32
}

//新建网格规则
func NewMeshConfigInProto(resp *namingpb.DiscoverResponse) *MeshConfigProto {
	value := &MeshConfigProto{}
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
	value.ruleValue = resp.Meshconfig
	value.revision = resp.GetMeshconfig().GetRevision().GetValue()
	value.ruleCache = model.NewRuleCache()
	return value

}

//获取规则类型
func (s *MeshConfigProto) GetType() model.EventType {
	return s.eventType
}

//缓存是否已经初始化
func (s *MeshConfigProto) IsInitialized() bool {
	return s.initialized
}

//缓存版本号，标识缓存是否更新
func (s *MeshConfigProto) GetRevision() string {
	return s.revision
}

//获取值
func (s *MeshConfigProto) GetValue() interface{} {
	return s.ruleValue
}

//获取Namespace
func (s *MeshConfigProto) GetNamespace() string {
	return s.Namespace
}

//获取Service
func (s *MeshConfigProto) GetService() string {
	return s.Service
}

//网格
type MeshProto struct {
	*model.ServiceKey
	initialized bool
	revision    string
	ruleValue   *namingpb.Mesh
	ruleCache   model.RuleCache
	eventType   model.EventType
	CacheLoaded int32
}

//新建网格
func NewMeshInProto(resp *namingpb.DiscoverResponse) *MeshProto {
	value := &MeshProto{}
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
	value.ruleValue = resp.Mesh
	value.revision = resp.GetMesh().GetRevision().GetValue()
	value.ruleCache = model.NewRuleCache()
	return value
}

//获取类型
func (s *MeshProto) GetType() model.EventType {
	return s.eventType
}

//缓存是否已经初始化
func (s *MeshProto) IsInitialized() bool {
	return s.initialized
}

//缓存版本号，标识缓存是否更新
func (s *MeshProto) GetRevision() string {
	return s.revision
}

//获取值
func (s *MeshProto) GetValue() interface{} {
	return s.ruleValue
}

//获取Namespace
func (s *MeshProto) GetNamespace() string {
	return s.Namespace
}

//获取Service
func (s *MeshProto) GetService() string {
	return s.Service
}

//路由规则配置对象
type ServiceRuleInProto struct {
	*model.ServiceKey
	initialized bool
	revision    string
	ruleValue   proto.Message
	ruleCache   model.RuleCache
	eventType   model.EventType
	assistant   ServiceRuleAssistant
	CacheLoaded int32
	//规则的校验错误缓存
	validateError error
}

//创建路由规则配置对象
func NewServiceRuleInProto(resp *namingpb.DiscoverResponse) *ServiceRuleInProto {
	value := &ServiceRuleInProto{}
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
	value.assistant = eventTypeToAssistant[value.eventType]
	value.ruleValue, value.revision = value.assistant.ParseRuleValue(resp)
	value.ruleCache = model.NewRuleCache()
	return value
}

//pb的值是否从缓存文件中加载
func (s *ServiceRuleInProto) IsCacheLoaded() bool {
	return atomic.LoadInt32(&s.CacheLoaded) > 0
}

//校验路由规则，以及构建正则表达式缓存
func (s *ServiceRuleInProto) ValidateAndBuildCache() error {
	s.assistant.SetDefault(s.ruleValue)
	if err := s.assistant.Validate(s.ruleValue, s.ruleCache); nil != err {
		//缓存规则解释失败异常
		s.validateError = err
		return err
	}
	return nil
}

//通过metadata来构建缓存
func buildCacheFromMatcher(metadata map[string]*namingpb.MatchString, ruleCache model.RuleCache) error {
	if len(metadata) == 0 {
		return nil
	}
	for _, metaValue := range metadata {
		valueRawStr := metaValue.GetValue().GetValue()
		//如果是 variable 类型，但是value 是空的，此时无法通过 value 获取环境变量，报错
		if metaValue.ValueType == namingpb.MatchString_VARIABLE && valueRawStr == ""{
			return fmt.Errorf("value of variable type can not be empty")
		}
		if metaValue.Type != namingpb.MatchString_REGEX || metaValue.ValueType != namingpb.MatchString_TEXT {
			continue
		}
		//如果是正则匹配类型，并且 value 是 text 模式，事先校验正则表达式是否合法
		if pattern := ruleCache.GetRegexMatcher(valueRawStr); nil != pattern {
			continue
		}
		regexValue, err := regexp.Compile(valueRawStr)
		if nil != err {
			return fmt.Errorf("invalid regex expression %s, error is %v", valueRawStr, err)
		}
		ruleCache.PutRegexMatcher(valueRawStr, regexValue)
	}
	return nil
}

//获取命名空间
func (s *ServiceRuleInProto) GetNamespace() string {
	if s.initialized {
		return s.Namespace
	}
	return ""
}

//获取服务名
func (s *ServiceRuleInProto) GetService() string {
	if s.initialized {
		return s.Service
	}
	return ""
}

//获取通用规则值
func (s *ServiceRuleInProto) GetValue() interface{} {
	return s.ruleValue
}

//获取规则类型
func (s *ServiceRuleInProto) GetType() model.EventType {
	return s.eventType
}

//缓存是否已经初始化
func (s *ServiceRuleInProto) IsInitialized() bool {
	return s.initialized
}

//缓存版本号，标识缓存是否更新
func (s *ServiceRuleInProto) GetRevision() string {
	return s.revision
}

//获取规则缓存信息
func (s *ServiceRuleInProto) GetRuleCache() model.RuleCache {
	return s.ruleCache
}

//获取规则校验错误
func (s *ServiceRuleInProto) GetValidateError() error {
	return s.validateError
}
