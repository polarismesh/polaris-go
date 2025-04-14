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

package pushgateway

import (
	"github.com/polarismesh/polaris-go/pkg/plugin"
)

func init() {
	plugin.RegisterConfigurablePlugin(&PushgatewayReporter{}, &Config{})
}

const (
	DefaultNamespaceName = "Polaris"
	DefaultServiceName   = "polaris.pushgateway"
)

type Config struct {
	Address        string `yaml:"address"`
	NamespaceName  string `yaml:"namespaceName"`
	ServiceName    string `yaml:"serviceName"`
	ReportPath     string `yaml:"reportPath"`
	EventQueueSize int    `yaml:"eventQueueSize"` // 队列满了，立即上报；定期每秒上报一次

}

// Verify verify config
func (c *Config) Verify() error {
	return nil
}

// SetDefault Setting defaults
func (c *Config) SetDefault() {
	if c.EventQueueSize == 0 {
		c.EventQueueSize = 512
	}
	if c.ReportPath == "" {
		c.ReportPath = "polaris/client/events" // 默认地址
	}
	if c.ServiceName == "" || c.NamespaceName == "" {
		c.ServiceName = DefaultServiceName
		c.NamespaceName = DefaultNamespaceName
	}
}
