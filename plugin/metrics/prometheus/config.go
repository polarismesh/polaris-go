// Tencent is pleased to support the open source community by making polaris-go available.
//
// Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
//
// Licensed under the BSD 3-Clause License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// https://opensource.org/licenses/BSD-3-Clause
//
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissionsr and limitations under the License.
//

package prometheus

import (
	"strconv"
	"time"

	"github.com/polarismesh/polaris-go/pkg/plugin"
)

func init() {
	plugin.RegisterConfigurablePlugin(&PrometheusReporter{}, &Config{})
}

const (
	defaultReportInterval      = 1 * time.Minute
	defaultMetricPort          = 28080
	defaultPushGatewayNamespace = "Polaris"
	defaultPushGatewayService   = "polaris.pushgateway"
	// pushErrLogInterval push 失败日志收敛间隔：首次 ERROR，后续同一错误每 30s 一次 WARN
	pushErrLogInterval = 30 * time.Second
)

// Config prometheus 的配置
type Config struct {
	Type     string        `yaml:"type"`
	IP       string        `yaml:"metricHost"`
	PortStr  string        `yaml:"metricPort"`
	port     int           `yaml:"-"`
	Interval time.Duration `yaml:"interval"`
	Address  string        `yaml:"address"`
	// pushgateway 服务发现配置：仅 push 模式 + address 为空时生效。
	// 参照 polaris-java ServiceAddressRepository 的两级回退模式。
	PushGatewayNamespace string `yaml:"pushGatewayNamespace"`
	PushGatewayService   string `yaml:"pushGatewayService"`
}

// Verify verify config
func (c *Config) Verify() error {
	return nil
}

// SetDefault Setting defaults
func (c *Config) SetDefault() {
	if c.Type == "" {
		c.Type = "pull"
	}
	if c.PortStr == "" {
		c.port = defaultMetricPort
	} else {
		port, _ := strconv.ParseInt(c.PortStr, 10, 64)
		c.port = int(port)
	}
	if c.Interval == 0 {
		c.Interval = 15 * time.Second
	}
	if c.PushGatewayNamespace == "" {
		c.PushGatewayNamespace = defaultPushGatewayNamespace
	}
	if c.PushGatewayService == "" {
		c.PushGatewayService = defaultPushGatewayService
	}
}
