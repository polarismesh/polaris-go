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

package composite

import (
	"testing"
	"time"

	regexp "github.com/dlclark/regexp2"
	"github.com/polarismesh/specification/source/go/api/v1/fault_tolerance"
	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// buildErrorCondition 构造一条返回码错误条件，简化测试代码
func buildErrorCondition(code string) *fault_tolerance.ErrorCondition {
	return &fault_tolerance.ErrorCondition{
		InputType: fault_tolerance.ErrorCondition_RET_CODE,
		Condition: &apimodel.MatchString{
			Type:  apimodel.MatchString_EXACT,
			Value: &wrapperspb.StringValue{Value: code},
		},
	}
}

// newRCForTest 构造一个仅用于纯逻辑测试的 ResourceCounters（不接触 executor / log）
// 仅设置 regexFunction，使 blockCounter.parseRetStatus 在 RET_CODE 路径上能正常运转
func newRCForTest(rule *fault_tolerance.CircuitBreakerRule) *ResourceCounters {
	return &ResourceCounters{
		activeRule:    rule,
		regexFunction: func(s string) *regexp.Regexp { return regexp.MustCompile(s, regexp.RE2) },
	}
}

// TestResolveBlockErrorConditions_PreferBlock 测试场景：块自身错误条件非空时优先使用块的列表
// 前置条件：BlockConfig.ErrorConditions 与 Rule.ErrorConditions 同时非空
// 预期结果：返回 BlockConfig 的 ErrorConditions，不退化为合并集
func TestResolveBlockErrorConditions_PreferBlock(t *testing.T) {
	bcCond := buildErrorCondition("500")
	topCond := buildErrorCondition("999")
	rule := &fault_tolerance.CircuitBreakerRule{
		ErrorConditions: []*fault_tolerance.ErrorCondition{topCond},
	}
	bc := &fault_tolerance.BlockConfig{
		ErrorConditions: []*fault_tolerance.ErrorCondition{bcCond},
	}

	got := resolveBlockErrorConditions(rule, bc)

	assert.Len(t, got, 1)
	assert.Equal(t, "500", got[0].GetCondition().GetValue().GetValue())
}

// TestResolveBlockErrorConditions_FallbackToRule 测试场景：块自身错误条件为空时回退到规则顶层
// 前置条件：BlockConfig.ErrorConditions 为空，仅 Rule.ErrorConditions 有值（兼容老规则路径）
// 预期结果：返回规则顶层的 ErrorConditions
func TestResolveBlockErrorConditions_FallbackToRule(t *testing.T) {
	topCond := buildErrorCondition("500")
	rule := &fault_tolerance.CircuitBreakerRule{
		ErrorConditions: []*fault_tolerance.ErrorCondition{topCond},
	}
	bc := &fault_tolerance.BlockConfig{Api: &apimodel.API{Protocol: "http"}}

	got := resolveBlockErrorConditions(rule, bc)

	assert.Len(t, got, 1)
	assert.Equal(t, "500", got[0].GetCondition().GetValue().GetValue())
}

// TestResolveBlockErrorConditions_AllEmpty 测试场景：块与规则顶层错误条件均为空
// 前置条件：BlockConfig 与 Rule 均不含 ErrorConditions
// 预期结果：返回 nil，调用方据此走 stat.RetStatus 透传逻辑
func TestResolveBlockErrorConditions_AllEmpty(t *testing.T) {
	rule := &fault_tolerance.CircuitBreakerRule{}
	bc := &fault_tolerance.BlockConfig{}

	got := resolveBlockErrorConditions(rule, bc)

	assert.Nil(t, got)
}

// TestBlockCounter_ParseRetStatus_RetCodeMatch 测试场景：块级 RET_CODE 错误条件命中时返回 RetFail
// 前置条件：块持有一条 RetCode=500 的错误条件
// 预期结果：上报 stat.RetCode=500 时返回 RetFail；上报 stat.RetCode=200 时返回 RetSuccess；
//
//	retCode="-1" 哨兵值始终返回 RetFail
func TestBlockCounter_ParseRetStatus_RetCodeMatch(t *testing.T) {
	rule := &fault_tolerance.CircuitBreakerRule{}
	rc := newRCForTest(rule)
	b := &blockCounter{
		name:            "rule#a",
		errorConditions: []*fault_tolerance.ErrorCondition{buildErrorCondition("500")},
		rc:              rc,
	}

	tests := []struct {
		name string
		stat *model.ResourceStat
		want model.RetStatus
	}{
		{
			name: "match_500_returns_fail",
			stat: &model.ResourceStat{RetCode: "500"},
			want: model.RetFail,
		},
		{
			name: "miss_returns_success",
			stat: &model.ResourceStat{RetCode: "200"},
			want: model.RetSuccess,
		},
		{
			name: "sentinel_minus_one_returns_fail",
			stat: &model.ResourceStat{RetCode: "-1"},
			want: model.RetFail,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, b.parseRetStatus(tt.stat))
		})
	}
}

// TestBlockCounter_ParseRetStatus_DelayThreshold 测试场景：块级 DELAY 错误条件超过阈值时返回 RetTimeout
// 前置条件：块持有一条 Delay>100ms 的错误条件
// 预期结果：超过阈值返回 RetTimeout，未超过返回 RetSuccess
//
// 同时验证：DELAY 单条件块下，retCode="-1" 不会被识别为哨兵（因为没有 RET_CODE 类条件）
func TestBlockCounter_ParseRetStatus_DelayThreshold(t *testing.T) {
	rule := &fault_tolerance.CircuitBreakerRule{}
	rc := newRCForTest(rule)
	delayCond := &fault_tolerance.ErrorCondition{
		InputType: fault_tolerance.ErrorCondition_DELAY,
		Condition: &apimodel.MatchString{
			Type:  apimodel.MatchString_EXACT,
			Value: &wrapperspb.StringValue{Value: "100"},
		},
	}
	b := &blockCounter{
		name:            "rule#delay",
		errorConditions: []*fault_tolerance.ErrorCondition{delayCond},
		rc:              rc,
	}

	tests := []struct {
		name string
		stat *model.ResourceStat
		want model.RetStatus
	}{
		{
			name: "exceed_threshold_returns_timeout",
			stat: &model.ResourceStat{Delay: 150 * time.Millisecond},
			want: model.RetTimeout,
		},
		{
			name: "below_threshold_returns_success",
			stat: &model.ResourceStat{Delay: 50 * time.Millisecond},
			want: model.RetSuccess,
		},
		{
			name: "minus_one_without_retcode_condition_returns_success",
			stat: &model.ResourceStat{
				RetCode: "-1",
				Delay:   50 * time.Millisecond,
			},
			want: model.RetSuccess,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, b.parseRetStatus(tt.stat))
		})
	}
}

// TestBlockCounter_ParseRetStatus_PassThroughWhenEmpty 测试场景：块错误条件为空时透传 stat.RetStatus
// 前置条件：blockCounter.errorConditions 为空
// 预期结果：parseRetStatus 直接返回 stat.RetStatus，不做改写
func TestBlockCounter_ParseRetStatus_PassThroughWhenEmpty(t *testing.T) {
	rule := &fault_tolerance.CircuitBreakerRule{}
	rc := newRCForTest(rule)
	b := &blockCounter{
		name:            "rule#empty",
		errorConditions: nil,
		rc:              rc,
	}

	tests := []struct {
		name string
		in   model.RetStatus
		want model.RetStatus
	}{
		{name: "passthrough_fail", in: model.RetFail, want: model.RetFail},
		{name: "passthrough_success", in: model.RetSuccess, want: model.RetSuccess},
		{name: "passthrough_timeout", in: model.RetTimeout, want: model.RetTimeout},
		{name: "passthrough_unknown", in: model.RetUnknown, want: model.RetUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, b.parseRetStatus(&model.ResourceStat{RetStatus: tt.in}))
		})
	}
}

// TestResourceCounters_EvaluateSuccess_BlocksAreIndependent 测试场景：多个块按各自错误条件独立判错
// 前置条件：构造 ResourceCounters 直接持有两个块，错误条件互斥（块A 命中 RetCode=500，块B 命中 RetCode=502）
// 预期结果：任一块判失败即整体失败；两个块都判成功才返回成功
func TestResourceCounters_EvaluateSuccess_BlocksAreIndependent(t *testing.T) {
	rule := &fault_tolerance.CircuitBreakerRule{}
	rc := newRCForTest(rule)
	rc.blocks = []*blockCounter{
		{
			name:            "rule#A",
			errorConditions: []*fault_tolerance.ErrorCondition{buildErrorCondition("500")},
			rc:              rc,
		},
		{
			name:            "rule#B",
			errorConditions: []*fault_tolerance.ErrorCondition{buildErrorCondition("502")},
			rc:              rc,
		},
	}

	tests := []struct {
		name    string
		retCode string
		want    bool
	}{
		{name: "block_A_hits_overall_fail", retCode: "500", want: false},
		{name: "block_B_hits_overall_fail", retCode: "502", want: false},
		{name: "no_block_hits_overall_success", retCode: "200", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rc.evaluateSuccess(&model.ResourceStat{RetCode: tt.retCode})
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestResourceCounters_EvaluateSuccess_LegacyBlockFallback 测试场景：BlockConfigs 为空时使用 legacyBlock
// 前置条件：rc.blocks 为空，仅设置 legacyBlock 持有一条 RetCode=500 错误条件
// 预期结果：评估按 legacyBlock 的错误条件执行
func TestResourceCounters_EvaluateSuccess_LegacyBlockFallback(t *testing.T) {
	rule := &fault_tolerance.CircuitBreakerRule{}
	rc := newRCForTest(rule)
	rc.legacyBlock = &blockCounter{
		name:            "legacy",
		errorConditions: []*fault_tolerance.ErrorCondition{buildErrorCondition("500")},
		rc:              rc,
	}

	assert.False(t, rc.evaluateSuccess(&model.ResourceStat{RetCode: "500"}))
	assert.True(t, rc.evaluateSuccess(&model.ResourceStat{RetCode: "200"}))
}

// TestResourceCounters_EvaluateSuccess_NoBlockPassThrough 测试场景：blocks 与 legacyBlock 均为空时透传 stat.RetStatus
// 前置条件：未配置任何块（理论上不应发生，作为防御性兜底）
// 预期结果：根据 stat.RetStatus 决定 success
func TestResourceCounters_EvaluateSuccess_NoBlockPassThrough(t *testing.T) {
	rule := &fault_tolerance.CircuitBreakerRule{}
	rc := newRCForTest(rule)

	assert.True(t, rc.evaluateSuccess(&model.ResourceStat{RetStatus: model.RetSuccess}))
	assert.False(t, rc.evaluateSuccess(&model.ResourceStat{RetStatus: model.RetFail}))
	assert.False(t, rc.evaluateSuccess(&model.ResourceStat{RetStatus: model.RetTimeout}))
}
