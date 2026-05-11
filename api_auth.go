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

package polaris

import (
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

// authAPI 鉴权 API 顶层包装
type authAPI struct {
	rawAPI api.AuthAPI
}

// SDKContext 获取 SDK 上下文
func (c *authAPI) SDKContext() api.SDKContext {
	return c.rawAPI.SDKContext()
}

// Authenticate 执行鉴权
func (c *authAPI) Authenticate(request *AuthenticateRequest) (*model.AuthenticateResponse, error) {
	return c.rawAPI.Authenticate((*api.AuthenticateRequest)(request))
}

// Destroy 销毁 API，销毁后无法再进行调用
func (c *authAPI) Destroy() {
	c.rawAPI.Destroy()
}

// NewAuthAPI 通过以默认域名为埋点 server 的默认配置创建 AuthAPI
func NewAuthAPI() (AuthAPI, error) {
	a, err := api.NewAuthAPI()
	if err != nil {
		return nil, err
	}
	return &authAPI{rawAPI: a}, nil
}

// NewAuthAPIByConfig 通过配置对象创建 SDK AuthAPI 对象
func NewAuthAPIByConfig(cfg config.Configuration) (AuthAPI, error) {
	a, err := api.NewAuthAPIByConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &authAPI{rawAPI: a}, nil
}

// NewAuthAPIByContext 通过上下文创建 SDK AuthAPI 对象
func NewAuthAPIByContext(context api.SDKContext) AuthAPI {
	a := api.NewAuthAPIByContext(context)
	return &authAPI{rawAPI: a}
}

// NewAuthAPIByFile 通过配置文件创建 SDK AuthAPI 对象
func NewAuthAPIByFile(path string) (AuthAPI, error) {
	a, err := api.NewAuthAPIByFile(path)
	if err != nil {
		return nil, err
	}
	return &authAPI{rawAPI: a}, nil
}

// NewAuthAPIByAddress 通过地址创建 SDK AuthAPI 对象
func NewAuthAPIByAddress(address ...string) (AuthAPI, error) {
	a, err := api.NewAuthAPIByAddress(address...)
	if err != nil {
		return nil, err
	}
	return &authAPI{rawAPI: a}, nil
}
