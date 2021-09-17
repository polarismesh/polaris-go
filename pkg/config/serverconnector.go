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
	"github.com/golang/protobuf/proto"
	"github.com/hashicorp/go-multierror"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"time"
)

// 对接注册中心相关配置
type ServerConnectorConfigImpl struct {
	Addresses []string `yaml:"addresses" json:"addresses"`

	Protocol string `yaml:"protocol" json:"protocol"`

	ConnectTimeout *time.Duration `yaml:"connectTimeout" json:"connectTimeout"`

	//远程请求超时时间
	MessageTimeout *time.Duration `yaml:"messageTimeout" json:"messageTimeout"`

	ConnectionIdleTimeout *time.Duration `yaml:"connectionIdleTimeout" json:"connectionIdleTimeout"`

	RequestQueueSize *int32 `yaml:"requestQueueSize" json:"requestQueueSize"`

	ServerSwitchInterval *time.Duration `yaml:"serverSwitchInterval" json:"serverSwitchInterval"`

	ReconnectInterval *time.Duration `yaml:"reconnectInterval" json:"reconnectInterval"`

	Plugin PluginConfigs `yaml:"plugin" json:"plugin"`
}

//GetAddresses global.serverConnector.addresses
//远端cl5 server地址，格式为<host>:<port>
func (s *ServerConnectorConfigImpl) GetAddresses() []string {
	return s.Addresses
}

//设置远端cl5 server地址，格式为<host>:<port>
func (s *ServerConnectorConfigImpl) SetAddresses(addresses []string) {
	s.Addresses = addresses
}

//GetProtocol global.serverConnector.protocol
//与cl5 server对接的协议
func (s *ServerConnectorConfigImpl) GetProtocol() string {
	return s.Protocol
}

//设置与cl5 server对接的协议
func (s *ServerConnectorConfigImpl) SetProtocol(protocol string) {
	s.Protocol = protocol
}

//GetConnectTimeout global.serverConnector.connectTimeout
//与server的连接超时时间
func (s *ServerConnectorConfigImpl) GetConnectTimeout() time.Duration {
	return *s.ConnectTimeout
}

//设置与server的连接超时时间
func (s *ServerConnectorConfigImpl) SetConnectTimeout(timeout time.Duration) {
	s.ConnectTimeout = &timeout
}

//GetMessageTimeout global.serverConnector.messageTimeout
//远程请求超时时间
func (s *ServerConnectorConfigImpl) GetMessageTimeout() time.Duration {
	return *s.MessageTimeout
}

//设置远程请求超时时间
func (s *ServerConnectorConfigImpl) SetMessageTimeout(timeout time.Duration) {
	s.MessageTimeout = &timeout
}

//GetConnectionExpireInterval global.serverConnector.connectionIdleTimeout
//连接空闲后超时时间
func (s *ServerConnectorConfigImpl) GetConnectionIdleTimeout() time.Duration {
	return *s.ConnectionIdleTimeout
}

//设置连接空闲后超时时间
func (s *ServerConnectorConfigImpl) SetConnectionIdleTimeout(timeout time.Duration) {
	s.ConnectionIdleTimeout = &timeout
}

//GetClientRequestQueueSize global.serverConnector.requestQueueSize
// 新请求的队列BUFFER容量
func (s *ServerConnectorConfigImpl) GetRequestQueueSize() int32 {
	return *s.RequestQueueSize
}

// 设置新请求的队列BUFFER容量
func (s *ServerConnectorConfigImpl) SetRequestQueueSize(queueSize int32) {
	s.RequestQueueSize = &queueSize
}

//GetServerSwitchInterval global.serverConnector.serverSwitchInterval
// server的切换时延
func (s *ServerConnectorConfigImpl) GetServerSwitchInterval() time.Duration {
	return *s.ServerSwitchInterval
}

// server的切换时延
func (s *ServerConnectorConfigImpl) SetServerSwitchInterval(interval time.Duration) {
	s.ServerSwitchInterval = &interval
}

//一次连接失败后，到下一次连接之间的最小间隔时间
func (s *ServerConnectorConfigImpl) GetReconnectInterval() time.Duration {
	return *s.ReconnectInterval
}

//一次连接失败后，到下一次连接之间的最小间隔时间
func (s *ServerConnectorConfigImpl) SetReconnectInterval(interval time.Duration) {
	s.ReconnectInterval = &interval
}

//GetPluginConfig global.serverConnector.plugin
func (s *ServerConnectorConfigImpl) GetPluginConfig(pluginName string) BaseConfig {
	cfgValue, ok := s.Plugin[pluginName]
	if !ok {
		return nil
	}
	return cfgValue.(BaseConfig)
}

//输出插件具体配置
func (s *ServerConnectorConfigImpl) SetPluginConfig(pluginName string, value BaseConfig) error {
	return s.Plugin.SetPluginConfig(common.TypeServerConnector, pluginName, value)
}

//检验ServerConnector配置
func (s *ServerConnectorConfigImpl) Verify() error {
	if nil == s {
		return errors.New("ServerConnectorConfig is nil")
	}
	var errs error
	if len(s.Addresses) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("global.serverConnector.addresses is empty"))
	}
	if nil != s.RequestQueueSize && *s.RequestQueueSize < 0 {
		errs = multierror.Append(errs,
			fmt.Errorf("global.serverConnector.requestQueueSize %v is invalid", s.RequestQueueSize))
	}
	if *s.ConnectionIdleTimeout < DefaultMinTimingInterval {
		errs = multierror.Append(errs,
			fmt.Errorf("global.serverConnector.connectionIdleTimeout %v"+
				" is less than  minimal timing interval %v",
				*s.ConnectionIdleTimeout, DefaultMinTimingInterval))
	}
	if *s.ServerSwitchInterval <= *s.ConnectionIdleTimeout {
		errs = multierror.Append(errs,
			fmt.Errorf("global.serverConnector.serverSwitchInterval %v"+
				" is less than or equal to global.serverConnector.connectionIdleTimeout %v",
				*s.ServerSwitchInterval, *s.ConnectionIdleTimeout))
	}
	return errs
}

//设置ServerConnector配置的默认值
func (s *ServerConnectorConfigImpl) SetDefault() {
	if nil == s.ConnectTimeout {
		s.ConnectTimeout = model.ToDurationPtr(DefaultServerConnectTimeout)
	}
	if nil == s.MessageTimeout {
		s.MessageTimeout = model.ToDurationPtr(DefaultServerMessageTimeout)
	}
	if nil == s.ConnectionIdleTimeout {
		s.ConnectionIdleTimeout = model.ToDurationPtr(DefaultServerConnectionIdleTimeout)
	}
	if nil == s.ServerSwitchInterval {
		s.ServerSwitchInterval = model.ToDurationPtr(DefaultServerSwitchInterval)
	}
	if nil == s.RequestQueueSize {
		s.RequestQueueSize = proto.Int(DefaultRequestQueueSize)
	}
	if nil == s.ReconnectInterval {
		s.ReconnectInterval = model.ToDurationPtr(DefaultReConnectInterval)
	}
	if len(s.Protocol) == 0 {
		s.Protocol = DefaultServerConnector
	}
	s.Plugin.SetDefault(common.TypeServerConnector)
}

//配置初始化
func (s *ServerConnectorConfigImpl) Init() {
	s.Plugin = PluginConfigs{}
	s.Plugin.Init(common.TypeServerConnector)
}
