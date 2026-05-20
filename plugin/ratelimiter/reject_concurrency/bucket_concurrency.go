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

package reject_concurrency

import (
	"fmt"
	"sync/atomic"

	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/ratelimiter"
)

// logTag 限流日志统一前缀，便于日志检索过滤.
const logTag = "[RateLimit][Concurrency]"

// ConcurrencyQuotaBucket 并发数限流桶，纯本地模式，对齐 polaris-java 的 ConcurrencyQuotaBucket.
// 通过 atomic 原子计数器维护当前并发数，请求进入时 +1 检查上限，超限则立即 -1 回退；
// 请求通过后由调用方在 finally / defer 中调用 Release 归还配额。
type ConcurrencyQuotaBucket struct {
	// maxAmount 并发上限，来自 Rule.ConcurrencyAmount.MaxAmount
	maxAmount int64
	// currentCount 当前并发计数
	currentCount int64
	// rule 关联的限流规则，用于错误信息和日志
	rule *apitraffic.Rule
	// logCtx 上下文 logger，由插件 Init 阶段透传；不应为 nil。
	// 直接调用 logCtx.GetRateLimitLogger().Xxxf 以保证 zap.AddCallerSkip 假设的栈深度成立，
	// 让日志中显示的 caller 是本文件而不是某个 helper 包装层。
	logCtx *log.ContextLogger
}

// NewConcurrencyQuotaBucket 根据规则创建并发数限流桶.
// logCtx 必须非 nil；与项目内 reject/router 等插件保持一致，不在桶内做 nil 防御.
func NewConcurrencyQuotaBucket(rule *apitraffic.Rule, logCtx *log.ContextLogger) *ConcurrencyQuotaBucket {
	rawAmount := rule.GetConcurrencyAmount().GetMaxAmount()
	maxAmount := int64(rawAmount)
	bucket := &ConcurrencyQuotaBucket{
		rule:   rule,
		logCtx: logCtx,
	}
	if maxAmount <= 0 {
		// 保底设为 1，避免配置为 0 时出现"全部放通"或"全部拒绝"的歧义行为.
		// 此时往往是控制面规则配置错误，需要让运维侧能看到这条信号 -> Warn 级别.
		maxAmount = 1
		logCtx.GetRateLimitLogger().Warnf(
			"%s invalid concurrencyAmount.maxAmount=%d for rule[%s], fallback to 1",
			logTag, rawAmount, ruleID(rule))
	}
	bucket.maxAmount = maxAmount
	logCtx.GetRateLimitLogger().Infof(
		"%s created bucket for rule[%s] service=%s/%s method=%s maxAmount=%d",
		logTag, ruleID(rule),
		rule.GetNamespace().GetValue(), rule.GetService().GetValue(),
		rule.GetMethod().GetValue().GetValue(),
		maxAmount,
	)
	return bucket
}

// GetQuota 尝试分配一个并发配额.
// 实现逻辑：先 Add(1)，若超限则 Add(-1) 回退并返回限流；通过则返回 OK 并注入 release 回调.
//
// 日志策略：通过 / 限流均为热路径，先 IsLevelEnabled 检查再格式化，避免生产 info 级别下产生格式化开销.
func (c *ConcurrencyQuotaBucket) GetQuota(curTimeMs int64, token uint32) *model.QuotaResponse {
	current := atomic.AddInt64(&c.currentCount, 1)
	logger := c.logCtx.GetRateLimitLogger()
	if current > c.maxAmount {
		// 超出并发上限，立即回退并返回限流
		now := atomic.AddInt64(&c.currentCount, -1)
		if logger.IsLevelEnabled(log.DebugLog) {
			logger.Debugf("%s limited rule[%s] current=%d/%d (rolled back to %d)",
				logTag, ruleID(c.rule), current, c.maxAmount, now)
		}
		return &model.QuotaResponse{
			Code: model.QuotaResultLimited,
			Info: fmt.Sprintf("%s:%d", apitraffic.Rule_CONCURRENCY.String(), c.maxAmount),
		}
	}
	if logger.IsLevelEnabled(log.DebugLog) {
		logger.Debugf("%s passed rule[%s] current=%d/%d",
			logTag, ruleID(c.rule), current, c.maxAmount)
	}
	// 通过，返回 OK 并注入 release 回调，由上层在请求完成后归还配额
	resp := &model.QuotaResponse{
		Code: model.QuotaResultOk,
	}
	resp.AddRelease(c.releaseQuota)
	return resp
}

// releaseQuota 释放一次并发配额并按需打 debug 日志.
// 抽成方法是为了避免每次构造闭包都要分配；该方法本身不会被嵌入额外栈层，
// 调用 logger.Debugf 时栈深度仍与 zap.AddCallerSkip(2) 的预期一致.
func (c *ConcurrencyQuotaBucket) releaseQuota() {
	now := atomic.AddInt64(&c.currentCount, -1)
	logger := c.logCtx.GetRateLimitLogger()
	if logger.IsLevelEnabled(log.DebugLog) {
		logger.Debugf("%s released rule[%s] current=%d/%d",
			logTag, ruleID(c.rule), now, c.maxAmount)
	}
}

// Release 释放并发配额（实现 QuotaBucket 接口；正常路径由 GetQuota 注入的回调归还，
// 此处保留实现以兼容 QuotaBucket 接口语义）.
func (c *ConcurrencyQuotaBucket) Release() {
	c.releaseQuota()
}

// OnRemoteUpdate 远程配额更新（并发数限流为纯本地模式，无需处理远程同步）.
func (c *ConcurrencyQuotaBucket) OnRemoteUpdate(remoteQuota ratelimiter.RemoteQuotaResult) {
	// 并发数限流不依赖远程配额服务器，刻意不打日志（无信号）.
}

// GetQuotaUsed 获取配额使用情况（并发数限流不上报滑窗数据）.
func (c *ConcurrencyQuotaBucket) GetQuotaUsed(curTimeMilli int64) ratelimiter.UsageInfo {
	return ratelimiter.UsageInfo{
		CurTimeMilli: curTimeMilli,
	}
}

// GetAmountInfos 获取规则的限流阈值信息.
func (c *ConcurrencyQuotaBucket) GetAmountInfos() []ratelimiter.AmountInfo {
	return []ratelimiter.AmountInfo{
		{
			ValidDuration: 0, // 并发数限流无时间窗口概念
			MaxAmount:     uint32(c.maxAmount),
		},
	}
}

// ruleID 返回规则的标识（id 优先，缺失时 fallback 到 name），仅用于日志可读性.
func ruleID(rule *apitraffic.Rule) string {
	if id := rule.GetId().GetValue(); id != "" {
		return id
	}
	return rule.GetName().GetValue()
}
