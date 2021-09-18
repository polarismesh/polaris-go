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

package config

import (
	"errors"
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"time"
)

// HealthCheckConfigImpl health check config implementation
type HealthCheckConfigImpl struct {
	When        When          `yaml:"when" json:"when"`
	Interval    time.Duration `yaml:"interval" json:"interval"`
	Timeout     time.Duration `yaml:"timeout" json:"timeout"`
	Chain       []string      `yaml:"chain" json:"chain"`
	Concurrency int           `yaml:"concurrency" json:"concurrency"`
	Plugin      PluginConfigs `yaml:"plugin" json:"plugin"`
}

// GetWhen get when to active health check
func (h *HealthCheckConfigImpl) GetWhen() When {
	return h.When
}

// SetWhen set when to active health check
func (h *HealthCheckConfigImpl) SetWhen(when When) {
	h.When = when
}

// GetInterval get health check interval
func (h *HealthCheckConfigImpl) GetInterval() time.Duration {
	return h.Interval
}

// SetInterval set health check interval
func (h *HealthCheckConfigImpl) SetInterval(duration time.Duration) {
	h.Interval = duration
}

// GetChain get health checking chain
func (h *HealthCheckConfigImpl) GetChain() []string {
	return h.Chain
}

// SetChain set health checking chain
func (h *HealthCheckConfigImpl) SetChain(chain []string) {
	h.Chain = chain
}

// GetTimeout get health check max timeout
func (h *HealthCheckConfigImpl) GetTimeout() time.Duration {
	return h.Timeout
}

// SetTimeout set health check max timeout
func (h *HealthCheckConfigImpl) SetTimeout(duration time.Duration) {
	h.Timeout = duration
}

// GetConcurrency get concurrency to execute the health check jobs
func (h *HealthCheckConfigImpl) GetConcurrency() int {
	return h.Concurrency
}

// SetConcurrency set concurrency to execute the health check jobs
func (h *HealthCheckConfigImpl) SetConcurrency(value int) {
	h.Concurrency = value
}

// GetPluginConfig get plugin config by name
func (h *HealthCheckConfigImpl) GetPluginConfig(pluginName string) BaseConfig {
	return h.Plugin.GetPluginConfig(pluginName)
}

// SetPluginConfig set plugin config by name
func (h *HealthCheckConfigImpl) SetPluginConfig(pluginName string, value BaseConfig) error {
	return h.Plugin.SetPluginConfig(common.TypeHealthCheck, pluginName, value)
}

// Verify verify the healthCheckConfig
func (h *HealthCheckConfigImpl) Verify() error {
	if nil == h {
		return errors.New("HealthCheckConfig is nil")
	}
	if h.When != HealthCheckNever && h.When != HealthCheckAlways && h.When != HealthCheckOnRecover {
		return errors.New(fmt.Sprintf("healthcheck.when %v is invalid", h.When))
	}
	if h.When == HealthCheckNever {
		return nil
	}
	if len(h.Chain) == 0 {
		return errors.New("at least one health check chain must config")
	}
	if h.Interval < MinHealthCheckInterval {
		return errors.New(fmt.Sprintf("consumer.healthCheck.checkPeriod should greater than %v",
			MinHealthCheckInterval))
	}
	if len(h.Plugin) == 0 {
		return errors.New("at least one health check plugin must config")
	}
	return h.Plugin.Verify()
}

// SetDefault set default values to healthCheckConfig
func (h *HealthCheckConfigImpl) SetDefault() {
	if len(h.When) == 0 {
		h.When = HealthCheckNever
	}
	if h.Interval == 0 {
		h.Interval = DefaultHealthCheckInterval
	}
	if h.Timeout == 0 {
		h.Timeout = DefaultHealthCheckTimeout
	}
	if h.Concurrency == 0 {
		if h.When == HealthCheckAlways {
			h.Concurrency = DefaultHealthCheckConcurrencyAlways
		} else {
			h.Concurrency = DefaultHealthCheckConcurrency
		}
	}
	h.Plugin.SetDefault(common.TypeHealthCheck)
}

// Init 初始化CircuitBreakerConfigImpl配置
func (h *HealthCheckConfigImpl) Init() {
	h.Plugin = PluginConfigs{}
	h.Plugin.Init(common.TypeHealthCheck)
}
