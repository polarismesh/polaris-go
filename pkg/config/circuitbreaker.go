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
	"github.com/hashicorp/go-multierror"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"time"
)

//熔断相关配置
type CircuitBreakerConfigImpl struct {
	//是否启动熔断
	Enable *bool `yaml:"enable" json:"enable"`
	//熔断器定时检查周期
	CheckPeriod *time.Duration `yaml:"checkPeriod" json:"checkPeriod"`
	//熔断插件链
	Chain []string `yaml:"chain"`
	//熔断周期，被熔断后多久可以变为半开
	SleepWindow *time.Duration `yaml:"sleepWindow" json:"sleepWindow"`
	//半开状态后最多分配多少个探测请求
	RequestCountAfterHalfOpen int `yaml:"requestCountAfterHalfOpen" json:"requestCountAfterHalfOpen"`
	//半开状态后多少个成功请求则恢复
	SuccessCountAfterHalfOpen int `yaml:"successCountAfterHalfOpen" json:"successCountAfterHalfOpen"`
	//半开后的恢复周期，在这个周期内统计成功数
	RecoverWindow *time.Duration `yaml:"recoverWindow" json:"recoverWindow"`
	//半开后的统计的滑窗数
	RecoverNumBuckets int `yaml:"recoverNumBuckets" json:"recoverNumBuckets"`
	// 插件配置反序列化后的对象
	Plugin PluginConfigs `yaml:"plugin" json:"plugin"`
}

//是否启用熔断
func (c *CircuitBreakerConfigImpl) IsEnable() bool {
	return *c.Enable
}

//设置是否启用熔断
func (c *CircuitBreakerConfigImpl) SetEnable(enable bool) {
	c.Enable = &enable
}

//熔断器插件链
func (c *CircuitBreakerConfigImpl) GetChain() []string {
	return c.Chain
}

//设置熔断器插件链
func (c *CircuitBreakerConfigImpl) SetChain(chain []string) {
	c.Chain = chain
}

//熔断器定时检测时间
func (c *CircuitBreakerConfigImpl) GetCheckPeriod() time.Duration {
	return *c.CheckPeriod
}

//设置熔断器定时检测时间
func (c *CircuitBreakerConfigImpl) SetCheckPeriod(period time.Duration) {
	c.CheckPeriod = &period
}

//获取熔断周期
func (c *CircuitBreakerConfigImpl) GetSleepWindow() time.Duration {
	return *c.SleepWindow
}

//设置熔断周期
func (c *CircuitBreakerConfigImpl) SetSleepWindow(interval time.Duration) {
	c.SleepWindow = &interval
}

//获取半开状态后最多分配多少个探测请求
func (c *CircuitBreakerConfigImpl) GetRequestCountAfterHalfOpen() int {
	return c.RequestCountAfterHalfOpen
}

//设置半开状态后最多分配多少个探测请求
func (c *CircuitBreakerConfigImpl) SetRequestCountAfterHalfOpen(count int) {
	c.RequestCountAfterHalfOpen = count
}

//获取半开状态后多少个成功请求则恢复
func (c *CircuitBreakerConfigImpl) GetSuccessCountAfterHalfOpen() int {
	return c.SuccessCountAfterHalfOpen
}

//设置半开状态后多少个成功请求则恢复
func (c *CircuitBreakerConfigImpl) SetSuccessCountAfterHalfOpen(count int) {
	c.SuccessCountAfterHalfOpen = count
}

//获取半开后的恢复周期，按周期来进行半开放量的统计
func (c *CircuitBreakerConfigImpl) GetRecoverWindow() time.Duration {
	return *c.RecoverWindow
}

//设置半开后的恢复周期，按周期来进行半开放量的统计
func (c *CircuitBreakerConfigImpl) SetRecoverWindow(value time.Duration) {
	c.RecoverWindow = &value
}

//半开后请求数统计滑桶数量
func (c *CircuitBreakerConfigImpl) GetRecoverNumBuckets() int {
	return c.RecoverNumBuckets
}

//设置半开后请求数统计滑桶数量
func (c *CircuitBreakerConfigImpl) SetRecoverNumBuckets(value int) {
	c.RecoverNumBuckets = value
}

//连续错误数熔断配置
func (c *CircuitBreakerConfigImpl) GetErrorCountConfig() ErrorCountConfig {
	return c.Plugin[DefaultCircuitBreakerErrCount].(ErrorCountConfig)
}

//错误率熔断配置
func (c *CircuitBreakerConfigImpl) GetErrorRateConfig() ErrorRateConfig {
	return c.Plugin[DefaultCircuitBreakerErrRate].(ErrorRateConfig)
}

//检验LocalCacheConfig配置
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
	if err := c.Plugin.Verify(); nil != err {
		errs = multierror.Append(errs, err)
	}
	return errs
}

//设置CircuitBreakerConfigImpl配置的默认值
func (c *CircuitBreakerConfigImpl) SetDefault() {
	if nil == c.CheckPeriod {
		c.CheckPeriod = model.ToDurationPtr(DefaultCircuitBreakerCheckPeriod)
	}
	if len(c.Chain) == 0 {
		c.Chain = []string{DefaultCircuitBreakerErrCount, DefaultCircuitBreakerErrRate}
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
	c.Plugin.SetDefault(common.TypeCircuitBreaker)
}

//获取一个熔断器插件配置
func (c *CircuitBreakerConfigImpl) GetPluginConfig(pluginName string) BaseConfig {
	cfg, ok := c.Plugin[pluginName]
	if !ok {
		return nil
	}
	return cfg.(BaseConfig)
}

//设置一个熔断器插件配置
func (c *CircuitBreakerConfigImpl) SetPluginConfig(pluginName string, value BaseConfig) error {
	return c.Plugin.SetPluginConfig(common.TypeCircuitBreaker, pluginName, value)
}

//初始化CircuitBreakerConfigImpl配置
func (c *CircuitBreakerConfigImpl) Init() {
	c.Plugin = PluginConfigs{}
	c.Plugin.Init(common.TypeCircuitBreaker)
}
