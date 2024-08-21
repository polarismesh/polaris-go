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
	"fmt"
)

// EventType 事件类型，用于标识各种不同的事件
type EventType uint32

const (
	// EventUnknown 未知事件
	EventUnknown EventType = 0
	// EventInstances 实例事件
	EventInstances EventType = 0x2001
	// EventRouting 路由配置事件
	EventRouting EventType = 0x2002
	// EventRateLimiting 限流配置事件
	EventRateLimiting EventType = 0x2003
	// EventServices 批量服务
	EventServices EventType = 0x2005
	// EventCircuitBreaker 熔断规则
	EventCircuitBreaker EventType = 0x2006
	// EventFaultDetect 探测规则
	EventFaultDetect EventType = 0x2007
)

// RegistryValue 存储于sdk缓存中的对象，包括服务实例和服务路由
type RegistryValue interface {
	// GetType 获取配置类型
	GetType() EventType
	// IsInitialized 是否初始化，实例列表或路由值是否加载
	IsInitialized() bool
	// GetRevision 获取服务实例或规则的版本号
	GetRevision() string
	// GetHashValue 获取资源的hash值
	GetHashValue() uint64
	// IsNotExists 资源是否存在
	IsNotExists() bool
}

// String ToString方法
func (e EventType) String() string {
	if value, ok := eventTypeToPresent[e]; ok {
		return value
	}
	return "unknown"
}

var (
	// 路由规则到日志回显
	eventTypeToPresent = map[EventType]string{
		EventInstances:      "instance",
		EventRouting:        "routing",
		EventRateLimiting:   "rate_limiting",
		EventServices:       "services",
		EventCircuitBreaker: "circuit_breaker",
		EventFaultDetect:    "fault_detect",
	}

	presentToEventType = map[string]EventType{
		"instance":        EventInstances,
		"routing":         EventRouting,
		"rate_limiting":   EventRateLimiting,
		"services":        EventServices,
		"circuit_breaker": EventCircuitBreaker,
		"fault_detect":    EventFaultDetect,
	}
)

// ToEventType 通过字符串构造事件类型
func ToEventType(value string) EventType {
	if eType, ok := presentToEventType[value]; ok {
		return eType
	}
	return EventUnknown
}

var EmptyServiceKey = &ServiceKey{}

// ServiceKey 服务的唯一标识KEY
type ServiceKey struct {
	// 命名空间
	Namespace string
	// 服务名
	Service string
}

// String ToString方法
func (s ServiceKey) String() string {
	return fmt.Sprintf("{namespace: \"%s\", service: \"%s\"}", s.Namespace, s.Service)
}

type MeshKey struct {
	// 命名空间
	// Namespace string
	Business string
	TypeUrl  string
}

// String ToString方法
func (s MeshKey) String() string {
	return fmt.Sprintf("{business: \"%s\", typeurl: \"%s\"}", s.Business, s.TypeUrl)
}

// ServiceEventKey 服务加规则的唯一标识KEY
type ServiceEventKey struct {
	// 服务标识
	ServiceKey
	// 网格标识
	// MeshKey
	// 值类型
	Type EventType
}

// String ToString方法
func (s ServiceEventKey) String() string {
	return fmt.Sprintf("{namespace: \"%s\", service: \"%s\", event: %v}", s.Namespace, s.Service, s.Type)
}

// InstanceKey 服务实例的唯一标识
type InstanceKey struct {
	ServiceKey
	Host string
	Port int
}

// String ToString方法
func (i InstanceKey) String() string {
	return fmt.Sprintf("{service: %s, host: %s, port: %v}", i.ServiceKey, i.Host, i.Port)
}
