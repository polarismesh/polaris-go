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

type BaseEvent interface {
	GetBaseType() BaseEventType
	GetConfigEvent() ConfigEvent
}

type ConfigEvent interface {
	GetEventName() EventName
	GetEventTime() string
	GetClientId() string
	SetClientId(string)
	GetClientIp() string
	SetClientIp(string)
	GetClientType() string
	GetNamespace() string
	GetConfigGroup() string
	GetConfigFileName() string
	GetConfigFileVersion() string
}

type BaseEventType int

const (
	ConfigBaseEvent BaseEventType = iota
)

type EventName string

const (
	ConfigUpdated EventName = "ConfigUpdated"
)

type BaseEventImpl struct {
	BaseType    BaseEventType
	ConfigEvent ConfigEvent
}

func (b *BaseEventImpl) GetBaseType() BaseEventType {
	return b.BaseType
}

func (b *BaseEventImpl) GetConfigEvent() ConfigEvent {
	return b.ConfigEvent
}

// ConfigEvent config Event
type ConfigEventImpl struct {
	EventName         EventName `json:"event_name"`
	EventTime         string    `json:"event_time"`
	ClientId          string    `json:"client_id"`
	ClientIp          string    `json:"client_ip"`
	ClientType        string    `json:"client_type"`
	Namespace         string    `json:"namespace"`
	ConfigGroup       string    `json:"config_group"`
	ConfigFileName    string    `json:"config_file_name"`
	ConfigFileVersion string    `json:"config_file_version"`
}

func (c *ConfigEventImpl) GetEventName() EventName {
	return c.EventName
}

func (c *ConfigEventImpl) GetEventTime() string {
	return c.EventTime
}

func (c *ConfigEventImpl) GetClientId() string {
	return c.ClientId
}

func (c *ConfigEventImpl) SetClientId(id string) {
	c.ClientId = id
}

func (c *ConfigEventImpl) GetClientIp() string {
	return c.ClientIp
}

func (c *ConfigEventImpl) SetClientIp(ip string) {
	c.ClientIp = ip
}

func (c *ConfigEventImpl) GetClientType() string {
	return c.ClientType
}

func (c *ConfigEventImpl) GetNamespace() string {
	return c.Namespace
}

func (c *ConfigEventImpl) GetConfigGroup() string {
	return c.ConfigGroup
}

func (c *ConfigEventImpl) GetConfigFileName() string {
	return c.ConfigFileName
}

func (c *ConfigEventImpl) GetConfigFileVersion() string {
	return c.ConfigFileVersion
}
