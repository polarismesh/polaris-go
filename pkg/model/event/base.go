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

package event

type BaseEvent interface {
	SetClientIp(ip string)
	SetClientId(id string)
	GetEventType() string
	GetEventName() EventName
	GetInstanceEvent() InstanceEvent
	GetConfigEvent() ConfigEvent
	GetLosslessEvent() LosslessEvent
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

type BaseEventImpl struct {
	BaseType      BaseEventType `json:"base_type"`
	EventType     string        `json:"event_type"`
	ClientId      string        `json:"client_id"`
	ClientIp      string        `json:"client_ip"`
	EventTime     string        `json:"event_time"`
	EventName     EventName     `json:"event_name"`
	InstanceEvent InstanceEvent `json:"instance_event,omitempty"`
	ConfigEvent   ConfigEvent   `json:"config_event,omitempty"`
	LosslessEvent LosslessEvent `json:"lossless_event,omitempty"`
}

func (c *BaseEventImpl) SetClientIp(ip string) {
	c.ClientIp = ip
}

func (c *BaseEventImpl) SetClientId(id string) {
	c.ClientId = id
}

func (c *BaseEventImpl) GetEventType() string {
	return c.EventType
}

func (c *BaseEventImpl) GetEventName() EventName {
	return c.EventName
}

func (c *BaseEventImpl) GetInstanceEvent() InstanceEvent {
	return c.InstanceEvent
}

func (c *BaseEventImpl) GetConfigEvent() ConfigEvent {
	return c.ConfigEvent
}

func (c *BaseEventImpl) GetLosslessEvent() LosslessEvent {
	return c.LosslessEvent
}
