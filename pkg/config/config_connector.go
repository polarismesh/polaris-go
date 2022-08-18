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
	"time"

	"github.com/hashicorp/go-multierror"
	"google.golang.org/protobuf/proto"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

// ConfigConnectorConfigImpl 对接配置中心连接器相关配置.
type ConfigConnectorConfigImpl struct {
	Addresses []string `yaml:"addresses,omitempty" json:"addresses,omitempty"`

	Protocol string `yaml:"protocol,omitempty" json:"protocol,omitempty"`

	ConnectTimeout *time.Duration `yaml:"connectTimeout,omitempty" json:"connectTimeout,omitempty"`

	// 远程请求超时时间
	MessageTimeout *time.Duration `yaml:"messageTimeout,omitempty" json:"messageTimeout,omitempty"`

	ConnectionIdleTimeout *time.Duration `yaml:"connectionIdleTimeout,omitempty" json:"connectionIdleTimeout,omitempty"`

	RequestQueueSize *int32 `yaml:"requestQueueSize,omitempty" json:"requestQueueSize,omitempty"`

	ServerSwitchInterval *time.Duration `yaml:"serverSwitchInterval" json:"serverSwitchInterval"`

	ReconnectInterval *time.Duration `yaml:"reconnectInterval" json:"reconnectInterval"`

	Plugin PluginConfigs `yaml:"plugin" json:"plugin"`

	ConnectorType string `yaml:"connectorType" json:"connectorType"`
}

// GetAddresses config.configConnector.addresses.
func (c *ConfigConnectorConfigImpl) GetAddresses() []string {
	return c.Addresses
}

// SetAddresses 设置远端server地址，格式为<host>:<port>.
func (c *ConfigConnectorConfigImpl) SetAddresses(addresses []string) {
	c.Addresses = addresses
}

// GetProtocol config.configConnector.protocol.
func (c *ConfigConnectorConfigImpl) GetProtocol() string {
	return c.Protocol
}

// SetProtocol 设置与server对接的协议.
func (c *ConfigConnectorConfigImpl) SetProtocol(protocol string) {
	c.Protocol = protocol
}

// GetConnectTimeout config.configConnector.connectTimeout.
func (c *ConfigConnectorConfigImpl) GetConnectTimeout() time.Duration {
	return *c.ConnectTimeout
}

// SetConnectTimeout 设置与server的连接超时时间.
func (c *ConfigConnectorConfigImpl) SetConnectTimeout(timeout time.Duration) {
	c.ConnectTimeout = &timeout
}

// GetMessageTimeout config.configConnector.messageTimeout.
func (c *ConfigConnectorConfigImpl) GetMessageTimeout() time.Duration {
	return *c.MessageTimeout
}

// SetMessageTimeout 设置远程请求超时时间.
func (c *ConfigConnectorConfigImpl) SetMessageTimeout(timeout time.Duration) {
	c.MessageTimeout = &timeout
}

// GetConnectionIdleTimeout config.configConnector.connectionIdleTimeout.
func (c *ConfigConnectorConfigImpl) GetConnectionIdleTimeout() time.Duration {
	return *c.ConnectionIdleTimeout
}

// SetConnectionIdleTimeout 设置连接空闲后超时时间.
func (c *ConfigConnectorConfigImpl) SetConnectionIdleTimeout(timeout time.Duration) {
	c.ConnectionIdleTimeout = &timeout
}

// GetRequestQueueSize config.configConnector.requestQueueSize.
func (c *ConfigConnectorConfigImpl) GetRequestQueueSize() int32 {
	return *c.RequestQueueSize
}

// SetRequestQueueSize 设置新请求的队列BUFFER容量.
func (c *ConfigConnectorConfigImpl) SetRequestQueueSize(queueSize int32) {
	c.RequestQueueSize = &queueSize
}

// GetServerSwitchInterval config.configConnector.serverSwitchInterval.
func (c *ConfigConnectorConfigImpl) GetServerSwitchInterval() time.Duration {
	return *c.ServerSwitchInterval
}

// SetServerSwitchInterval 设置server的切换时延.
func (c *ConfigConnectorConfigImpl) SetServerSwitchInterval(interval time.Duration) {
	c.ServerSwitchInterval = &interval
}

// GetReconnectInterval 一次连接失败后，到下一次连接之间的最小间隔时间.
func (c *ConfigConnectorConfigImpl) GetReconnectInterval() time.Duration {
	return *c.ReconnectInterval
}

// SetReconnectInterval 一次连接失败后，到下一次连接之间的最小间隔时间.
func (c *ConfigConnectorConfigImpl) SetReconnectInterval(interval time.Duration) {
	c.ReconnectInterval = &interval
}

// GetPluginConfig config.configConnector.plugin.
func (c *ConfigConnectorConfigImpl) GetPluginConfig(pluginName string) BaseConfig {
	cfgValue, ok := c.Plugin[pluginName]
	if !ok {
		return nil
	}
	return cfgValue.(BaseConfig)
}

// SetPluginConfig 输出插件具体配置.
func (c *ConfigConnectorConfigImpl) SetPluginConfig(pluginName string, value BaseConfig) error {
	return c.Plugin.SetPluginConfig(common.TypeServerConnector, pluginName, value)
}

// GetConnectorType 获取连接器类型.
func (c *ConfigConnectorConfigImpl) GetConnectorType() string {
	return c.ConnectorType
}

// SetConnectorType 设置连接器类型.
func (c *ConfigConnectorConfigImpl) SetConnectorType(connectorType string) {
	c.ConnectorType = connectorType
}

// Verify 检验ConfigConnector配置.
func (c *ConfigConnectorConfigImpl) Verify() error {
	if nil == c {
		return errors.New("ConfigConnectorConfig is nil")
	}
	var errs error
	if len(c.Addresses) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("config.configConnector.addresses is empty"))
	}
	if nil != c.RequestQueueSize && *c.RequestQueueSize < 0 {
		errs = multierror.Append(errs,
			fmt.Errorf("config.configConnector.requestQueueSize %v is invalid", c.RequestQueueSize))
	}
	if *c.ConnectionIdleTimeout < DefaultMinTimingInterval {
		errs = multierror.Append(errs,
			fmt.Errorf("config.configConnector.connectionIdleTimeout %v"+
				" is less than  minimal timing interval %v",
				*c.ConnectionIdleTimeout, DefaultMinTimingInterval))
	}
	if *c.ServerSwitchInterval <= *c.ConnectionIdleTimeout {
		errs = multierror.Append(errs,
			fmt.Errorf("config.configConnector.serverSwitchInterval %v"+
				" is less than or equal to config.configConnector.connectionIdleTimeout %v",
				*c.ServerSwitchInterval, *c.ConnectionIdleTimeout))
	}
	if len(c.ConnectorType) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("config.configConnector.connectorType is empty"))
	}
	return errs
}

// SetDefault 设置ConfigConnector配置的默认值.
func (c *ConfigConnectorConfigImpl) SetDefault() {
	if c.ConnectTimeout == nil {
		c.ConnectTimeout = model.ToDurationPtr(DefaultServerConnectTimeout)
	}
	if c.MessageTimeout == nil {
		c.MessageTimeout = model.ToDurationPtr(DefaultServerMessageTimeout)
	}
	if c.ConnectionIdleTimeout == nil {
		c.ConnectionIdleTimeout = model.ToDurationPtr(DefaultServerConnectionIdleTimeout)
	}
	if c.ServerSwitchInterval == nil {
		c.ServerSwitchInterval = model.ToDurationPtr(DefaultServerSwitchInterval)
	}
	if c.RequestQueueSize == nil {
		c.RequestQueueSize = proto.Int32(int32(DefaultRequestQueueSize))
	}
	if c.ReconnectInterval == nil {
		c.ReconnectInterval = model.ToDurationPtr(DefaultReConnectInterval)
	}
	if len(c.Protocol) == 0 {
		c.Protocol = DefaultConfigConnector
	}
	if len(c.Addresses) == 0 {
		c.SetAddresses([]string{DefaultConfigConnectorAddresses})
	}
	if len(c.ConnectorType) == 0 {
		c.ConnectorType = DefaultConnectorType
	}
	c.Plugin.SetDefault(common.TypeConfigConnector)
}

// Init 配置初始化.
func (c *ConfigConnectorConfigImpl) Init() {
	c.Plugin = PluginConfigs{}
	c.Plugin.Init(common.TypeConfigConnector)
}
