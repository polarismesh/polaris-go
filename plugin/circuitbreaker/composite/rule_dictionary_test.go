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

// buildSimpleRule 构造一条简单熔断规则用于字典测试
// level    规则等级
// dstNs    目标命名空间（空串视为通配 *）
// dstSvc   目标服务（空串视为通配 *）
// blocks   BlockConfig 列表（METHOD 级用其 Api 做接口匹配）
// dstNs    目标命名空间（空串视为通配 *）
// dstSvc   目标服务（空串视为通配 *）
// blocks   BlockConfig 列表（METHOD 级用其 Api 做接口匹配）
func buildSimpleRule(name string, level fault_tolerance.Level, dstNs, dstSvc string,
	blocks []*fault_tolerance.BlockConfig) *fault_tolerance.CircuitBreakerRule {
	if dstNs == "" {
		dstNs = "*"
	}
	if dstSvc == "" {
		dstSvc = "*"
	}
	rule := &fault_tolerance.CircuitBreakerRule{
		Id:     name,
		Name:   name,
		Enable: true,
		Level:  level,
		RuleMatcher: &fault_tolerance.RuleMatcher{
			Destination: &fault_tolerance.RuleMatcher_DestinationService{
				Namespace: dstNs,
				Service:   dstSvc,
				Method: &apimodel.MatchString{
					Type:  apimodel.MatchString_EXACT,
					Value: &wrapperspb.StringValue{Value: "*"},
				},
			},
			Source: &fault_tolerance.RuleMatcher_SourceService{Namespace: "*", Service: "*"},
		},
		BlockConfigs: blocks,
	}
	return rule
}

// TestRuleDictionary_LookupHitsByLevel 测试场景：同一服务下三个 level 的规则按 level 命中各自资源
// 前置条件：注入 SERVICE / METHOD / INSTANCE 三条规则到同一 ServiceKey
// 预期结果：对每个 level 的资源 Lookup 只返回与其 level 一致的规则
func TestRuleDictionary_LookupHitsByLevel(t *testing.T) {
	dict := newCircuitBreakerRuleDictionary(nopRegex, noopLogger{})

	svcKey := model.ServiceKey{Namespace: "default", Service: "order"}
	caller := &model.ServiceKey{Namespace: "default", Service: "front"}

	// METHOD 规则带一个匹配 path=/api/* 的 BlockConfig
	methodBlock := &fault_tolerance.BlockConfig{
		Name: "method-block",
		Api: &apimodel.API{
			Path: &apimodel.MatchString{
				Type:  apimodel.MatchString_REGEX,
				Value: &wrapperspb.StringValue{Value: "/api/.+"},
			},
		},
	}

	resp := &model.ServiceRuleResponse{
		Value: &fault_tolerance.CircuitBreaker{
			Rules: []*fault_tolerance.CircuitBreakerRule{
				buildSimpleRule("svc-rule", fault_tolerance.Level_SERVICE, "default", "order", nil),
				buildSimpleRule("method-rule", fault_tolerance.Level_METHOD, "default", "order",
					[]*fault_tolerance.BlockConfig{methodBlock}),
				buildSimpleRule("ins-rule", fault_tolerance.Level_INSTANCE, "default", "order", nil),
			},
		},
	}
	dict.PutServiceRule(svcKey, resp)

	svcRes, _ := model.NewServiceResource(&svcKey, caller)
	got := dict.Lookup(svcRes)
	assert.NotNil(t, got)
	assert.Equal(t, "svc-rule", got.GetName())

	methodRes, _ := model.NewMethodResourceWithAPI(&svcKey, caller, "http", "GET", "/api/v1/orders")
	got = dict.Lookup(methodRes)
	assert.NotNil(t, got)
	assert.Equal(t, "method-rule", got.GetName())

	insRes, _ := model.NewInstanceResource(&svcKey, caller, "http", "127.0.0.1", 8080)
	got = dict.Lookup(insRes)
	assert.NotNil(t, got)
	assert.Equal(t, "ins-rule", got.GetName())
}

// TestRuleDictionary_OnServiceChangedClearsAllLevels 测试场景：OnServiceChanged 一次性清空该服务全部 level 缓存
// 前置条件：注入两条规则后调用 OnServiceChanged
// 预期结果：再次 Lookup 任一 level 都返回 nil
func TestRuleDictionary_OnServiceChangedClearsAllLevels(t *testing.T) {
	dict := newCircuitBreakerRuleDictionary(nopRegex, noopLogger{})

	svcKey := model.ServiceKey{Namespace: "default", Service: "order"}
	caller := &model.ServiceKey{Namespace: "default", Service: "front"}

	dict.PutServiceRule(svcKey, &model.ServiceRuleResponse{
		Value: &fault_tolerance.CircuitBreaker{
			Rules: []*fault_tolerance.CircuitBreakerRule{
				buildSimpleRule("svc-rule", fault_tolerance.Level_SERVICE, "default", "order", nil),
				buildSimpleRule("ins-rule", fault_tolerance.Level_INSTANCE, "default", "order", nil),
			},
		},
	})
	dict.OnServiceChanged(svcKey)

	svcRes, _ := model.NewServiceResource(&svcKey, caller)
	insRes, _ := model.NewInstanceResource(&svcKey, caller, "http", "127.0.0.1", 8080)
	assert.Nil(t, dict.Lookup(svcRes))
	assert.Nil(t, dict.Lookup(insRes))
}

// TestRuleDictionary_LookupSkipsDisabledRule 测试场景：Lookup 跳过 Enable=false 的规则
// 前置条件：唯一一条规则 Enable=false
// 预期结果：Lookup 返回 nil
func TestRuleDictionary_LookupSkipsDisabledRule(t *testing.T) {
	dict := newCircuitBreakerRuleDictionary(nopRegex, noopLogger{})

	svcKey := model.ServiceKey{Namespace: "default", Service: "order"}
	caller := &model.ServiceKey{Namespace: "default", Service: "front"}
	rule := buildSimpleRule("svc-rule", fault_tolerance.Level_SERVICE, "default", "order", nil)
	rule.Enable = false

	dict.PutServiceRule(svcKey, &model.ServiceRuleResponse{
		Value: &fault_tolerance.CircuitBreaker{
			Rules: []*fault_tolerance.CircuitBreakerRule{rule},
		},
	})
	svcRes, _ := model.NewServiceResource(&svcKey, caller)
	assert.Nil(t, dict.Lookup(svcRes))
}

// TestRuleDictionary_LookupReplacesOnRePut 测试场景：相同服务再次 PutServiceRule 时替换旧规则
// 前置条件：先注入名为 old 的规则，再注入名为 new 的规则
// 预期结果：Lookup 命中 new；老规则不再返回
func TestRuleDictionary_LookupReplacesOnRePut(t *testing.T) {
	dict := newCircuitBreakerRuleDictionary(nopRegex, noopLogger{})
	svcKey := model.ServiceKey{Namespace: "default", Service: "order"}
	caller := &model.ServiceKey{Namespace: "default", Service: "front"}

	dict.PutServiceRule(svcKey, &model.ServiceRuleResponse{
		Value: &fault_tolerance.CircuitBreaker{
			Rules: []*fault_tolerance.CircuitBreakerRule{
				buildSimpleRule("old", fault_tolerance.Level_SERVICE, "default", "order", nil),
			},
		},
	})
	dict.PutServiceRule(svcKey, &model.ServiceRuleResponse{
		Value: &fault_tolerance.CircuitBreaker{
			Rules: []*fault_tolerance.CircuitBreakerRule{
				buildSimpleRule("new", fault_tolerance.Level_SERVICE, "default", "order", nil),
			},
		},
	})
	svcRes, _ := model.NewServiceResource(&svcKey, caller)
	got := dict.Lookup(svcRes)
	assert.NotNil(t, got)
	assert.Equal(t, "new", got.GetName())
}

// TestRuleDictionary_LookupOnEmptyResp 测试场景：PutServiceRule 传入空响应等价于清空
// 前置条件：先注入一条规则，再用空响应覆盖
// 预期结果：Lookup 返回 nil
func TestRuleDictionary_LookupOnEmptyResp(t *testing.T) {
	dict := newCircuitBreakerRuleDictionary(nopRegex, noopLogger{})
	svcKey := model.ServiceKey{Namespace: "default", Service: "order"}
	caller := &model.ServiceKey{Namespace: "default", Service: "front"}

	dict.PutServiceRule(svcKey, &model.ServiceRuleResponse{
		Value: &fault_tolerance.CircuitBreaker{
			Rules: []*fault_tolerance.CircuitBreakerRule{
				buildSimpleRule("svc", fault_tolerance.Level_SERVICE, "default", "order", nil),
			},
		},
	})
	dict.PutServiceRule(svcKey, nil)

	svcRes, _ := model.NewServiceResource(&svcKey, caller)
	assert.Nil(t, dict.Lookup(svcRes))
}
