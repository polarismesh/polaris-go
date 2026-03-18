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
	"time"
)

const (
	// LosslessDelayRegisterStrategyDelayByTime 时长延迟注册策略
	LosslessDelayRegisterStrategyDelayByTime = "DELAY_BY_TIME"
	// LosslessDelayRegisterStrategyDelayByHealthCheck 探测延迟注册策略
	LosslessDelayRegisterStrategyDelayByHealthCheck = "DELAY_BY_HEALTH_CHECK"
)

var SupportedDelayRegisterStrategies = map[string]struct{}{
	LosslessDelayRegisterStrategyDelayByTime:        {},
	LosslessDelayRegisterStrategyDelayByHealthCheck: {},
}

type LosslessInfo struct {
	Instance            *InstanceRegisterRequest `json:"instance,omitempty"`
	DelayRegisterConfig *DelayRegisterConfig     `json:"delayRegisterConfig,omitempty"`
	ReadinessProbe      string                   `json:"readinessProbe,omitempty"`
	OfflineProbe        string                   `json:"offlineProbe,omitempty"`
	WarmUpConfig        *WarmUpConfig            `json:"warmUpConfig,omitempty"`
}

func (l *LosslessInfo) IsDelayRegisterEnabled() bool {
	return l != nil && l.DelayRegisterConfig != nil && l.DelayRegisterConfig.Strategy != ""
}

func (l *LosslessInfo) IsReadinessProbeEnabled() bool {
	return l != nil && l.ReadinessProbe != ""
}

func (l *LosslessInfo) IsOfflineProbeEnabled() bool {
	return l != nil && l.OfflineProbe != ""
}

func (l *LosslessInfo) IsWarmUpEnabled() bool {
	return l != nil && l.WarmUpConfig != nil && l.WarmUpConfig.Interval > 0
}

func (l *LosslessInfo) GetJsonString() string {
	if l == nil {
		return ""
	}
	return JsonString(l)
}

type DelayRegisterConfig struct {
	Strategy              string                     `json:"strategy"`
	DelayRegisterInterval time.Duration              `json:"delayRegisterInterval"`
	HealthCheckConfig     *LosslessHealthCheckConfig `json:"healthCheckConfig,omitempty"`
}

type LosslessHealthCheckConfig struct {
	HealthCheckInterval time.Duration `json:"healthCheckInterval"`
	HealthCheckPath     string        `json:"healthCheckPath"`
	HealthCheckProtocol string        `json:"healthCheckProtocol"`
	HealthCheckMethod   string        `json:"healthCheckMethod"`
}

type WarmUpConfig struct {
	Interval time.Duration `json:"interval"`
}
