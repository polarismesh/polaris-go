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
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// PluginConfig 插件配置对象.
type PluginConfig interface {
	// GetPluginConfig 获取plugin.<name>下的插件配置，将map[string]interface形式的配置marshal进value中
	GetPluginConfig(pluginName string) BaseConfig
	// SetPluginConfig 设置插件配置，将value的内容unmarshal为map[string]interface{}形式
	SetPluginConfig(plugName string, value BaseConfig) error
}

// GlobalConfig 全局配置对象.
type GlobalConfig interface {
	BaseConfig
	// GetSystem .
	GetSystem() SystemConfig
	// GetAPI global.api前缀开头的所有配置项
	GetAPI() APIConfig
	// GetServerConnector global.serverConnector前缀开头的所有配置项
	GetServerConnector() ServerConnectorConfig
	// GetStatReporter global.statReporter前缀开头的所有配置项
	GetStatReporter() StatReporterConfig
	// GetLocation global.location前缀开头的所有配置项
	GetLocation() LocationConfig
}

// ConsumerConfig consumer config object.
type ConsumerConfig interface {
	BaseConfig
	// GetLocalCache get local cache config
	GetLocalCache() LocalCacheConfig
	// GetServiceRouter get service router config
	GetServiceRouter() ServiceRouterConfig
	// GetLoadbalancer get load balancer config
	GetLoadbalancer() LoadbalancerConfig
	// GetCircuitBreaker get circuit breaker config
	GetCircuitBreaker() CircuitBreakerConfig
	// GetHealthCheck get health check config
	GetHealthCheck() HealthCheckConfig
	// GetServiceSpecific 服务独立配置
	GetServiceSpecific(namespace string, service string) ServiceSpecificConfig
}

// ProviderConfig 被调端配置对象.
type ProviderConfig interface {
	BaseConfig
	// GetRateLimit 获取限流配置
	GetRateLimit() RateLimitConfig
	// GetMinRegisterInterval get minimum interval between two register operation
	GetMinRegisterInterval() time.Duration
}

// ConfigFileConfig 配置中心的配置.
type ConfigFileConfig interface {
	BaseConfig
	// IsEnable 是否启用配置中心
	IsEnable() bool
	// GetConfigConnectorConfig 配置文件连接器
	GetConfigConnectorConfig() ConfigConnectorConfig
	// GetPropertiesValueCacheSize 值缓存的最大数量
	GetPropertiesValueCacheSize() int32
	// GetPropertiesValueExpireTime 缓存的过期时间，默认为 60s
	GetPropertiesValueExpireTime() int64
}

// RateLimitConfig 限流相关配置.
type RateLimitConfig interface {
	BaseConfig
	PluginConfig
	// IsEnable 是否启用限流能力
	IsEnable() bool
	// SetEnable 设置是否启用限流能力
	SetEnable(bool)
	// GetMaxWindowSize 获取最大限流窗口数量
	GetMaxWindowSize() int
	// SetMaxWindowSize 设置最大限流窗口数量
	SetMaxWindowSize(maxSize int)
	// GetPurgeInterval 获取超时淘汰周期
	GetPurgeInterval() time.Duration
	// SetPurgeInterval 设置超时淘汰周期
	SetPurgeInterval(time.Duration)
	// GetLimiterService 获取限流服务
	GetLimiterService() string
	// SetLimiterService 设置限流服务
	SetLimiterService(value string)
	// SetLimiterNamespace 设置限流命名空间
	SetLimiterNamespace(value string)
	// GetLimiterNamespace 获取限流命名空间
	GetLimiterNamespace() string
}

// SystemConfig 系统配置信息.
type SystemConfig interface {
	BaseConfig
	// GetMode global.systemConfig.mode
	// SDK运行模式，agent还是noagent
	GetMode() model.RunMode
	// SetMode 设置SDK运行模式
	SetMode(model.RunMode)
	// GetDiscoverCluster global.systemConfig.discoverCluster
	// 服务发现集群
	GetDiscoverCluster() ServerClusterConfig
	// GetHealthCheckCluster global.systemConfig.healthCheckCluster
	// 健康检查集群
	GetHealthCheckCluster() ServerClusterConfig
	// GetMonitorCluster global.systemConfig.monitorCluster
	// 监控上报集群
	GetMonitorCluster() ServerClusterConfig
	// GetVariable global.systemConfig.variables
	// 获取一个路由环境变量
	GetVariable(key string) (string, bool)
	// SetVariable global.systemConfig.variables
	// 设置一个路由环境变量
	SetVariable(key, value string)
	// UnsetVariable 取消一个路由环境变量
	UnsetVariable(key string)
}

// ServerClusterConfig 单个系统服务集群.
type ServerClusterConfig interface {
	BaseConfig
	// GetNamespace 获取命名空间
	GetNamespace() string
	// SetNamespace 设置命名空间
	SetNamespace(string)
	// GetService 获取服务名
	GetService() string
	// SetService 设置服务名
	SetService(string)
	// GetRefreshInterval 系统服务的刷新间隔
	GetRefreshInterval() time.Duration
	// SetRefreshInterval 设置系统服务的刷新间隔
	SetRefreshInterval(time.Duration)
}

// APIConfig api相关的配置对象.
type APIConfig interface {
	BaseConfig
	// GetTimeout global.api.timeout
	// 默认调用超时时间
	GetTimeout() time.Duration
	// SetTimeout 设置默认调用超时时间
	SetTimeout(time.Duration)
	// GetBindIntf global.api.bindIf
	// 默认客户端绑定的网卡地址
	GetBindIntf() string
	// SetBindIntf 设置默认客户端绑定的网卡地址
	SetBindIntf(string)
	// GetBindIP global.api.bindIP
	// 默认客户端绑定的IP地址
	GetBindIP() string
	// SetBindIP 设置默认客户端绑定的IP地址
	SetBindIP(string)
	// GetReportInterval global.api.reportInterval
	// 默认客户端定时上报周期
	GetReportInterval() time.Duration
	// SetReportInterval 设置默认客户端定时上报周期
	SetReportInterval(time.Duration)
	// GetMaxRetryTimes global.api.maxRetryTimes
	// api调用最多重试时间
	GetMaxRetryTimes() int
	// SetMaxRetryTimes 设置api调用最多重试时间
	SetMaxRetryTimes(int)
	// GetRetryInterval global.api.retryInterval
	// api调用重试时间
	GetRetryInterval() time.Duration
	// SetRetryInterval 设置api调用重试时间
	SetRetryInterval(time.Duration)
}

// StatReporterConfig 统计上报配置.
type StatReporterConfig interface {
	BaseConfig
	PluginConfig
	// IsEnable 是否启用上报
	IsEnable() bool
	// SetEnable 设置是否启用上报
	SetEnable(bool)
	// GetChain 统计上报器插件链
	GetChain() []string
	// SetChain 设置统计上报器插件链
	SetChain([]string)
}

// LocationConfig SDK获取自身当前地理位置配置.
type LocationConfig interface {
	BaseConfig
	// GetProvider 获取地理位置的提供者插件名称
	GetProviders() []*LocationProviderConfigImpl

	GetProvider(typ string) *LocationProviderConfigImpl
}

// ServerConnectorConfig 与名字服务服务端的连接配置.
type ServerConnectorConfig interface {
	BaseConfig
	PluginConfig
	// GetAddresses global.serverConnector.addresses
	// 远端server地址，格式为<host>:<port>
	GetAddresses() []string
	// SetAddresses 设置远端server地址，格式为<host>:<port>
	SetAddresses([]string)
	// GetProtocol global.serverConnector.protocol
	// 与server对接的协议
	GetProtocol() string
	// SetProtocol 设置与server对接的协议
	SetProtocol(string)
	// GetConnectTimeout global.serverConnector.connectTimeout
	// 与server的连接超时时间
	GetConnectTimeout() time.Duration
	// SetConnectTimeout 设置与server的连接超时时间
	SetConnectTimeout(time.Duration)
	// GetMessageTimeout global.registry.messageTimeout
	// 远程请求超时时间
	GetMessageTimeout() time.Duration
	// SetMessageTimeout 设置远程请求超时时间
	SetMessageTimeout(time.Duration)
	// GetRequestQueueSize global.serverConnector.clientRequestQueueSize
	// 新请求的队列BUFFER容量
	GetRequestQueueSize() int32
	// SetRequestQueueSize 设置新请求的队列BUFFER容量
	SetRequestQueueSize(int32)
	// GetServerSwitchInterval global.serverConnector.serverSwitchInterval
	// server的切换时延
	GetServerSwitchInterval() time.Duration
	// SetServerSwitchInterval 设置server的切换时延
	SetServerSwitchInterval(time.Duration)
	// GetReconnectInterval 获取一次连接失败后，到下一次重连的间隔时间
	GetReconnectInterval() time.Duration
	// SetReconnectInterval 设置重试连接的间隔时间
	SetReconnectInterval(time.Duration)
	// GetConnectionIdleTimeout global.serverConnector.connectionIdleTimeout
	// 连接会被释放的空闲的时长
	GetConnectionIdleTimeout() time.Duration
	// SetConnectionIdleTimeout 设置连接会被释放的空闲的时长
	SetConnectionIdleTimeout(time.Duration)
}

// LocalCacheConfig 本地缓存相关配置项.
type LocalCacheConfig interface {
	BaseConfig
	PluginConfig
	// GetServiceExpireTime consumer.localCache.service.expireTime,
	// 服务的超时淘汰时间
	GetServiceExpireTime() time.Duration
	// SetServiceExpireTime 设置服务的超时淘汰时间
	SetServiceExpireTime(time.Duration)
	// GetServiceRefreshInterval consumer.localCache.service.refreshInterval
	// 服务的定期刷新时间
	GetServiceRefreshInterval() time.Duration
	// SetServiceRefreshInterval 设置服务的定期刷新时间
	SetServiceRefreshInterval(time.Duration)
	// IsPersistEnable consumer.localCache.persistEnable
	// 是否启用本地缓存
	IsPersistEnable() bool
	// SetPersistEnable 设置是否启用本地缓存
	SetPersistEnable(enable bool)
	// GetPersistDir consumer.localCache.persistDir
	// 本地缓存持久化路径
	GetPersistDir() string
	// SetPersistDir 设置本地缓存持久化路径
	SetPersistDir(string)
	// GetType consumer.localCache.type
	// 本地缓存类型，默认default，可修改成具体的缓存插件名
	GetType() string
	// SetType 设置本地缓存类型
	SetType(string)
	// GetPersistMaxWriteRetry consumer.localCache.persistMaxWriteRetry
	// 缓存最大写重试次数
	GetPersistMaxWriteRetry() int
	// SetPersistMaxWriteRetry 设置缓存最大写重试次数
	SetPersistMaxWriteRetry(int)
	// GetPersistMaxReadRetry consumer.localCache.persistMaxReadRetry
	// 缓存最大读重试次数
	GetPersistMaxReadRetry() int
	// SetPersistMaxReadRetry 设置缓存最大读重试次数
	SetPersistMaxReadRetry(int)
	// GetPersistRetryInterval consumer.localCache.persistRetryInterval
	// 缓存持久化重试间隔
	GetPersistRetryInterval() time.Duration
	// SetPersistRetryInterval 设置缓存持久化重试间隔
	SetPersistRetryInterval(time.Duration)
	// GetPersistAvailableInterval 获取缓存文件有效时间
	GetPersistAvailableInterval() time.Duration
	// SetPersistAvailableInterval 设置缓存文件有效时间
	SetPersistAvailableInterval(interval time.Duration)
	// GetStartUseFileCache 获取是否可以直接使用缓存标签
	GetStartUseFileCache() bool
	// SetStartUseFileCache 设置是否可以直接使用缓存
	SetStartUseFileCache(useCacheFile bool)
	// SetPushEmptyProtection 设置推空保护开关
	SetPushEmptyProtection(pushEmptyProtection bool)
	// GetPushEmptyProtection 获取推空保护开关
	GetPushEmptyProtection() bool
}

// NearbyConfig 就近路由配置.
type NearbyConfig interface {
	BaseConfig
	// SetMatchLevel 设置匹配级别，consumer.serviceRouter.plugin.nearbyBasedRouter.matchLevel
	SetMatchLevel(level string)
	// GetMatchLevel 获取匹配级别，consumer.serviceRouter.plugin.nearbyBasedRouter.matchLevel
	GetMatchLevel() string
	// SetLowestMatchLevel .
	// Deprecated: 设置可以降级的最低匹配级别, 已废弃，请使用SetMaxMatchLevel
	SetLowestMatchLevel(level string)
	// GetLowestMatchLevel .
	// Deprecated: 获取可以降级的最低匹配级别,已废弃，请使用GetMaxMatchLevel
	GetLowestMatchLevel() string
	// SetMaxMatchLevel 设置可以降级的最低匹配级别,consumer.serviceRouter.plugin.nearbyBasedRouter.maxMatchLevel
	SetMaxMatchLevel(level string)
	// GetMaxMatchLevel 获取可以降级的最低匹配级别,consumer.serviceRouter.plugin.nearbyBasedRouter.maxMatchLevel
	GetMaxMatchLevel() string
	// SetStrictNearby 设置是否进行严格就近,consumer.serviceRouter.plugin.nearbyBasedRouter.strictNearby
	SetStrictNearby(s bool)
	// IsStrictNearby 获取是否进行严格就近,consumer.serviceRouter.plugin.nearbyBasedRouter.strictNearby
	IsStrictNearby() bool
	// IsEnableDegradeByUnhealthyPercent 是否开启根据不健康实例比例进行降级就近匹配,
	// consumer.serviceRouter.plugin.nearbyBasedRouter.enableDegradeByUnhealthyPercent
	IsEnableDegradeByUnhealthyPercent() bool
	// SetEnableDegradeByUnhealthyPercent 设置是否开启根据不健康实例比例进行降级就近匹配
	// consumer.serviceRouter.plugin.nearbyBasedRouter.enableDegradeByUnhealthyPercent
	SetEnableDegradeByUnhealthyPercent(e bool)
	// GetUnhealthyPercentToDegrade 获取触发降级匹配的不健康实例比例,
	// consumer.serviceRouter.plugin.nearbyBasedRouter.unhealthyPercentToDegrade
	GetUnhealthyPercentToDegrade() int
	// SetUnhealthyPercentToDegrade 设置触发降级匹配的不健康实例比例,consumer.serviceRouter.plugin.nearbyBasedRouter.unhealthyPercentToDegrade
	SetUnhealthyPercentToDegrade(u int)
}

// ServiceRouterConfig 服务路由相关配置项.
type ServiceRouterConfig interface {
	BaseConfig
	PluginConfig
	// GetChain consumer.serviceRouter.chain
	// 路由责任链配置
	GetChain() []string
	// SetChain 设置路由责任链配置
	SetChain([]string)
	// GetPercentOfMinInstances 获取PercentOfMinInstances参数
	GetPercentOfMinInstances() float64
	// SetPercentOfMinInstances 设置PercentOfMinInstances参数
	SetPercentOfMinInstances(float64)
	// IsEnableRecoverAll 是否启用全死全活机制
	IsEnableRecoverAll() bool
	// SetEnableRecoverAll 设置启用全死全活机制
	SetEnableRecoverAll(bool)
	// GetNearbyConfig 获取就近路由配置
	GetNearbyConfig() NearbyConfig
}

// LoadbalancerConfig 负载均衡相关配置项.
type LoadbalancerConfig interface {
	BaseConfig
	PluginConfig
	// GetType 负载均衡类型
	GetType() string
	// SetType 设置负载均衡类型
	SetType(string)
}

// CircuitBreakerConfig 熔断相关的配置项.
type CircuitBreakerConfig interface {
	BaseConfig
	PluginConfig
	// IsEnable 是否启用熔断
	IsEnable() bool
	// SetEnable 设置是否启用熔断
	SetEnable(bool)
	// GetChain 熔断器插件链
	GetChain() []string
	// SetChain 设置熔断器插件链
	SetChain([]string)
	// GetCheckPeriod 熔断器定时检测时间
	GetCheckPeriod() time.Duration
	// SetCheckPeriod 设置熔断器定时检测时间
	SetCheckPeriod(time.Duration)
	// GetSleepWindow 获取熔断周期
	GetSleepWindow() time.Duration
	// SetSleepWindow 设置熔断周期
	SetSleepWindow(interval time.Duration)
	// GetRequestCountAfterHalfOpen 获取半开状态后最多分配多少个探测请求
	GetRequestCountAfterHalfOpen() int
	// SetRequestCountAfterHalfOpen 设置半开状态后最多分配多少个探测请求
	SetRequestCountAfterHalfOpen(count int)
	// GetSuccessCountAfterHalfOpen 获取半开状态后多少个成功请求则恢复
	GetSuccessCountAfterHalfOpen() int
	// SetSuccessCountAfterHalfOpen 设置半开状态后多少个成功请求则恢复
	SetSuccessCountAfterHalfOpen(count int)
	// GetRecoverWindow 获取半开后的恢复周期，按周期来进行半开放量的统计
	GetRecoverWindow() time.Duration
	// SetRecoverWindow 设置半开后的恢复周期，按周期来进行半开放量的统计
	SetRecoverWindow(value time.Duration)
	// GetRecoverNumBuckets 半开后请求数统计滑桶数量
	GetRecoverNumBuckets() int
	// SetRecoverNumBuckets 设置半开后请求数统计滑桶数量
	SetRecoverNumBuckets(value int)
	// GetErrorCountConfig 连续错误数熔断配置
	GetErrorCountConfig() ErrorCountConfig
	// GetErrorRateConfig 错误率熔断配置
	GetErrorRateConfig() ErrorRateConfig
}

// Configuration 全量配置对象.
type Configuration interface {
	BaseConfig
	// GetGlobal global前缀开头的所有配置项
	GetGlobal() GlobalConfig
	// GetConsumer consumer前缀开头的所有配置项
	GetConsumer() ConsumerConfig
	// GetProvider provider前缀开头的所有配置项
	GetProvider() ProviderConfig
	// GetConfigFile config前缀开头的所有配置项
	GetConfigFile() ConfigFileConfig
}

// When when to active health check.
type When string

const (
	// HealthCheckNever never active health check.
	HealthCheckNever When = "never"
	// HealthCheckAlways always active health check.
	HealthCheckAlways = "always"
	// HealthCheckOnRecover active health check when instance has fail.
	HealthCheckOnRecover = "on_recover"
)

// HealthCheckConfig active health check config.
type HealthCheckConfig interface {
	BaseConfig
	PluginConfig
	// GetWhen get when to active health check
	GetWhen() When
	// SetWhen set when to active health check
	SetWhen(When)
	// GetInterval get health check interval
	GetInterval() time.Duration
	// SetInterval set health check interval
	SetInterval(duration time.Duration)
	// GetTimeout get health check max timeout
	GetTimeout() time.Duration
	// SetTimeout set health check max timeout
	SetTimeout(duration time.Duration)
	// GetConcurrency get concurrency to execute the health check jobs
	GetConcurrency() int
	// SetConcurrency set concurrency to execute the health check jobs
	SetConcurrency(int)
	// GetChain get health checking chain
	GetChain() []string
	// SetChain set health checking chain
	SetChain([]string)
}

// ServiceSpecificConfig 配置.
type ServiceSpecificConfig interface {
	BaseConfig

	GetServiceCircuitBreaker() CircuitBreakerConfig

	GetServiceRouter() ServiceRouterConfig
}

// ConfigConnectorConfig 配置中心连接相关的配置.
type ConfigConnectorConfig interface {
	ServerConnectorConfig

	// GetConnectorType 后端服务器类型，默认是 polaris
	GetConnectorType() string
}
