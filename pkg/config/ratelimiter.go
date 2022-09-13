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

	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

// RateLimitConfigImpl 限流配置对象.
type RateLimitConfigImpl struct {
	// 是否启动限流
	Enable *bool `yaml:"enable" json:"enable"`
	// 各个限流插件的配置
	Plugin PluginConfigs `yaml:"plugin" json:"plugin"`
	// 最大限流窗口数量
	MaxWindowSize int `yaml:"maxWindowSize" json:"maxWindowSize"`
	// 超时window检查周期
	PurgeInterval time.Duration `yaml:"purgeInterval" json:"purgeInterval"`
	// LimiterNamespace 限流服务的命名空间
	LimiterNamespace string `yaml:"limiterNamespace" json:"limiterNamespace"`
	// LimiterService 限流服务的服务名
	LimiterService string `yaml:"limiterService" json:"limiterService		"`
}

// IsEnable 是否启用限流能力.
func (r *RateLimitConfigImpl) IsEnable() bool {
	return *r.Enable
}

// SetEnable 设置是否启用限流能力.
func (r *RateLimitConfigImpl) SetEnable(value bool) {
	r.Enable = &value
}

// ForbidServerMetricService 已经禁用的限流集群名.
const ForbidServerMetricService = "polaris.metric"

// Verify 校验配置参数.
func (r *RateLimitConfigImpl) Verify() error {
	if nil == r {
		return errors.New("RateLimitConfig is nil")
	}
	if nil == r.Enable {
		return fmt.Errorf("provider.rateLimit.enable must not be nil")
	}
	return r.Plugin.Verify()
}

// GetPluginConfig 获取插件配置.
func (r *RateLimitConfigImpl) GetPluginConfig(pluginName string) BaseConfig {
	cfgValue, ok := r.Plugin[pluginName]
	if !ok {
		return nil
	}
	return cfgValue.(BaseConfig)
}

// SetDefault 设置默认参数.
func (r *RateLimitConfigImpl) SetDefault() {
	if r.Enable == nil {
		r.Enable = &DefaultRateLimitEnable
	}
	if r.MaxWindowSize == 0 {
		r.MaxWindowSize = MaxRateLimitWindowSize
	}
	if r.PurgeInterval == 0 {
		r.PurgeInterval = DefaultRateLimitPurgeInterval
	}
	if len(r.LimiterNamespace) == 0 {
		r.LimiterNamespace = DefaultLimiterNamespace
	}
	if len(r.LimiterService) == 0 {
		r.LimiterService = DefaultLimiterService
	}
	r.Plugin.SetDefault(common.TypeRateLimiter)
}

// SetPluginConfig 设置插件配置.
func (r *RateLimitConfigImpl) SetPluginConfig(pluginName string, value BaseConfig) error {
	return r.Plugin.SetPluginConfig(common.TypeRateLimiter, pluginName, value)
}

// Init 配置初始化.
func (r *RateLimitConfigImpl) Init() {
	r.Plugin = PluginConfigs{}
	r.Plugin.Init(common.TypeRateLimiter)
}

// GetMaxWindowSize .
func (r *RateLimitConfigImpl) GetMaxWindowSize() int {
	return r.MaxWindowSize
}

// SetMaxWindowSize .
func (r *RateLimitConfigImpl) SetMaxWindowSize(maxSize int) {
	r.MaxWindowSize = maxSize
}

// GetPurgeInterval .
func (r *RateLimitConfigImpl) GetPurgeInterval() time.Duration {
	return r.PurgeInterval
}

// SetPurgeInterval  .
func (r *RateLimitConfigImpl) SetPurgeInterval(v time.Duration) {
	r.PurgeInterval = v
}

func (r *RateLimitConfigImpl) GetLimiterService() string {
	return r.LimiterService
}

func (r *RateLimitConfigImpl) SetLimiterService(value string) {
	r.LimiterService = value
}

func (r *RateLimitConfigImpl) SetLimiterNamespace(value string) {
	r.LimiterNamespace = value
}

func (r *RateLimitConfigImpl) GetLimiterNamespace() string {
	return r.LimiterNamespace
}
