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

//事件类型，用于标识各种不同的事件
type EventType uint32

const (
	//未知事件
	EventUnknown EventType = 0
	//EventInstances 实例事件
	EventInstances EventType = 0x2001
	//EventTypeConfig 路由配置事件
	EventRouting EventType = 0x2002
	//EventRateLimiting 限流配置事件
	EventRateLimiting EventType = 0x2003
	//mesh config
	EventMeshConfig EventType = 0x2004
	//EventRateLimiting 批量服务
	EventServices EventType = 0x2005
	//mesh
	EventMesh EventType = 0x2006
)

//存储于sdk缓存中的对象，包括服务实例和服务路由
type RegistryValue interface {
	//获取配置类型
	GetType() EventType
	//是否初始化，实例列表或路由值是否加载
	IsInitialized() bool
	//获取服务实例或规则的版本号
	GetRevision() string
}

//ToString方法
func (e EventType) String() string {
	if value, ok := eventTypeToPresent[e]; ok {
		return value
	}
	return "unknown"
}

var (
	//路由规则到日志回显
	eventTypeToPresent = map[EventType]string{
		EventInstances:    "instance",
		EventRouting:      "routing",
		EventRateLimiting: "rate_limiting",
		EventMeshConfig:   "mesh_config",
		EventMesh:         "mesh",
		EventServices:     "services",
	}

	presentToEventType = map[string]EventType{
		"instance":      EventInstances,
		"routing":       EventRouting,
		"rate_limiting": EventRateLimiting,
		"mesh_config":   EventMeshConfig,
		"mesh":          EventMesh,
		"services":      EventServices,
	}
)

//通过字符串构造事件类型
func ToEventType(value string) EventType {
	if eType, ok := presentToEventType[value]; ok {
		return eType
	}
	return EventUnknown
}

//服务的唯一标识KEY
type ServiceKey struct {
	//命名空间
	Namespace string
	//服务名
	Service string
}

//ToString方法
func (s ServiceKey) String() string {
	return fmt.Sprintf("{namespace: \"%s\", service: \"%s\"}", s.Namespace, s.Service)
}

type MeshKey struct {
	//命名空间
	//Namespace string
	Business string
	TypeUrl  string
}

//ToString方法
func (s MeshKey) String() string {
	return fmt.Sprintf("{business: \"%s\", typeurl: \"%s\"}", s.Business, s.TypeUrl)
}

//服务加规则的唯一标识KEY
type ServiceEventKey struct {
	//服务标识
	ServiceKey
	//网格标识
	//MeshKey
	//值类型
	Type EventType
}

//ToString方法
func (s ServiceEventKey) String() string {
	return fmt.Sprintf("{namespace: \"%s\", service: \"%s\", event: %v}", s.Namespace, s.Service, s.Type)
}

//服务实例的唯一标识
type InstanceKey struct {
	ServiceKey
	Host string
	Port int
}

//ToString方法
func (i InstanceKey) String() string {
	return fmt.Sprintf("{service: %s, host: %s, port: %v}", i.ServiceKey, i.Host, i.Port)
}
