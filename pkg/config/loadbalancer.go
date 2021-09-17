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

//负载均衡配置
type LoadBalancerConfigImpl struct {
	//负载均衡类型
	Type string `yaml:"type" json:"type"`
	// 插件相关配置
	Plugin PluginConfigs `yaml:"plugin" json:"plugin"`
}

//负载均衡类型
func (l *LoadBalancerConfigImpl) GetType() string {
	return l.Type
}

//设置负载均衡类型
func (l *LoadBalancerConfigImpl) SetType(typ string) {
	l.Type = typ
}

//GetPluginConfig consumer.loadbalancer.plugin
func (l *LoadBalancerConfigImpl) GetPluginConfig(pluginName string) BaseConfig {
	cfgValue, ok := l.Plugin[pluginName]
	if !ok {
		return nil
	}
	return cfgValue.(BaseConfig)
}

//输出插件具体配置
func (l *LoadBalancerConfigImpl) SetPluginConfig(pluginName string, value BaseConfig) error {
	return l.Plugin.SetPluginConfig(common.TypeLoadBalancer, pluginName, value)
}

//检验LocalCacheConfig配置
func (l *LoadBalancerConfigImpl) Verify() error {
	return l.Plugin.Verify()
}

//设置LocalCacheConfig配置的默认值
func (l *LoadBalancerConfigImpl) SetDefault() {
	if len(l.Type) == 0 {
		l.Type = DefaultLoadBalancerWR
	}
	l.Plugin.SetDefault(common.TypeLoadBalancer)
}

//负载均衡配置初始化
func (l *LoadBalancerConfigImpl) Init() {
	l.Plugin = PluginConfigs{}
	l.Plugin.Init(common.TypeLoadBalancer)
}