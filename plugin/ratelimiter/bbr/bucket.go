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

package bbr

import (
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/ratelimiter"
	"github.com/polarismesh/polaris-go/plugin/ratelimiter/bbr/core"
	"sort"

	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"
)

var (
	denyResp = &model.QuotaResponse{
		Code: model.QuotaResultLimited,
	}
)

// BBRQuotaBucket 实现 BBRQuotaBucket 接口的结构体
type BBRQuotaBucket struct {
	BBR *core.BBR
}

// GetQuota 获取限额
func (b *BBRQuotaBucket) GetQuota(_ int64, _ uint32) *model.QuotaResponse {
	release, allow := b.BBR.Allow()
	if allow {
		return &model.QuotaResponse{
			Code:         model.QuotaResultOk,
			ReleaseFuncs: []model.ReleaseFunc{release},
		}
	}
	return denyResp
}

// Release 释放配额（仅对于并发数限流有用）
func (l *BBRQuotaBucket) Release() {

}

// OnRemoteUpdate 远端更新的时候通知。CPU限流为单机限流策略，不实现该函数
func (b *BBRQuotaBucket) OnRemoteUpdate(_ ratelimiter.RemoteQuotaResult) {

}

// GetQuotaUsed 返回本地限流信息用于上报
func (b *BBRQuotaBucket) GetQuotaUsed(_ int64) ratelimiter.UsageInfo {
	return ratelimiter.UsageInfo{}
}

// GetAmountInfos 获取规则的限流阈值信息，用于与服务端pb交互
func (b *BBRQuotaBucket) GetAmountInfos() []ratelimiter.AmountInfo {
	return nil
}

// createBBRPlugin 初始化
func createBBRPlugin(rule *apitraffic.Rule) *BBRQuotaBucket {
	options := make([]core.Option, 0)

	if amounts := rule.GetAmounts(); len(amounts) > 0 {
		// 如果有多条规则：
		// 1. 先按CPU阈值比较，阈值小的生效
		// 2. 阈值相同时，按时间窗口比较，窗口小的生效
		// 3. 窗口也相同时，按精度比较，精度大的生效（polaris-server 做了校验，不会出现窗口相同的情况。这里也可以不用判断）
		sort.Slice(amounts, func(i, j int) bool {
			a, b := amounts[i], amounts[j]
			threshold1, threshold2 := a.GetMaxAmount().GetValue(), b.GetMaxAmount().GetValue()
			window1, window2 := a.GetValidDuration().AsDuration(), b.GetValidDuration().AsDuration()
			precision1, precision2 := a.GetPrecision().GetValue(), b.GetPrecision().GetValue()

			if threshold1 == threshold2 {
				if window1 == window2 {
					return precision1 > precision2
				}
				return window1 < window2
			}
			return threshold1 < threshold2
		})

		amount := amounts[0]

		// CPU使用率阈值，默认80%
		if threshold := amount.GetMaxAmount().GetValue(); threshold > 0 {
			// bbr 的参数为 800‰ 的形式，需要从 rule 中的百分号转到千分号，因此这里乘10
			options = append(options, core.WithCPUThreshold(int64(threshold*10)))
		}
		// 统计时间窗口，默认 10s
		if window := amount.GetValidDuration().AsDuration(); window > 0 {
			options = append(options, core.WithWindow(window))
		}
		// 观测时间窗口内 计数桶 的个数（控制滑动窗口精度），默认100个
		// 如 window=1s, bucket=10 时，整个滑动窗口用来保存最近 1s 的采样数据，每个小的桶用来保存 100ms 的采样数据。当时间流动之后，过期的桶会自动被新桶的数据覆盖掉
		if precision := amount.GetPrecision().GetValue(); precision > 0 {
			options = append(options, core.WithBucket(int(precision)))
		}
	}

	return &BBRQuotaBucket{
		BBR: core.NewLimiter(options...),
	}
}
