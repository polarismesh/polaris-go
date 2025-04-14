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
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

type EventReporterConfigImpl struct {
	// 是否启动上报
	Enable *bool `yaml:"enable" json:"enable"`
	// 上报插件链
	Chain []string `yaml:"chain" json:"chain"`
	// 插件相关配置
	Plugin PluginConfigs `yaml:"plugin" json:"plugin"`
}

func (e *EventReporterConfigImpl) IsEnable() bool {
	return *e.Enable
}

func (e *EventReporterConfigImpl) SetEnable(enable bool) {
	e.Enable = &enable
}

func (e *EventReporterConfigImpl) GetChain() []string {
	return e.Chain
}

func (e *EventReporterConfigImpl) SetChain(chain []string) {
	e.Chain = chain
}

func (e *EventReporterConfigImpl) GetPluginConfig(name string) BaseConfig {
	value, ok := e.Plugin[name]
	if !ok {
		return nil
	}
	return value.(BaseConfig)
}

func (e *EventReporterConfigImpl) Verify() error {
	if e.IsEnable() {
		return e.Plugin.Verify()
	}

	return nil
}

func (e *EventReporterConfigImpl) SetDefault() {
	if nil == e.Enable {
		enable := DefaultEventReporterEnabled
		e.Enable = &enable
		return
	}

	e.Plugin.SetDefault(common.TypeEventReporter)
}

func (e *EventReporterConfigImpl) SetPluginConfig(plugName string, value BaseConfig) error {
	return e.Plugin.SetPluginConfig(common.TypeEventReporter, plugName, value)
}

// Init 配置初始化.
func (e *EventReporterConfigImpl) Init() {
	e.Plugin = PluginConfigs{}
	e.Plugin.Init(common.TypeEventReporter)
}
