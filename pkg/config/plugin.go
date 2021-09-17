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
	"fmt"
	"github.com/hashicorp/go-multierror"
	"github.com/modern-go/reflect2"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"gopkg.in/yaml.v2"
	"reflect"
	"time"
)

var (
	//插件配置类型
	pluginConfigTypes = make(map[common.Type]map[string]reflect.Type)
)

//RegisterPlugin 注册插件到全局配置对象，并注册插件配置类型
func RegisterPluginConfigType(typ common.Type, name string, cfg BaseConfig) {
	if reflect2.IsNil(cfg) {
		return
	}
	cfgTypes, exists := pluginConfigTypes[typ]
	if !exists {
		cfgTypes = make(map[string]reflect.Type)
		pluginConfigTypes[typ] = cfgTypes
	}
	cfgTypes[name] = reflect.TypeOf(cfg).Elem()
}

//连续错误数熔断配置
type ErrorCountConfig interface {
	//连续错误数阈值
	GetContinuousErrorThreshold() int
	//设置连续错误数阈值
	SetContinuousErrorThreshold(int)
	//连续错误数统计时间窗口
	GetMetricStatTimeWindow() time.Duration
	//设置连续错误数统计时间窗口
	SetMetricStatTimeWindow(time.Duration)
	//连续错误数统计滑桶数量
	GetMetricNumBuckets() int
	//设置连续错误数统计滑桶数量
	SetMetricNumBuckets(int)
	//获取单个滑桶的时间间隔
	GetBucketInterval() time.Duration
}

//错误率熔断配置
type ErrorRateConfig interface {
	//触发错误率熔断的请求量阈值
	GetRequestVolumeThreshold() int
	//设置触发错误率熔断的请求量阈值
	SetRequestVolumeThreshold(int)
	//触发熔断的错误率阈值，取值范围(0, 100]
	GetErrorRatePercent() int
	//设置错误率阈值
	SetErrorRatePercent(int)
	//错误率统计时间窗口
	GetMetricStatTimeWindow() time.Duration
	//设置错误率统计时间窗口
	SetMetricStatTimeWindow(time.Duration)
	//统计窗口细分的桶数量
	GetMetricNumBuckets() int
	//设置统计窗口细分的桶数量
	SetMetricNumBuckets(int)
	//获取单个滑桶的时间间隔
	GetBucketInterval() time.Duration
}

//插件配置实现类
type PluginConfigs map[string]interface{}

//获取插件配置类型，返回插件名到类型的映射
func getPluginConfigTypes(typ common.Type) map[string]reflect.Type {
	plugs, exists := pluginConfigTypes[typ]
	if !exists {
		return nil
	}
	return plugs
}

//获取插件配置类型，具体插件类型
func getPluginConfigType(typ common.Type, name string) (reflect.Type, bool) {
	plugs, exists := pluginConfigTypes[typ]
	if !exists {
		return nil, false
	}
	if plugCfg, ok := plugs[name]; ok {
		return plugCfg, ok
	}
	return nil, false
}

//初始化配置对象
func (p PluginConfigs) Init(typ common.Type) {
	cfgTypes := getPluginConfigTypes(typ)
	for name, cfgType := range cfgTypes {
		value := reflect.New(cfgType).Interface()
		p[name] = value
	}
}

//对于从yaml/json加载的结构，做一个转换
func convertFromTextValues(cfgType reflect.Type, cfgValue interface{}) BaseConfig {
	//如果在配置文件中没有相关内容，直接创建一个配置对象返回即可
	if reflect2.IsNil(cfgValue) {
		return reflect.New(cfgType).Interface().(BaseConfig)
	}
	var jsonValues map[interface{}]interface{}
	var ok bool
	if jsonValues, ok = cfgValue.(map[interface{}]interface{}); !ok {
		return cfgValue.(BaseConfig)
	}
	configValue := reflect.New(cfgType).Interface()
	buf, _ := yaml.Marshal(jsonValues)
	_ = yaml.Unmarshal(buf, configValue)
	return configValue.(BaseConfig)
}

//设置默认值
func (p PluginConfigs) SetDefault(typ common.Type) {
	for plugName := range p {
		//如果不是注册进来的配置项，从PluginConfigs里面删除掉
		if _, exists := getPluginConfigType(typ, plugName); !exists {
			delete(p, plugName)
			continue
		}
	}
	configTypes := getPluginConfigTypes(typ)
	if configTypes == nil {
		return
	}
	for pluginName, configType := range configTypes {
		cfg := convertFromTextValues(configType, p[pluginName])
		cfg.SetDefault()
		p[pluginName] = cfg
	}
}

//校验插件配置
func (p PluginConfigs) Verify() error {
	for name, cfgValue := range p {
		cfg, ok := cfgValue.(BaseConfig)
		if !ok {
			continue
		}
		//检验插件配置
		if err := cfg.Verify(); nil != err {
			return fmt.Errorf("fail to verify plugin %s config, err is %v", name, err)
		}
	}
	return nil
}

// SetPluginConfig 设置单独一个插件的值
func (p PluginConfigs) SetPluginConfig(plugType common.Type, plugName string, value BaseConfig) error {
	configType, exists := getPluginConfigType(plugType, plugName)
	if !exists {
		return fmt.Errorf("config not registered for plugin(type %s, name %s)", plugType, plugName)
	}
	valueType := reflect.TypeOf(value).Elem()
	if valueType != configType {
		return fmt.Errorf("type %s not match config type %s for plugin(type %s, name %s)",
			valueType, configType, plugType, plugName)
	}
	value.SetDefault()
	err := value.Verify()
	if err != nil {
		return multierror.Prefix(err, "Fail to verify config value of "+plugType.String()+":"+plugName+": ")
	}
	p[plugName] = value
	return nil
}

// GetPluginConfig 根据插件名获取配置
func (p PluginConfigs) GetPluginConfig(pluginName string) BaseConfig {
	cfg, ok := p[pluginName]
	if !ok {
		return nil
	}
	return cfg.(BaseConfig)
}
