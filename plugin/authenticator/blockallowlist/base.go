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

// Package blockallowlist 提供基于黑白名单规则的服务鉴权插件实现，对应
// polaris-java 的 BlockAllowListAuthenticator。
package blockallowlist

import (
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/sdk"
)

// PluginName 插件名称
const PluginName = "blockAllowList"

// BlockAllowListAuthenticator 黑白名单服务鉴权插件
type BlockAllowListAuthenticator struct {
	*plugin.PluginBase
	pluginCtx *plugin.InitContext
	engine    sdk.Engine
	log       log.Logger
}

// Type 插件类型
func (p *BlockAllowListAuthenticator) Type() common.Type {
	return common.TypeAuthenticator
}

// Name 插件名
func (p *BlockAllowListAuthenticator) Name() string {
	return PluginName
}

// Init 初始化插件
func (p *BlockAllowListAuthenticator) Init(ctx *plugin.InitContext) error {
	p.PluginBase = plugin.NewPluginBase(ctx)
	p.pluginCtx = ctx
	p.log = ctx.ValueCtx.GetContextLogger().GetBaseLogger()
	return nil
}

// SetEngine 由 Proxy 在 SetRealPlugin 时注入 Engine 引用，
// 用于按需调用 SyncGetServiceRule 拉取规则。实现 authenticator.EngineSetter 接口。
func (p *BlockAllowListAuthenticator) SetEngine(engine sdk.Engine) {
	p.engine = engine
}

// Destroy 销毁插件
func (p *BlockAllowListAuthenticator) Destroy() error {
	return nil
}

// init 注册插件
func init() {
	plugin.RegisterConfigurablePlugin(&BlockAllowListAuthenticator{}, &Config{})
}
