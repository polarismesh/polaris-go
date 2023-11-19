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
	"time"

	"github.com/hashicorp/go-multierror"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

// LocalCacheConfigImpl 本地缓存配置.
type LocalCacheConfigImpl struct {
	// consumer.localCache.service.expireTime,
	// 服务的超时淘汰时间
	ServiceExpireTime *time.Duration `yaml:"serviceExpireTime" json:"serviceExpireTime"`
	// consumer.localCache.service.refreshInterval
	// 服务的定期刷新时间
	ServiceRefreshInterval *time.Duration `yaml:"serviceRefreshInterval" json:"serviceRefreshInterval"`
	// consumer.localCache.persistDir
	// 本地缓存持久化路径
	PersistDir string `yaml:"persistDir" json:"persistDir"`
	// consumer.localCache.type
	// 本地缓存类型，默认inmemory，可修改成具体的缓存插件名
	Type string `yaml:"type" json:"type"`
	// 是否启用本地缓存
	PersistEnable *bool `yaml:"persistEnable" json:"persistEnable"`
	// consumer.localCache.persistMaxWriteRetry
	PersistMaxWriteRetry int `yaml:"persistMaxWriteRetry" json:"persistMaxWriteRetry"`
	// consumer.localCache.persistReadRetry
	PersistMaxReadRetry int `yaml:"persistMaxReadRetry" json:"persistMaxReadRetry"`
	// consumer.localCache.persistRetryInterval
	PersistRetryInterval *time.Duration `yaml:"persistRetryInterval" json:"persistRetryInterval"`
	// 缓存文件有效时间差值
	PersistAvailableInterval *time.Duration `yaml:"persistAvailableInterval" json:"persistAvailableInterval"`
	// 启动后，首次名字服务是否可以使用缓存文件
	StartUseFileCache *bool `yaml:"startUseFileCache" json:"startUseFileCache"`
	// PushEmptyProtection 推空保护开关
	PushEmptyProtection *bool `yaml:"pushEmptyProtection" json:"pushEmptyProtection"`
	// 插件相关配置
	Plugin PluginConfigs `yaml:"plugin" json:"plugin"`
}

var (
	// DefaultUseFileCacheFlag 默认启动后，首次名字服务是否可以使用缓存文件
	DefaultUseFileCacheFlag = true
	// DefaultPushEmptyProtection 推空保护默认关闭
	DefaultPushEmptyProtection = false
)

// GetServiceExpireTime consumer.localCache.service.expireTime,
// 服务的超时淘汰时间.
func (l *LocalCacheConfigImpl) GetServiceExpireTime() time.Duration {
	return *l.ServiceExpireTime
}

// SetServiceExpireTime 设置服务超时淘汰时间.
func (l *LocalCacheConfigImpl) SetServiceExpireTime(expireTime time.Duration) {
	l.ServiceExpireTime = &expireTime
}

// GetServiceRefreshInterval consumer.localCache.service.refreshInterval
// 服务的定期刷新间隔.
func (l *LocalCacheConfigImpl) GetServiceRefreshInterval() time.Duration {
	return *l.ServiceRefreshInterval
}

// SetServiceRefreshInterval 设置服务定时刷新间隔.
func (l *LocalCacheConfigImpl) SetServiceRefreshInterval(interval time.Duration) {
	l.ServiceRefreshInterval = &interval
}

// IsPersistEnable consumer.localCache.persistEnable
// 是否启用本地缓存
func (l *LocalCacheConfigImpl) IsPersistEnable() bool {
	return *l.PersistEnable
}

// SetPersistEnable 设置是否启用本地缓存
func (l *LocalCacheConfigImpl) SetPersistEnable(enable bool) {
	l.PersistEnable = &enable
}

// GetPersistDir consumer.localCache.persist.path
// 本地缓存持久化路径.
func (l *LocalCacheConfigImpl) GetPersistDir() string {
	return l.PersistDir
}

// SetPersistDir 设置本地缓存持久化路径.
func (l *LocalCacheConfigImpl) SetPersistDir(dir string) {
	l.PersistDir = dir
}

// GetPersistMaxWriteRetry consumer.localCache.persist.maxWriteRetry.
func (l *LocalCacheConfigImpl) GetPersistMaxWriteRetry() int {
	return l.PersistMaxWriteRetry
}

// SetPersistMaxWriteRetry 设置本地缓存持久化写入失败重试次数.
func (l *LocalCacheConfigImpl) SetPersistMaxWriteRetry(maxWriteRetry int) {
	l.PersistMaxWriteRetry = maxWriteRetry
}

// GetPersistMaxReadRetry consumer.localCache.persist.maxReadRetry.
func (l *LocalCacheConfigImpl) GetPersistMaxReadRetry() int {
	return l.PersistMaxReadRetry
}

// SetPersistMaxReadRetry 设置本地缓存持久化读取失败重试次数.
func (l *LocalCacheConfigImpl) SetPersistMaxReadRetry(maxReadRetry int) {
	l.PersistMaxReadRetry = maxReadRetry
}

// GetPersistRetryInterval consumer.localCache.persist.retryInterval.
func (l *LocalCacheConfigImpl) GetPersistRetryInterval() time.Duration {
	return *l.PersistRetryInterval
}

// SetPersistRetryInterval 设置本地缓存持久化重试间隔.
func (l *LocalCacheConfigImpl) SetPersistRetryInterval(interval time.Duration) {
	l.PersistRetryInterval = &interval
}

// GetPersistAvailableInterval consumer.localCache.persist.availableInterval.
func (l *LocalCacheConfigImpl) GetPersistAvailableInterval() time.Duration {
	return *l.PersistAvailableInterval
}

// SetPersistAvailableInterval 设置本地缓存持久化文件有效时间差值.
func (l *LocalCacheConfigImpl) SetPersistAvailableInterval(interval time.Duration) {
	l.PersistAvailableInterval = &interval
}

// GetStartUseFileCache 获取是否可以直接使用缓存标签.
func (l *LocalCacheConfigImpl) GetStartUseFileCache() bool {
	return *l.StartUseFileCache
}

// SetStartUseFileCache 设置是否可以直接使用缓存.
func (l *LocalCacheConfigImpl) SetStartUseFileCache(useCacheFile bool) {
	l.StartUseFileCache = &useCacheFile
}

// GetType consumer.localCache.type
// 本地缓存类型，默认default，可修改成具体的缓存插件名.
func (l *LocalCacheConfigImpl) GetType() string {
	return l.Type
}

// SetType 设置本地缓存类型.
func (l *LocalCacheConfigImpl) SetType(typ string) {
	l.Type = typ
}

// SetPushEmptyProtection 设置推空保护开关
func (l *LocalCacheConfigImpl) SetPushEmptyProtection(pushEmptyProtection bool) {
	l.PushEmptyProtection = &pushEmptyProtection
}

// GetPushEmptyProtection 获取推空保护开关
func (l *LocalCacheConfigImpl) GetPushEmptyProtection() bool {
	return *l.PushEmptyProtection
}

// GetPluginConfig consumer.localCache.plugin.
func (l *LocalCacheConfigImpl) GetPluginConfig(pluginName string) BaseConfig {
	cfgValue, ok := l.Plugin[pluginName]
	if !ok {
		return nil
	}
	return cfgValue.(BaseConfig)
}

// SetPluginConfig 输出插件具体配置.
func (l *LocalCacheConfigImpl) SetPluginConfig(pluginName string, value BaseConfig) error {
	return l.Plugin.SetPluginConfig(common.TypeLocalRegistry, pluginName, value)
}

// Verify 检验LocalCacheConfig配置.
func (l *LocalCacheConfigImpl) Verify() error {
	if nil == l {
		return errors.New("LocalCacheConfig is nil")
	}
	var errs error
	if l.ServiceExpireTime.Nanoseconds() < DefaultMinServiceExpireTime.Nanoseconds() {
		errs = multierror.Append(errs, fmt.Errorf("consumer.localCache.serviceExpireTime %v"+
			" is less than the minimal allowed duration %v", l.ServiceExpireTime, DefaultMinServiceExpireTime))
	}
	plugErr := l.Plugin.Verify()
	if nil != plugErr {
		errs = multierror.Append(errs, plugErr)
	}
	return errs
}

// SetDefault 设置LocalCacheConfig配置的默认值.
func (l *LocalCacheConfigImpl) SetDefault() {
	if nil == l.ServiceExpireTime {
		l.ServiceExpireTime = model.ToDurationPtr(DefaultServiceExpireTime)
	}
	if nil == l.ServiceRefreshInterval {
		l.ServiceRefreshInterval = model.ToDurationPtr(DefaultServiceRefreshIntervalDuration)
	}
	if nil == l.PersistEnable {
		l.PersistEnable = model.ToBoolPtr(DefaultCachePersistEnable)
	}
	if len(l.PersistDir) == 0 {
		l.PersistDir = DefaultCachePersistDir
	}
	if len(l.Type) == 0 {
		l.Type = DefaultLocalCache
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
	if nil == l.PersistAvailableInterval {
		l.PersistAvailableInterval = model.ToDurationPtr(DefaultPersistAvailableInterval)
	}
	if nil == l.StartUseFileCache {
		l.StartUseFileCache = model.ToBoolPtr(DefaultUseFileCacheFlag)
	}
	if nil == l.PushEmptyProtection {
		l.PushEmptyProtection = &DefaultPushEmptyProtection
	}
	l.Plugin.SetDefault(common.TypeLocalRegistry)
}

// Init localche配置初始化.
func (l *LocalCacheConfigImpl) Init() {
	l.Plugin = PluginConfigs{}
	l.Plugin.Init(common.TypeLocalRegistry)
}
