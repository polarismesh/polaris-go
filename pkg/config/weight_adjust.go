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

// WeightAdjustConfigImpl 权重调整配置实现
type WeightAdjustConfigImpl struct {
	// Enable 是否启用权重调整
	Enable *bool `yaml:"enable" json:"enable"`
	// Chain 权重调整插件链
	Chain []string `yaml:"chain" json:"chain"`
	// Plugin 权重调整插件配置
	Plugin PluginConfigs `yaml:"plugin" json:"plugin"`
}

// IsEnable 是否启用权重调整
func (w *WeightAdjustConfigImpl) IsEnable() bool {
	if w.Enable == nil {
		return false
	}
	return *w.Enable
}

// SetEnable 设置是否启用权重调整
func (w *WeightAdjustConfigImpl) SetEnable(enable bool) {
	w.Enable = &enable
}

// GetChain 获取权重调整插件链
func (w *WeightAdjustConfigImpl) GetChain() []string {
	return w.Chain
}

// SetChain 设置权重调整插件链
func (w *WeightAdjustConfigImpl) SetChain(chain []string) {
	w.Chain = chain
}

// GetPluginConfig 获取插件配置
func (w *WeightAdjustConfigImpl) GetPluginConfig(pluginName string) BaseConfig {
	return w.Plugin.GetPluginConfig(pluginName)
}

// SetPluginConfig 设置插件配置
func (w *WeightAdjustConfigImpl) SetPluginConfig(plugName string, value BaseConfig) error {
	return w.Plugin.SetPluginConfig(common.TypeWeightAdjuster, plugName, value)
}

// Verify 校验配置
func (w *WeightAdjustConfigImpl) Verify() error {
	if w == nil {
		return errors.New("WeightAdjustConfig is nil")
	}
	return nil
}

// SetDefault 设置默认值
func (w *WeightAdjustConfigImpl) SetDefault() {
	if w.Enable == nil {
		enable := DefaultWeightAdjustEnabled
		w.Enable = &enable
	}
	if len(w.Chain) == 0 && w.IsEnable() {
		w.Chain = []string{DefaultWeightAdjuster}
	}
	if w.Plugin == nil {
		w.Plugin = make(PluginConfigs)
	}
}

// Init 初始化
func (w *WeightAdjustConfigImpl) Init() {
	w.Plugin = make(PluginConfigs)
}
