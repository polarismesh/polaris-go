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

package authenticator

import (
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/sdk"
)

// Proxy 鉴权插件代理对象，注入 Engine 引用以便插件实现按需调用 SyncGetServiceRule
// 等流程能力。
type Proxy struct {
	Authenticator
	engine sdk.Engine
}

// SetRealPlugin 设置代理的真实插件并注入 Engine 引用
func (p *Proxy) SetRealPlugin(plug plugin.Plugin, engine sdk.Engine) {
	p.Authenticator = plug.(Authenticator)
	p.engine = engine
	if engineSetter, ok := plug.(EngineSetter); ok {
		engineSetter.SetEngine(engine)
	}
}

// Authenticate 透传调用真实插件的 Authenticate 方法
func (p *Proxy) Authenticate(info *AuthInfo) *AuthResult {
	return p.Authenticator.Authenticate(info)
}

// EngineSetter 鉴权插件可选实现的接口，用于接收 Engine 引用，
// 方便插件实现按需调用流程编排能力（例如 SyncGetServiceRule）。
type EngineSetter interface {
	SetEngine(engine sdk.Engine)
}

// init 注册 Proxy
func init() {
	plugin.RegisterPluginProxy(common.TypeAuthenticator, &Proxy{})
}
