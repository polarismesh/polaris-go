/**
 * Tencent is pleased to support the open source community by making CL5 available.
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
	"time"
)

//OutlierDetectionConfig实现类
type OutlierDetectionConfigImpl struct {
	//是否启动熔断
	Enable *bool `yaml:"enable" json:"enable"`
	//定时探测周期
	CheckPeriod *time.Duration `yaml:"checkPeriod" json:"checkPeriod"`
	//熔断插件链
	Chain []string `yaml:"chain" json:"chain"`
	// 插件相关配置
	Plugin PluginConfigs `yaml:"plugin" json:"plugin"`
}

//是否启用熔断
func (o *OutlierDetectionConfigImpl) IsEnable() bool {
	return *o.Enable
}

//设置是否启用熔断
func (o *OutlierDetectionConfigImpl) SetEnable(enable bool) {
	o.Enable = &enable
}

//熔断器插件链
func (o *OutlierDetectionConfigImpl) GetChain() []string {
	return o.Chain
}

//设置熔断器插件链
func (o *OutlierDetectionConfigImpl) SetChain(chain []string) {
	o.Chain = chain
}

//熔断器定时检测时间
func (o *OutlierDetectionConfigImpl) GetCheckPeriod() time.Duration {
	return *o.CheckPeriod
}

//设置熔断器定时检测时间
func (o *OutlierDetectionConfigImpl) SetCheckPeriod(period time.Duration) {
	o.CheckPeriod = &period
}

//获取插件配置
func (o *OutlierDetectionConfigImpl) GetPluginConfig(pluginName string) BaseConfig {
	cfgValue, ok := o.Plugin[pluginName]
	if !ok {
		return nil
	}
	return cfgValue.(BaseConfig)
}

//设置单独插件配置
func (o *OutlierDetectionConfigImpl) SetPluginConfig(pluginName string, value BaseConfig) error {
	return o.Plugin.SetPluginConfig(common.TypeOutlierDetector, pluginName, value)
}

//检验outlierDetectionConfig配置
func (o *OutlierDetectionConfigImpl) Verify() error {
	if nil == o {
		return errors.New("OutlierDetectionConfig is nil")
	}
	if nil != o.CheckPeriod && *o.CheckPeriod < MinOutlierDetectPeriod {
		return fmt.Errorf("consumer.outlierDetection.checkPeriod should greater than %v",
			MinOutlierDetectPeriod)
	}
	if nil == o.Enable {
		return fmt.Errorf("consumer.outlierDetection.enable must not be nil")
	}
	if *o.Enable && len(o.Chain) == 0 {
		return fmt.Errorf("consumer.outlierDetection.chain can not be empty when enabled")
	}
	return o.Plugin.Verify()
}

//配置初始化
func (o *OutlierDetectionConfigImpl) Init() {
	o.Plugin = PluginConfigs{}
	o.Plugin.Init(common.TypeOutlierDetector)
}

//设置outlierDetectionConfig的默认值
func (o *OutlierDetectionConfigImpl) SetDefault() {
	if nil == o.CheckPeriod {
		o.CheckPeriod = model.ToDurationPtr(DefaultOutlierDetectPeriod)
	}
	if nil == o.Enable {
		enable := DefaultOutlierDetectEnabled
		o.Enable = &enable
	}
	o.Plugin.SetDefault(common.TypeOutlierDetector)
}

//获取该域下所有插件的名字
func (o *OutlierDetectionConfigImpl) GetPluginNames() model.HashSet {
	nameMap := model.HashSet{}
	for _, name := range o.Chain {
		nameMap.Add(name)
	}
	return nameMap
}
