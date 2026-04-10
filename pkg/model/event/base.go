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

// Package event provides event model definitions for polaris-go.
package event

type BaseEvent interface {
	SetClientIP(ip string)
	SetClientID(id string)
	GetEventType() string
	GetEventName() EventName
}

type BaseEventType int

const (
	InstanceEventType BaseEventType = iota
	ConfigEventType   BaseEventType = iota
	LosslessEventType BaseEventType = iota
)

func (b BaseEventType) EventTypeString() string {
	switch b {
	case InstanceEventType:
		return "Instance"
	case ConfigEventType:
		return "Config"
	case LosslessEventType:
		return "Lossless"
	default:
		return "Unknown"
	}
}

// EventName 事件名称
type EventName string

const (
	// ConfigUpdated 配置事件-更新
	ConfigUpdated EventName = "ConfigUpdated"
	// LosslessOnlineStart 无损上线事件-开始
	LosslessOnlineStart EventName = "LosslessOnlineStart"
	// LosslessOnlineEnd 无损上线事件-结束
	LosslessOnlineEnd EventName = "LosslessOnlineEnd"
	// LosslessWarmupStart 服务预热-开始
	LosslessWarmupStart EventName = "LosslessWarmupStart"
	// LosslessWarmupEnd 服务预热-结束
	LosslessWarmupEnd EventName = "LosslessWarmupEnd"
	// LosslessOfflineStart 无损下线事件-开始
	LosslessOfflineStart EventName = "LosslessOfflineStart"
	// InstanceThreadEnd 实例线程结束
	InstanceThreadEnd EventName = "InstanceThreadEnd"
)

// BaseEventImpl 扁平化事件结构，与服务端 ClientEventRequest 格式对齐
type BaseEventImpl struct {
	// 公共字段
	EventType string    `json:"event_type"`
	EventName EventName `json:"event_name"`
	EventTime string    `json:"event_time"`
	ClientID  string    `json:"client_id"`
	ClientIP  string    `json:"client_ip"`

	// 实例/无损上下线相关字段
	Namespace  string `json:"namespace,omitempty"`
	Service    string `json:"service,omitempty"`
	Host       string `json:"host,omitempty"`
	Port       string `json:"port,omitempty"`
	InstanceID string `json:"instance_id,omitempty"`

	// API 相关字段
	APIProtocol string `json:"api_protocol,omitempty"`
	APIPath     string `json:"api_path,omitempty"`
	APIMethod   string `json:"api_method,omitempty"`

	// 来源服务相关字段
	SourceNamespace string `json:"source_namespace,omitempty"`
	SourceService   string `json:"source_service,omitempty"`

	// 配置相关字段
	ClientType        string `json:"client_type,omitempty"`
	ConfigGroup       string `json:"config_group,omitempty"`
	ConfigFileName    string `json:"config_file_name,omitempty"`
	ConfigFileVersion string `json:"config_file_version,omitempty"`

	// 其他字段
	Labels string `json:"labels,omitempty"`
	Reason string `json:"reason,omitempty"`
}

func (c *BaseEventImpl) SetClientIP(ip string) {
	c.ClientIP = ip
}

func (c *BaseEventImpl) SetClientID(id string) {
	c.ClientID = id
}

func (c *BaseEventImpl) GetEventType() string {
	return c.EventType
}

func (c *BaseEventImpl) GetEventName() EventName {
	return c.EventName
}
