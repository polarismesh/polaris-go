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
	"github.com/hashicorp/go-multierror"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"time"
)

// 本地缓存配置
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
	// consumer.localCache.persistMaxWriteRetry
	PersistMaxWriteRetry int `yaml:"persistMaxWriteRetry" json:"persistMaxWriteRetry"`
	// consumer.localCache.persistReadRetry
	PersistMaxReadRetry int `yaml:"persistMaxReadRetry" json:"persistMaxReadRetry"`
	// consumer.localCache.persistRetryInterval
	PersistRetryInterval *time.Duration `yaml:"persistRetryInterval" json:"persistRetryInterval"`
	// 缓存文件有效时间差值
	PersistAvailableInterval *time.Duration `yaml:"persistAvailableInterval" json:"persistAvailableInterval"`
	//启动后，首次名字服务是否可以使用缓存文件
	StartUseFileCache *bool `yaml:"startUseFileCache" json:"startUseFileCache"`
	// 插件相关配置
	Plugin PluginConfigs `yaml:"plugin" json:"plugin"`
}

var (
	DefaultUseFileCacheFlag = true
)

//GetServiceExpireTime consumer.localCache.service.expireTime,
// 服务的超时淘汰时间
func (l *LocalCacheConfigImpl) GetServiceExpireTime() time.Duration {
	return *l.ServiceExpireTime
}

// 设置服务超时淘汰时间
func (l *LocalCacheConfigImpl) SetServiceExpireTime(expireTime time.Duration) {
	l.ServiceExpireTime = &expireTime
}

//GetServiceRefreshInterval consumer.localCache.service.refreshInterval
// 服务的定期刷新间隔
func (l *LocalCacheConfigImpl) GetServiceRefreshInterval() time.Duration {
	return *l.ServiceRefreshInterval
}

//设置服务定时刷新间隔
func (l *LocalCacheConfigImpl) SetServiceRefreshInterval(interval time.Duration) {
	l.ServiceRefreshInterval = &interval
}

//GetPersistPath consumer.localCache.persist.path
// 本地缓存持久化路径
func (l *LocalCacheConfigImpl) GetPersistDir() string {
	return l.PersistDir
}

// 设置本地缓存持久化路径
func (l *LocalCacheConfigImpl) SetPersistDir(dir string) {
	l.PersistDir = dir
}

//
func (l *LocalCacheConfigImpl) GetPersistMaxWriteRetry() int {
	return l.PersistMaxWriteRetry
}

//
func (l *LocalCacheConfigImpl) SetPersistMaxWriteRetry(maxWriteRetry int) {
	l.PersistMaxWriteRetry = maxWriteRetry
}

//
func (l *LocalCacheConfigImpl) GetPersistMaxReadRetry() int {
	return l.PersistMaxReadRetry
}

//
func (l *LocalCacheConfigImpl) SetPersistMaxReadRetry(maxReadRetry int) {
	l.PersistMaxReadRetry = maxReadRetry
}

//
func (l *LocalCacheConfigImpl) GetPersistRetryInterval() time.Duration {
	return *l.PersistRetryInterval
}

//
func (l *LocalCacheConfigImpl) SetPersistRetryInterval(interval time.Duration) {
	l.PersistRetryInterval = &interval
}

//GetPersistAvailableInterval
func (l *LocalCacheConfigImpl) GetPersistAvailableInterval() time.Duration {
	return *l.PersistAvailableInterval
}

//SetPersistAvailableInterval
func (l *LocalCacheConfigImpl) SetPersistAvailableInterval(interval time.Duration) {
	l.PersistAvailableInterval = &interval
}

// 获取是否可以直接使用缓存标签
func (l *LocalCacheConfigImpl) GetStartUseFileCache() bool {
	return *l.StartUseFileCache
}

// 设置是否可以直接使用缓存
func (l *LocalCacheConfigImpl) SetStartUseFileCache(useCacheFile bool) {
	l.StartUseFileCache = &useCacheFile
}

//GetType consumer.localCache.type
// 本地缓存类型，默认default，可修改成具体的缓存插件名
func (l *LocalCacheConfigImpl) GetType() string {
	return l.Type
}

// 设置本地缓存类型
func (l *LocalCacheConfigImpl) SetType(typ string) {
	l.Type = typ
}

//GetPluginConfig consumer.localCache.plugin
func (l *LocalCacheConfigImpl) GetPluginConfig(pluginName string) BaseConfig {
	cfgValue, ok := l.Plugin[pluginName]
	if !ok {
		return nil
	}
	return cfgValue.(BaseConfig)
}

//输出插件具体配置
func (l *LocalCacheConfigImpl) SetPluginConfig(pluginName string, value BaseConfig) error {
	return l.Plugin.SetPluginConfig(common.TypeLocalRegistry, pluginName, value)
}

//检验LocalCacheConfig配置
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

//设置LocalCacheConfig配置的默认值
func (l *LocalCacheConfigImpl) SetDefault() {
	if nil == l.ServiceExpireTime {
		l.ServiceExpireTime = model.ToDurationPtr(DefaultServiceExpireTime)
	}
	if nil == l.ServiceRefreshInterval {
		l.ServiceRefreshInterval = model.ToDurationPtr(DefaultServiceRefreshIntervalDuration)
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
	if 0 == l.PersistMaxReadRetry {
		l.PersistMaxReadRetry = DefaultPersistMaxReadRetry
	}
	if 0 == l.PersistMaxWriteRetry {
		l.PersistMaxWriteRetry = DefaultPersistMaxWriteRetry
	}
	if nil == l.PersistAvailableInterval {
		l.PersistAvailableInterval = model.ToDurationPtr(DefaultPersistAvailableInterval)
	}
	if nil == l.StartUseFileCache {
		l.StartUseFileCache = &DefaultUseFileCacheFlag
	}
	l.Plugin.SetDefault(common.TypeLocalRegistry)
}

//localche配置初始化
func (l *LocalCacheConfigImpl) Init() {
	l.Plugin = PluginConfigs{}
	l.Plugin.Init(common.TypeLocalRegistry)
}
