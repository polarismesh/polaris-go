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

package event

import (
	"testing"

	"github.com/polarismesh/specification/source/go/api/v1/fault_tolerance"
	"github.com/stretchr/testify/assert"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// TestParseCircuitBreakerStatus_Mapping 验证熔断状态到字符串的映射。
func TestParseCircuitBreakerStatus_Mapping(t *testing.T) {
	assert.Equal(t, "OPEN", parseCircuitBreakerStatus(model.Open))
	assert.Equal(t, "HALF_OPEN", parseCircuitBreakerStatus(model.HalfOpen))
	assert.Equal(t, "CLOSE", parseCircuitBreakerStatus(model.Close))
	assert.Equal(t, "UNKNOWN", parseCircuitBreakerStatus(model.Status(99)))
}

// TestParseFlowEventName_Mapping 验证目标状态字符串到事件名的映射。
func TestParseFlowEventName_Mapping(t *testing.T) {
	assert.Equal(t, CircuitBreakerOpen, parseFlowEventName("OPEN"))
	assert.Equal(t, CircuitBreakerHalfOpen, parseFlowEventName("HALF_OPEN"))
	assert.Equal(t, CircuitBreakerClose, parseFlowEventName("CLOSE"))
	assert.Equal(t, CircuitBreakerDestroy, parseFlowEventName("UNKNOWN"))
}

// TestBuildCircuitBreakerEvent_ServiceLevel 验证 SERVICE 级熔断事件的字段填充。
func TestBuildCircuitBreakerEvent_ServiceLevel(t *testing.T) {
	res, err := model.NewServiceResource(
		&model.ServiceKey{Namespace: "ns", Service: "svc"},
		&model.ServiceKey{Namespace: "caller-ns", Service: "caller-svc"},
	)
	assert.NoError(t, err)

	rule := newDemoCBRule()
	got := BuildCircuitBreakerEvent(res, rule, model.Close, model.Open, "ERROR_RATE:80% (threshold:50%)")

	assert.NotNil(t, got)
	assert.Equal(t, CircuitBreakerEventType.EventTypeString(), got.EventType)
	assert.Equal(t, CircuitBreakerOpen, got.EventName)
	assert.Equal(t, "ns", got.Namespace)
	assert.Equal(t, "svc", got.Service)
	assert.Equal(t, "caller-ns", got.SourceNamespace)
	assert.Equal(t, "caller-svc", got.SourceService)
	assert.Equal(t, "OPEN", got.CurrentStatus)
	assert.Equal(t, "CLOSE", got.PreviousStatus)
	assert.Equal(t, "SERVICE", got.ResourceType)
	assert.Equal(t, "demo-cb-rule", got.RuleName)
	assert.Equal(t, "ERROR_RATE:80% (threshold:50%)", got.Reason)
	assert.Equal(t, "ns#svc", got.AdditionalParams[IsolationObjectKey])
	assert.Equal(t, "50", got.AdditionalParams[FailureRateKey])
	assert.Equal(t, "30", got.AdditionalParams[SlowCallDurationKey])
	assert.NotEmpty(t, got.EventTime)
}

// TestBuildCircuitBreakerEvent_MethodLevel 验证 METHOD 级熔断事件的 API 字段与隔离对象。
func TestBuildCircuitBreakerEvent_MethodLevel(t *testing.T) {
	res, err := model.NewMethodResourceWithAPI(
		&model.ServiceKey{Namespace: "ns", Service: "svc"},
		&model.ServiceKey{Namespace: "caller-ns", Service: "caller-svc"},
		"http", "GET", "/api/foo",
	)
	assert.NoError(t, err)

	got := BuildCircuitBreakerEvent(res, newDemoCBRule(), model.HalfOpen, model.Open, "")

	assert.Equal(t, "METHOD", got.ResourceType)
	assert.Equal(t, "http", got.APIProtocol)
	assert.Equal(t, "/api/foo", got.APIPath)
	assert.Equal(t, "GET", got.APIMethod)
	assert.Equal(t, "ns#svc#(/api/foo,'GET')", got.AdditionalParams[IsolationObjectKey])
}

// TestBuildCircuitBreakerEvent_InstanceLevel 验证 INSTANCE 级熔断事件的 host/port 与隔离对象。
func TestBuildCircuitBreakerEvent_InstanceLevel(t *testing.T) {
	res, err := model.NewInstanceResource(
		&model.ServiceKey{Namespace: "ns", Service: "svc"},
		nil,
		"http", "1.2.3.4", 8080,
	)
	assert.NoError(t, err)

	got := BuildCircuitBreakerEvent(res, newDemoCBRule(), model.HalfOpen, model.Close, "")

	assert.Equal(t, CircuitBreakerClose, got.EventName)
	assert.Equal(t, "INSTANCE", got.ResourceType)
	assert.Equal(t, "1.2.3.4", got.Host)
	assert.Equal(t, "8080", got.Port)
	assert.Equal(t, "1.2.3.4:8080", got.AdditionalParams[IsolationObjectKey])
	// caller 为 nil 时来源字段保持空
	assert.Empty(t, got.SourceNamespace)
	assert.Empty(t, got.SourceService)
}

// TestBuildCircuitBreakerEvent_NilRule_EmptyRateParams 验证 rule 为 nil 时阈值参数留空且不 panic。
func TestBuildCircuitBreakerEvent_NilRule_EmptyRateParams(t *testing.T) {
	res, err := model.NewServiceResource(&model.ServiceKey{Namespace: "ns", Service: "svc"}, nil)
	assert.NoError(t, err)

	got := BuildCircuitBreakerEvent(res, nil, model.Close, model.Open, "")
	assert.NotNil(t, got)
	assert.Equal(t, "", got.RuleName)
	assert.Equal(t, "", got.AdditionalParams[FailureRateKey])
	assert.Equal(t, "", got.AdditionalParams[SlowCallDurationKey])
}

// newDemoCBRule 构造一条含 failure / slow 两个 BlockConfig 的熔断规则用于测试。
func newDemoCBRule() *fault_tolerance.CircuitBreakerRule {
	return &fault_tolerance.CircuitBreakerRule{
		Name: "demo-cb-rule",
		BlockConfigs: []*fault_tolerance.BlockConfig{
			{
				Name: "failure",
				TriggerConditions: []*fault_tolerance.TriggerCondition{
					{ErrorPercent: 50},
				},
			},
			{
				Name: "slow",
				TriggerConditions: []*fault_tolerance.TriggerCondition{
					{ErrorPercent: 30},
				},
			},
		},
	}
}
