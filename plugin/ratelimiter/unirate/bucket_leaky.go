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
	"fmt"
	"math"
	"sync/atomic"
	"time"

	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/plugin/ratelimiter"
)

// logTag unirate 插件限流日志统一前缀；与 reject/reject_concurrency 形成对照.
const logTag = "[RateLimit][Unirate]"

// ruleID 返回规则的标识，仅用于日志可读性（与同包其他实现保持独立）.
func ruleID(rule *apitraffic.Rule) string {
	if rule == nil {
		return ""
	}
	if id := rule.GetId().GetValue(); id != "" {
		return id
	}
	return rule.GetName().GetValue()
}

// LeakyBucket 远程配额分配的算法桶
type LeakyBucket struct {
	rule *apitraffic.Rule
	// 上次分配配额的时间戳
	lastGrantTime int64
	// 等效配额
	effectiveAmount uint32
	// 等效时间窗
	effectiveDuration time.Duration
	// 为一个实例生成一个配额的平均时间
	effectiveRate int64
	// 所有实例分配一个配额的平均时间
	totalRate float64
	// 最大排队时间
	maxQueuingDuration int64
	// 是不是有amount为0
	rejectAll bool
	// logCtx 上下文 logger，由插件 Init 阶段透传；不应为 nil（与 reject/reject_concurrency 一致）.
	// 直接调用 logCtx.GetRateLimitLogger().Xxxf 以保证 zap.AddCallerSkip 假设的栈深度成立.
	logCtx *log.ContextLogger
}

func createLeakyBucket(criteria *ratelimiter.InitCriteria, cfg *Config, logCtx *log.ContextLogger) *LeakyBucket {
	bucket := &LeakyBucket{logCtx: logCtx}
	bucket.rule = criteria.DstRule

	instCount := 1
	effective := false
	effectiveRate := 0.0
	bucket.maxQueuingDuration = cfg.MaxQueuingTime.Milliseconds()
	if bucket.rule.GetMaxQueueDelay().GetValue() > 0 {
		bucket.maxQueuingDuration = int64(bucket.rule.GetMaxQueueDelay().GetValue()) * 1000
	}
	var maxDuration time.Duration
	for _, a := range bucket.rule.Amounts {
		if a.MaxAmount.GetValue() == 0 {
			bucket.rejectAll = true
			// rule.amount=0 等价于"全部拒绝"，多半是控制面误配；用 warn 让运维能看见，
			// 与 reject_concurrency 对 maxAmount<=0 的 warn 处理保持一致.
			logCtx.GetRateLimitLogger().Warnf(
				"%s created bucket windowKey=%q rule[%s] rejectAll=true (amount=0, all requests will be rejected)",
				logTag, criteria.WindowKey, ruleID(bucket.rule))
			return bucket
		}
		duration, _ := pb.ConvertDuration(a.ValidDuration)
		// 选出允许qps最低的amount和duration组合，作为effectiveAmount和effectiveDuration
		// 在匀速排队限流器里面，就是每个请求都要间隔同样的时间，
		// 如限制1s 10个请求，那么每个请求只有在上个请求允许过去100ms后才能通过下一个请求
		// 这种机制下面，那么在多个amount组合里面，只要允许qps最低的组合生效，那么所有限制都满足了
		if !effective {
			bucket.effectiveAmount = a.MaxAmount.GetValue()
			bucket.effectiveDuration = duration
			maxDuration = duration
			effective = true
			effectiveRate = float64(bucket.effectiveDuration.Milliseconds()) / float64(bucket.effectiveAmount)
		} else {
			newRate := float64(duration) / float64(a.MaxAmount.GetValue())
			if newRate > effectiveRate {
				bucket.effectiveAmount = a.MaxAmount.GetValue()
				bucket.effectiveDuration = duration
				effectiveRate = newRate
			}
			if duration > maxDuration {
				maxDuration = duration
			}
		}
	}
	effectiveRate = float64(bucket.effectiveDuration.Milliseconds()) / float64(bucket.effectiveAmount)
	bucket.totalRate = effectiveRate
	if bucket.rule.Type == apitraffic.Rule_GLOBAL {
		effectiveRate *= float64(instCount)
	}
	bucket.effectiveRate = int64(math.Round(effectiveRate))
	// bucket 生命周期开端用 info 输出一条（与 reject/reject_concurrency 对齐）.
	// windowKey 格式 = "{Service}#{Namespace}[#{Labels}]"，已覆盖 service/namespace/labels；
	// method 与等效阈值（amount/duration/rate/maxQueueing）单独列出便于阅读.
	logCtx.GetRateLimitLogger().Infof(
		"%s created bucket windowKey=%q rule[%s] method=%s type=%s effectiveAmount=%d effectiveDuration=%s effectiveRate=%dms maxQueuingDuration=%dms",
		logTag, criteria.WindowKey, ruleID(bucket.rule),
		bucket.rule.GetMethod().GetValue().GetValue(),
		bucket.rule.GetType().String(),
		bucket.effectiveAmount, bucket.effectiveDuration,
		bucket.effectiveRate, bucket.maxQueuingDuration,
	)
	return bucket
}

func (l *LeakyBucket) allocateQuota() *model.QuotaResponse {
	logger := l.logCtx.GetRateLimitLogger()
	if l.rejectAll {
		if logger.IsLevelEnabled(log.DebugLog) {
			logger.Debugf("%s limited rule[%s] reason=rejectAll",
				logTag, ruleID(l.rule))
		}
		return &model.QuotaResponse{
			Code: model.QuotaResultLimited,
			Info: "uniRate RateLimiter: reject for zero rule amount",
		}
	}
	// 需要多久产生这么请求的配额
	costDuration := atomic.LoadInt64(&l.effectiveRate)

	var waitDuration int64
	for {
		currentTime := model.CurrentMillisecond()
		expectedTime := atomic.AddInt64(&l.lastGrantTime, costDuration)

		waitDuration = expectedTime - currentTime
		if waitDuration >= 0 {
			break
		}
		// 首次访问，尝试更新时间间隔
		if atomic.CompareAndSwapInt64(&l.lastGrantTime, expectedTime, currentTime) {
			// 更新时间成功，此时他是第一个进来的，等待时间归0
			waitDuration = 0
			break
		}
	}

	if waitDuration == 0 {
		if logger.IsLevelEnabled(log.DebugLog) {
			logger.Debugf("%s passed rule[%s] waitMs=0 (no queueing)",
				logTag, ruleID(l.rule))
		}
		return &model.QuotaResponse{
			Code: model.QuotaResultOk,
			Info: "uniRate RateLimiter: grant quota",
		}
	}
	// 如果等待时间在上限之内，那么放通
	if waitDuration <= l.maxQueuingDuration {
		if logger.IsLevelEnabled(log.DebugLog) {
			logger.Debugf("%s queued rule[%s] waitMs=%d maxQueuingMs=%d",
				logTag, ruleID(l.rule), waitDuration, l.maxQueuingDuration)
		}
		return &model.QuotaResponse{
			Code:   model.QuotaResultOk,
			WaitMs: waitDuration,
		}
	}
	// 如果等待时间超过配置的上限，那么拒绝
	// 归还等待间隔
	info := fmt.Sprintf(
		"uniRate RateLimiter: queueing time %d exceed maxQueuingTime %s",
		time.Duration(waitDuration), time.Duration(waitDuration))
	atomic.AddInt64(&l.lastGrantTime, 0-costDuration)
	if logger.IsLevelEnabled(log.DebugLog) {
		logger.Debugf("%s limited rule[%s] reason=queueTimeout waitMs=%d maxQueuingMs=%d",
			logTag, ruleID(l.rule), waitDuration, l.maxQueuingDuration)
	}
	return &model.QuotaResponse{
		Code: model.QuotaResultLimited,
		Info: info,
	}
}

// GetQuota 在令牌桶/漏桶中进行单个配额的划扣，并返回本次分配的结果
func (l *LeakyBucket) GetQuota(curTimeMs int64, token uint32) *model.QuotaResponse {
	return l.allocateQuota()
}

// Release 释放配额（仅对于并发数限流有用）
func (l *LeakyBucket) Release() {

}

// OnRemoteUpdate 远程配额更新
func (l *LeakyBucket) OnRemoteUpdate(remoteQuota ratelimiter.RemoteQuotaResult) {

}

// GetQuotaUsed 拉取本地使用配额情况以供上报
func (l *LeakyBucket) GetQuotaUsed(curTimeMilli int64) ratelimiter.UsageInfo {
	return ratelimiter.UsageInfo{CurTimeMilli: curTimeMilli}
}

// GetAmountInfos 获取规则的限流阈值信息
func (l *LeakyBucket) GetAmountInfos() []ratelimiter.AmountInfo {
	return nil
}
