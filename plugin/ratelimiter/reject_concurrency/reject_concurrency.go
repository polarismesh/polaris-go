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

// Package rejectconcurrency 提供并发数限流插件，实现纯本地的并发计数语义。
// 当限流规则 Rule.Resource == CONCURRENCY 时由框架选用本插件，无远程同步依赖。
//
// 目录名 reject_concurrency 带下划线（与同目录下的 reject、unirate 模块命名风格保持一致），
// 但 Go 包名不允许下划线，故包名收敛为 rejectconcurrency.
package rejectconcurrency

import (
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/ratelimiter"
)

// RateLimiterRejectConcurrency 基于本地并发数计数的限流控制器
type RateLimiterRejectConcurrency struct {
	*plugin.PluginBase
	logCtx *log.ContextLogger
}

// Type 插件类型
func (g *RateLimiterRejectConcurrency) Type() common.Type {
	return common.TypeRateLimiter
}

// Name 插件名，一个类型下插件名唯一
func (g *RateLimiterRejectConcurrency) Name() string {
	return config.DefaultConcurrencyRateLimiter
}

// Init 初始化插件
func (g *RateLimiterRejectConcurrency) Init(ctx *plugin.InitContext) error {
	g.PluginBase = plugin.NewPluginBase(ctx)
	g.logCtx = ctx.ValueCtx.GetContextLogger()
	return nil
}

// Destroy 销毁插件，可用于释放资源
func (g *RateLimiterRejectConcurrency) Destroy() error {
	return nil
}

// IsEnable enable ?
func (g *RateLimiterRejectConcurrency) IsEnable(cfg config.Configuration) bool {
	return cfg.GetGlobal().GetSystem().GetMode() != model.ModeWithAgent
}

// InitQuota 初始化并创建并发数限流窗口
// 主流程会在首次调用，以及规则对象变更的时候，调用该方法
func (g *RateLimiterRejectConcurrency) InitQuota(criteria *ratelimiter.InitCriteria) ratelimiter.QuotaBucket {
	return NewConcurrencyQuotaBucket(criteria.DstRule, criteria.WindowKey, g.logCtx)
}

// init 注册插件
func init() {
	plugin.RegisterPlugin(&RateLimiterRejectConcurrency{})
}
