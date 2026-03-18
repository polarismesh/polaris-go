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
	"net"
	"sync"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

// Ensure AdminConfigImpl implements AdminConfig
var _ AdminConfig = (*AdminConfigImpl)(nil)

// AdminConfigImpl Admin配置实现
type AdminConfigImpl struct {
	// Host Admin监听的IP地址
	Host string `yaml:"host" json:"host"`
	// Port Admin监听的端口
	Port int `yaml:"port" json:"port"`
	// Type admin 监听协议类型
	Type string `yaml:"type" json:"type"`
	// Plugin 插件配置反序列化后的对象
	Plugin PluginConfigs `yaml:"plugin" json:"plugin"`
	// Handlers 注册的路径
	Handlers []model.AdminHandler `yaml:"-" json:"-"`
	// mu 保护 Handlers 的并发访问
	mu *sync.RWMutex `yaml:"-" json:"-"`
	// muOnce 确保 mu 只初始化一次
	muOnce sync.Once `yaml:"-" json:"-"`
}

// GetHost 获取Admin监听的IP地址
func (a *AdminConfigImpl) GetHost() string {
	if a == nil {
		return DefaultAdminHost
	}
	return a.Host
}

// SetHost 设置Admin监听的IP地址
func (a *AdminConfigImpl) SetHost(host string) {
	a.Host = host
}

// GetPort 获取Admin监听的端口
func (a *AdminConfigImpl) GetPort() int {
	if a == nil {
		return DefaultAdminPort
	}
	return a.Port
}

// SetPort 设置Admin监听的端口
func (a *AdminConfigImpl) SetPort(port int) {
	a.Port = port
}

// GetType 负载均衡类型
func (a *AdminConfigImpl) GetType() string {
	return a.Type
}

// SetType 设置负载均衡类型
func (a *AdminConfigImpl) SetType(typ string) {
	a.Type = typ
}

func (a *AdminConfigImpl) RegisterPath(adminHandler model.AdminHandler) {
	a.ensureMutex()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Handlers = append(a.Handlers, adminHandler)
}

// ensureMutex 确保互斥锁已初始化（懒加载，并发安全）
func (a *AdminConfigImpl) ensureMutex() {
	a.muOnce.Do(func() {
		a.mu = &sync.RWMutex{}
	})
}

// GetPaths 获取已注册的路径（并发安全）
func (a *AdminConfigImpl) GetPaths() []model.AdminHandler {
	a.ensureMutex()
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.Handlers
}

// GetPluginConfig 获取插件配置
func (a *AdminConfigImpl) GetPluginConfig(name string) BaseConfig {
	value, ok := a.Plugin[name]
	if !ok {
		return nil
	}
	return value.(BaseConfig)
}

// SetPluginConfig 设置插件配置
func (a *AdminConfigImpl) SetPluginConfig(plugName string, value BaseConfig) error {
	return a.Plugin.SetPluginConfig(common.TypeAdmin, plugName, value)
}

// Verify 校验配置参数
func (a *AdminConfigImpl) Verify() error {
	if a == nil {
		return errors.New("AdminConfig is nil")
	}

	// 校验Host是否是合理的TCP端口监听地址
	if a.Host != "" {
		if ip := net.ParseIP(a.Host); ip == nil {
			return errors.New("admin.host must be a valid IP address")
		}
	}

	if a.Port < 0 || a.Port > 65535 {
		return errors.New("admin.port must be between 0 and 65535")
	}
	return nil
}

// SetDefault 设置默认参数
func (a *AdminConfigImpl) SetDefault() {
	if a.Host == "" {
		a.Host = DefaultAdminHost
	}
	if a.Port == 0 {
		a.Port = DefaultAdminPort
	}
	if a.Type == "" {
		a.Type = DefaultAdminType
	}
	a.Plugin.SetDefault(common.TypeAdmin)
}

// Init 配置初始化
func (a *AdminConfigImpl) Init() {
	a.Plugin = PluginConfigs{}
	a.Plugin.Init(common.TypeAdmin)
	a.mu = &sync.RWMutex{}
}
