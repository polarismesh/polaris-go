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
	"github.com/polarismesh/polaris-go/pkg/model"
)

// AuthenticateRequest 鉴权请求，封装 model.AuthenticateRequest 以保留扩展空间
type AuthenticateRequest struct {
	model.AuthenticateRequest
}

// AuthAPI 服务鉴权 API 接口
type AuthAPI interface {
	SDKOwner
	// Authenticate 执行鉴权，按 provider.auth.chain 顺序调用各鉴权插件
	Authenticate(request *AuthenticateRequest) (*model.AuthenticateResponse, error)
	// Destroy 销毁 API，销毁后无法再进行调用
	Destroy()
}

var (
	// NewAuthAPI 通过以默认域名为埋点 server 的默认配置创建 AuthAPI
	NewAuthAPI = newAuthAPI
	// NewAuthAPIByConfig 通过配置对象创建 AuthAPI
	NewAuthAPIByConfig = newAuthAPIByConfig
	// NewAuthAPIByContext 通过 sdkContext 创建 AuthAPI
	NewAuthAPIByContext = newAuthAPIByContext
	// NewAuthAPIByFile 通过配置文件创建 AuthAPI
	NewAuthAPIByFile = newAuthAPIByFile
	// NewAuthAPIByAddress 通过 address 创建 AuthAPI
	NewAuthAPIByAddress = newAuthAPIByAddress
)
