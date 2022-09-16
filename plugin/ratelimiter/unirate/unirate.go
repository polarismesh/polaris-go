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

package unirate

import (
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/ratelimiter"
)

// RateLimiterUniformRate 基于匀速排队策略的限流控制器
type RateLimiterUniformRate struct {
	*plugin.PluginBase
	cfg *Config
}

// Type 插件类型
func (g *RateLimiterUniformRate) Type() common.Type {
	return common.TypeRateLimiter
}

// Name 插件名，一个类型下插件名唯一
func (g *RateLimiterUniformRate) Name() string {
	return config.DefaultUniformRateLimiter
}

// Init 初始化插件
func (g *RateLimiterUniformRate) Init(ctx *plugin.InitContext) error {
	g.PluginBase = plugin.NewPluginBase(ctx)
	cfgValue := ctx.Config.GetProvider().GetRateLimit().GetPluginConfig(g.Name())
	if cfgValue != nil {
		g.cfg = cfgValue.(*Config)
	}
	return nil
}

// Destroy 销毁插件，可用于释放资源
func (g *RateLimiterUniformRate) Destroy() error {
	return nil
}

// IsEnable enable ?
func (g *RateLimiterUniformRate) IsEnable(cfg config.Configuration) bool {
	return cfg.GetGlobal().GetSystem().GetMode() != model.ModeWithAgent
}

// InitQuota 初始化并创建限流窗口
// 主流程会在首次调用，以及规则对象变更的时候，调用该方法
func (g *RateLimiterUniformRate) InitQuota(criteria *ratelimiter.InitCriteria) ratelimiter.QuotaBucket {
	return createLeakyBucket(criteria, g.cfg)
}

// init 注册插件
func init() {
	plugin.RegisterConfigurablePlugin(&RateLimiterUniformRate{}, &Config{})
}
