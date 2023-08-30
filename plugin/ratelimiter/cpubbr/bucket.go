package cpubbr

import (
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/plugin/ratelimiter"
	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"

	aegislimiter "github.com/go-kratos/aegis/ratelimit"
	"github.com/go-kratos/aegis/ratelimit/bbr"
)

type CpuBbrQuota struct {
	aegislimiter.Limiter
}

// GetQuota 获取限额
func (b *CpuBbrQuota) GetQuota(curTimeMs int64, token uint32) *model.QuotaResponse {
	// 默认返回 ok
	resp := &model.QuotaResponse{Code: model.QuotaResultOk}

	// 如果触发限流，err 值将等于 aegislimiter.ErrLimitExceed
	done, err := b.Limiter.Allow()
	if err != nil {
		resp.Code = model.QuotaResultLimited
		return resp
	}

	// 返回函数执行
	done(aegislimiter.DoneInfo{})

	return resp
}

// Release 释放资源
func (b *CpuBbrQuota) Release() {}

// OnRemoteUpdate 远端更新的时候通知，cpu限流策略是本地模式，不用实现
func (b *CpuBbrQuota) OnRemoteUpdate(result ratelimiter.RemoteQuotaResult) {

}

// GetQuotaUsed 返回本地限流信息用于上报，cpu限流策略本地模式，不用实现
func (b *CpuBbrQuota) GetQuotaUsed(curTimeMilli int64) ratelimiter.UsageInfo {
	return ratelimiter.UsageInfo{}
}

// GetAmountInfos 获取规则的限流阈值信息，用于与服务端pb交互
func (b *CpuBbrQuota) GetAmountInfos() []ratelimiter.AmountInfo {
	return nil
}

// createCpuBbrLimiter 初始化一个CPU策略桶
func createCpuBbrLimiter(amount *apitraffic.SystemResourceAmount) *CpuBbrQuota {
	cpuQuota := &CpuBbrQuota{}

	var options []bbr.Option

	// 统计时间窗口，默认 10s
	window, _ := pb.ConvertDuration(amount.GetWindow())
	if window > 0 {
		options = append(options, bbr.WithWindow(window))
	}
	// 观测时间窗口内 计数桶 的个数，默认100个
	precision := int(amount.GetPrecision().GetValue())
	if precision > 0 {
		options = append(options, bbr.WithBucket(precision))
	}
	// CPU使用率阈值，默认80%
	threshold := int64(amount.GetThreshold().GetValue() * 1000)
	if threshold > 0 {
		options = append(options, bbr.WithCPUThreshold(threshold))
	}
	cpuQuota.Limiter = bbr.NewLimiter(options...)
	return cpuQuota
}
