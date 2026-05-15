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

package api

import (
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

// authAPI 鉴权 API 实现
type authAPI struct {
	context SDKContext
}

// SDKContext 获取 SDK 上下文
func (c *authAPI) SDKContext() SDKContext {
	return c.context
}

// Authenticate 执行鉴权
func (c *authAPI) Authenticate(request *AuthenticateRequest) (*model.AuthenticateResponse, error) {
	if err := checkAvailable(c); err != nil {
		return nil, err
	}
	if request == nil {
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil, "AuthenticateRequest can not be nil")
	}
	if err := request.Validate(); err != nil {
		return nil, err
	}
	return c.context.GetEngine().SyncAuthenticate(&request.AuthenticateRequest)
}

// Destroy 销毁 API
func (c *authAPI) Destroy() {
	if nil != c.context {
		c.context.Destroy()
	}
}

// newAuthAPI 通过以默认域名为埋点 server 的默认配置创建 AuthAPI
func newAuthAPI() (AuthAPI, error) {
	return newAuthAPIByConfig(config.NewDefaultConfigurationWithDomain())
}

// newAuthAPIByConfig 通过配置对象创建 AuthAPI
func newAuthAPIByConfig(cfg config.Configuration) (AuthAPI, error) {
	context, err := InitContextByConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &authAPI{context}, nil
}

// newAuthAPIByContext 通过上下文创建 AuthAPI
func newAuthAPIByContext(context SDKContext) AuthAPI {
	return &authAPI{context}
}

// newAuthAPIByFile 通过配置文件创建 AuthAPI
func newAuthAPIByFile(path string) (AuthAPI, error) {
	context, err := InitContextByFile(path)
	if err != nil {
		return nil, err
	}
	return &authAPI{context: context}, nil
}

// newAuthAPIByAddress 通过 address 创建 AuthAPI
func newAuthAPIByAddress(address ...string) (AuthAPI, error) {
	conf := config.NewDefaultConfiguration(address)
	return newAuthAPIByConfig(conf)
}
