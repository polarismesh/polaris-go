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

package flow

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// TestCircuitBreakerStatusToResult_OpenAlwaysReject 测试场景：Open 态总是返回 Pass=false
// 前置条件：传入 status=Open 的 BaseCircuitBreakerStatus
// 预期结果：CheckResult.Pass=false，RuleName 透传规则名
func TestCircuitBreakerStatusToResult_OpenAlwaysReject(t *testing.T) {
	st := model.NewCircuitBreakerStatus("rule-A", model.Open, time.Now())
	got := circuitBreakerStatusToResult(st)
	assert.False(t, got.Pass)
	assert.Equal(t, "rule-A", got.RuleName)
}

// TestCircuitBreakerStatusToResult_HalfOpenWithinQuotaPasses 测试场景：HalfOpen 配额未耗尽时放行
// 前置条件：maxRequest=2，依次申请 2 次配额
// 预期结果：两次 Check 均 Pass=true；超出配额时返回 Pass=false
func TestCircuitBreakerStatusToResult_HalfOpenWithinQuotaPasses(t *testing.T) {
	hs := model.NewHalfOpenStatus("rule-half", time.Now(), 2).(*model.HalfOpenStatus)

	got := circuitBreakerStatusToResult(hs)
	assert.True(t, got.Pass, "first acquire should pass")

	got = circuitBreakerStatusToResult(hs)
	assert.True(t, got.Pass, "second acquire should pass")

	got = circuitBreakerStatusToResult(hs)
	assert.False(t, got.Pass, "third acquire should be rejected when quota exhausted")
	assert.Equal(t, "rule-half", got.RuleName)
}

// TestCircuitBreakerStatusToResult_CloseAlwaysPass 测试场景：Close 态始终放行
// 前置条件：传入 status=Close 的 BaseCircuitBreakerStatus
// 预期结果：Pass=true
func TestCircuitBreakerStatusToResult_CloseAlwaysPass(t *testing.T) {
	st := model.NewCircuitBreakerStatus("rule-close", model.Close, time.Now())
	got := circuitBreakerStatusToResult(st)
	assert.True(t, got.Pass)
	assert.Equal(t, "rule-close", got.RuleName)
}

// TestCallAborted_CarriesAbortSignal 测试场景：CallAborted 错误能被 errors.Is 正确识别为熔断信号
// 前置条件：用 model.ErrorCallAborted 构造一个 CallAborted（fallback 传 nil）
// 预期结果：errors.Is(aborted.GetError(), model.ErrorCallAborted) 为 true，可被 OnError 识别为 RetReject
func TestCallAborted_CarriesAbortSignal(t *testing.T) {
	aborted := model.NewCallAborted(model.ErrorCallAborted, "rule-A", nil)

	assert.NotNil(t, aborted)
	assert.True(t, errors.Is(aborted.GetError(), model.ErrorCallAborted),
		"CallAborted should carry ErrorCallAborted as sentinel")
	assert.False(t, aborted.HasFallback(),
		"fallback nil should be reported via HasFallback=false")
	assert.Equal(t, 0, aborted.GetFallbackCode())
	assert.Equal(t, "", aborted.GetFallbackBody())
	assert.Empty(t, aborted.GetFallbackHeaders())
}

// TestCallAborted_OptionalFallback 测试场景：CallAborted 携带 fallback 时各字段正常透出
// 前置条件：构造一份完整 FallbackInfo（code=503、body 非空、headers 非空）
// 预期结果：HasFallback=true，code/body/headers 全部能取到
func TestCallAborted_OptionalFallback(t *testing.T) {
	fb := &model.FallbackInfo{
		Code:    503,
		Body:    "service unavailable",
		Headers: map[string]string{"X-Cb": "open"},
	}
	aborted := model.NewCallAborted(model.ErrorCallAborted, "rule-B", fb)

	assert.True(t, aborted.HasFallback())
	assert.Equal(t, 503, aborted.GetFallbackCode())
	assert.Equal(t, "service unavailable", aborted.GetFallbackBody())
	assert.Equal(t, "open", aborted.GetFallbackHeaders()["X-Cb"])
}
