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
	"bytes"
	"io/ioutil"
	"log"
	"os"
	"time"

	"gopkg.in/yaml.v2"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// ConfigurationImpl cl5全局配置.
type ConfigurationImpl struct {
	Global   *GlobalConfigImpl     `yaml:"global,omitempty" json:"global,omitempty"`
	Consumer *ConsumerConfigImpl   `yaml:"consumer,omitempty" json:"consumer,omitempty"`
	Provider *ProviderConfigImpl   `yaml:"provider,omitempty" json:"provider,omitempty"`
	Config   *ConfigFileConfigImpl `yaml:"config,omitempty" json:"config,omitempty"`
}

// GetGlobal cl5.global前缀开头的所有配置项.
func (c *ConfigurationImpl) GetGlobal() GlobalConfig {
	return c.Global
}

// GetConsumer cl5.consumer前缀开头的所有配置项.
func (c *ConfigurationImpl) GetConsumer() ConsumerConfig {
	return c.Consumer
}

// GetProvider consumer前缀开头的所有配置项.
func (c *ConfigurationImpl) GetProvider() ProviderConfig {
	return c.Provider
}

// GetConfig config前缀开头的所有配置项.
func (c *ConfigurationImpl) GetConfigFile() ConfigFileConfig {
	return c.Config
}

// GlobalConfigImpl 全局配置.
type GlobalConfigImpl struct {
	System          *SystemConfigImpl          `yaml:"system,omitempty" json:"system,omitempty"`
	API             *APIConfigImpl             `yaml:"api,omitempty" json:"api,omitempty"`
	ServerConnector *ServerConnectorConfigImpl `yaml:"serverConnector,omitempty" json:"serverConnector,omitempty"`
	StatReporter    *StatReporterConfigImpl    `yaml:"statReporter,omitempty" json:"statReporter,omitempty"`
	Location        *LocationConfigImpl        `yaml:"location,omitempty" json:"location,omitempty"`
}

// GetSystem 获取系统配置.
func (g *GlobalConfigImpl) GetSystem() SystemConfig {
	return g.System
}

// GetAPI global.api前缀开头的所有配置项.
func (g *GlobalConfigImpl) GetAPI() APIConfig {
	return g.API
}

// GetServerConnector global.serverConnector前缀开头的所有配置项.
func (g *GlobalConfigImpl) GetServerConnector() ServerConnectorConfig {
	return g.ServerConnector
}

// GetStatReporter cl5.global.statReporter前缀开头的所有配置项.
func (g *GlobalConfigImpl) GetStatReporter() StatReporterConfig {
	return g.StatReporter
}

// GetLocation cl5.global.location前缀开头的所有配置项.
func (g *GlobalConfigImpl) GetLocation() LocationConfig {
	return g.Location
}

// ConsumerConfigImpl 消费者配置.
type ConsumerConfigImpl struct {
	LocalCache       *LocalCacheConfigImpl     `yaml:"localCache,omitempty" json:"localCache,omitempty"`
	ServiceRouter    *ServiceRouterConfigImpl  `yaml:"serviceRouter,omitempty" json:"serviceRouter,omitempty"`
	Loadbalancer     *LoadBalancerConfigImpl   `yaml:"loadbalancer,omitempty" json:"loadbalancer,omitempty"`
	CircuitBreaker   *CircuitBreakerConfigImpl `yaml:"circuitBreaker,omitempty" json:"circuitBreaker,omitempty"`
	HealthCheck      *HealthCheckConfigImpl    `yaml:"healthCheck,omitempty" json:"healthCheck,omitempty"`
	Subscribe        *SubscribeImpl            `yaml:"subscribe,omitempty" json:"subscribe,omitempty"`
	ServicesSpecific []*ServiceSpecific        `yaml:"servicesSpecific,omitempty" json:"servicesSpecific,omitempty"`
}

// GetLocalCache consumer.localCache前缀开头的所有配置.
func (c *ConsumerConfigImpl) GetLocalCache() LocalCacheConfig {
	return c.LocalCache
}

// GetServiceRouter consumer.serviceRouter前缀开头的所有配置.
func (c *ConsumerConfigImpl) GetServiceRouter() ServiceRouterConfig {
	return c.ServiceRouter
}

// GetLoadbalancer consumer.loadbalancer前缀开头的所有配置.
func (c *ConsumerConfigImpl) GetLoadbalancer() LoadbalancerConfig {
	return c.Loadbalancer
}

// GetCircuitBreaker consumer.circuitbreaker前缀开头的所有配置.
func (c *ConsumerConfigImpl) GetCircuitBreaker() CircuitBreakerConfig {
	return c.CircuitBreaker
}

// GetHealthCheck get health check config.
func (c *ConsumerConfigImpl) GetHealthCheck() HealthCheckConfig {
	return c.HealthCheck
}

// GetSubScribe 订阅配置.
func (c *ConsumerConfigImpl) GetSubScribe() SubscribeConfig {
	return c.Subscribe
}

// GetServiceSpecific 服务独立配置.
func (c *ConsumerConfigImpl) GetServiceSpecific(namespace string, service string) ServiceSpecificConfig {
	for _, v := range c.ServicesSpecific {
		if v.Namespace == namespace && v.Service == service {
			return v
		}
	}
	return nil
}

// SystemConfigImpl 系统配置.
type SystemConfigImpl struct {
	// SDK运行模式
	Mode model.RunMode `yaml:"mode,omitempty" json:"mode,omitempty"`
	// 服务发现集群
	DiscoverCluster *ServerClusterConfigImpl `yaml:"discoverCluster,omitempty" json:"discoverCluster,omitempty"`
	// 健康检查集群
	HealthCheckCluster *ServerClusterConfigImpl `yaml:"healthCheckCluster,omitempty" json:"healthCheckCluster,omitempty"`
	// 监控上报集群
	MonitorCluster *ServerClusterConfigImpl `yaml:"monitorCluster,omitempty" json:"monitorCluster,omitempty"`
	// 传入的路由规则variables
	Variables map[string]string `yaml:"variables,omitempty" json:"variables,omitempty"`
}

// GetMode SDK运行模式，agent还是noagent.
func (s *SystemConfigImpl) GetMode() model.RunMode {
	return s.Mode
}

// SetMode 设置SDK运行模式.
func (s *SystemConfigImpl) SetMode(mode model.RunMode) {
	s.Mode = mode
}

// GetDiscoverCluster 服务发现集群.
func (s *SystemConfigImpl) GetDiscoverCluster() ServerClusterConfig {
	return s.DiscoverCluster
}

// GetHealthCheckCluster 健康检查集群.
func (s *SystemConfigImpl) GetHealthCheckCluster() ServerClusterConfig {
	return s.HealthCheckCluster
}

// GetMonitorCluster 监控上报集群.
func (s *SystemConfigImpl) GetMonitorCluster() ServerClusterConfig {
	return s.MonitorCluster
}

// GetVariable 获取一个路由variable.
func (s *SystemConfigImpl) GetVariable(key string) (string, bool) {
	if s.Variables == nil {
		return "", false
	}
	value, ok := s.Variables[key]
	return value, ok
}

// SetVariable 设置一个路由variable.
func (s *SystemConfigImpl) SetVariable(key, value string) {
	if s.Variables == nil {
		s.Variables = make(map[string]string)
	}
	s.Variables[key] = value
}

// UnsetVariable 取消一个路由variable.
func (s *SystemConfigImpl) UnsetVariable(key string) {
	if s.Variables != nil {
		delete(s.Variables, key)
	}
}

// ServerClusterConfigImpl 单个服务集群配置.
type ServerClusterConfigImpl struct {
	Namespace       string         `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	Service         string         `yaml:"service,omitempty" json:"service,omitempty"`
	RefreshInterval *time.Duration `yaml:"refreshInterval,omitempty" json:"refreshInterval,omitempty"`
}

// GetNamespace 获取命名空间.
func (s *ServerClusterConfigImpl) GetNamespace() string {
	return s.Namespace
}

// SetNamespace 设置命名空间.
func (s *ServerClusterConfigImpl) SetNamespace(namespace string) {
	s.Namespace = namespace
}

// GetService 获取服务名.
func (s *ServerClusterConfigImpl) GetService() string {
	return s.Service
}

// SetService 设置服务名.
func (s *ServerClusterConfigImpl) SetService(service string) {
	s.Service = service
}

// GetRefreshInterval 获取系统服务刷新间隔.
func (s *ServerClusterConfigImpl) GetRefreshInterval() time.Duration {
	return *s.RefreshInterval
}

// SetRefreshInterval 获取系统服务刷新间隔.
func (s *ServerClusterConfigImpl) SetRefreshInterval(interval time.Duration) {
	s.RefreshInterval = &interval
}

// NewServerClusterConfig 通过服务信息创建服务集群配置.
func NewServerClusterConfig(svcKey model.ServiceKey) *ServerClusterConfigImpl {
	return &ServerClusterConfigImpl{
		Namespace: svcKey.Namespace,
		Service:   svcKey.Service,
	}
}

// ServiceClusterToServiceKey 服务集群信息转换为服务对象.
func ServiceClusterToServiceKey(config ServerClusterConfig) model.ServiceKey {
	return model.ServiceKey{
		Namespace: config.GetNamespace(),
		Service:   config.GetService(),
	}
}

// APIConfigImpl API访问相关的配置.
type APIConfigImpl struct {
	Timeout        *time.Duration `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	BindIntf       string         `yaml:"bindIf,omitempty" json:"bindIf,omitempty"`
	BindIP         string         `yaml:"bindIP,omitempty" json:"bindIP,omitempty"`
	BindIPValue    string         `yaml:"-" json:"-"`
	ReportInterval *time.Duration `yaml:"reportInterval,omitempty" json:"reportInterval,omitempty"`
	MaxRetryTimes  int            `yaml:"maxRetryTimes,omitempty" json:"maxRetryTimes,omitempty"`
	RetryInterval  *time.Duration `yaml:"retryInterval,omitempty" json:"retryInterval,omitempty"`
}

// GetTimeout 默认调用超时时间.
func (a *APIConfigImpl) GetTimeout() time.Duration {
	return *a.Timeout
}

// SetTimeout 设置默认超时时间.
func (a *APIConfigImpl) SetTimeout(timeout time.Duration) {
	a.Timeout = &timeout
}

// GetBindIntf 默认客户端绑定的网卡地址.
func (a *APIConfigImpl) GetBindIntf() string {
	return a.BindIntf
}

// SetBindIntf 设置默认客户端绑定的网卡地址.
func (a *APIConfigImpl) SetBindIntf(bindIntf string) {
	a.BindIntf = bindIntf
}

// GetBindIP 默认客户端绑定的网卡地址.
func (a *APIConfigImpl) GetBindIP() string {
	return a.BindIPValue
}

// SetBindIP 设置默认客户端绑定的网卡地址.
func (a *APIConfigImpl) SetBindIP(bindIPValue string) {
	a.BindIPValue = bindIPValue
}

// GetReportInterval 默认客户端上报周期.
func (a *APIConfigImpl) GetReportInterval() time.Duration {
	return *a.ReportInterval
}

// SetReportInterval 设置默认客户端上报周期.
func (a *APIConfigImpl) SetReportInterval(interval time.Duration) {
	a.ReportInterval = &interval
}

// GetMaxRetryTimes 最大重试次数.
func (a *APIConfigImpl) GetMaxRetryTimes() int {
	return a.MaxRetryTimes
}

// SetMaxRetryTimes 最大重试次数.
func (a *APIConfigImpl) SetMaxRetryTimes(maxRetryTimes int) {
	a.MaxRetryTimes = maxRetryTimes
}

// GetRetryInterval 重试周期.
func (a *APIConfigImpl) GetRetryInterval() time.Duration {
	return *a.RetryInterval
}

// SetRetryInterval 重试周期.
func (a *APIConfigImpl) SetRetryInterval(interval time.Duration) {
	a.RetryInterval = &interval
}

// NewDefaultConfiguration 创建默认配置对象.
func NewDefaultConfiguration(addresses []string) *ConfigurationImpl {
	cfg := &ConfigurationImpl{}
	cfg.Init()
	cfg.SetDefault()
	if len(addresses) > 0 {
		cfg.GetGlobal().GetServerConnector().(*ServerConnectorConfigImpl).Addresses = addresses
	}
	return cfg
}

// GetContainerNameEnvList 获取可以从获取的容器.
func GetContainerNameEnvList() []string {
	res := make([]string, len(containerNameEnvs))
	for i, c := range containerNameEnvs {
		res[i] = c
	}
	return res
}

// NewDefaultConfigurationWithDomain 创建带有默认埋点server域名的默认配置.
func NewDefaultConfigurationWithDomain() *ConfigurationImpl {
	var cfg *ConfigurationImpl
	var err error
	if model.IsFile(DefaultConfigFile) {
		cfg, err = LoadConfigurationByDefaultFile()
		if err != nil {
			log.Printf("fail to load default config from %s, err is %v", DefaultConfigFile, err)
		}
	}
	if nil != cfg {
		return cfg
	}
	return NewDefaultConfiguration(nil)
}

// LoadConfigurationByFile 通过文件加载配置项.
func LoadConfigurationByFile(path string) (*ConfigurationImpl, error) {
	if !model.IsFile(path) {
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil, "invalid context file %s", path)
	}
	buff, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidArgument, err, "fail to read context file %s", path)
	}
	return LoadConfiguration(buff)
}

// LoadConfigurationByDefaultFile 通过默认配置文件加载配置项.
func LoadConfigurationByDefaultFile() (*ConfigurationImpl, error) {
	return LoadConfigurationByFile(DefaultConfigFile)
}

// LoadConfiguration 加载配置项.
func LoadConfiguration(buf []byte) (*ConfigurationImpl, error) {
	var err error
	cfg := &ConfigurationImpl{}
	cfg.Init()
	// to support environment variables
	content := os.ExpandEnv(string(buf))
	decoder := yaml.NewDecoder(bytes.NewBufferString(content))
	if err = decoder.Decode(cfg); err != nil {
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidConfig, err,
			"fail to decode config string")
	}
	cfg.SetDefault()
	if err = cfg.Verify(); err != nil {
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidConfig, err,
			"fail to verify config string")
	}

	return cfg, nil
}
