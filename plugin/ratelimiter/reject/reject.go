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

package reject

import (
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/ratelimiter"
)

//基于直接拒绝策略的限流控制器
type RateLimiterReject struct {
	*plugin.PluginBase
}

//Type 插件类型
func (g *RateLimiterReject) Type() common.Type {
	return common.TypeRateLimiter
}

//Name 插件名，一个类型下插件名唯一
func (g *RateLimiterReject) Name() string {
	return config.DefaultRejectRateLimiter
}

//Init 初始化插件
func (g *RateLimiterReject) Init(ctx *plugin.InitContext) error {
	g.PluginBase = plugin.NewPluginBase(ctx)
	return nil
}

//Destroy 销毁插件，可用于释放资源
func (g *RateLimiterReject) Destroy() error {
	return nil
}

// enable
func (g *RateLimiterReject) IsEnable(cfg config.Configuration) bool {
	if cfg.GetGlobal().GetSystem().GetMode() == model.ModeWithAgent {
		return false
	} else {
		return true
	}
}

//空实现的bucket
type DoNothingBucket struct {
}

//在令牌桶/漏桶中进行单个配额的划扣，并返回本次分配的结果
func (d *DoNothingBucket) GetQuota() (*ratelimiter.QuotaResult, error) {
	return &ratelimiter.QuotaResult{Code: model.QuotaResultOk}, nil
}

//释放配额（仅对于并发数限流有用）
func (d *DoNothingBucket) Release() {

}

//初始化并创建限流窗口
//主流程会在首次调用，以及规则对象变更的时候，调用该方法
func (g *RateLimiterReject) InitQuota(criteria *ratelimiter.InitCriteria) (ratelimiter.QuotaBucket, error) {
	return &DoNothingBucket{}, nil
}

//init 注册插件
func init() {
	plugin.RegisterPlugin(&RateLimiterReject{})
}
