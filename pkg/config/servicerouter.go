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
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/hashicorp/go-multierror"
)

//服务路由配置
type ServiceRouterConfigImpl struct {
	//服务路由责任链
	Chain []string `yaml:"chain" json:"chain"`
	// 插件相关配置
	Plugin PluginConfigs `yaml:"plugin" json:"plugin"`
	// 进行过滤时的最大过滤比例
	PercentOfMinInstances *float64 `yaml:"percentOfMinInstances" json:"percentOfMinInstances"`
	//是否启用全死全活机制
	EnableRecoverAll *bool `yaml:"enableRecoverAll" json:"enableRecoverAll"`
}

//获取就近路由配置
func (s *ServiceRouterConfigImpl) GetNearbyConfig() NearbyConfig {
	s.SetDefault()
	cfgValue, ok := s.Plugin[DefaultServiceRouterNearbyBased]
	if !ok {
		return nil
	}
	return cfgValue.(NearbyConfig)
}

//consumer.serviceRouter.filterChain
// 路由责任链配置
func (s *ServiceRouterConfigImpl) GetChain() []string {
	return s.Chain
}

// 设置路由责任链配置
func (s *ServiceRouterConfigImpl) SetChain(chain []string) {
	s.Chain = chain
}

//GetPluginConfig consumer.serviceRouter.plugin
func (s *ServiceRouterConfigImpl) GetPluginConfig(pluginName string) BaseConfig {
	cfgValue, ok := s.Plugin[pluginName]
	if !ok {
		return nil
	}
	return cfgValue.(BaseConfig)
}

//输出插件具体配置
func (s *ServiceRouterConfigImpl) SetPluginConfig(pluginName string, value BaseConfig) error {
	return s.Plugin.SetPluginConfig(common.TypeServiceRouter, pluginName, value)
}

//获取PercentOfMinInstances参数
func (s *ServiceRouterConfigImpl) GetPercentOfMinInstances() float64 {
	return *(s.PercentOfMinInstances)
}

//设置PercentOfMinInstances参数
func (s *ServiceRouterConfigImpl) SetPercentOfMinInstances(percent float64) {
	s.PercentOfMinInstances = &percent
}

//是否启用全死全活机制
func (s *ServiceRouterConfigImpl) IsEnableRecoverAll() bool {
	return *(s.EnableRecoverAll)
}

//设置启用全死全活机制
func (s *ServiceRouterConfigImpl) SetEnableRecoverAll(recoverAll bool) {
	s.EnableRecoverAll = &recoverAll
}

//检验ServiceRouterConfig配置
func (s *ServiceRouterConfigImpl) Verify() error {
	if nil == s {
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

//设置ServiceRouterConfig配置的默认值
func (s *ServiceRouterConfigImpl) SetDefault() {
	if len(s.Chain) == 0 {
		s.Chain = append(s.Chain, DefaultServiceRouterRuleBased)
		s.Chain = append(s.Chain, DefaultServiceRouterNearbyBased)
	}
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

//配置初始化
func (s *ServiceRouterConfigImpl) Init() {
	s.Plugin = PluginConfigs{}
	s.Plugin.Init(common.TypeServiceRouter)
}

//获取该域下所有插件的名字
func (s *ServiceRouterConfigImpl) GetPluginNames() model.HashSet {
	names := model.HashSet{}
	for _, name := range s.Chain {
		names.Add(name)
	}
	names.Add(DefaultServiceRouterFilterOnly)
	for _, v := range DefaultServerServiceRouterChain {
		names.Add(v)
	}
	// 支持兼容 TRPC 路由插件
	names.Add(DefaultServiceRouterDstMeta)
	names.Add(DefaultServiceRouterSetDivision)
	return names
}

////为不同的路由插件创建属于其自身的空配置
//func (s *ServiceRouterConfigImpl) CreateDefaultPluginCfg() {
//	if nil == s.Plugin {
//		s.Plugin = make(map[string]map[string]interface{})
//	}
//	createEmptyCfgForChainedPlug(s.Chain, s.Plugin)
//}
