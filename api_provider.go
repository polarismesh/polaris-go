/**
 * Tencent is pleased to support the open source community by making Polaris available.
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

package polaris

import (
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

// providerAPI 调用者对外函数实现
type providerAPI struct {
	rawAPI api.ProviderAPI
}

// SDKContext 获取SDK上下文
func (p *providerAPI) SDKContext() api.SDKContext {
	return p.rawAPI.SDKContext()
}

// RegisterInstance
// minimum supported version of polaris-server is v1.10.0
func (p *providerAPI) RegisterInstance(instance *InstanceRegisterRequest) (*model.InstanceRegisterResponse, error) {
	return p.rawAPI.RegisterInstance((*api.InstanceRegisterRequest)(instance))
}

// Register
// 同步注册服务，服务注册成功后会填充instance中的InstanceID字段
// 用户可保持该instance对象用于反注册和心跳上报
func (p *providerAPI) Register(instance *InstanceRegisterRequest) (*model.InstanceRegisterResponse, error) {
	return p.rawAPI.Register((*api.InstanceRegisterRequest)(instance))
}

// Deregister synchronize the anti registration service
func (p *providerAPI) Deregister(instance *InstanceDeRegisterRequest) error {
	return p.rawAPI.Deregister((*api.InstanceDeRegisterRequest)(instance))
}

// Heartbeat the heartbeat report
func (p *providerAPI) Heartbeat(instance *InstanceHeartbeatRequest) error {
	return p.rawAPI.Heartbeat((*api.InstanceHeartbeatRequest)(instance))
}

// Destroy the api is destroyed and cannot be called again
func (p *providerAPI) Destroy() {
	p.rawAPI.Destroy()
}

// NewProviderAPI 通过以默认域名为埋点server的默认配置创建ProviderAPI
func NewProviderAPI() (ProviderAPI, error) {
	p, err := api.NewProviderAPI()
	if err != nil {
		return nil, err
	}
	return &providerAPI{rawAPI: p}, nil
}

// NewProviderAPIByFile 通过配置文件创建SDK ProviderAPI对象
func NewProviderAPIByFile(path string) (ProviderAPI, error) {
	p, err := api.NewProviderAPIByFile(path)
	if err != nil {
		return nil, err
	}
	return &providerAPI{rawAPI: p}, nil
}

// NewProviderAPIByConfig 通过配置对象创建SDK ProviderAPI对象
func NewProviderAPIByConfig(cfg config.Configuration) (ProviderAPI, error) {
	p, err := api.NewProviderAPIByConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &providerAPI{rawAPI: p}, nil
}

// NewProviderAPIByContext 通过上下文创建SDK ProviderAPI对象
func NewProviderAPIByContext(context api.SDKContext) ProviderAPI {
	p := api.NewProviderAPIByContext(context)
	return &providerAPI{rawAPI: p}
}

// NewProviderAPIByAddress 通过address创建ProviderAPI
func NewProviderAPIByAddress(address ...string) (ProviderAPI, error) {
	p, err := api.NewProviderAPIByAddress(address...)
	if err != nil {
		return nil, err
	}
	return &providerAPI{rawAPI: p}, nil
}
