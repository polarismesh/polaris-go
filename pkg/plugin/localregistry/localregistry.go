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

package localregistry

import (
	"fmt"
	"strings"

	"github.com/golang/protobuf/proto"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

const (
	// PropertyCircuitBreakerStatus InstanceProperties中Properties的key,熔断结果状态
	PropertyCircuitBreakerStatus = "CircuitBreakerStatus"
	// PropertyHealthCheckStatus InstanceProperties中Properties的key,健康探测结果状态
	PropertyHealthCheckStatus = "HealthCheckStatus"
)

// InstanceProperties 待更新的实例属性
type InstanceProperties struct {
	Service    *model.ServiceKey
	ID         string
	Host       string
	Port       uint32
	Properties map[string]interface{}
}

// String ToString方法
func (i InstanceProperties) String() string {
	buffer := strings.Builder{}
	if len(i.Properties) == 0 {
		buffer.WriteString("<nil>")
	} else {
		for key, value := range i.Properties {
			buffer.WriteString(fmt.Sprintf("%s:%s", key, value))
		}
	}
	return fmt.Sprintf("{ID: %s, Properties: %s}", i.ID, buffer.String())
}

// ServiceUpdateRequest 服务更新请求体
type ServiceUpdateRequest struct {
	model.ServiceKey
	Properties []InstanceProperties
}

// String ToString方法
func (s ServiceUpdateRequest) String() string {
	builder := strings.Builder{}
	if len(s.Properties) == 0 {
		builder.WriteString("<nil>")
	} else {
		for i, value := range s.Properties {
			if i > 0 {
				builder.WriteString(", ")
			}
			builder.WriteString(fmt.Sprintf("%s", value))
		}
	}
	return fmt.Sprintf("{Service: %s, Namespace: %s, Properties: %s}", s.Service, s.Namespace, builder.String())
}

// InstancesRegistry 实例缓存
type InstancesRegistry interface {
	// GetInstances 非阻塞获取服务实例列表，只读取缓存
	GetInstances(svcKey *model.ServiceKey, includeCache bool, isInternalRequest bool) model.ServiceInstances
	// LoadInstances 非阻塞发起一次缓存远程加载操作
	// 如果已经加载过了，那就直接进行notify
	// 否则，加载完毕后调用notify函数
	LoadInstances(svcKey *model.ServiceKey) (*common.Notifier, error)
	// UpdateInstances 批量更新服务实例状态，properties存放的是状态值，当前支持2个key
	// 1. ReadyToServe: 故障熔断标识，true or false
	// 2. DynamicWeight：动态权重值
	UpdateInstances(*ServiceUpdateRequest) error
	// PersistMessage 对PB缓存进行持久化
	PersistMessage(file string, msg proto.Message) error
	// LoadPersistedMessage 从文件中加载PB缓存
	LoadPersistedMessage(file string, msg proto.Message) error
}

// InstancesFilter 用于在向缓存获取实例时进行过滤
type InstancesFilter struct {
	Service           string
	Namespace         string
	IsInternalRequest bool
}

// LocalRegistry 【扩展点接口】本地缓存扩展点
type LocalRegistry interface {
	plugin.Plugin
	InstancesRegistry
	RuleRegistry
}

// RuleFilter 配置获取的过滤器
type RuleFilter struct {
	model.ServiceEventKey
}

// RuleRegistry ConfigRegistry 配置缓存
type RuleRegistry interface {
	// GetServiceRule 非阻塞获取配置信息
	GetServiceRule(key *model.ServiceEventKey, includeCache bool) model.ServiceRule
	// LoadServiceRule 非阻塞发起配置加载
	LoadServiceRule(key *model.ServiceEventKey) (*common.Notifier, error)
	// GetServiceRouteRule 非阻塞获取配置信息
	GetServiceRouteRule(key *model.ServiceKey, includeCache bool) model.ServiceRule
	// LoadServiceRouteRule 非阻塞发起配置加载
	LoadServiceRouteRule(key *model.ServiceKey) (*common.Notifier, error)
	// GetServiceNearByRouteRule 非阻塞获取就近路由信息
	GetServiceNearByRouteRule(key *model.ServiceKey, includeCache bool) model.ServiceRule
	// LoadServiceNearByRouteRule 非阻塞发起就近路由加载
	LoadServiceNearByRouteRule(key *model.ServiceKey) (*common.Notifier, error)
	// GetServiceRateLimitRule 非阻塞获取限流规则
	GetServiceRateLimitRule(key *model.ServiceKey, includeCache bool) model.ServiceRule
	// LoadServiceRateLimitRule 非阻塞发起限流规则加载
	LoadServiceRateLimitRule(key *model.ServiceKey) (*common.Notifier, error)
	// GetServicesByMeta 非阻塞获取批量服务
	GetServicesByMeta(key *model.ServiceKey, includeCache bool) model.Services
	// LoadServices 非阻塞加载批量服务
	LoadServices(key *model.ServiceKey) (*common.Notifier, error)
	// WatchService 添加服务监听标识
	WatchService(svcEventKey model.ServiceEventKey)
	// UnwatchService 取消服务监听标识
	UnwatchService(svcEventKey model.ServiceEventKey)
}

// init 初始化
func init() {
	plugin.RegisterPluginInterface(common.TypeLocalRegistry, new(LocalRegistry))
}
