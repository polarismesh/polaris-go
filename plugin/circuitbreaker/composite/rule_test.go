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

	"github.com/polarismesh/polaris-go/pkg/model"
)

// buildMethodRule 用最小代价构造一条 METHOD 级熔断规则
// 调用方按需向 blockConfigs 提供匹配条件
func buildMethodRule(name string, destNs, destSvc string,
	blockConfigs []*fault_tolerance.BlockConfig) *fault_tolerance.CircuitBreakerRule {
	return &fault_tolerance.CircuitBreakerRule{
		Name:   name,
		Enable: true,
		Level:  fault_tolerance.Level_METHOD,
		RuleMatcher: &fault_tolerance.RuleMatcher{
			Source: &fault_tolerance.RuleMatcher_SourceService{
				Namespace: "*", Service: "*",
			},
			Destination: &fault_tolerance.RuleMatcher_DestinationService{
				Namespace: destNs, Service: destSvc,
			},
		},
		BlockConfigs: blockConfigs,
	}
}

// buildServiceRule 构造一条 SERVICE 级熔断规则
func buildServiceRule(name string, destNs, destSvc string) *fault_tolerance.CircuitBreakerRule {
	return &fault_tolerance.CircuitBreakerRule{
		Name:   name,
		Enable: true,
		Level:  fault_tolerance.Level_SERVICE,
		RuleMatcher: &fault_tolerance.RuleMatcher{
			Source: &fault_tolerance.RuleMatcher_SourceService{
				Namespace: "*", Service: "*",
			},
			Destination: &fault_tolerance.RuleMatcher_DestinationService{
				Namespace: destNs, Service: destSvc,
			},
		},
	}
}

// wrapAsRuleResponse 把规则包装成 selectCircuitBreakerRule 期望的 ServiceRuleResponse
func wrapAsRuleResponse(rules ...*fault_tolerance.CircuitBreakerRule) *model.ServiceRuleResponse {
	return &model.ServiceRuleResponse{
		Value: &fault_tolerance.CircuitBreaker{Rules: rules},
	}
}

// TestSelectCircuitBreakerRule_ServiceLevelMatchesWithoutMethod 测试场景：SERVICE 级规则不参与 method 维度匹配
// 前置条件：ServiceResource + 一条 SERVICE 级规则（不含 BlockConfigs）
// 预期结果：规则命中，不会因为没有 BlockConfigs 而被跳过
func TestSelectCircuitBreakerRule_ServiceLevelMatchesWithoutMethod(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "Production", Service: "payment-service"}
	res, _ := model.NewServiceResource(svcKey, nil)
	rule := buildServiceRule("svc-rule", "Production", "payment-service")
	resp := wrapAsRuleResponse(rule)

	got := selectCircuitBreakerRule(res, resp, nopRegex, noopLogger{})
	assert.NotNil(t, got)
	assert.Equal(t, "svc-rule", got.Name)
}

// TestSelectCircuitBreakerRule_MethodLevelByBlockConfigsAny 测试场景：METHOD 级规则的多 BlockConfig 任一命中即命中
// 前置条件：一条规则包含 2 个 BlockConfig，第二个的 path 与资源匹配
// 预期结果：规则被选中（不要求第一个匹配，验证"任一即可"语义）
func TestSelectCircuitBreakerRule_MethodLevelByBlockConfigsAny(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "Production", Service: "order-service"}
	res, _ := model.NewMethodResourceWithAPI(svcKey, nil, "http", "POST", "/api/v1/orders")
	bc1 := &fault_tolerance.BlockConfig{
		Name: "users-only",
		Api: &apimodel.API{Protocol: "http", Method: "POST",
			Path: newMatchString(apimodel.MatchString_EXACT, "/api/v1/users")},
	}
	bc2 := &fault_tolerance.BlockConfig{
		Name: "orders-strict",
		Api: &apimodel.API{Protocol: "http", Method: "POST",
			Path: newMatchString(apimodel.MatchString_EXACT, "/api/v1/orders")},
	}
	rule := buildMethodRule("multi-bc-rule", "Production", "order-service",
		[]*fault_tolerance.BlockConfig{bc1, bc2})

	got := selectCircuitBreakerRule(res, wrapAsRuleResponse(rule), nopRegex, noopLogger{})
	assert.NotNil(t, got)
	assert.Equal(t, "multi-bc-rule", got.Name)
}

// TestSelectCircuitBreakerRule_MethodLevelNoBlockConfigMatched 测试场景：METHOD 级规则所有 BlockConfig 均不匹配
// 前置条件：资源 path 与所有 BlockConfig.api.path 不一致
// 预期结果：规则不命中，返回 nil
func TestSelectCircuitBreakerRule_MethodLevelNoBlockConfigMatched(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "Production", Service: "order-service"}
	res, _ := model.NewMethodResourceWithAPI(svcKey, nil, "http", "POST", "/api/v1/orders")
	bc := &fault_tolerance.BlockConfig{
		Api: &apimodel.API{Protocol: "http", Method: "POST",
			Path: newMatchString(apimodel.MatchString_EXACT, "/api/v1/users")},
	}
	rule := buildMethodRule("no-match-rule", "Production", "order-service",
		[]*fault_tolerance.BlockConfig{bc})

	got := selectCircuitBreakerRule(res, wrapAsRuleResponse(rule), nopRegex, noopLogger{})
	assert.Nil(t, got)
}

// TestSelectCircuitBreakerRule_MethodLevelLegacyDestinationMethodFallback 测试场景：BlockConfigs 为空时回退到 destination.Method
// 前置条件：规则未填 BlockConfigs，但 destination.Method 指向资源 path
// 预期结果：兼容路径生效，规则命中
func TestSelectCircuitBreakerRule_MethodLevelLegacyDestinationMethodFallback(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "Production", Service: "order-service"}
	res, _ := model.NewMethodResource(svcKey, nil, "/legacy/api")
	rule := buildMethodRule("legacy-rule", "Production", "order-service", nil)
	rule.RuleMatcher.Destination.Method = newMatchString(apimodel.MatchString_EXACT, "/legacy/api")

	got := selectCircuitBreakerRule(res, wrapAsRuleResponse(rule), nopRegex, noopLogger{})
	assert.NotNil(t, got)
	assert.Equal(t, "legacy-rule", got.Name)
}

// TestSelectCircuitBreakerRule_DisabledRuleSkipped 测试场景：Enable=false 的规则被跳过
// 前置条件：规则字段一致但 Enable 标志为 false
// 预期结果：返回 nil
func TestSelectCircuitBreakerRule_DisabledRuleSkipped(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "Production", Service: "order-service"}
	res, _ := model.NewServiceResource(svcKey, nil)
	rule := buildServiceRule("disabled-rule", "Production", "order-service")
	rule.Enable = false

	got := selectCircuitBreakerRule(res, wrapAsRuleResponse(rule), nopRegex, noopLogger{})
	assert.Nil(t, got)
}

// TestSelectCircuitBreakerRule_LevelMismatch 测试场景：规则 Level 与资源 Level 不一致时跳过
// 前置条件：MethodResource 但只配 SERVICE 级规则
// 预期结果：返回 nil
func TestSelectCircuitBreakerRule_LevelMismatch(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "Production", Service: "order-service"}
	res, _ := model.NewMethodResourceWithAPI(svcKey, nil, "http", "GET", "/a")
	rule := buildServiceRule("svc-only", "Production", "order-service")

	got := selectCircuitBreakerRule(res, wrapAsRuleResponse(rule), nopRegex, noopLogger{})
	assert.Nil(t, got)
}
