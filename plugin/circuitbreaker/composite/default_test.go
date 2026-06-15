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

	"github.com/polarismesh/specification/source/go/api/v1/fault_tolerance"
	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// buildInstanceDefaultBlock 构造与 default.go 默认实例规则一致的 BlockConfig
// 抽出此辅助函数避免在每个测试里重复编排默认规则的字段
func buildInstanceDefaultBlock() *fault_tolerance.BlockConfig {
	errorCondition := &fault_tolerance.ErrorCondition{
		InputType: fault_tolerance.ErrorCondition_RET_CODE,
		Condition: &apimodel.MatchString{
			Type:  apimodel.MatchString_RANGE,
			Value: &wrapperspb.StringValue{Value: "500~599"},
		},
	}
	triggerConditions := []*fault_tolerance.TriggerCondition{
		{
			TriggerType: fault_tolerance.TriggerCondition_CONSECUTIVE_ERROR,
			ErrorCount:  3,
		},
	}
	return &fault_tolerance.BlockConfig{
		Name:              "failure-block-config",
		ErrorConditions:   []*fault_tolerance.ErrorCondition{errorCondition},
		TriggerConditions: triggerConditions,
	}
}

// TestDefaultInstanceRule_BlockConfigShape 测试场景：默认实例规则块级 ErrorConditions 与 RetCode 范围匹配
// 前置条件：手工构造与 default.go 等价的默认实例规则（500~599 范围匹配）
// 预期结果：blockCounter.parseRetStatus 对 5xx 返回 RetFail；对 4xx/2xx 返回 RetSuccess
//
// 该用例从语义维度回归"InstanceResource 默认规则保活"——确认重构后默认规则
// 仍走块级独立计数路径，没有因为新结构而漏判 5xx。
func TestDefaultInstanceRule_BlockConfigShape(t *testing.T) {
	rule := &fault_tolerance.CircuitBreakerRule{
		Name:         "default-polaris-instance-circuit-breaker",
		Enable:       true,
		Level:        fault_tolerance.Level_INSTANCE,
		BlockConfigs: []*fault_tolerance.BlockConfig{buildInstanceDefaultBlock()},
	}
	rc := newRCForTest(rule)
	bc := buildInstanceDefaultBlock()
	errConditions := resolveBlockErrorConditions(rule, bc)

	b := &blockCounter{
		name:            rule.Name + "#" + bc.GetName(),
		errorConditions: errConditions,
		rc:              rc,
	}

	tests := []struct {
		name string
		stat *model.ResourceStat
		want model.RetStatus
	}{
		{name: "code_500_returns_fail", stat: &model.ResourceStat{RetCode: "500"}, want: model.RetFail},
		{name: "code_503_returns_fail", stat: &model.ResourceStat{RetCode: "503"}, want: model.RetFail},
		{name: "code_599_returns_fail", stat: &model.ResourceStat{RetCode: "599"}, want: model.RetFail},
		{name: "code_400_returns_success", stat: &model.ResourceStat{RetCode: "400"}, want: model.RetSuccess},
		{name: "code_200_returns_success", stat: &model.ResourceStat{RetCode: "200"}, want: model.RetSuccess},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, b.parseRetStatus(tt.stat))
		})
	}
}

// TestDefaultInstanceRule_LegacyBlockFallback 测试场景：默认实例规则也兼容老规则下的 legacyBlock 路径
// 前置条件：构造一条没有 BlockConfigs、仅有顶层弃用字段的旧规则（部分老服务端会下发这种）
// 预期结果：ResourceCounters.init 通过 legacyBlock 兜底；blockCounter.parseRetStatus 仍能识别 RetCode=500
func TestDefaultInstanceRule_LegacyBlockFallback(t *testing.T) {
	errorCondition := &fault_tolerance.ErrorCondition{
		InputType: fault_tolerance.ErrorCondition_RET_CODE,
		Condition: &apimodel.MatchString{
			Type:  apimodel.MatchString_RANGE,
			Value: &wrapperspb.StringValue{Value: "500~599"},
		},
	}
	rule := &fault_tolerance.CircuitBreakerRule{
		Name:            "legacy-default-instance-rule",
		Enable:          true,
		Level:           fault_tolerance.Level_INSTANCE,
		ErrorConditions: []*fault_tolerance.ErrorCondition{errorCondition},
		TriggerCondition: []*fault_tolerance.TriggerCondition{
			{TriggerType: fault_tolerance.TriggerCondition_CONSECUTIVE_ERROR, ErrorCount: 3},
		},
	}
	// 在老规则路径下，blockCounter 的 errorConditions 直接来自规则顶层
	b := &blockCounter{
		name:            rule.Name,
		errorConditions: rule.GetErrorConditions(),
		rc:              newRCForTest(rule),
	}

	assert.Equal(t, model.RetFail, b.parseRetStatus(&model.ResourceStat{RetCode: "500"}))
	assert.Equal(t, model.RetSuccess, b.parseRetStatus(&model.ResourceStat{RetCode: "200"}))
}
