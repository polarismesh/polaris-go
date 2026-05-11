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

// Package authenticator 定义服务鉴权插件接口。
// 对应 polaris-java 的 PluginTypes.AUTHENTICATOR，按 chain 顺序执行，
// 任一插件返回 Forbidden 即短路拒绝。
package authenticator

import (
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

// AuthCode 鉴权结果码
type AuthCode int

const (
	// AuthResultOk 鉴权通过
	AuthResultOk AuthCode = iota
	// AuthResultForbidden 鉴权拒绝
	AuthResultForbidden
)

// AuthInfo 鉴权输入信息
type AuthInfo struct {
	// Namespace 被调命名空间
	Namespace string
	// Service 被调服务名
	Service string
	// Method 调用方法（HTTP method 或 gRPC method）
	Method string
	// Path 调用路径（HTTP path）
	Path string
	// Protocol 调用协议（HTTP / gRPC 等）
	Protocol string
	// SourceService 主调服务信息（含 Metadata）
	SourceService *model.ServiceInfo
	// Arguments 流量标签参数（复用 Argument 8 维体系，包括 Header/Query/Cookie/Custom/CallerIP 等）
	Arguments []model.Argument
}

// AuthResult 鉴权结果
type AuthResult struct {
	// Code 鉴权结果码
	Code AuthCode
	// Info 鉴权信息描述（拒绝原因等）
	Info string
}

// Authenticator 服务鉴权插件接口
type Authenticator interface {
	plugin.Plugin
	// Authenticate 执行鉴权
	Authenticate(info *AuthInfo) *AuthResult
}

// init 注册插件接口类型
func init() {
	plugin.RegisterPluginInterface(common.TypeAuthenticator, new(Authenticator))
}
