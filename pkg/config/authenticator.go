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

	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

// AuthenticatorConfig 服务鉴权相关配置
type AuthenticatorConfig interface {
	BaseConfig
	PluginConfig
	// IsEnable 是否启用鉴权
	IsEnable() bool
	// SetEnable 设置是否启用鉴权
	SetEnable(bool)
	// GetChain 鉴权插件链
	GetChain() []string
	// SetChain 设置鉴权插件链
	SetChain([]string)
}

// 编译期校验 AuthenticatorConfigImpl 实现 AuthenticatorConfig
var _ AuthenticatorConfig = (*AuthenticatorConfigImpl)(nil)

// AuthenticatorConfigImpl 鉴权配置实现
type AuthenticatorConfigImpl struct {
	// Enable 是否启用鉴权
	Enable *bool `yaml:"enable" json:"enable"`
	// Chain 鉴权插件执行链
	Chain []string `yaml:"chain" json:"chain"`
	// Plugin 各鉴权插件的配置
	Plugin PluginConfigs `yaml:"plugin" json:"plugin"`
}

// IsEnable 是否启用鉴权
func (a *AuthenticatorConfigImpl) IsEnable() bool {
	if a == nil || a.Enable == nil {
		return DefaultAuthenticatorEnabled
	}
	return *a.Enable
}

// SetEnable 设置是否启用鉴权
func (a *AuthenticatorConfigImpl) SetEnable(enable bool) {
	a.Enable = &enable
}

// GetChain 获取鉴权插件链
func (a *AuthenticatorConfigImpl) GetChain() []string {
	return a.Chain
}

// SetChain 设置鉴权插件链
func (a *AuthenticatorConfigImpl) SetChain(chain []string) {
	a.Chain = chain
}

// Verify 校验配置参数
func (a *AuthenticatorConfigImpl) Verify() error {
	if nil == a {
		return errors.New("AuthenticatorConfig is nil")
	}
	if nil == a.Enable {
		// enable 未配置时按默认值处理，不报错
		enable := DefaultAuthenticatorEnabled
		a.Enable = &enable
	}
	return a.Plugin.Verify()
}

// GetPluginConfig 获取插件配置
func (a *AuthenticatorConfigImpl) GetPluginConfig(pluginName string) BaseConfig {
	cfgValue, ok := a.Plugin[pluginName]
	if !ok {
		return nil
	}
	return cfgValue.(BaseConfig)
}

// SetPluginConfig 设置插件配置
func (a *AuthenticatorConfigImpl) SetPluginConfig(pluginName string, value BaseConfig) error {
	return a.Plugin.SetPluginConfig(common.TypeAuthenticator, pluginName, value)
}

// SetDefault 设置默认参数
func (a *AuthenticatorConfigImpl) SetDefault() {
	if a.Enable == nil {
		enable := DefaultAuthenticatorEnabled
		a.Enable = &enable
	}
	a.Plugin.SetDefault(common.TypeAuthenticator)
}

// Init 配置初始化
func (a *AuthenticatorConfigImpl) Init() {
	a.Plugin = PluginConfigs{}
	a.Plugin.Init(common.TypeAuthenticator)
}
