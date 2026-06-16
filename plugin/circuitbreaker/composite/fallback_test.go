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
	"github.com/stretchr/testify/assert"
)

// buildFallbackResponse 构造一条降级响应，简化测试代码
func buildFallbackResponse(code int32, body string, headers map[string]string) *fault_tolerance.FallbackResponse {
	resp := &fault_tolerance.FallbackResponse{
		Code: code,
		Body: body,
	}
	for k, v := range headers {
		resp.Headers = append(resp.Headers, &fault_tolerance.FallbackResponse_MessageHeader{
			Key:   k,
			Value: v,
		})
	}
	return resp
}

// TestBuildFallbackInfo_NilRule 测试场景：规则为 nil
// 预期结果：返回 nil
func TestBuildFallbackInfo_NilRule(t *testing.T) {
	assert.Nil(t, buildFallbackInfo(nil))
}

// TestBuildFallbackInfo_InstanceLevelReturnsNil 测试场景：INSTANCE 级规则不支持降级
// 前置条件：Level=INSTANCE，且配置了 enable=true 的完整 fallback
// 预期结果：返回 nil（与 java composite 一致，仅 SERVICE/METHOD 支持）
func TestBuildFallbackInfo_InstanceLevelReturnsNil(t *testing.T) {
	rule := &fault_tolerance.CircuitBreakerRule{
		Level: fault_tolerance.Level_INSTANCE,
		FallbackConfig: &fault_tolerance.FallbackConfig{
			Enable:   true,
			Response: buildFallbackResponse(503, "degraded", nil),
		},
	}
	assert.Nil(t, buildFallbackInfo(rule))
}

// TestBuildFallbackInfo_DisabledReturnsNil 测试场景：enable=false 不启用降级（D1 核心修正）
// 前置条件：Level=SERVICE，FallbackConfig.Enable=false 但 response 非空
// 预期结果：返回 nil，避免 enable=false 的规则被误当作启用的降级配置
func TestBuildFallbackInfo_DisabledReturnsNil(t *testing.T) {
	rule := &fault_tolerance.CircuitBreakerRule{
		Level: fault_tolerance.Level_SERVICE,
		FallbackConfig: &fault_tolerance.FallbackConfig{
			Enable:   false,
			Response: buildFallbackResponse(599, "should-not-appear", map[string]string{"X-Fallback": "true"}),
		},
	}
	assert.Nil(t, buildFallbackInfo(rule))
}

// TestBuildFallbackInfo_NilConfigReturnsNil 测试场景：FallbackConfig 为 nil
// 前置条件：Level=SERVICE，FallbackConfig 未设置
// 预期结果：返回 nil
func TestBuildFallbackInfo_NilConfigReturnsNil(t *testing.T) {
	rule := &fault_tolerance.CircuitBreakerRule{
		Level: fault_tolerance.Level_SERVICE,
	}
	assert.Nil(t, buildFallbackInfo(rule))
}

// TestBuildFallbackInfo_EnabledNilResponseReturnsNil 测试场景：enable=true 但 response 为空
// 前置条件：Level=METHOD，Enable=true，Response=nil
// 预期结果：返回 nil
func TestBuildFallbackInfo_EnabledNilResponseReturnsNil(t *testing.T) {
	rule := &fault_tolerance.CircuitBreakerRule{
		Level: fault_tolerance.Level_METHOD,
		FallbackConfig: &fault_tolerance.FallbackConfig{
			Enable:   true,
			Response: nil,
		},
	}
	assert.Nil(t, buildFallbackInfo(rule))
}

// TestBuildFallbackInfo_ServiceLevelFullFields 测试场景：SERVICE 级完整降级配置逐字段映射
// 前置条件：Level=SERVICE，Enable=true，response 含 code/body/headers
// 预期结果：FallbackInfo 各字段与规则配置逐一相等
func TestBuildFallbackInfo_ServiceLevelFullFields(t *testing.T) {
	rule := &fault_tolerance.CircuitBreakerRule{
		Level: fault_tolerance.Level_SERVICE,
		FallbackConfig: &fault_tolerance.FallbackConfig{
			Enable: true,
			Response: buildFallbackResponse(599, "service degraded", map[string]string{
				"X-Fallback":   "true",
				"Content-Type": "text/plain",
			}),
		},
	}

	got := buildFallbackInfo(rule)

	assert.NotNil(t, got)
	assert.Equal(t, 599, got.Code)
	assert.Equal(t, "service degraded", got.Body)
	assert.Equal(t, "true", got.Headers["X-Fallback"])
	assert.Equal(t, "text/plain", got.Headers["Content-Type"])
	assert.Len(t, got.Headers, 2)
}

// TestBuildFallbackInfo_MethodLevelEmptyHeaders 测试场景：METHOD 级 headers 为空
// 前置条件：Level=METHOD，Enable=true，response 只有 code/body
// 预期结果：FallbackInfo.Headers 为非 nil 空 map，code/body 正确
func TestBuildFallbackInfo_MethodLevelEmptyHeaders(t *testing.T) {
	rule := &fault_tolerance.CircuitBreakerRule{
		Level: fault_tolerance.Level_METHOD,
		FallbackConfig: &fault_tolerance.FallbackConfig{
			Enable:   true,
			Response: buildFallbackResponse(429, "method degraded", nil),
		},
	}

	got := buildFallbackInfo(rule)

	assert.NotNil(t, got)
	assert.Equal(t, 429, got.Code)
	assert.Equal(t, "method degraded", got.Body)
	assert.NotNil(t, got.Headers)
	assert.Len(t, got.Headers, 0)
}
