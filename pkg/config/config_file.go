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
	"path/filepath"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/hashicorp/go-multierror"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// DefaultConfigFileEnable 默认打开配置中心能力
var DefaultConfigFileEnable = true

// ConfigFileConfigImpl 对接配置中心相关配置.
type ConfigFileConfigImpl struct {
	LocalCache            *ConfigLocalCacheConfigImpl `yaml:"localCache" json:"localCache"`
	ConfigConnectorConfig *ConfigConnectorConfigImpl  `yaml:"configConnector" json:"configConnector"`
	ConfigFilterConfig    *ConfigFilterConfigImpl     `yaml:"configFilter" json:"configFilter"`
	// 是否启动配置中心
	Enable                    *bool  `yaml:"enable" json:"enable"`
	PropertiesValueCacheSize  *int32 `yaml:"propertiesValueCacheSize" json:"propertiesValueCacheSize"`
	PropertiesValueExpireTime *int64 `yaml:"propertiesValueExpireTime" json:"propertiesValueExpireTime"`
}

// GetConfigConnectorConfig config.configConnector前缀开头的所有配置项.
func (c *ConfigFileConfigImpl) GetConfigConnectorConfig() ConfigConnectorConfig {
	return c.ConfigConnectorConfig
}

// GetConfigFilterConfig config.configFilter前缀开头的所有配置项.
func (c *ConfigFileConfigImpl) GetConfigFilterConfig() ConfigFilterConfig {
	return c.ConfigFilterConfig
}

// IsEnable config.enable.
func (c *ConfigFileConfigImpl) IsEnable() bool {
	return *c.Enable
}

// SetEnable 设置是否开启配置中心功能.
func (c *ConfigFileConfigImpl) SetEnable(enable bool) {
	c.Enable = &enable
}

// GetPropertiesValueCacheSize config.propertiesValueCacheSize.
func (c *ConfigFileConfigImpl) GetPropertiesValueCacheSize() int32 {
	return *c.PropertiesValueCacheSize
}

// SetPropertiesValueCacheSize 设置类型转化缓存的key数量.
func (c *ConfigFileConfigImpl) SetPropertiesValueCacheSize(propertiesValueCacheSize int32) {
	c.PropertiesValueCacheSize = &propertiesValueCacheSize
}

// GetPropertiesValueExpireTime config.propertiesValueExpireTime.
func (c *ConfigFileConfigImpl) GetPropertiesValueExpireTime() int64 {
	return *c.PropertiesValueExpireTime
}

// SetPropertiesValueExpireTime 设置类型转化缓存的过期时间，默认为1分钟.
func (c *ConfigFileConfigImpl) SetPropertiesValueExpireTime(propertiesValueExpireTime int64) {
	c.PropertiesValueExpireTime = &propertiesValueExpireTime
}

// GetLocalCache .
func (c *ConfigFileConfigImpl) GetLocalCache() ConfigLocalCacheConfig {
	return c.LocalCache
}

// Verify 检验ConfigConnector配置.
func (c *ConfigFileConfigImpl) Verify() error {
	if c == nil {
		return errors.New("ConfigFileConfig is nil")
	}
	var errs error
	if err := c.ConfigConnectorConfig.Verify(); err != nil {
		errs = multierror.Append(errs, err)
	}
	if err := c.ConfigFilterConfig.Verify(); err != nil {
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

// SetDefault 设置ConfigConnector配置的默认值.
func (c *ConfigFileConfigImpl) SetDefault() {
	c.ConfigConnectorConfig.SetDefault()
	c.ConfigFilterConfig.SetDefault()
	c.LocalCache.SetDefault()
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

// Init 配置初始化.
func (c *ConfigFileConfigImpl) Init() {
	c.ConfigConnectorConfig = &ConfigConnectorConfigImpl{}
	c.ConfigConnectorConfig.Init()
	c.ConfigFilterConfig = &ConfigFilterConfigImpl{}
	c.ConfigFilterConfig.Init()
	c.LocalCache = &ConfigLocalCacheConfigImpl{}
	c.LocalCache.Init()
}

// ConfigLocalCacheConfigImpl 本地缓存配置.
type ConfigLocalCacheConfigImpl struct {
	// config.localCache.persistDir
	// 本地缓存持久化路径
	PersistDir string `yaml:"persistDir" json:"persistDir"`
	// 是否启用本地缓存
	PersistEnable *bool `yaml:"persistEnable" json:"persistEnable"`
	// config.localCache.persistMaxWriteRetry
	PersistMaxWriteRetry int `yaml:"persistMaxWriteRetry" json:"persistMaxWriteRetry"`
	// config.localCache.persistReadRetry
	PersistMaxReadRetry int `yaml:"persistMaxReadRetry" json:"persistMaxReadRetry"`
	// config.localCache.persistRetryInterval
	PersistRetryInterval *time.Duration `yaml:"persistRetryInterval" json:"persistRetryInterval"`
	// config.localCache.fallbackToLocalCache
	FallbackToLocalCache *bool `yaml:"fallbackToLocalCache" json:"fallbackToLocalCache"`
}

// IsPersistEnable consumer.localCache.persistEnable
// 是否启用本地缓存
func (l *ConfigLocalCacheConfigImpl) IsPersistEnable() bool {
	if l.PersistEnable == nil {
		return true
	}
	return *l.PersistEnable
}

// SetPersistEnable 设置是否启用本地缓存
func (l *ConfigLocalCacheConfigImpl) SetPersistEnable(enable bool) {
	l.PersistEnable = &enable
}

// GetPersistDir consumer.localCache.persist.path
// 本地缓存持久化路径.
func (l *ConfigLocalCacheConfigImpl) GetPersistDir() string {
	return l.PersistDir
}

// SetPersistDir 设置本地缓存持久化路径.
func (l *ConfigLocalCacheConfigImpl) SetPersistDir(dir string) {
	l.PersistDir = dir
}

// GetPersistMaxWriteRetry consumer.localCache.persist.maxWriteRetry.
func (l *ConfigLocalCacheConfigImpl) GetPersistMaxWriteRetry() int {
	return l.PersistMaxWriteRetry
}

// SetPersistMaxWriteRetry 设置本地缓存持久化写入失败重试次数.
func (l *ConfigLocalCacheConfigImpl) SetPersistMaxWriteRetry(maxWriteRetry int) {
	l.PersistMaxWriteRetry = maxWriteRetry
}

// GetPersistMaxReadRetry consumer.localCache.persist.maxReadRetry.
func (l *ConfigLocalCacheConfigImpl) GetPersistMaxReadRetry() int {
	return l.PersistMaxReadRetry
}

// SetPersistMaxReadRetry 设置本地缓存持久化读取失败重试次数.
func (l *ConfigLocalCacheConfigImpl) SetPersistMaxReadRetry(maxReadRetry int) {
	l.PersistMaxReadRetry = maxReadRetry
}

// GetPersistRetryInterval consumer.localCache.persist.retryInterval.
func (l *ConfigLocalCacheConfigImpl) GetPersistRetryInterval() time.Duration {
	return *l.PersistRetryInterval
}

// SetPersistRetryInterval 设置本地缓存持久化重试间隔.
func (l *ConfigLocalCacheConfigImpl) SetPersistRetryInterval(interval time.Duration) {
	l.PersistRetryInterval = &interval
}

// SetFallbackToLocalCache .
func (l *ConfigLocalCacheConfigImpl) SetFallbackToLocalCache(enable bool) {
	l.FallbackToLocalCache = &enable
}

// IsFallbackToLocalCache .
func (l *ConfigLocalCacheConfigImpl) IsFallbackToLocalCache() bool {
	if l.FallbackToLocalCache == nil {
		return true
	}
	return *l.FallbackToLocalCache
}

// Verify 检验LocalCacheConfig配置.
func (l *ConfigLocalCacheConfigImpl) Verify() error {
	if nil == l {
		return errors.New("LocalCacheConfig is nil")
	}
	return nil
}

// SetDefault 设置LocalCacheConfig配置的默认值.
func (l *ConfigLocalCacheConfigImpl) SetDefault() {
	if l.PersistEnable == nil {
		l.PersistEnable = model.ToBoolPtr(true)
	}
	if l.FallbackToLocalCache == nil {
		l.FallbackToLocalCache = model.ToBoolPtr(true)
	}
	if len(l.PersistDir) == 0 {
		l.PersistDir = filepath.Join(DefaultCachePersistDir, "config")
	}
	if nil == l.PersistRetryInterval {
		l.PersistRetryInterval = model.ToDurationPtr(DefaultPersistRetryInterval)
	}
	if l.PersistMaxReadRetry == 0 {
		l.PersistMaxReadRetry = DefaultPersistMaxReadRetry
	}
	if l.PersistMaxWriteRetry == 0 {
		l.PersistMaxWriteRetry = DefaultPersistMaxWriteRetry
	}
}

// Init localche配置初始化.
func (l *ConfigLocalCacheConfigImpl) Init() {
}
