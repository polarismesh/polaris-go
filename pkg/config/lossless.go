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
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

// Ensure LosslessConfigImpl implements LosslessConfig
var _ LosslessConfig = (*LosslessConfigImpl)(nil)

// LosslessConfigImpl 无损上下线配置实现
type LosslessConfigImpl struct {
	// Enable 是否启用无损上下线
	Enable *bool `yaml:"enable" json:"enable"`
	// Type 无损上下线插件类型
	Type string `yaml:"type" json:"type"`
	// Strategy 延迟注册策略，可选值：DELAY_BY_TIME(时长延迟), DELAY_BY_HEALTH_CHECK(探测延迟)
	Strategy string `yaml:"strategy" json:"strategy"`
	// DelayRegisterInterval 时长延迟注册的等待时间，兜底延迟多久会进行上线
	DelayRegisterInterval time.Duration `yaml:"delayRegisterInterval" json:"delayRegisterInterval"`
	// HealthCheckInterval 探测延迟注册的探测周期
	HealthCheckInterval time.Duration `yaml:"healthCheckInterval" json:"healthCheckInterval"`
	// Plugin 各个无损上下线插件的配置
	Plugin PluginConfigs `yaml:"plugin" json:"plugin"`
}

// IsEnable 是否启用无损上下线
func (l *LosslessConfigImpl) IsEnable() bool {
	if l == nil || l.Enable == nil {
		return DefaultLosslessEnabled
	}
	return *l.Enable
}

// SetEnable 设置是否启用无损上下线
func (l *LosslessConfigImpl) SetEnable(enable bool) {
	l.Enable = &enable
}

// GetType 获取无损上下线插件类型
func (l *LosslessConfigImpl) GetType() string {
	return l.Type
}

// SetType 设置无损上下线插件类型
func (l *LosslessConfigImpl) SetType(t string) {
	l.Type = t
}

// GetStrategy 获取延迟注册策略
func (l *LosslessConfigImpl) GetStrategy() string {
	return l.Strategy
}

// SetStrategy 设置延迟注册策略
func (l *LosslessConfigImpl) SetStrategy(s string) {
	l.Strategy = s
}

// GetDelayRegisterInterval 获取时长延迟注册的等待时间
func (l *LosslessConfigImpl) GetDelayRegisterInterval() time.Duration {
	return l.DelayRegisterInterval
}

// SetDelayRegisterInterval 设置时长延迟注册的等待时间
func (l *LosslessConfigImpl) SetDelayRegisterInterval(d time.Duration) {
	l.DelayRegisterInterval = d
}

// GetHealthCheckInterval 获取探测延迟注册的探测周期
func (l *LosslessConfigImpl) GetHealthCheckInterval() time.Duration {
	return l.HealthCheckInterval
}

// SetHealthCheckInterval 设置探测延迟注册的探测周期
func (l *LosslessConfigImpl) SetHealthCheckInterval(d time.Duration) {
	l.HealthCheckInterval = d
}

// Verify 校验配置参数
func (l *LosslessConfigImpl) Verify() error {
	if nil == l {
		return errors.New("LosslessConfig is nil")
	}
	if nil == l.Enable {
		return fmt.Errorf("provider.lossless.enable must not be nil")
	}
	if l.Type == "" {
		return fmt.Errorf("provider.lossless.type must not be empty")
	}
	if l.Strategy == "" {
		return fmt.Errorf("provider.lossless.strategy must not be empty")
	}
	if _, ok := model.SupportedDelayRegisterStrategies[l.Strategy]; !ok {
		return fmt.Errorf("provider.lossless.strategy %s is invalid", l.Strategy)
	}
	if l.DelayRegisterInterval == 0 && l.HealthCheckInterval == 0 {
		return fmt.Errorf("provider.lossless.delayRegisterInterval and healthCheckInterval must not be all zero")
	}
	return l.Plugin.Verify()
}

// GetPluginConfig 获取插件配置
func (l *LosslessConfigImpl) GetPluginConfig(pluginName string) BaseConfig {
	cfgValue, ok := l.Plugin[pluginName]
	if !ok {
		return nil
	}
	return cfgValue.(BaseConfig)
}

// SetPluginConfig 设置插件配置
func (l *LosslessConfigImpl) SetPluginConfig(pluginName string, value BaseConfig) error {
	return l.Plugin.SetPluginConfig(common.TypeLossless, pluginName, value)
}

// SetDefault 设置默认参数
func (l *LosslessConfigImpl) SetDefault() {
	if l.Enable == nil {
		enable := DefaultLosslessEnabled
		l.Enable = &enable
	}
	if l.Type == "" {
		l.Type = DefaultLosslessType
	}
	if l.Strategy == "" {
		l.Strategy = DefaultLosslessStrategy
	}
	if l.DelayRegisterInterval == 0 {
		l.DelayRegisterInterval = DefaultLosslessDelayRegisterInterval
	}
	if l.HealthCheckInterval == 0 {
		l.HealthCheckInterval = DefaultLosslessHealthCheckInterval
	}
	l.Plugin.SetDefault(common.TypeLossless)
}

// Init 配置初始化
func (l *LosslessConfigImpl) Init() {
	l.Plugin = PluginConfigs{}
	l.Plugin.Init(common.TypeLossless)
}
