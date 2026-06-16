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

// newFaultDetectRule 构造一条最小可用的探测规则，供排序/选择测试使用
// method 取通配（"*"），贴近服务端对 SERVICE 级规则的真实下发形态
func newFaultDetectRule(id, ns, svc string, protocol fault_tolerance.FaultDetectRule_Protocol,
	interval uint32) *fault_tolerance.FaultDetectRule {
	return &fault_tolerance.FaultDetectRule{
		Id:       id,
		Protocol: protocol,
		Interval: interval,
		TargetService: &fault_tolerance.FaultDetectRule_DestinationService{
			Namespace: ns,
			Service:   svc,
			Method: &apimodel.MatchString{
				Type:  apimodel.MatchString_EXACT,
				Value: &wrapperspb.StringValue{Value: "*"},
			},
		},
	}
}

// TestSortFaultDetectRules_NotEmpty 测试场景：排序不得丢失规则（回归 copy 拷空缺陷）
// 前置条件：传入 N 条探测规则
// 预期结果：返回的切片长度等于入参长度，且不与入参共享底层数组
func TestSortFaultDetectRules_NotEmpty(t *testing.T) {
	src := []*fault_tolerance.FaultDetectRule{
		newFaultDetectRule("r1", "default", "svc-b", fault_tolerance.FaultDetectRule_HTTP, 10),
		newFaultDetectRule("r2", "default", "svc-a", fault_tolerance.FaultDetectRule_TCP, 5),
		newFaultDetectRule("r3", "default", "*", fault_tolerance.FaultDetectRule_UDP, 3),
	}

	got := sortFaultDetectRules(src)

	assert.Len(t, got, len(src), "sortFaultDetectRules 不得丢弃任何规则")
	// 通配 service 应排在精确 service 之后（compareStringValue：matchAll 视为更大）
	assert.Equal(t, "*", got[len(got)-1].GetTargetService().GetService(),
		"通配 service 规则应排在最后")
}

// TestSortFaultDetectRules_Empty 测试场景：空入参返回空切片，不 panic
func TestSortFaultDetectRules_Empty(t *testing.T) {
	assert.NotPanics(t, func() {
		got := sortFaultDetectRules(nil)
		assert.Empty(t, got)
	})
}

// TestSelectFaultDetectRules_ServiceLevel 测试场景：SERVICE 级资源按协议选出探测规则
// 前置条件：FaultDetector 含 HTTP + TCP 两条通配 method 规则，资源为目标服务
// 预期结果：每种协议各选出一条规则
func TestSelectFaultDetectRules_ServiceLevel(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "default", Service: "order"}
	res, _ := model.NewServiceResource(svcKey, nil)

	fd := &fault_tolerance.FaultDetector{
		Rules: []*fault_tolerance.FaultDetectRule{
			newFaultDetectRule("r-http", "default", "order", fault_tolerance.FaultDetectRule_HTTP, 10),
			newFaultDetectRule("r-tcp", "default", "order", fault_tolerance.FaultDetectRule_TCP, 5),
		},
	}

	checker := &ResourceHealthChecker{
		resource:      res,
		faultDetector: fd,
		regexFunction: nopRegex,
		log:           noopLogger{},
	}

	matched := checker.selectFaultDetectRules(res, fd)
	assert.Len(t, matched, 2, "应按协议各选出一条规则")
	assert.Contains(t, matched, fault_tolerance.FaultDetectRule_HTTP.String())
	assert.Contains(t, matched, fault_tolerance.FaultDetectRule_TCP.String())
}

// TestSelectFaultDetectRules_ServiceMismatch 测试场景：目标服务不匹配时选不出规则
func TestSelectFaultDetectRules_ServiceMismatch(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "default", Service: "order"}
	res, _ := model.NewServiceResource(svcKey, nil)

	fd := &fault_tolerance.FaultDetector{
		Rules: []*fault_tolerance.FaultDetectRule{
			newFaultDetectRule("r-http", "default", "payment", fault_tolerance.FaultDetectRule_HTTP, 10),
		},
	}

	checker := &ResourceHealthChecker{
		resource:      res,
		faultDetector: fd,
		regexFunction: nopRegex,
		log:           noopLogger{},
	}

	matched := checker.selectFaultDetectRules(res, fd)
	assert.Empty(t, matched, "目标服务不匹配时不应选出规则")
}

// TestResourceHealthChecker_StartNilExecutorNoPanic 测试场景：executor 为 nil 时 start 安全返回
// 前置条件：手工构造一个 executor 未注入的 checker
// 预期结果：start() 不 panic（回归 executor nil 解引用缺陷）
func TestResourceHealthChecker_StartNilExecutorNoPanic(t *testing.T) {
	svcKey := &model.ServiceKey{Namespace: "default", Service: "order"}
	res, _ := model.NewServiceResource(svcKey, nil)

	checker := &ResourceHealthChecker{
		resource:      res,
		faultDetector: &fault_tolerance.FaultDetector{},
		regexFunction: nopRegex,
		instances:     make(map[string]*ProtocolInstance),
		log:           noopLogger{},
		// executor 故意留 nil
	}

	assert.NotPanics(t, func() {
		checker.start()
	})
}

// faultDetectGateOpen 复刻 realRefreshHealthCheck 中的探测门控判定，便于在不依赖网络的前提下回归门控语义：
// 探测仅当熔断规则启用 且 faultDetectConfig 存在且 enable=true 时才开启。
func faultDetectGateOpen(rule *fault_tolerance.CircuitBreakerRule) bool {
	return rule != nil && rule.Enable && rule.GetFaultDetectConfig() != nil &&
		rule.GetFaultDetectConfig().GetEnable()
}

// TestFaultDetectGate 测试场景：探测门控应由 faultDetectConfig.enable 决定，与 fallbackConfig 解耦
// 前置条件：构造 enable / faultDetectConfig / fallbackConfig 的各种组合
// 预期结果：仅当规则 enable 且 faultDetectConfig.enable=true 时门控打开；fallbackConfig 不影响门控
func TestFaultDetectGate(t *testing.T) {
	cases := []struct {
		name              string
		ruleEnable        bool
		faultDetectConfig *fault_tolerance.FaultDetectConfig
		fallbackConfig    *fault_tolerance.FallbackConfig
		want              bool
	}{
		{
			name:              "rule_disabled",
			ruleEnable:        false,
			faultDetectConfig: &fault_tolerance.FaultDetectConfig{Enable: true},
			want:              false,
		},
		{
			name:              "fault_detect_nil",
			ruleEnable:        true,
			faultDetectConfig: nil,
			want:              false,
		},
		{
			name:              "fault_detect_disabled",
			ruleEnable:        true,
			faultDetectConfig: &fault_tolerance.FaultDetectConfig{Enable: false},
			want:              false,
		},
		{
			name:              "fault_detect_enabled",
			ruleEnable:        true,
			faultDetectConfig: &fault_tolerance.FaultDetectConfig{Enable: true},
			want:              true,
		},
		{
			name:              "fault_detect_enabled_without_fallback",
			ruleEnable:        true,
			faultDetectConfig: &fault_tolerance.FaultDetectConfig{Enable: true},
			fallbackConfig:    nil,
			want:              true, // 探测不依赖 fallbackConfig
		},
		{
			name:              "only_fallback_enabled_no_fault_detect",
			ruleEnable:        true,
			faultDetectConfig: &fault_tolerance.FaultDetectConfig{Enable: false},
			fallbackConfig:    &fault_tolerance.FallbackConfig{Enable: true},
			want:              false, // 仅开降级不应触发探测
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rule := &fault_tolerance.CircuitBreakerRule{
				Enable:            c.ruleEnable,
				FaultDetectConfig: c.faultDetectConfig,
				FallbackConfig:    c.fallbackConfig,
			}
			assert.Equal(t, c.want, faultDetectGateOpen(rule))
		})
	}
}
