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
	"log"

	"github.com/hashicorp/go-multierror"

	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

// ServiceRouterConfigImpl 服务路由配置.
type ServiceRouterConfigImpl struct {
	// 服务路由责任链前置链，在主链之前执行（如泳道路由）
	BeforeChain []string `yaml:"beforeChain" json:"beforeChain"`
	// 服务路由责任链
	Chain []string `yaml:"chain" json:"chain"`
	// 服务路由责任链后置链
	AfterChain []string `yaml:"afterChain" json:"afterChain"`
	// 插件相关配置
	Plugin PluginConfigs `yaml:"plugin" json:"plugin"`
	// 进行过滤时的最大过滤比例
	PercentOfMinInstances *float64 `yaml:"percentOfMinInstances" json:"percentOfMinInstances"`
	// 是否启用全死全活机制
	EnableRecoverAll *bool `yaml:"enableRecoverAll" json:"enableRecoverAll"`
	// chainWarnings 配置去重过程中累积的告警信息，由 deduplicateChains 填充，
	// 由 FlushChainWarnings 在 SDK 日志系统就绪后消费。不参与 YAML 序列化。
	chainWarnings []string `yaml:"-" json:"-"`
}

// GetNearbyConfig 获取就近路由配置.
func (s *ServiceRouterConfigImpl) GetNearbyConfig() NearbyConfig {
	s.SetDefault()
	cfgValue, ok := s.Plugin[DefaultServiceRouterNearbyBased]
	if !ok {
		return nil
	}
	return cfgValue.(NearbyConfig)
}

// GetBeforeChain consumer.serviceRouter.beforeChain
// 路由责任链前置路由配置.
func (s *ServiceRouterConfigImpl) GetBeforeChain() []string {
	return s.BeforeChain
}

// SetBeforeChain 设置路由责任链前置路由配置.
func (s *ServiceRouterConfigImpl) SetBeforeChain(chain []string) {
	s.BeforeChain = chain
}

// GetChain consumer.serviceRouter.filterChain
// 路由责任链配置.
func (s *ServiceRouterConfigImpl) GetChain() []string {
	return s.Chain
}

// SetChain 设置路由责任链配置.
func (s *ServiceRouterConfigImpl) SetChain(chain []string) {
	s.Chain = chain
}

// GetAfterChain consumer.serviceRouter.afterChain
// 路由责任链后置路由配置.
func (s *ServiceRouterConfigImpl) GetAfterChain() []string {
	return s.AfterChain
}

// SetAfterChain 设置路由责任链配置.
func (s *ServiceRouterConfigImpl) SetAfterChain(chain []string) {
	s.AfterChain = chain
}

// GetPluginConfig consumer.serviceRouter.plugin.
func (s *ServiceRouterConfigImpl) GetPluginConfig(pluginName string) BaseConfig {
	cfgValue, ok := s.Plugin[pluginName]
	if !ok {
		return nil
	}
	return cfgValue.(BaseConfig)
}

// SetPluginConfig 输出插件具体配置.
func (s *ServiceRouterConfigImpl) SetPluginConfig(pluginName string, value BaseConfig) error {
	return s.Plugin.SetPluginConfig(common.TypeServiceRouter, pluginName, value)
}

// GetPercentOfMinInstances 获取PercentOfMinInstances参数.
func (s *ServiceRouterConfigImpl) GetPercentOfMinInstances() float64 {
	return *(s.PercentOfMinInstances)
}

// SetPercentOfMinInstances 设置PercentOfMinInstances参数.
func (s *ServiceRouterConfigImpl) SetPercentOfMinInstances(percent float64) {
	s.PercentOfMinInstances = &percent
}

// IsEnableRecoverAll 是否启用全死全活机制.
func (s *ServiceRouterConfigImpl) IsEnableRecoverAll() bool {
	return *(s.EnableRecoverAll)
}

// SetEnableRecoverAll 设置启用全死全活机制.
func (s *ServiceRouterConfigImpl) SetEnableRecoverAll(recoverAll bool) {
	s.EnableRecoverAll = &recoverAll
}

// Verify 检验ServiceRouterConfig配置.
func (s *ServiceRouterConfigImpl) Verify() error {
	if s == nil {
		return errors.New("ServiceRouterConfig is nil")
	}
	var errs error
	if *(s.PercentOfMinInstances) >= 1 || *(s.PercentOfMinInstances) < 0 {
		errs = multierror.Append(errs, fmt.Errorf("consumer.servicerouter.percentOfMinInstances must be in range [0.0, 1.0)"))
	}
	plugErr := s.Plugin.Verify()
	if plugErr != nil {
		errs = multierror.Append(errs, plugErr)
	}
	return errs
}

// SetDefault 设置ServiceRouterConfig配置的默认值.
func (s *ServiceRouterConfigImpl) SetDefault() {
	// 默认开启泳道路由。当服务端无泳道规则时，laneRouter.Enable() 返回 false，
	// 不会执行实际过滤逻辑，性能开销可忽略。
	if len(s.BeforeChain) == 0 {
		s.BeforeChain = append(s.BeforeChain, DefaultServiceRouterLane)
	}
	if len(s.Chain) == 0 {
		s.Chain = append(s.Chain, DefaultServiceRouterRuleBased)
		s.Chain = append(s.Chain, DefaultServiceRouterNearbyBased)
	}
	if len(s.AfterChain) == 0 {
		s.AfterChain = append(s.AfterChain, DefaultServiceRouterFilterOnly)
	}
	// 去重：同一个路由插件不能出现在多条链中，beforeChain 优先级最高
	s.deduplicateChains()
	if nil == s.PercentOfMinInstances {
		s.PercentOfMinInstances = new(float64)
		*(s.PercentOfMinInstances) = DefaultPercentOfMinInstances
	}
	if nil == s.EnableRecoverAll {
		s.EnableRecoverAll = new(bool)
		*(s.EnableRecoverAll) = DefaultRecoverAllEnabled
	}
	s.Plugin.SetDefault(common.TypeServiceRouter)
}

// deduplicateChains 去重路由链配置：同一个路由插件不能同时出现在多条链中。
// 优先级：beforeChain > chain > afterChain。
// 低优先级链中的重复插件会被自动移除。
func (s *ServiceRouterConfigImpl) deduplicateChains() {
	seen := make(map[string]string) // plugin name → 所属链名
	for _, name := range s.BeforeChain {
		seen[name] = "beforeChain"
	}
	s.Chain = s.deduplicateSlice(s.Chain, seen, "chain")
	s.AfterChain = s.deduplicateSlice(s.AfterChain, seen, "afterChain")
}

// deduplicateSlice 从 chain 中移除 seen 中已存在的插件，同时将 chain 中的插件加入 seen。
// 注意：此函数在 SetDefault() 阶段调用，SDK 日志框架（pkg/log）可能尚未初始化。
// 告警通过两条通道发出：
//  1. 立即用标准库 log 输出到 stderr，保证即便 SDK 未启动也能被看到；
//  2. 累积到 s.chainWarnings，待 SDK 日志系统就绪后由 FlushChainWarnings 补打一条，
//     保证容器环境/日志聚合系统都能收到。
func (s *ServiceRouterConfigImpl) deduplicateSlice(chain []string, seen map[string]string, chainName string) []string {
	result := make([]string, 0, len(chain))
	for _, name := range chain {
		if existIn, ok := seen[name]; ok {
			msg := fmt.Sprintf(
				"serviceRouter: plugin %q is already in %s, removing from %s",
				name, existIn, chainName)
			log.Printf("[WARNING] %s", msg)
			s.chainWarnings = append(s.chainWarnings, msg)
			continue
		}
		seen[name] = chainName
		result = append(result, name)
	}
	return result
}

// FlushChainWarnings 将 SetDefault 阶段累积的链配置告警输出到指定 logger，
// 并清空内部缓冲。应在 SDK 日志系统就绪后调用一次。
// logger 为 nil 时不输出但仍清空缓冲。
func (s *ServiceRouterConfigImpl) FlushChainWarnings(logger interface {
	Warnf(format string, args ...interface{})
}) {
	if logger != nil {
		for _, msg := range s.chainWarnings {
			logger.Warnf("[Config] %s", msg)
		}
	}
	s.chainWarnings = nil
}

// Init 配置初始化.
func (s *ServiceRouterConfigImpl) Init() {
	s.Plugin = PluginConfigs{}
	s.Plugin.Init(common.TypeServiceRouter)
}
