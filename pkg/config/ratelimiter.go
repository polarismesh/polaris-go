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
	"strings"
	"time"

	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

// RateLimitConfigImpl 限流配置对象
type RateLimitConfigImpl struct {
	// 是否启动限流
	Enable *bool `yaml:"enable" json:"enable"`
	// 各个限流插件的配置
	Plugin PluginConfigs `yaml:"plugin" json:"plugin"`
	// 最大限流窗口数量
	MaxWindowSize int `yaml:"maxWindowSize" json:"maxWindowSize"`
	// 超时window检查周期
	PurgeInterval time.Duration `yaml:"purgeInterval" json:"purgeInterval"`
	// 本地限流规则
	Rules []RateLimitRule `yaml:"rules"`
}

// RateLimitRule 限流规则
type RateLimitRule struct {
	Namespace     string             `yaml:"namespace"`
	Service       string             `yaml:"service"`
	Labels        map[string]Matcher `yaml:"labels"`
	MaxAmount     int                `yaml:"maxAmount"`
	ValidDuration time.Duration      `yaml:"validDuration"`
}

// Verify 校验限流规则
func (r *RateLimitRule) Verify() error {
	if len(r.Namespace) == 0 {
		return errors.New("namespace is empty")
	}
	if len(r.Service) == 0 {
		return errors.New("service is empty")
	}
	if len(r.Labels) > 0 {
		for _, matcher := range r.Labels {
			if len(matcher.Type) > 0 {
				upperType := strings.ToUpper(matcher.Type)
				if upperType != TypeExact && upperType != TypeRegex {
					return fmt.Errorf("matcher.type should be %s or %s", TypeExact, TypeRegex)
				}
			}
		}
	}
	if r.ValidDuration < time.Second {
		return errors.New("validDuration must greater than or equals to 1s")
	}
	if r.MaxAmount < 0 {
		return errors.New("maxAmount must greater than or equals to 0")
	}
	return nil
}

const (
	// TypeExact .
	TypeExact = "EXACT"
	// TypeRegex .
	TypeRegex = "REGEX"
)

// Matcher 标签匹配类型
type Matcher struct {
	Type  string `yaml:"type"`
	Value string `yaml:"value"`
}

// IsEnable 是否启用限流能力
func (r *RateLimitConfigImpl) IsEnable() bool {
	return *r.Enable
}

// SetEnable 设置是否启用限流能力
func (r *RateLimitConfigImpl) SetEnable(value bool) {
	r.Enable = &value
}

// ForbidServerMetricService 已经禁用的限流集群名
const ForbidServerMetricService = "polaris.metric"

// Verify 校验配置参数
func (r *RateLimitConfigImpl) Verify() error {
	if nil == r {
		return errors.New("RateLimitConfig is nil")
	}
	if nil == r.Enable {
		return fmt.Errorf("provider.rateLimit.enable must not be nil")
	}
	if len(r.Rules) > 0 {
		for _, rule := range r.Rules {
			if err := rule.Verify(); nil != err {
				return err
			}
		}
	}
	return r.Plugin.Verify()
}

// GetPluginConfig 获取插件配置
func (r *RateLimitConfigImpl) GetPluginConfig(pluginName string) BaseConfig {
	cfgValue, ok := r.Plugin[pluginName]
	if !ok {
		return nil
	}
	return cfgValue.(BaseConfig)
}

// SetDefault 设置默认参数
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
	r.Plugin.SetDefault(common.TypeRateLimiter)
}

// SetPluginConfig 设置插件配置
func (r *RateLimitConfigImpl) SetPluginConfig(pluginName string, value BaseConfig) error {
	return r.Plugin.SetPluginConfig(common.TypeRateLimiter, pluginName, value)
}

// Init 配置初始化
func (r *RateLimitConfigImpl) Init() {
	r.Rules = []RateLimitRule{}
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

// GetRules 获取规则
func (r *RateLimitConfigImpl) GetRules() []RateLimitRule {
	return r.Rules
}

// SetRules 。设置规则
func (r *RateLimitConfigImpl) SetRules(rules []RateLimitRule) {
	r.Rules = rules
}
