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

package common

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// TestConvertCircuitBreakGaugeToLabels_ServiceLevel 验证 SERVICE 级 gauge 的标签转换：
// callee 取被调服务、caller 取主调服务、caller_ip 由 bindIP 注入、rule_name 正确填充。
func TestConvertCircuitBreakGaugeToLabels_ServiceLevel(t *testing.T) {
	gauge := &model.CircuitBreakGauge{
		Level:         "SERVICE",
		RuleName:      "cb-rule",
		CalleeService: &model.ServiceKey{Namespace: "callee-ns", Service: "callee-svc"},
		CallerService: &model.ServiceKey{Namespace: "caller-ns", Service: "caller-svc"},
		CBStatus:      model.NewCircuitBreakerStatus("cb-rule", model.Open, time.Now()),
	}

	labels := ConvertCircuitBreakGaugeToLabels(gauge, "10.0.0.1")

	assert.Equal(t, "callee-ns", labels[CalleeNamespace])
	assert.Equal(t, "callee-svc", labels[CalleeService])
	assert.Equal(t, "caller-ns", labels[CallerNamespace])
	assert.Equal(t, "caller-svc", labels[CallerService])
	assert.Equal(t, "10.0.0.1", labels[CallerIP])
	assert.Equal(t, "cb-rule", labels[RuleName])
	// SERVICE 级无实例维度
	assert.Equal(t, "", labels[CalleeInstance])
	assert.Equal(t, "", labels[CalleeSubset])
}

// TestConvertCircuitBreakGaugeToLabels_NilServiceKeys 验证 CalleeService/CallerService 为 nil
// 时各 namespace/service 标签安全返回空串，不 panic。
func TestConvertCircuitBreakGaugeToLabels_NilServiceKeys(t *testing.T) {
	gauge := &model.CircuitBreakGauge{
		Level:    "SERVICE",
		RuleName: "cb-rule",
	}
	labels := ConvertCircuitBreakGaugeToLabels(gauge, "")
	assert.Equal(t, "", labels[CalleeNamespace])
	assert.Equal(t, "", labels[CalleeService])
	assert.Equal(t, "", labels[CallerNamespace])
	assert.Equal(t, "", labels[CallerService])
	assert.Equal(t, "", labels[CalleeInstance])
}

// TestCircuitBreakerStrategy_PointerAssertion 验证缺陷 A 修复：策略对 *CircuitBreakGauge
// （指针）断言成功，Open/HalfOpen 初值能正确计算（修复前断言值类型恒失败，指标恒为 0）。
func TestCircuitBreakerStrategy_PointerAssertion(t *testing.T) {
	openGauge := &model.CircuitBreakGauge{
		CBStatus: model.NewCircuitBreakerStatus("cb-rule", model.Open, time.Now()),
	}
	halfOpenGauge := &model.CircuitBreakGauge{
		CBStatus: model.NewHalfOpenStatus("cb-rule", time.Now(), 3),
	}

	openStrategy := &CircuitBreakerOpenStrategy{}
	halfOpenStrategy := &CircuitBreakerHalfOpenStrategy{}

	assert.Equal(t, float64(1), openStrategy.InitMetricValue(openGauge), "OPEN 状态 open 指标初值应为 1")
	assert.Equal(t, float64(0), openStrategy.InitMetricValue(halfOpenGauge))
	assert.Equal(t, float64(1), halfOpenStrategy.InitMetricValue(halfOpenGauge), "HALF_OPEN 状态 halfopen 指标初值应为 1")
	assert.Equal(t, float64(0), halfOpenStrategy.InitMetricValue(openGauge))

	// 值类型传入应断言失败返回 0（确认全链路统一为指针）
	assert.Equal(t, float64(0), openStrategy.InitMetricValue(model.CircuitBreakGauge{
		CBStatus: model.NewCircuitBreakerStatus("cb-rule", model.Open, time.Now()),
	}))
}
