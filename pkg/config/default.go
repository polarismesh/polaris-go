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

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
)

const (
	// DefaultAPIInvokeTimeout 默认API调用的超时时间.
	DefaultAPIInvokeTimeout = 1 * time.Second
	// DefaultAPIMaxRetryTimes 默认api调用重试次数.
	DefaultAPIMaxRetryTimes int = 1
	// DefaultAPIRetryInterval 默认api调用重试间隔.
	DefaultAPIRetryInterval = 1 * time.Second
	// DefaultDiscoverServiceRetryInterval 默认首次发现discovery服务重试间隔.
	DefaultDiscoverServiceRetryInterval = 5 * time.Second
	// DefaultServiceExpireTime 默认的服务超时淘汰时间.
	DefaultServiceExpireTime = 24 * time.Hour
	// DefaultServiceRefreshIntervalDuration 默认的服务刷新间隔.
	DefaultServiceRefreshIntervalDuration = 2 * time.Second
	// DefaultServerConnectTimeout 默认SDK往Server连接超时时间间隔.
	DefaultServerConnectTimeout = 500 * time.Millisecond
	// DefaultReConnectInterval 默认重连的间隔.
	DefaultReConnectInterval = 500 * time.Millisecond
	// DefaultServerMessageTimeout 默认消息超时时间.
	DefaultServerMessageTimeout = 1500 * time.Millisecond
	// DefaultServerConnectionIdleTimeout 默认服务端stream闲置超时时间.
	DefaultServerConnectionIdleTimeout = 3 * time.Second
	// DefaultBuiltInServerConnectionCloseTimeout 默认埋点server连接过期关闭时间.
	DefaultBuiltInServerConnectionCloseTimeout = 2 * DefaultServerConnectionIdleTimeout
	// DefaultRequestQueueSize 默认发送队列的buffer大小，支持的最大瞬时并发度，默认1000.
	DefaultRequestQueueSize int = 1000
	// DefaultServerSwitchInterval 默认server的切换时间时间.
	DefaultServerSwitchInterval = 10 * time.Minute
	// DefaultCachePersistDir 默认缓存持久化存储目录.
	DefaultCachePersistDir string = "./polaris/backup"
	// DefaultPersistMaxWriteRetry 持久化缓存写文件的默认重试次数.
	DefaultPersistMaxWriteRetry int = 5
	// DefaultPersistMaxReadRetry 读取持久化缓存的默认重试次数.
	DefaultPersistMaxReadRetry = 1
	// DefaultPersistRetryInterval 默认持久化重试间隔时间.
	DefaultPersistRetryInterval = 1 * time.Second
	// DefaultPersistAvailableInterval 默认持久化文件有效时间.
	DefaultPersistAvailableInterval = 60 * time.Second
	// DefaultCircuitBreakerCheckPeriod 默认熔断节点检查周期.
	DefaultCircuitBreakerCheckPeriod = 10 * time.Second
	// MinCircuitBreakerCheckPeriod 最低熔断节点检查周期.
	MinCircuitBreakerCheckPeriod = 1 * time.Second
	// DefaultCircuitBreakerEnabled 熔断器默认开启与否.
	DefaultCircuitBreakerEnabled bool = true
	// DefaultRecoverAllEnabled 服务路由的全死全活默认开启与否.
	DefaultRecoverAllEnabled bool = true
	// DefaultPercentOfMinInstances 路由至少返回节点数百分比.
	DefaultPercentOfMinInstances float64 = 0.0
	// DefaultHealthCheckConcurrency 默认心跳检测的并发数.
	DefaultHealthCheckConcurrency int = 1
	// DefaultHealthCheckConcurrencyAlways 默认持续心跳检测的并发数.
	DefaultHealthCheckConcurrencyAlways int = 10
	// DefaultHealthCheckInterval 默认健康探测周期.
	DefaultHealthCheckInterval = 10 * time.Second
	// MinHealthCheckInterval 最低健康探测周期.
	MinHealthCheckInterval = 500 * time.Millisecond
	// DefaultHealthCheckTimeout 默认健康探测超时时间.
	DefaultHealthCheckTimeout = 100 * time.Millisecond
	// DefaultReportClientIntervalDuration 客户端信息上报周期，默认2分钟.
	DefaultReportClientIntervalDuration = 2 * time.Minute
	// MaxRedirectTimes 最大重定向次数，默认1.
	MaxRedirectTimes = 1
	// DefaultReportSDKConfigurationInterval sdk配置上报周期.
	DefaultReportSDKConfigurationInterval = 5 * time.Minute
	// DefaultSleepWindow 熔断周期，被熔断后多久变为半开.
	DefaultSleepWindow = 30 * time.Second
	// MinSleepWindow 最小熔断周期，1s.
	MinSleepWindow = 1 * time.Second
	// DefaultRecoverWindow 默认恢复周期，半开后按多久的统计窗口进行恢复统计.
	DefaultRecoverWindow = 60 * time.Second
	// MinRecoverWindow 最小恢复周期，10s.
	MinRecoverWindow = 10 * time.Second
	// DefaultRecoverNumBuckets 默认恢复统计的滑桶数.
	DefaultRecoverNumBuckets = 10
	// MinRecoverNumBuckets 最小恢复统计的滑桶数.
	MinRecoverNumBuckets = 1
	// DefaultRequestCountAfterHalfOpen 半开状态后分配的探测请求数.
	DefaultRequestCountAfterHalfOpen = 10
	// DefaultSuccessCountAfterHalfOpen 半开状态后恢复的成功请求数.
	DefaultSuccessCountAfterHalfOpen = 8
	// DefaultRateLimitWindowCount 限流上报时间窗数量，上报间隔=时间间隔/时间窗数量.
	DefaultRateLimitWindowCount = 10
	// MinRateLimitReportInterval 最小限流上报周期.
	MinRateLimitReportInterval = 10 * time.Millisecond
	// DefaultRateLimitAcquireInterval 限流默认和sever acquire配额间隔, 弃用.
	DefaultRateLimitAcquireInterval = 100 * time.Millisecond
	// MaxRateLimitReportInterval 最大限流上报周期, 弃用.
	MaxRateLimitReportInterval = 5 * time.Second
	// DefaultRateLimitReportAmountPresent 默认满足百分之80的请求后立刻限流上报.
	DefaultRateLimitReportAmountPresent = 80
	// MaxRateLimitReportAmountPresent 最大实时上报百分比.
	MaxRateLimitReportAmountPresent = 100
	// MinRateLimitReportAmountPresent 最小实时上报百分比.
	MinRateLimitReportAmountPresent = 0
	// DefaultNamesSeparator 默认的名字分隔符.
	DefaultNamesSeparator = "#"
	// DefaultMapKeyValueSeparator 默认Map组装str key value分割符.
	DefaultMapKeyValueSeparator = ":"
	// DefaultMapKVTupleSeparator 默认Map组装str (key:value) 二元组分割符.
	DefaultMapKVTupleSeparator = "|"
	// DefaultLocationProvider 默认实例地理位置提供者插件名称.
	DefaultLocationProvider = ""
	// DefaultPropertiesValueCacheSize 默认类型转化缓存的key数量.
	DefaultPropertiesValueCacheSize = 100
	// DefaultPropertiesValueExpireTime 默认类型转化缓存的过期时间，1分钟.
	DefaultPropertiesValueExpireTime = 60000
	// DefaultConnectorType 默认连接器类型.
	DefaultConnectorType = "polaris"
	// DefaultConfigConnectorAddresses 默认连接器类型.
	DefaultConfigConnectorAddresses = "127.0.0.1:8093"
	// DefaultMinRegisterInterval
	DefaultMinRegisterInterval = 30 * time.Second
	// DefaultConfigFilterEnabled 默认配置过滤是否开启
	DefaultConfigFilterEnabled bool = true
)

// defaultBuiltinServerPort 默认埋点server的端口，与上面的IP一一对应.
const defaultBuiltinServerPort = 8081

// 各种容器平台的获取容器名字的环境变量.
var containerNameEnvs = []string{
	// taf/sumeru容器环境变量
	"CONTAINER_NAME",
	// 123容器的环境变量
	"SUMERU_POD_NAME",
	// STKE(CSIG)  微信TKE   TKE-x(TEG)
	"POD_NAME",
	// tkestack(CDG)
	"MY_POD_NAME",
}

const (
	// DefaultServerConnector 默认的服务端连接器插件.
	DefaultServerConnector string = "grpc"
	// DefaultLocalCache 默认本地缓存策略.
	DefaultLocalCache string = "inmemory"
	// DefaultServiceRouterRuleBased 默认规则路由.
	DefaultServiceRouterRuleBased string = "ruleBasedRouter"
	// DefaultServiceRouterFilterOnly 默认只过滤健康实例的路由.
	DefaultServiceRouterFilterOnly string = "filterOnlyRouter"
	// DefaultServiceRouterNearbyBased 默认就近路由.
	DefaultServiceRouterNearbyBased string = "nearbyBasedRouter"
	// DefaultServiceRouterSetDivision 默认set分组.
	DefaultServiceRouterSetDivision string = "setDivisionRouter"
	// DefaultServiceRouterDstMeta 默认基于目标元数据路由.
	DefaultServiceRouterDstMeta string = "dstMetaRouter"
	// DefaultServiceRouterCanary 金丝雀路由.
	DefaultServiceRouterCanary string = "canaryRouter"
	// DefaultServiceRouterZeroProtect 零实例保护
	DefaultServiceRouterZeroProtect string = "zeroProtectRouter"

	// DefaultLoadBalancerWR 默认负载均衡器,权重随机.
	DefaultLoadBalancerWR string = "weightedRandom"
	// DefaultLoadBalancerRingHash 负载均衡器,一致性hash环.
	DefaultLoadBalancerRingHash string = "ringHash"
	// DefaultLoadBalancerMaglev 负载均衡器,maglev hash.
	DefaultLoadBalancerMaglev string = "maglev"
	// DefaultLoadBalancerL5CST 负载均衡器,l5一致性hash兼容.
	DefaultLoadBalancerL5CST string = "l5cst"
	// DefaultLoadBalancerHash 负载均衡器,普通hash.
	DefaultLoadBalancerHash string = "hash"
	// DefaultCircuitBreakerErrRate 默认错误率熔断器.
	DefaultCircuitBreakerErrRate string = "errorRate"
	// DefaultCircuitBreakerErrCount 默认持续错误熔断器.
	DefaultCircuitBreakerErrCount string = "errorCount"
	// DefaultCircuitBreakerErrCheck 默认错误探测熔断器.
	DefaultCircuitBreakerErrCheck string = "errorCheck"
	// DefaultTCPHealthCheck 默认TCP探测器.
	DefaultTCPHealthCheck string = "tcp"
	// DefaultUDPHealthCheck 默认UDP探测器.
	DefaultUDPHealthCheck string = "udp"

	// DefaultRejectRateLimiter 默认的reject限流器.
	DefaultRejectRateLimiter = "reject"
	// DefaultWarmUpRateLimiter 默认warmup限流器.
	DefaultWarmUpRateLimiter = "warmUp"
	// DefaultUniformRateLimiter 默认的匀速限流器.
	DefaultUniformRateLimiter = "unirate"
	// DefaultWarmUpWaitLimiter 默认限流插件，预热匀速.
	DefaultWarmUpWaitLimiter = "warmup-wait"
	// DefaultBBRRateLimiter 默认的 CPU 自适应限流器. 使用 BBR 算法
	DefaultBBRRateLimiter = "bbr"
	// SubscribeLocalChannel 默认订阅事件处理插件.
	SubscribeLocalChannel = "subscribeLocalChannel"

	// MaxRateLimitWindowSize 默认限流最大窗口数量.
	MaxRateLimitWindowSize = 20000
	// DefaultRateLimitPurgeInterval 默认超时清理时延.
	DefaultRateLimitPurgeInterval = 1 * time.Minute
	// DefaultConfigConnector 默认的注册中心连接器插件.
	DefaultConfigConnector string = "polaris"
	// DefaultLimiterNamespace 默认的限流服务
	DefaultLimiterNamespace string = "Polaris"
	// DefaultLimiterService 默认的限流服务
	DefaultLimiterService string = "polaris.limiter"
)

// 默认的就近路由配置.
const (
	DefaultMatchLevel = "zone"
	RegionLevel       = "region"
	ZoneLevel         = "zone"
	CampusLevel       = "campus"
	AllLevel          = ""
)

const (
	// DefaultStatReporter .
	DefaultStatReporter = "stat2Monitor"
	// DefaultCacheReporter .
	DefaultCacheReporter = "serviceCache"
	// DefaultPluginReporter .
	DefaultPluginReporter = "pluginInfo"
	// DefaultLoadBalanceReporter .
	DefaultLoadBalanceReporter = "lbInfo"
	// DefaultRateLimitReporter .
	DefaultRateLimitReporter = "rateLimitRecord"
	// DefaultServiceRouteReporter .
	DefaultServiceRouteReporter = "serviceRoute"
	// DefaultStatReportEnabled .
	DefaultStatReportEnabled = true
	// DefaultMetricsChain .
	DefaultMetricsChain = "prometheus"
)

const (
	// DefaultMinServiceExpireTime .
	DefaultMinServiceExpireTime = 5 * time.Second
	// DefaultMaxServiceExpireCheckTime .
	DefaultMaxServiceExpireCheckTime = 1 * time.Hour
	// DefaultMinTimingInterval .
	DefaultMinTimingInterval = 100 * time.Millisecond
	// DefaultServerServiceRefreshInterval .
	DefaultServerServiceRefreshInterval = 1 * time.Minute
)

// ClusterType 集群类型，用以标识系统服务集群.
type ClusterType string

// 默认集群类型.
const (
	BuiltinCluster     ClusterType = "builtin"
	DiscoverCluster    ClusterType = "discover"
	ConfigCluster      ClusterType = "config"
	HealthCheckCluster ClusterType = "healthCheck"
	MonitorCluster     ClusterType = "monitor"
)

// 默认注册中心服务名.
const (
	ServerNamespace        = "Polaris"
	ServerDiscoverService  = "polaris.discover"
	ServerHeartBeatService = "polaris.healthcheck"
	ServerMonitorService   = "polaris.monitor"
	ServerConfigService    = "polaris.config"
)

// ClusterService server集群服务信息.
type ClusterService struct {
	model.ServiceKey
	ClusterType   ClusterType
	ClusterConfig ServerClusterConfig
}

// 集群服务打印信息.
func (c ClusterService) String() string {
	return fmt.Sprintf("{ServiceKey: %s, ClusterType: %v}", c.ServiceKey, c.ClusterType)
}

// ServerServices 系统服务列表数据.
type ServerServices map[ClusterType]ClusterService

// GetClusterService 获取集群服务.
func (s ServerServices) GetClusterService(clsType ClusterType) *ClusterService {
	svc, ok := s[clsType]
	if !ok {
		return nil
	}
	return &svc
}

// GetServerServices 获取系统服务列表.
func GetServerServices(cfg Configuration) ServerServices {
	discoverConfig := cfg.GetGlobal().GetSystem().GetDiscoverCluster()
	healthCheckConfig := cfg.GetGlobal().GetSystem().GetHealthCheckCluster()
	monitorConfig := cfg.GetGlobal().GetSystem().GetMonitorCluster()

	retMap := make(map[ClusterType]ClusterService)
	if len(discoverConfig.GetService()) > 0 && len(discoverConfig.GetNamespace()) > 0 {
		retMap[DiscoverCluster] = ClusterService{
			ServiceKey:    ServiceClusterToServiceKey(discoverConfig),
			ClusterType:   DiscoverCluster,
			ClusterConfig: discoverConfig,
		}
	}
	if len(healthCheckConfig.GetService()) > 0 && len(healthCheckConfig.GetNamespace()) > 0 {
		retMap[HealthCheckCluster] = ClusterService{
			ServiceKey:    ServiceClusterToServiceKey(healthCheckConfig),
			ClusterType:   HealthCheckCluster,
			ClusterConfig: healthCheckConfig,
		}
	}
	if len(monitorConfig.GetService()) > 0 && len(monitorConfig.GetNamespace()) > 0 {
		retMap[MonitorCluster] = ClusterService{
			ServiceKey:    ServiceClusterToServiceKey(monitorConfig),
			ClusterType:   MonitorCluster,
			ClusterConfig: monitorConfig,
		}
	}
	return retMap
}

// 系统服务相关变量.
var (
	DefaultServerServiceRouterChain = []string{DefaultServiceRouterDstMeta, DefaultServiceRouterNearbyBased}
	// DefaultPolarisServicesRouterChain 系统命名空间下的服务默认路由链.
	DefaultPolarisServicesRouterChain  = []string{DefaultServiceRouterDstMeta}
	DefaultServerServiceToLoadBalancer = map[ClusterType]string{
		DiscoverCluster:    DefaultLoadBalancerWR,
		ConfigCluster:      DefaultLoadBalancerWR,
		HealthCheckCluster: DefaultLoadBalancerMaglev,
		MonitorCluster:     DefaultLoadBalancerMaglev,
	}
	DefaultServerServiceToUseDefault = map[ClusterType]bool{
		DiscoverCluster:    true,
		HealthCheckCluster: true,
	}
)

const (
	// DefaultConfigFile 系统默认配置文件.
	DefaultConfigFile = "./polaris.yaml"
)

// BaseConfig 自身自带校验器的配置集合.
type BaseConfig interface {
	// Verify 校验配置是否OK
	Verify() error
	// SetDefault 对关键值设置默认值
	SetDefault()
}

// Verify 检验API配置.
func (a *APIConfigImpl) Verify() error {
	if nil == a {
		return errors.New("APIConfig is nil")
	}
	if a.MaxRetryTimes < 0 {
		return fmt.Errorf("global.api.maxRetryTimes must be greater than 0")
	}
	if len(a.BindIP) == 0 {
		if len(a.BindIntf) == 0 {
			log.GetBaseLogger().Warnf("no IP or interface name configured")
		} else {
			var err error
			a.BindIPValue, err = model.GetIP(a.BindIntf)
			if err != nil {
				return fmt.Errorf(
					"can not get ip from provided bind interface %s, err is %s", a.BindIntf, err.Error())
			}
		}
	}
	if *a.RetryInterval < DefaultAPIRetryInterval {
		return fmt.Errorf("global.api.retryInterval must be greater than %v", DefaultAPIRetryInterval)
	}
	return nil
}

// SetDefault 设置API配置的默认值.
func (a *APIConfigImpl) SetDefault() {
	if nil == a.Timeout {
		a.Timeout = model.ToDurationPtr(DefaultAPIInvokeTimeout)
	}
	if nil == a.ReportInterval {
		a.ReportInterval = model.ToDurationPtr(DefaultReportClientIntervalDuration)
	}
	if nil == a.RetryInterval {
		a.RetryInterval = model.ToDurationPtr(DefaultAPIRetryInterval)
	}
	if a.MaxRetryTimes == 0 {
		a.MaxRetryTimes = DefaultAPIMaxRetryTimes
	}
	if len(a.BindIP) > 0 {
		a.BindIPValue = a.BindIP
	}
}

// Verify 检验globalConfig配置.
func (g *GlobalConfigImpl) Verify() error {
	if nil == g {
		return errors.New("GlobalConfig is nil")
	}
	var errs error
	var err error
	if err = g.ServerConnector.Verify(); err != nil {
		errs = multierror.Append(errs, err)
	}
	if err = g.API.Verify(); err != nil {
		errs = multierror.Append(errs, err)
	}
	if err = g.System.Verify(); err != nil {
		errs = multierror.Append(errs, err)
	}
	if err = g.StatReporter.Verify(); err != nil {
		errs = multierror.Append(errs, err)
	}
	if err = g.Location.Verify(); err != nil {
		errs = multierror.Append(errs, err)
	}
	return errs
}

// SetDefault 设置globalConfig配置的默认值.
func (g *GlobalConfigImpl) SetDefault() {
	g.API.SetDefault()
	g.ServerConnector.SetDefault()
	g.System.SetDefault()
	g.StatReporter.SetDefault()
	g.Location.SetDefault()
}

// Init 全局配置初始化.
func (g *GlobalConfigImpl) Init() {
	g.API = &APIConfigImpl{}
	g.System = &SystemConfigImpl{}
	g.System.Init()
	g.ServerConnector = &ServerConnectorConfigImpl{}
	g.ServerConnector.Init()
	g.StatReporter = &StatReporterConfigImpl{}
	g.StatReporter.Init()
	g.Location = &LocationConfigImpl{}
	g.Location.Init()
}

// Init 初始化ConsumerConfigImpl.
func (c *ConsumerConfigImpl) Init() {
	c.CircuitBreaker = &CircuitBreakerConfigImpl{}
	c.CircuitBreaker.Init()
	c.LocalCache = &LocalCacheConfigImpl{}
	c.LocalCache.Init()
	c.ServiceRouter = &ServiceRouterConfigImpl{}
	c.ServiceRouter.Init()
	c.Loadbalancer = &LoadBalancerConfigImpl{}
	c.Loadbalancer.Init()
	c.HealthCheck = &HealthCheckConfigImpl{}
	c.HealthCheck.Init()
}

// Verify 检验consumerConfig配置.
func (c *ConsumerConfigImpl) Verify() error {
	if nil == c {
		return errors.New("ConsumerConfig is nil")
	}
	var errs error
	var err error
	if err = c.LocalCache.Verify(); err != nil {
		errs = multierror.Append(errs, err)
	}
	if err = c.ServiceRouter.Verify(); err != nil {
		errs = multierror.Append(errs, err)
	}
	if err = c.Loadbalancer.Verify(); err != nil {
		errs = multierror.Append(errs, err)
	}
	if err = c.CircuitBreaker.Verify(); err != nil {
		errs = multierror.Append(errs, err)
	}
	if err = c.HealthCheck.Verify(); err != nil {
		errs = multierror.Append(errs, err)
	}
	return errs
}

// SetDefault 设置consumerConfig配置的默认值.
func (c *ConsumerConfigImpl) SetDefault() {
	c.LocalCache.SetDefault()
	c.Loadbalancer.SetDefault()
	c.ServiceRouter.SetDefault()
	c.CircuitBreaker.SetDefault()
	c.HealthCheck.SetDefault()
}

// Init 初始化整体配置对象.
func (c *ConfigurationImpl) Init() {
	c.Global = &GlobalConfigImpl{}
	c.Global.Init()
	c.Consumer = &ConsumerConfigImpl{}
	c.Consumer.Init()
	c.Provider = &ProviderConfigImpl{}
	c.Provider.Init()
	c.Config = &ConfigFileConfigImpl{}
	c.Config.Init()
}

// Verify 检验configuration配置.
func (c *ConfigurationImpl) Verify() error {
	if nil == c {
		return errors.New("configuration is nil")
	}
	var errs error
	var err error
	if err = c.Global.Verify(); err != nil {
		errs = multierror.Append(errs, err)
	}
	if err = c.Consumer.Verify(); err != nil {
		errs = multierror.Append(errs, err)
	}
	if err = c.Provider.Verify(); err != nil {
		errs = multierror.Append(errs, err)
	}
	if err = c.Config.Verify(); err != nil {
		errs = multierror.Append(errs, err)
	}
	return errs
}

// SetDefault 设置consumerConfig配置的默认值.
func (c *ConfigurationImpl) SetDefault() {
	c.Global.SetDefault()
	c.Consumer.SetDefault()
	c.Provider.SetDefault()
	c.Config.SetDefault()
}

// Init systemConfig init.
func (s *SystemConfigImpl) Init() {
	s.DiscoverCluster = &ServerClusterConfigImpl{}
	s.HealthCheckCluster = &ServerClusterConfigImpl{}
	s.MonitorCluster = &ServerClusterConfigImpl{
		Namespace: ServerNamespace,
		Service:   ServerMonitorService,
	}
}

// SetDefault 设置systemConfig默认值.
func (s *SystemConfigImpl) SetDefault() {
	s.DiscoverCluster.SetDefault()
	s.HealthCheckCluster.SetDefault()
	s.MonitorCluster.SetDefault()
}

// Verify 校验systemConfig配置.
func (s *SystemConfigImpl) Verify() error {
	if nil == s {
		return errors.New("SystemConfig is nil")
	}
	var errs error
	if s.Mode != model.ModeNoAgent && s.Mode != model.ModeWithAgent {
		errs = multierror.Append(errs,
			fmt.Errorf("global.api.mode=%v is invalid, you can use no-agent(%v) or with-agent(%v)",
				s.Mode, model.ModeNoAgent, model.ModeWithAgent))
	}
	var err error
	if err = s.DiscoverCluster.Verify(); err != nil {
		errs = multierror.Append(errs,
			fmt.Errorf("fail to verify serverClusters.discoverCluster, error is %v", err))
	}
	if err = s.HealthCheckCluster.Verify(); err != nil {
		errs = multierror.Append(errs,
			fmt.Errorf("fail to verify serverClusters.healthCheckCluster, error is %v", err))
	}
	if err = s.MonitorCluster.Verify(); err != nil {
		errs = multierror.Append(errs,
			fmt.Errorf("fail to verify serverClusters.monitorCluster, error is %v", err))
	}
	return errs
}

// SetDefault 设置ServerClusterConfig默认配置.
func (s *ServerClusterConfigImpl) SetDefault() {
	if nil == s.RefreshInterval {
		s.RefreshInterval = model.ToDurationPtr(DefaultServerServiceRefreshInterval)
	}
}

// Verify 校验ServerClusterConfig配置.
func (s *ServerClusterConfigImpl) Verify() error {
	if nil == s {
		return errors.New("ServerClusterConfig is nil")
	}
	var errs error
	if nil == s.RefreshInterval || *s.RefreshInterval < DefaultMinTimingInterval {
		errs = multierror.Append(errs,
			fmt.Errorf("refreshInterval can not be empty and must greater than %v", DefaultMinTimingInterval))
	}
	return errs
}
