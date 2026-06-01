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

package pb

import (
	"errors"
	"fmt"
	"testing"

	"github.com/golang/protobuf/ptypes/duration"
	"github.com/golang/protobuf/ptypes/wrappers"
	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"
	"github.com/stretchr/testify/assert"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// 说明：本测试文件不主动注册任何 RateLimiter 插件，因此 plugin.IsPluginRegistered
// 对任意 behavior 名称都会返回 false。这一前提让 Validate 的 behavior 校验分支能够被
// 直接触发，无需对全局插件注册表做 mock。如果未来在 pkg/model/pb 测试目录下引入了
// 会触发 RateLimiter 插件 init() 的依赖，请同步检查这些用例的有效性。

// TestBehaviorNotRegisteredError_Error 验证 *BehaviorNotRegisteredError 错误文案
// 与历史版本完全一致。该文案是现网监控/告警关键字匹配的契约，禁止漂移。
func TestBehaviorNotRegisteredError_Error(t *testing.T) {
	tests := []struct {
		name     string
		behavior string
		want     string
	}{
		{
			name:     "tsf behavior",
			behavior: "tsf",
			want:     "behavior plugin tsf not registered",
		},
		{
			name:     "empty behavior",
			behavior: "",
			want:     "behavior plugin  not registered",
		},
		{
			name:     "behavior with special chars",
			behavior: "custom-rate/limiter",
			want:     "behavior plugin custom-rate/limiter not registered",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &BehaviorNotRegisteredError{Behavior: tt.behavior}
			assert.Equal(t, tt.want, err.Error(),
				"error message must keep the legacy format for log alert grep compatibility")
		})
	}
}

// TestBehaviorNotRegisteredError_ErrorsAs 验证 errors.As 能够正确识别该 typed error。
// 这是 inmemory.go::logServiceRuleValidationFailure 选择 WARN 还是 ERROR 的关键依据，
// 必须保证：
// 1) 直接的 *BehaviorNotRegisteredError 能被识别；
// 2) 普通 error 不会被错误识别为该类型；
// 3) 经 fmt.Errorf("%w") 包裹后仍能被识别（防止上层包装层破坏分支判断）。
func TestBehaviorNotRegisteredError_ErrorsAs(t *testing.T) {
	t.Run("direct typed error matches", func(t *testing.T) {
		var err error = &BehaviorNotRegisteredError{Behavior: "tsf"}
		var target *BehaviorNotRegisteredError
		assert.True(t, errors.As(err, &target))
		assert.Equal(t, "tsf", target.Behavior)
	})

	t.Run("plain error does not match", func(t *testing.T) {
		err := errors.New("amount illegal")
		var target *BehaviorNotRegisteredError
		assert.False(t, errors.As(err, &target))
		assert.Nil(t, target)
	})

	t.Run("wrapped typed error matches", func(t *testing.T) {
		inner := &BehaviorNotRegisteredError{Behavior: "tsf"}
		wrapped := fmt.Errorf("ratelimit validation failed: %w", inner)
		var target *BehaviorNotRegisteredError
		assert.True(t, errors.As(wrapped, &target))
		assert.Equal(t, "tsf", target.Behavior)
	})
}

// makeRateLimit 构造一条最简、能够走到 behavior 校验阶段的 RateLimit 消息。
// validateAmount 对空 Amounts 直接返回 nil，amountPercent 默认 0 也落在
// (0..=100] 之外的允许区间内，因此校验流程会一直走到 behavior 检查。
func makeRateLimit(action string) *apitraffic.RateLimit {
	return &apitraffic.RateLimit{
		Rules: []*apitraffic.Rule{
			{
				Action: &wrappers.StringValue{Value: action},
			},
		},
	}
}

// makeRateLimitWithAmount 构造一条带合法 Amount 与 validDuration 的 RateLimit。
// 用于验证 amount/duration 合法但 behavior 不合法时仍会触发本错误。
func makeRateLimitWithAmount(action string) *apitraffic.RateLimit {
	return &apitraffic.RateLimit{
		Rules: []*apitraffic.Rule{
			{
				Amounts: []*apitraffic.Amount{
					{
						MaxAmount:     &wrappers.UInt32Value{Value: 100},
						ValidDuration: &duration.Duration{Seconds: 1},
					},
				},
				Action: &wrappers.StringValue{Value: action},
			},
		},
	}
}

// TestRateLimitingAssistant_Validate_BehaviorNotRegistered 验证：
// 当限流规则的 action 引用了未注册的 RateLimiter 插件时，Validate 必须返回
// *BehaviorNotRegisteredError，且其 Behavior 字段等于规则中的 action 值。
//
// 该测试不主动注册任何 RateLimiter 插件，因此对任意 behavior 名称都能稳定触发该分支。
func TestRateLimitingAssistant_Validate_BehaviorNotRegistered(t *testing.T) {
	tests := []struct {
		name         string
		buildMessage func(string) *apitraffic.RateLimit
		action       string
	}{
		{
			name:         "minimal rule with unregistered behavior",
			buildMessage: makeRateLimit,
			action:       "tsf",
		},
		{
			name:         "rule with valid amount but unregistered behavior",
			buildMessage: makeRateLimitWithAmount,
			action:       "tsf",
		},
		{
			name:         "vendor-specific behavior name",
			buildMessage: makeRateLimit,
			action:       "vendor-only-limiter",
		},
		{
			name:         "default reject behavior is also unregistered in this test binary",
			buildMessage: makeRateLimit,
			action:       "reject",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RateLimitingAssistant{}
			err := r.Validate(tt.buildMessage(tt.action), model.NewRuleCache())

			assert.Error(t, err, "unregistered behavior must yield an error")
			var behaviorErr *BehaviorNotRegisteredError
			assert.True(t, errors.As(err, &behaviorErr),
				"err must be unwrapped into *BehaviorNotRegisteredError")
			assert.Equal(t, tt.action, behaviorErr.Behavior,
				"Behavior field must carry the offending plugin name")
			assert.Equal(t,
				fmt.Sprintf("behavior plugin %s not registered", tt.action),
				err.Error(),
				"error message must remain stable for log alert grep")
		})
	}
}

// TestRateLimitingAssistant_Validate_EmptyRules 验证空规则集快速返回 nil，
// 不会进入任何 behavior 校验。
func TestRateLimitingAssistant_Validate_EmptyRules(t *testing.T) {
	r := &RateLimitingAssistant{}
	err := r.Validate(&apitraffic.RateLimit{}, model.NewRuleCache())
	assert.NoError(t, err)
}

// TestRateLimitingAssistant_Validate_ShortCircuit 固化"整组规则丢弃"的行为契约：
// Validate 在遇到第一条非法规则时立即短路返回，数组中位于其后的规则不会被处理
// （RuleCache 也不会写入对应缓存项）。
//
// 这一行为决定了上层（pkg/flow/quota/assist.go::lookupRules）会在 validateError
// 非空时整体跳过该 RateLimit 中的所有规则。如果未来调整为"标记并跳过单条规则"，
// 本测试需要同步更新。
func TestRateLimitingAssistant_Validate_ShortCircuit(t *testing.T) {
	rl := &apitraffic.RateLimit{
		Rules: []*apitraffic.Rule{
			{Action: &wrappers.StringValue{Value: "first-unregistered"}},  // 触发短路
			{Action: &wrappers.StringValue{Value: "second-unregistered"}}, // 永远不会被处理
		},
	}
	cache := model.NewRuleCache()
	r := &RateLimitingAssistant{}

	err := r.Validate(rl, cache)
	var behaviorErr *BehaviorNotRegisteredError
	assert.True(t, errors.As(err, &behaviorErr),
		"the first unregistered behavior must short-circuit Validate")
	assert.Equal(t, "first-unregistered", behaviorErr.Behavior,
		"Behavior must equal the FIRST offending action, proving short-circuit on the 1st failure")

	// 两条规则都没有合法 behavior，因此都不应被写入 cache（Validate 只对合法规则写缓存）。
	assert.Nil(t, cache.GetMessageCache(rl.Rules[0]),
		"rule 0 must not be cached: it failed validation")
	assert.Nil(t, cache.GetMessageCache(rl.Rules[1]),
		"rule 1 must not be cached: short-circuit prevents processing")
}
