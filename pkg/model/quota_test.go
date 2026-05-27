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

package model

import (
	"testing"

	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// TestQuotaResponse_GetActiveRule_NilReceiver 验证 nil receiver 时安全返回 nil。
// 前置条件：调用方持有的 *QuotaResponse 为 nil。
// 预期结果：GetActiveRule 不 panic，返回 nil。
func TestQuotaResponse_GetActiveRule_NilReceiver(t *testing.T) {
	var resp *QuotaResponse
	assert.Nil(t, resp.GetActiveRule(), "nil receiver 应安全返回 nil")
	assert.Equal(t, "", resp.GetActiveRuleName(), "nil receiver 的 RuleName 应为空串")
	assert.Equal(t, "", resp.GetActiveRuleId(), "nil receiver 的 RuleId 应为空串")
}

// TestQuotaResponse_GetActiveRule_PassResultNoRule 验证非限流场景下 ActiveRule 默认为 nil。
// 前置条件：QuotaResultOk 路径下未填充 ActiveRule。
// 预期结果：GetActiveRule 返回 nil；衍生 getter 返回空串。
func TestQuotaResponse_GetActiveRule_PassResultNoRule(t *testing.T) {
	resp := &QuotaResponse{Code: QuotaResultOk}
	assert.Nil(t, resp.GetActiveRule(), "非限流时 ActiveRule 应为 nil")
	assert.Equal(t, "", resp.GetActiveRuleName())
	assert.Equal(t, "", resp.GetActiveRuleId())
}

// TestQuotaResponse_GetActiveRule_LimitedHasRule 验证限流场景下 ActiveRule 字段被正确读取。
// 前置条件：构造一条命名为 demo-rule、ID 为 rule-id-001、CustomResponse.body=customBody 的规则，
//
//	赋给 QuotaResponse.ActiveRule。
//
// 预期结果：GetActiveRule 返回原 *Rule，名称/ID/自定义返回均可通过 getter 读取。
func TestQuotaResponse_GetActiveRule_LimitedHasRule(t *testing.T) {
	rule := &apitraffic.Rule{
		Id:   wrapperspb.String("rule-id-001"),
		Name: wrapperspb.String("demo-rule"),
		CustomResponse: &apitraffic.CustomResponse{
			Body: "customBody",
		},
	}
	resp := &QuotaResponse{
		Code:       QuotaResultLimited,
		Info:       "limited",
		ActiveRule: rule,
	}

	got := resp.GetActiveRule()
	assert.NotNil(t, got, "限流时 ActiveRule 应非 nil")
	assert.Same(t, rule, got, "返回的应是同一指针")
	assert.Equal(t, "demo-rule", resp.GetActiveRuleName())
	assert.Equal(t, "rule-id-001", resp.GetActiveRuleId())
	assert.Equal(t, "customBody", got.GetCustomResponse().GetBody(),
		"业务侧链式调用应能取到 CustomResponse.body")
}
