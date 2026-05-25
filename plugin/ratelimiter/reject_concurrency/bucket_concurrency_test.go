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

package rejectconcurrency

import (
	"sync"
	"sync/atomic"
	"testing"

	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"
	"github.com/stretchr/testify/assert"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/ratelimiter"
)

// buildConcurrencyRule 构造并发数限流规则.
func buildConcurrencyRule(maxAmount uint32) *apitraffic.Rule {
	return &apitraffic.Rule{
		Resource: apitraffic.Rule_CONCURRENCY,
		ConcurrencyAmount: &apitraffic.ConcurrencyAmount{
			MaxAmount: maxAmount,
		},
	}
}

// silentLogger 一个完全静默的 log.Logger 实现，用于"只关心计数行为、不关心日志输出"的用例.
// 注意：所有方法均不打印；IsLevelEnabled 返回 false 让 hot-path 的 Debug 闸门直接短路，
// 减少测试运行时的无谓开销.
type silentLogger struct{}

func (silentLogger) Tracef(string, ...interface{}) {}
func (silentLogger) Debugf(string, ...interface{}) {}
func (silentLogger) Infof(string, ...interface{})  {}
func (silentLogger) Warnf(string, ...interface{})  {}
func (silentLogger) Errorf(string, ...interface{}) {}
func (silentLogger) Fatalf(string, ...interface{}) {}
func (silentLogger) IsLevelEnabled(int) bool       { return false }
func (silentLogger) SetLogLevel(int) error         { return nil }

// noopCtx 返回一个挂载了静默 logger 的 ContextLogger，供"只关心计数行为、不关心日志输出"的用例使用.
//
// 实现策略：临时把全局 RateLimitLogger 替换为 silentLogger，调用 ContextLogger.Init 让它从全局取，
// 再恢复原值。这样既不会污染其他测试，也不依赖 zaplog 真实插件链路.
func noopCtx() *log.ContextLogger {
	orig := log.GetRateLimitLogger()
	log.SetRateLimitLogger(silentLogger{})
	ctx := &log.ContextLogger{}
	ctx.Init()
	log.SetRateLimitLogger(orig)
	return ctx
}

func TestNewConcurrencyQuotaBucket_NormalAmount_ShouldUseRuleValue(t *testing.T) {
	// 测试场景：正常 MaxAmount 应直接使用规则配置值
	rule := buildConcurrencyRule(10)
	bucket := NewConcurrencyQuotaBucket(rule, "test-svc#default", noopCtx())

	assert.Equal(t, int64(10), bucket.maxAmount)
	assert.Equal(t, int64(0), atomic.LoadInt64(&bucket.currentCount))
}

func TestNewConcurrencyQuotaBucket_ZeroMaxAmount_ShouldFallbackToOne(t *testing.T) {
	// 测试场景：MaxAmount = 0 时应保底为 1，避免无限放通
	rule := buildConcurrencyRule(0)
	bucket := NewConcurrencyQuotaBucket(rule, "test-svc#default", noopCtx())

	assert.Equal(t, int64(1), bucket.maxAmount)
}

func TestConcurrencyQuotaBucket_GetQuota_BelowLimit_ShouldPass(t *testing.T) {
	// 测试场景：并发数未达上限时请求应通过并注入 release 回调
	bucket := NewConcurrencyQuotaBucket(buildConcurrencyRule(2), "test-svc#default", noopCtx())

	resp := bucket.GetQuota(0, 1)

	assert.Equal(t, model.QuotaResultOk, resp.Code)
	assert.Equal(t, int64(1), atomic.LoadInt64(&bucket.currentCount))
	assert.Len(t, resp.GetReleaseFuncs(), 1, "通过的请求必须注入 release 回调")
}

func TestConcurrencyQuotaBucket_GetQuota_ExceedLimit_ShouldLimitedAndRollback(t *testing.T) {
	// 测试场景：达到并发上限后新请求应被限流，且计数应回退（不超过 maxAmount）
	bucket := NewConcurrencyQuotaBucket(buildConcurrencyRule(2), "test-svc#default", noopCtx())

	first := bucket.GetQuota(0, 1)
	second := bucket.GetQuota(0, 1)
	third := bucket.GetQuota(0, 1)

	assert.Equal(t, model.QuotaResultOk, first.Code)
	assert.Equal(t, model.QuotaResultOk, second.Code)
	assert.Equal(t, model.QuotaResultLimited, third.Code)
	assert.Contains(t, third.Info, "CONCURRENCY")
	// 限流后必须立即回退，当前计数等于 maxAmount，不会因失败请求泄漏
	assert.Equal(t, int64(2), atomic.LoadInt64(&bucket.currentCount))
}

func TestConcurrencyQuotaBucket_Release_ShouldDecrementCount(t *testing.T) {
	// 测试场景：通过 QuotaFutureImpl.Release() 触发回调后应归还配额，新请求可继续通过
	bucket := NewConcurrencyQuotaBucket(buildConcurrencyRule(1), "test-svc#default", noopCtx())

	resp := bucket.GetQuota(0, 1)
	assert.Equal(t, model.QuotaResultOk, resp.Code)
	assert.Equal(t, int64(1), atomic.LoadInt64(&bucket.currentCount))

	// 第二次请求应被限流
	limited := bucket.GetQuota(0, 1)
	assert.Equal(t, model.QuotaResultLimited, limited.Code)

	// 模拟上层 future.Release() 调用回调链
	future := model.QuotaFutureWithResponse(resp)
	future.Release()
	assert.Equal(t, int64(0), atomic.LoadInt64(&bucket.currentCount))

	// 配额释放后新请求可通过
	again := bucket.GetQuota(0, 1)
	assert.Equal(t, model.QuotaResultOk, again.Code)
}

func TestConcurrencyQuotaBucket_Release_DuplicateCall_ShouldBeIdempotent(t *testing.T) {
	// 测试场景：重复调用 Release 不应导致计数器异常（首次执行后回调链清空）
	bucket := NewConcurrencyQuotaBucket(buildConcurrencyRule(2), "test-svc#default", noopCtx())
	resp := bucket.GetQuota(0, 1)
	assert.Equal(t, int64(1), atomic.LoadInt64(&bucket.currentCount))

	future := model.QuotaFutureWithResponse(resp)
	future.Release()
	future.Release() // 重复调用
	future.Release() // 再次重复调用

	assert.Equal(t, int64(0), atomic.LoadInt64(&bucket.currentCount),
		"重复 Release 应是幂等操作，不会让计数变为负数")
}

func TestConcurrencyQuotaBucket_GetQuota_ConcurrentAccess_ShouldNotExceedLimit(t *testing.T) {
	// 测试场景：高并发下计数器不会出现竞态，最终成功数等于上限
	const maxAmount = uint32(50)
	const goroutines = 200
	bucket := NewConcurrencyQuotaBucket(buildConcurrencyRule(maxAmount), "test-svc#default", noopCtx())

	var wg sync.WaitGroup
	var passCount int64
	var limitCount int64

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			resp := bucket.GetQuota(0, 1)
			if resp.Code == model.QuotaResultOk {
				atomic.AddInt64(&passCount, 1)
			} else {
				atomic.AddInt64(&limitCount, 1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(maxAmount), atomic.LoadInt64(&passCount), "通过数应严格等于 maxAmount")
	assert.Equal(t, int64(goroutines)-int64(maxAmount), atomic.LoadInt64(&limitCount))
	assert.Equal(t, int64(maxAmount), atomic.LoadInt64(&bucket.currentCount),
		"并发结束后当前计数应等于 maxAmount（未释放的请求）")
}

func TestConcurrencyQuotaBucket_GetAmountInfos_ShouldReturnMaxAmount(t *testing.T) {
	// 测试场景：GetAmountInfos 应返回与规则一致的配额信息
	bucket := NewConcurrencyQuotaBucket(buildConcurrencyRule(8), "test-svc#default", noopCtx())

	infos := bucket.GetAmountInfos()
	assert.Len(t, infos, 1)
	assert.Equal(t, uint32(8), infos[0].MaxAmount)
	assert.Equal(t, uint32(0), infos[0].ValidDuration, "并发数限流无时间窗口概念")
}

func TestConcurrencyQuotaBucket_OnRemoteUpdate_ShouldBeNoop(t *testing.T) {
	// 测试场景：并发数限流为本地模式，OnRemoteUpdate 应为空操作不影响计数
	bucket := NewConcurrencyQuotaBucket(buildConcurrencyRule(5), "test-svc#default", noopCtx())
	resp := bucket.GetQuota(0, 1)
	assert.Equal(t, model.QuotaResultOk, resp.Code)

	// 调用 OnRemoteUpdate 不应改变状态
	bucket.OnRemoteUpdate(ratelimiter.RemoteQuotaResult{Left: 100})
	assert.Equal(t, int64(1), atomic.LoadInt64(&bucket.currentCount))
}
