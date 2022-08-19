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

import "github.com/polarismesh/polaris-go/pkg/plugin/common"

// LocationConfigImpl 地理位置配置.
type LocationConfigImpl struct {
	Provider string `yaml:"provider" json:"provider"`
	// 插件相关配置
	Plugin PluginConfigs `yaml:"plugin" json:"plugin"`
}

// GetProvider 获取地理位置的提供者插件名称.
func (a *LocationConfigImpl) GetProvider() string {
	return a.Provider
}

// SetProvider 设置地理位置的提供者插件名称.
func (a *LocationConfigImpl) SetProvider(provider string) {
	a.Provider = provider
}

// Init 配置初始化.
func (a *LocationConfigImpl) Init() {
	a.Plugin = PluginConfigs{}
	a.Plugin.Init(common.TypeLocationProvider)
}

// GetPluginConfig consumer.loadbalancer.plugin.
func (a *LocationConfigImpl) GetPluginConfig(pluginName string) BaseConfig {
	cfgValue, ok := a.Plugin[pluginName]
	if !ok {
		return nil
	}
	return cfgValue.(BaseConfig)
}

// SetPluginConfig 输出插件具体配置.
func (a *LocationConfigImpl) SetPluginConfig(pluginName string, value BaseConfig) error {
	return a.Plugin.SetPluginConfig(common.TypeLocationProvider, pluginName, value)
}

// Verify 检验LocalCacheConfig配置.
func (a *LocationConfigImpl) Verify() error {
	return nil
}

// SetDefault 设置LocalCacheConfig配置的默认值.
func (a *LocationConfigImpl) SetDefault() {
	if len(a.Provider) == 0 {
		a.Provider = DefaultLocationProvider
	}
}
