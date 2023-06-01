/*
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
 *  under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
 * CONDITIONS OF ANY KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations under the License.
 *
 */

package config

import (
	"errors"

	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

// ConfigFilterConfigImpl 配置过滤器配置
type ConfigFilterConfigImpl struct {
	Enable *bool         `yaml:"enable" json:"enable"`
	Chain  []string      `yaml:"chain" json:"chain"`
	Plugin PluginConfigs `yaml:"plugin" json:"plugin"`
}

// IsEnable is enable config filter
func (c *ConfigFilterConfigImpl) IsEnable() bool {
	return *c.Enable
}

// SetEnable set enable config filter
func (c *ConfigFilterConfigImpl) SetEnable(enable bool) {
	c.Enable = &enable
}

// GetChain get config filter chain
func (c *ConfigFilterConfigImpl) GetChain() []string {
	return c.Chain
}

// SetChain set config filter chain
func (c *ConfigFilterConfigImpl) SetChain(chain []string) {
	c.Chain = chain
}

// GetPluginConfig get config filter plugin
func (c *ConfigFilterConfigImpl) GetPluginConfig(pluginName string) BaseConfig {
	cfgValue, ok := c.Plugin[pluginName]
	if !ok {
		return nil
	}
	return cfgValue.(BaseConfig)
}

// SetPluginConfig set config filter plugin
func (c *ConfigFilterConfigImpl) SetPluginConfig(pluginName string, value BaseConfig) error {
	return c.Plugin.SetPluginConfig(common.TypeConfigFilter, pluginName, value)
}

// Verify verify config filter
func (c *ConfigFilterConfigImpl) Verify() error {
	if nil == c {
		return errors.New("ConfigFilterConfig is nil")
	}
	return nil
}

// SetDefault set default config filter
func (c *ConfigFilterConfigImpl) SetDefault() {
	if c.Enable == nil {
		enable := DefaultConfigFilterEnabled
		c.Enable = &enable
	}
	c.Plugin.SetDefault(common.TypeConfigFilter)
}

// Init init config filter
func (c *ConfigFilterConfigImpl) Init() {
	c.Plugin = PluginConfigs{}
	c.Plugin.Init(common.TypeConfigFilter)
}
