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

package losslessController

import (
	"fmt"
	"strings"
)

const (
	// PluginName 插件名称
	PluginName = "losslessController"

	// DefaultHealthCheckProtocol 默认健康检查协议
	DefaultHealthCheckProtocol = "HTTP"
	// DefaultHealthCheckMethod 默认健康检查方法
	DefaultHealthCheckMethod = "GET"
	// DefaultHealthCheckPath 默认健康检查路径
	DefaultHealthCheckPath = "/health"
	// DefaultOfflinePath 默认无损下线preStop路径
	DefaultOfflinePath = "/offline"
	// DefaultReadinessPath 默认就绪检查路径
	DefaultReadinessPath = "/readiness"
	// DefaultHealthCheckMaxRetry 默认健康检查最大重试次数
	DefaultHealthCheckMaxRetry = 10
)

var supportedHealthCheckProtocols = map[string]struct{}{"HTTP": {}}
var supportedHealthCheckMethods = map[string]struct{}{"GET": {}}

// Config 延迟注册无损策略配置
type Config struct {
	// HealthCheckProtocol 探测延迟注册的探测协议
	HealthCheckProtocol string `yaml:"healthCheckProtocol" json:"healthCheckProtocol"`
	// HealthCheckMethod 探测延迟注册的探测方法
	HealthCheckMethod string `yaml:"healthCheckMethod" json:"healthCheckMethod"`
	// HealthCheckPath 探测延迟注册的探测路径
	HealthCheckPath string `yaml:"healthCheckPath" json:"healthCheckPath"`
	// HealthCheckMaxRetry 探测延迟注册的探测最大重试次数
	HealthCheckMaxRetry int `yaml:"healthCheckMaxRetry" json:"healthCheckMaxRetry"`
	// OfflineProbeEnabled 是否开启无损下线preStop探测
	OfflineProbeEnabled bool `yaml:"offlineProbeEnabled" json:"offlineProbeEnabled"`
	// OfflinePath 无损下线preStop请求接口, 会请求反注册接口
	OfflinePath string `yaml:"offlinePath" json:"offlinePath"`
	// ReadinessProbeEnabled 是否开启就绪检查
	ReadinessProbeEnabled bool `yaml:"readinessProbeEnabled" json:"readinessProbeEnabled"`
	// ReadinessPath 无损上线就绪检查的请求接口
	ReadinessPath string `yaml:"readinessPath" json:"readinessPath"`
}

// Verify 验证配置
func (c *Config) Verify() error {
	// 校验健康检查协议
	if c.HealthCheckProtocol != "" {
		protocol := strings.ToUpper(c.HealthCheckProtocol)
		if _, ok := supportedHealthCheckProtocols[protocol]; !ok {
			return fmt.Errorf("lossless plugin config healthCheckProtocol must be one of: %v, got: %s",
				mapKeys(supportedHealthCheckProtocols), c.HealthCheckProtocol)
		}
	}
	// 校验健康检查方法
	if c.HealthCheckMethod != "" {
		method := strings.ToUpper(c.HealthCheckMethod)
		if _, ok := supportedHealthCheckMethods[method]; !ok {
			return fmt.Errorf("lossless plugin config healthCheckMethod must be one of: %v, got: %s",
				mapKeys(supportedHealthCheckMethods), c.HealthCheckMethod)
		}
	}
	// 校验健康检查路径
	if c.HealthCheckPath != "" && !strings.HasPrefix(c.HealthCheckPath, "/") {
		return fmt.Errorf("lossless plugin config healthCheckPath must start with '/', got: %s", c.HealthCheckPath)
	}
	// 校验下线路径
	if c.OfflinePath != "" && !strings.HasPrefix(c.OfflinePath, "/") {
		return fmt.Errorf("lossless plugin config offlinePath must start with '/', got: %s", c.OfflinePath)
	}
	// 校验就绪检查路径
	if c.ReadinessPath != "" && !strings.HasPrefix(c.ReadinessPath, "/") {
		return fmt.Errorf("lossless plugin config readinessPath must start with '/', got: %s", c.ReadinessPath)
	}
	return nil
}

// SetDefault 设置默认值
func (c *Config) SetDefault() {
	if c.HealthCheckProtocol == "" {
		c.HealthCheckProtocol = DefaultHealthCheckProtocol
	}
	if c.HealthCheckMethod == "" {
		c.HealthCheckMethod = DefaultHealthCheckMethod
	}
	if c.HealthCheckPath == "" {
		c.HealthCheckPath = DefaultHealthCheckPath
	}
	if c.OfflinePath == "" {
		c.OfflinePath = DefaultOfflinePath
	}
	if c.ReadinessPath == "" {
		c.ReadinessPath = DefaultReadinessPath
	}
	if c.HealthCheckMaxRetry == 0 {
		c.HealthCheckMaxRetry = DefaultHealthCheckMaxRetry
	}
}

// mapKeys 获取map的所有键
func mapKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
