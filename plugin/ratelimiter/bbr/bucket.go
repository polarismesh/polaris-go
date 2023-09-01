package bbr

import (
	aegislimiter "github.com/go-kratos/aegis/ratelimit"
	"github.com/go-kratos/aegis/ratelimit/bbr"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/plugin/ratelimiter"
	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"
)

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
		var amount = amounts[0]
		var threshold = amount.GetMaxAmount().GetValue()

		// 有多条规则时，只允许cpu使用率最低的规则生效
		for i := 1; i < len(amounts); i++ {
			if curThreshold := amounts[i].GetMaxAmount().GetValue(); curThreshold < threshold {
				threshold = curThreshold
				amount = amounts[i]
			}
		}
		// CPU使用率阈值，默认80%
		if threshold > 0 {
			// bbr 的参数为 800‰ 的形式，要从 rule 中的百分号转到千分号，乘10
			options = append(options, bbr.WithCPUThreshold(int64(threshold*10)))
		}
		// 统计时间窗口，默认 10s
		if window, _ := pb.ConvertDuration(amount.GetValidDuration()); window > 0 {
			options = append(options, bbr.WithWindow(window))
		}
		// 观测时间窗口内 计数桶 的个数（控制滑动窗口精度），默认100个
		if precision := amount.GetPrecision().GetValue(); precision > 0 {
			options = append(options, bbr.WithBucket(int(precision)))
		}
	}

	return &BbrQuotaBucket{
		bbr.NewLimiter(options...),
	}
}
