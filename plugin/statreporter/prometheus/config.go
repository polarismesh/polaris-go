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
	"time"

	"github.com/polarismesh/polaris-go/pkg/plugin"
)

func init() {
	plugin.RegisterConfigurablePlugin(&PrometheusReporter{}, &Config{})
}

const (
	defaultReportInterval = 1 * time.Minute
	defaultMetricPort = 28080
)

// Config prometheus 的配置
type Config struct {
	IP   string `yaml:"metricHost"`
	Port uint32 `yaml:"metricPort"`
}

// Verify verify config
func (c *Config) Verify() error {
	return nil
}

// SetDefault Setting defaults
func (c *Config) SetDefault() {
	if c.Port == 0 {
		c.Port = defaultMetricPort
	}
}
