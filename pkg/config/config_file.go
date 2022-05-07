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
	"fmt"

	"github.com/golang/protobuf/proto"

	"github.com/hashicorp/go-multierror"
)

var (
	// DefaultConfigFileEnable 默认打开配置中心能力
	DefaultConfigFileEnable = true
)

// 对接配置中心相关配置
type ConfigFileConfigImpl struct {
	ConfigConnectorConfig *ConfigConnectorConfigImpl `yaml:"configConnector" json:"configConnector"`
	// 是否启动配置中心
	Enable                    *bool  `yaml:"enable" json:"enable"`
	PropertiesValueCacheSize  *int32 `yaml:"propertiesValueCacheSize" json:"propertiesValueCacheSize"`
	PropertiesValueExpireTime *int64 `yaml:"propertiesValueExpireTime" json:"propertiesValueExpireTime"`
}

// GetConfigConnectorConfig config.configConnector前缀开头的所有配置项
func (c *ConfigFileConfigImpl) GetConfigConnectorConfig() ConfigConnectorConfig {
	return c.ConfigConnectorConfig
}

// IsEnable config.enable
func (c *ConfigFileConfigImpl) IsEnable() bool {
	return *c.Enable
}

// SetEnable 设置是否开启配置中心功能
func (c *ConfigFileConfigImpl) SetEnable(enable bool) {
	c.Enable = &enable
}

// GetPropertiesValueCacheSize config.propertiesValueCacheSize
func (c *ConfigFileConfigImpl) GetPropertiesValueCacheSize() int32 {
	return *c.PropertiesValueCacheSize
}

// SetPropertiesValueCacheSize 设置类型转化缓存的key数量
func (c *ConfigFileConfigImpl) SetPropertiesValueCacheSize(propertiesValueCacheSize int32) {
	c.PropertiesValueCacheSize = &propertiesValueCacheSize
}

// GetPropertiesValueExpireTime config.propertiesValueExpireTime
func (c *ConfigFileConfigImpl) GetPropertiesValueExpireTime() int64 {
	return *c.PropertiesValueExpireTime
}

// SetPropertiesValueExpireTime 设置类型转化缓存的过期时间，默认为1分钟
func (c *ConfigFileConfigImpl) SetPropertiesValueExpireTime(propertiesValueExpireTime int64) {
	c.PropertiesValueExpireTime = &propertiesValueExpireTime
}

// Verify 检验ConfigConnector配置
func (c *ConfigFileConfigImpl) Verify() error {
	if c == nil {
		return errors.New("ConfigFileConfig is nil")
	}
	var errs error
	if err := c.ConfigConnectorConfig.Verify(); err != nil {
		errs = multierror.Append(errs, err)
	}
	if c.Enable == nil {
		return fmt.Errorf("config.enable must not be nil")
	}
	if c.PropertiesValueCacheSize != nil && *c.PropertiesValueCacheSize < 0 {
		errs = multierror.Append(errs, fmt.Errorf("config.propertiesValueCacheSize %v is invalid", c.PropertiesValueCacheSize))
	}
	if c.PropertiesValueExpireTime != nil && *c.PropertiesValueExpireTime < 0 {
		errs = multierror.Append(errs, fmt.Errorf("config.propertiesValueExpireTime %v is invalid", c.PropertiesValueExpireTime))
	}
	return errs
}

// SetDefault 设置ConfigConnector配置的默认值
func (c *ConfigFileConfigImpl) SetDefault() {
	c.ConfigConnectorConfig.SetDefault()
	if c.Enable == nil {
		c.Enable = &DefaultConfigFileEnable
	}
	if c.PropertiesValueCacheSize == nil {
		c.PropertiesValueCacheSize = proto.Int32(int32(DefaultPropertiesValueCacheSize))
	}
	if c.PropertiesValueCacheSize == nil {
		c.PropertiesValueExpireTime = proto.Int64(int64(DefaultPropertiesValueCacheSize))
	}
}

// Init 配置初始化
func (c *ConfigFileConfigImpl) Init() {
	c.ConfigConnectorConfig = &ConfigConnectorConfigImpl{}
	c.ConfigConnectorConfig.Init()
}
