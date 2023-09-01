package bbr

import (
	aegislimiter "github.com/go-kratos/aegis/ratelimit"
	"github.com/go-kratos/aegis/ratelimit/bbr"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/ratelimiter"
	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"
	"sort"
)

// BbrQuotaBucket 实现 BbrQuotaBucket 接口的结构体
type BbrQuotaBucket struct {
	aegislimiter.Limiter
}

// GetQuota 获取限额
func (b *BbrQuotaBucket) GetQuota(_ int64, _ uint32) *model.QuotaResponse {
	// 如果触发限流，err 值将等于 aegislimiter.ErrLimitExceed
	done, err := b.Limiter.Allow()
	if err != nil {
		return &model.QuotaResponse{
			Code: model.QuotaResultLimited,
		}
	}

	// 如果未触发限流，则执行一些后续函数
	done(aegislimiter.DoneInfo{})

	return &model.QuotaResponse{Code: model.QuotaResultOk}
}

// Release 释放资源
func (b *BbrQuotaBucket) Release() {}

// OnRemoteUpdate 远端更新的时候通知。CPU限流为单机限流策略，不实现该函数
func (b *BbrQuotaBucket) OnRemoteUpdate(_ ratelimiter.RemoteQuotaResult) {

}

// GetQuotaUsed 返回本地限流信息用于上报
func (b *BbrQuotaBucket) GetQuotaUsed(_ int64) ratelimiter.UsageInfo {
	return ratelimiter.UsageInfo{}
}

// GetAmountInfos 获取规则的限流阈值信息，用于与服务端pb交互
func (b *BbrQuotaBucket) GetAmountInfos() []ratelimiter.AmountInfo {
	return nil
}

// createBbrLimiter 初始化
func createBbrLimiter(rule *apitraffic.Rule) *BbrQuotaBucket {
	options := make([]bbr.Option, 0)

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
			// bbr 的参数为 800‰ 的形式，需要从 rule 中的百分号转到千分号，乘10
			options = append(options, bbr.WithCPUThreshold(int64(threshold*10)))
		}
		// 统计时间窗口，默认 10s
		if window := amount.GetValidDuration().AsDuration(); window > 0 {
			options = append(options, bbr.WithWindow(window))
		}
		// 观测时间窗口内 计数桶 的个数（控制滑动窗口精度），默认100个
		// 如 window=1s, bucket=10 时，整个滑动窗口用来保存最近 1s 的采样数据，每个小的桶用来保存 100ms 的采样数据。当时间流动之后，过期的桶会自动被新桶的数据覆盖掉
		if precision := amount.GetPrecision().GetValue(); precision > 0 {
			options = append(options, bbr.WithBucket(int(precision)))
		}
	}

	return &BbrQuotaBucket{
		bbr.NewLimiter(options...),
	}
}
