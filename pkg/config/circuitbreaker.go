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

	"github.com/hashicorp/go-multierror"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

// CircuitBreakerConfigImpl 熔断相关配置
type CircuitBreakerConfigImpl struct {
	// Enable 是否启动熔断
	Enable *bool `yaml:"enable" json:"enable"`
	// CheckPeriod 熔断器定时检查周期
	CheckPeriod *time.Duration `yaml:"checkPeriod" json:"checkPeriod"`
	// Chain 熔断插件链
	Chain []string `yaml:"chain"`
	// SleepWindow 熔断周期，被熔断后多久可以变为半开
	SleepWindow *time.Duration `yaml:"sleepWindow" json:"sleepWindow"`
	// RequestCountAfterHalfOpen 半开状态后最多分配多少个探测请求
	RequestCountAfterHalfOpen int `yaml:"requestCountAfterHalfOpen" json:"requestCountAfterHalfOpen"`
	// SuccessCountAfterHalfOpen 半开状态后多少个成功请求则恢复
	SuccessCountAfterHalfOpen int `yaml:"successCountAfterHalfOpen" json:"successCountAfterHalfOpen"`
	// RecoverWindow 半开后的恢复周期，在这个周期内统计成功数
	RecoverWindow *time.Duration `yaml:"recoverWindow" json:"recoverWindow"`
	// RecoverNumBuckets 半开后的统计的滑窗数
	RecoverNumBuckets int `yaml:"recoverNumBuckets" json:"recoverNumBuckets"`
	// DefaultRuleEnable 是否启用默认熔断规则
	DefaultRuleEnable *bool `yaml:"defaultRuleEnable" json:"defaultRuleEnable"`
	// DefaultErrorCount 连续错误数熔断器默认连续错误数
	DefaultErrorCount *int `yaml:"defaultErrorCount" json:"defaultErrorCount"`
	// DefaultErrorPercent 错误率熔断器默认错误率
	DefaultErrorPercent *int `yaml:"defaultErrorPercent" json:"defaultErrorPercent"`
	// DefaultInterval 错误率熔断器默认统计周期
	DefaultInterval *time.Duration `yaml:"defaultInterval" json:"defaultInterval"`
	// DefaultMinimumRequest 错误率熔断器默认最小请求数
	DefaultMinimumRequest *int `yaml:"defaultMinimumRequest" json:"defaultMinimumRequest"`
	// Plugin 插件配置反序列化后的对象
	Plugin PluginConfigs `yaml:"plugin" json:"plugin"`
}

// IsEnable 是否启用熔断
func (c *CircuitBreakerConfigImpl) IsEnable() bool {
	return *c.Enable
}

// SetEnable 设置是否启用熔断
func (c *CircuitBreakerConfigImpl) SetEnable(enable bool) {
	c.Enable = &enable
}

// GetChain 熔断器插件链
func (c *CircuitBreakerConfigImpl) GetChain() []string {
	return c.Chain
}

// SetChain 设置熔断器插件链
func (c *CircuitBreakerConfigImpl) SetChain(chain []string) {
	c.Chain = chain
}

// GetCheckPeriod 熔断器定时检测时间
func (c *CircuitBreakerConfigImpl) GetCheckPeriod() time.Duration {
	return *c.CheckPeriod
}

// SetCheckPeriod 设置熔断器定时检测时间
func (c *CircuitBreakerConfigImpl) SetCheckPeriod(period time.Duration) {
	c.CheckPeriod = &period
}

// GetSleepWindow 获取熔断周期
func (c *CircuitBreakerConfigImpl) GetSleepWindow() time.Duration {
	return *c.SleepWindow
}

// SetSleepWindow 设置熔断周期
func (c *CircuitBreakerConfigImpl) SetSleepWindow(interval time.Duration) {
	c.SleepWindow = &interval
}

// GetRequestCountAfterHalfOpen 获取半开状态后最多分配多少个探测请求
func (c *CircuitBreakerConfigImpl) GetRequestCountAfterHalfOpen() int {
	return c.RequestCountAfterHalfOpen
}

// SetRequestCountAfterHalfOpen 设置半开状态后最多分配多少个探测请求
func (c *CircuitBreakerConfigImpl) SetRequestCountAfterHalfOpen(count int) {
	c.RequestCountAfterHalfOpen = count
}

// GetSuccessCountAfterHalfOpen 获取半开状态后多少个成功请求则恢复
func (c *CircuitBreakerConfigImpl) GetSuccessCountAfterHalfOpen() int {
	return c.SuccessCountAfterHalfOpen
}

// SetSuccessCountAfterHalfOpen 设置半开状态后多少个成功请求则恢复
func (c *CircuitBreakerConfigImpl) SetSuccessCountAfterHalfOpen(count int) {
	c.SuccessCountAfterHalfOpen = count
}

// GetRecoverWindow 获取半开后的恢复周期，按周期来进行半开放量的统计
func (c *CircuitBreakerConfigImpl) GetRecoverWindow() time.Duration {
	return *c.RecoverWindow
}

// SetRecoverWindow 设置半开后的恢复周期，按周期来进行半开放量的统计
func (c *CircuitBreakerConfigImpl) SetRecoverWindow(value time.Duration) {
	c.RecoverWindow = &value
}

// GetRecoverNumBuckets 半开后请求数统计滑桶数量
func (c *CircuitBreakerConfigImpl) GetRecoverNumBuckets() int {
	return c.RecoverNumBuckets
}

// SetRecoverNumBuckets 设置半开后请求数统计滑桶数量
func (c *CircuitBreakerConfigImpl) SetRecoverNumBuckets(value int) {
	c.RecoverNumBuckets = value
}

// IsDefaultRuleEnable 是否启用默认熔断规则
func (c *CircuitBreakerConfigImpl) IsDefaultRuleEnable() bool {
	if c.DefaultRuleEnable == nil {
		return false
	}
	return *c.DefaultRuleEnable
}

// SetDefaultRuleEnable 设置是否启用默认熔断规则
func (c *CircuitBreakerConfigImpl) SetDefaultRuleEnable(enable bool) {
	c.DefaultRuleEnable = &enable
}

// GetDefaultErrorCount 获取连续错误数熔断器默认连续错误数
func (c *CircuitBreakerConfigImpl) GetDefaultErrorCount() int {
	if c.DefaultErrorCount == nil {
		return 0
	}
	return *c.DefaultErrorCount
}

// SetDefaultErrorCount 设置连续错误数熔断器默认连续错误数
func (c *CircuitBreakerConfigImpl) SetDefaultErrorCount(count int) {
	c.DefaultErrorCount = &count
}

// GetDefaultErrorPercent 获取错误率熔断器默认错误率
func (c *CircuitBreakerConfigImpl) GetDefaultErrorPercent() int {
	if c.DefaultErrorPercent == nil {
		return 0
	}
	return *c.DefaultErrorPercent
}

// SetDefaultErrorPercent 设置错误率熔断器默认错误率
func (c *CircuitBreakerConfigImpl) SetDefaultErrorPercent(percent int) {
	c.DefaultErrorPercent = &percent
}

// GetDefaultInterval 获取错误率熔断器默认统计周期
func (c *CircuitBreakerConfigImpl) GetDefaultInterval() time.Duration {
	if c.DefaultInterval == nil {
		return 0
	}
	return *c.DefaultInterval
}

// SetDefaultInterval 设置错误率熔断器默认统计周期
func (c *CircuitBreakerConfigImpl) SetDefaultInterval(interval time.Duration) {
	c.DefaultInterval = &interval
}

// GetDefaultMinimumRequest 获取错误率熔断器默认最小请求数
func (c *CircuitBreakerConfigImpl) GetDefaultMinimumRequest() int {
	if c.DefaultMinimumRequest == nil {
		return 0
	}
	return *c.DefaultMinimumRequest
}

// SetDefaultMinimumRequest 设置错误率熔断器默认最小请求数
func (c *CircuitBreakerConfigImpl) SetDefaultMinimumRequest(count int) {
	c.DefaultMinimumRequest = &count
}

// GetErrorCountConfig 获取连续错误数熔断配置
func (c *CircuitBreakerConfigImpl) GetErrorCountConfig() ErrorCountConfig {
	return c.Plugin[DefaultCircuitBreakerErrCount].(ErrorCountConfig)
}

// GetErrorRateConfig 错误率熔断配置
func (c *CircuitBreakerConfigImpl) GetErrorRateConfig() ErrorRateConfig {
	return c.Plugin[DefaultCircuitBreakerErrRate].(ErrorRateConfig)
}

// Verify 检验LocalCacheConfig配置
func (c *CircuitBreakerConfigImpl) Verify() error {
	if nil == c {
		return errors.New("CircuitBreakerConfig is nil")
	}
	var errs error
	if nil != c.CheckPeriod && *c.CheckPeriod < MinCircuitBreakerCheckPeriod {
		errs = multierror.Append(errs,
			fmt.Errorf("consumer.circuitbreaker.checkPeriod should greater than %v",
				MinCircuitBreakerCheckPeriod))
	}
	if nil != c.SleepWindow && *c.SleepWindow < MinSleepWindow {
		errs = multierror.Append(errs,
			fmt.Errorf("consumer.circuitbreaker.sleepWindow must be greater than %v", MinSleepWindow))
	}
	if c.RequestCountAfterHalfOpen <= 0 {
		errs = multierror.Append(errs,
			fmt.Errorf("consumer.circuitbreaker.requestCountAfterHalfOpen must be greater than 0"))
	}
	if c.SuccessCountAfterHalfOpen <= 0 || c.SuccessCountAfterHalfOpen > c.RequestCountAfterHalfOpen {
		errs = multierror.Append(errs,
			fmt.Errorf("consumer.circuitbreaker.successCountAfterHalfOpen must be in "+
				"(0: consumer.circuitbreaker.requestCountAfterHalfOpen]"))
	}
	if nil != c.RecoverWindow && *c.RecoverWindow < MinRecoverWindow {
		errs = multierror.Append(errs,
			fmt.Errorf("consumer.circuitbreaker.recoverWindow must be greater than %v", MinRecoverWindow))
	}
	if c.RecoverNumBuckets < MinRecoverNumBuckets {
		errs = multierror.Append(errs,
			fmt.Errorf(
				"consumer.circuitbreaker.recoverNumBuckets must be greater than %d", MinRecoverNumBuckets))
	}
	// 校验默认熔断规则参数
	if nil != c.DefaultErrorCount && *c.DefaultErrorCount <= 0 {
		errs = multierror.Append(errs,
			fmt.Errorf("consumer.circuitbreaker.defaultErrorCount must be greater than 0"))
	}
	if nil != c.DefaultErrorPercent && (*c.DefaultErrorPercent < 0 || *c.DefaultErrorPercent > 100) {
		errs = multierror.Append(errs,
			fmt.Errorf("consumer.circuitbreaker.defaultErrorPercent must be in range [0, 100]"))
	}
	if nil != c.DefaultInterval && *c.DefaultInterval <= 0 {
		errs = multierror.Append(errs,
			fmt.Errorf("consumer.circuitbreaker.defaultInterval must be greater than 0"))
	}
	if nil != c.DefaultMinimumRequest && *c.DefaultMinimumRequest <= 0 {
		errs = multierror.Append(errs,
			fmt.Errorf("consumer.circuitbreaker.defaultMinimumRequest must be greater than 0"))
	}
	if err := c.Plugin.Verify(); err != nil {
		errs = multierror.Append(errs, err)
	}
	return errs
}

// SetDefault 设置CircuitBreakerConfigImpl配置的默认值
func (c *CircuitBreakerConfigImpl) SetDefault() {
	if nil == c.CheckPeriod {
		c.CheckPeriod = model.ToDurationPtr(DefaultCircuitBreakerCheckPeriod)
	}
	if len(c.Chain) == 0 {
		c.Chain = []string{DefaultCircuitBreaker}
	}
	if nil == c.Enable {
		enable := DefaultCircuitBreakerEnabled
		c.Enable = &enable
	}
	if nil == c.SleepWindow {
		c.SleepWindow = model.ToDurationPtr(DefaultSleepWindow)
	}
	if c.RequestCountAfterHalfOpen == 0 {
		c.RequestCountAfterHalfOpen = DefaultRequestCountAfterHalfOpen
	}
	if c.SuccessCountAfterHalfOpen == 0 && c.RequestCountAfterHalfOpen >= DefaultRequestCountAfterHalfOpen {
		c.SuccessCountAfterHalfOpen = DefaultSuccessCountAfterHalfOpen
	}
	if nil == c.RecoverWindow {
		c.RecoverWindow = model.ToDurationPtr(DefaultRecoverWindow)
	}
	if c.RecoverNumBuckets == 0 {
		c.RecoverNumBuckets = DefaultRecoverNumBuckets
	}
	if nil == c.DefaultRuleEnable {
		enable := DefaultRuleEnable
		c.DefaultRuleEnable = &enable
	}
	if nil == c.DefaultErrorCount {
		count := DefaultErrorCount
		c.DefaultErrorCount = &count
	}
	if nil == c.DefaultErrorPercent {
		percent := DefaultErrorPercent
		c.DefaultErrorPercent = &percent
	}
	if nil == c.DefaultInterval {
		interval := DefaultInterval
		c.DefaultInterval = &interval
	}
	if nil == c.DefaultMinimumRequest {
		count := DefaultMinimumRequest
		c.DefaultMinimumRequest = &count
	}
	c.Plugin.SetDefault(common.TypeCircuitBreaker)
}

// GetPluginConfig 获取一个熔断器插件配置
func (c *CircuitBreakerConfigImpl) GetPluginConfig(pluginName string) BaseConfig {
	cfg, ok := c.Plugin[pluginName]
	if !ok {
		return nil
	}
	return cfg.(BaseConfig)
}

// SetPluginConfig 设置一个熔断器插件配置
func (c *CircuitBreakerConfigImpl) SetPluginConfig(pluginName string, value BaseConfig) error {
	return c.Plugin.SetPluginConfig(common.TypeCircuitBreaker, pluginName, value)
}

// Init 初始化CircuitBreakerConfigImpl配置
func (c *CircuitBreakerConfigImpl) Init() {
	c.Plugin = PluginConfigs{}
	c.Plugin.Init(common.TypeCircuitBreaker)
}
