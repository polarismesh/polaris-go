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
	"sync/atomic"
	"testing"
	"time"

	"github.com/polarismesh/specification/source/go/api/v1/fault_tolerance"
	"github.com/stretchr/testify/assert"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/event"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/events"
	"github.com/polarismesh/polaris-go/pkg/sdk"
)

// fakeReportEngine 仅实现监控/事件上报相关的两个方法，其余 sdk.Engine 方法继承嵌入的 nil 接口，
// 一旦被误调用会 panic，可在测试中迅速暴露超出预期的依赖。
type fakeReportEngine struct {
	sdk.Engine // 嵌入 nil 接口；未覆写的方法被调用即 panic
	// reportedType / reportedGauge 记录最后一次 SyncReportStat 的入参，供断言
	reportedType  model.MetricType
	reportedGauge model.InstanceGauge
	reportCalled  int
	// eventChain 由 GetEventReportChain 返回，模拟已组装的 EventReporter 链
	eventChain interface{}
}

// SyncReportStat 捕获上报指标的调用参数，始终返回成功（不触发上报失败日志路径）。
func (f *fakeReportEngine) SyncReportStat(typ model.MetricType, stat model.InstanceGauge) error {
	f.reportCalled++
	f.reportedType = typ
	f.reportedGauge = stat
	return nil
}

// GetEventReportChain 返回预置的事件上报链。
func (f *fakeReportEngine) GetEventReportChain() interface{} {
	return f.eventChain
}

// fakeEventReporter 记录收到的事件，用于断言 sendEvent 的投递行为。
// 嵌入 plugin.Plugin（nil 接口）以满足 events.EventReporter 的方法集，仅覆写 ReportEvent。
type fakeEventReporter struct {
	plugin.Plugin
	events []event.BaseEvent
}

// ReportEvent 记录事件并返回成功（不触发 sendEvent 的失败日志路径）。
func (r *fakeEventReporter) ReportEvent(e event.BaseEvent) error {
	r.events = append(r.events, e)
	return nil
}

// newRCWithResource 构造一个仅携带资源、规则与引擎的 ResourceCounters，用于上报方法测试。
func newRCWithResource(resource model.Resource, rule *fault_tolerance.CircuitBreakerRule,
	engine sdk.Engine) *ResourceCounters {
	return &ResourceCounters{
		activeRule: rule,
		resource:   resource,
		engineFlow: engine,
	}
}

// TestBuildCircuitBreakGauge_ServiceLevel 测试场景：SERVICE 级资源构造的 gauge 携带正确的被调/主调服务与 Level
// 前置条件：NewServiceResource 构造的 SERVICE 级资源 + 含 Name 的规则
// 预期结果：Level=SERVICE、RuleName 取规则名、CalleeService/CallerService 取资源服务键、无 Method/ChangeInstance
func TestBuildCircuitBreakGauge_ServiceLevel(t *testing.T) {
	svc := &model.ServiceKey{Namespace: "default", Service: "callee-svc"}
	caller := &model.ServiceKey{Namespace: "default", Service: "caller-svc"}
	resource, err := model.NewServiceResource(svc, caller)
	assert.NoError(t, err)

	rule := &fault_tolerance.CircuitBreakerRule{Name: "rule-service"}
	rc := newRCWithResource(resource, rule, nil)

	status := model.NewCircuitBreakerStatus("rule-service", model.Open, time.Now())
	gauge := rc.buildCircuitBreakGauge(status)

	assert.Equal(t, "SERVICE", gauge.Level)
	assert.Equal(t, "rule-service", gauge.RuleName)
	assert.Equal(t, svc, gauge.CalleeService)
	assert.Equal(t, caller, gauge.CallerService)
	assert.Equal(t, "", gauge.Method)
	assert.Nil(t, gauge.ChangeInstance)
	assert.Equal(t, status, gauge.CBStatus)
}

// TestBuildCircuitBreakGauge_MethodLevel 测试场景：METHOD 级资源构造的 gauge 携带方法路径
// 前置条件：NewMethodResourceWithAPI 构造的 METHOD 级资源
// 预期结果：Level=METHOD、Method 取资源 Path、无 ChangeInstance
func TestBuildCircuitBreakGauge_MethodLevel(t *testing.T) {
	svc := &model.ServiceKey{Namespace: "default", Service: "callee-svc"}
	caller := &model.ServiceKey{Namespace: "default", Service: "caller-svc"}
	resource, err := model.NewMethodResourceWithAPI(svc, caller, "http", "GET", "/api/echo")
	assert.NoError(t, err)

	rule := &fault_tolerance.CircuitBreakerRule{Name: "rule-method"}
	rc := newRCWithResource(resource, rule, nil)

	status := model.NewCircuitBreakerStatus("rule-method", model.Open, time.Now())
	gauge := rc.buildCircuitBreakGauge(status)

	assert.Equal(t, "METHOD", gauge.Level)
	assert.Equal(t, "/api/echo", gauge.Method)
	assert.Equal(t, svc, gauge.CalleeService)
	assert.Nil(t, gauge.ChangeInstance)
}

// TestBuildCircuitBreakGauge_InstanceLevel 测试场景：INSTANCE 级资源构造的 gauge 携带被熔断实例
// 前置条件：NewInstanceResource 构造的 INSTANCE 级资源（host:port）
// 预期结果：Level=INSTANCE、ChangeInstance 非 nil 且 host/port 与资源节点一致
func TestBuildCircuitBreakGauge_InstanceLevel(t *testing.T) {
	svc := &model.ServiceKey{Namespace: "default", Service: "callee-svc"}
	caller := &model.ServiceKey{Namespace: "default", Service: "caller-svc"}
	resource, err := model.NewInstanceResource(svc, caller, "http", "1.2.3.4", 8080)
	assert.NoError(t, err)

	rule := &fault_tolerance.CircuitBreakerRule{Name: "rule-instance"}
	rc := newRCWithResource(resource, rule, nil)

	status := model.NewCircuitBreakerStatus("rule-instance", model.Open, time.Now())
	gauge := rc.buildCircuitBreakGauge(status)

	assert.Equal(t, "INSTANCE", gauge.Level)
	assert.NotNil(t, gauge.ChangeInstance)
	assert.Equal(t, "1.2.3.4", gauge.ChangeInstance.GetHost())
	assert.Equal(t, uint32(8080), gauge.ChangeInstance.GetPort())
}

// TestReportCircuitBreakMetric_NilEngine 测试场景：engineFlow 为 nil 时安全静默
// 前置条件：ResourceCounters.engineFlow 未注入
// 预期结果：不 panic，不产生任何上报
func TestReportCircuitBreakMetric_NilEngine(t *testing.T) {
	svc := &model.ServiceKey{Namespace: "default", Service: "callee-svc"}
	resource, err := model.NewServiceResource(svc, nil)
	assert.NoError(t, err)
	rc := newRCWithResource(resource, &fault_tolerance.CircuitBreakerRule{Name: "r"}, nil)

	assert.NotPanics(t, func() {
		rc.reportCircuitBreakMetric(model.NewCircuitBreakerStatus("r", model.Open, time.Now()))
	})
}

// TestReportCircuitBreakMetric_ReportsGauge 测试场景：engineFlow 非 nil 时按 CircuitBreakStat 类型上报 gauge
// 前置条件：注入 fakeReportEngine 捕获 SyncReportStat 调用
// 预期结果：SyncReportStat 被调用一次，metricType 为 CircuitBreakStat，gauge 字段正确
func TestReportCircuitBreakMetric_ReportsGauge(t *testing.T) {
	svc := &model.ServiceKey{Namespace: "default", Service: "callee-svc"}
	resource, err := model.NewServiceResource(svc, nil)
	assert.NoError(t, err)
	engine := &fakeReportEngine{}
	rc := newRCWithResource(resource, &fault_tolerance.CircuitBreakerRule{Name: "r"}, engine)

	status := model.NewCircuitBreakerStatus("r", model.Open, time.Now())
	rc.reportCircuitBreakMetric(status)

	assert.Equal(t, 1, engine.reportCalled)
	assert.Equal(t, model.CircuitBreakStat, engine.reportedType)
	gauge, ok := engine.reportedGauge.(*model.CircuitBreakGauge)
	assert.True(t, ok)
	assert.Equal(t, "SERVICE", gauge.Level)
	assert.Equal(t, status, gauge.CBStatus)
}

// TestSendEvent_NilEngineOrNilEvent 测试场景：engineFlow 或 event 为 nil 时安全静默
// 前置条件：分别构造 engineFlow=nil 与 eventInfo=nil 两种入参
// 预期结果：均不 panic
func TestSendEvent_NilEngineOrNilEvent(t *testing.T) {
	rcNilEngine := &ResourceCounters{}
	assert.NotPanics(t, func() {
		rcNilEngine.sendEvent(&event.BaseEventImpl{})
	})

	engine := &fakeReportEngine{eventChain: []events.EventReporter{&fakeEventReporter{}}}
	rcNilEvent := &ResourceCounters{engineFlow: engine}
	assert.NotPanics(t, func() {
		rcNilEvent.sendEvent(nil)
	})
}

// TestSendEvent_EmptyChain 测试场景：事件链为空或类型不符时静默返回
// 前置条件：GetEventReportChain 返回 nil / 空切片
// 预期结果：不 panic，无投递
func TestSendEvent_EmptyChain(t *testing.T) {
	engineNilChain := &fakeReportEngine{eventChain: nil}
	rc := &ResourceCounters{engineFlow: engineNilChain}
	assert.NotPanics(t, func() {
		rc.sendEvent(&event.BaseEventImpl{})
	})

	engineEmptyChain := &fakeReportEngine{eventChain: []events.EventReporter{}}
	rc2 := &ResourceCounters{engineFlow: engineEmptyChain}
	assert.NotPanics(t, func() {
		rc2.sendEvent(&event.BaseEventImpl{})
	})
}

// TestSendEvent_DeliversToChain 测试场景：事件被投递到链上每个 reporter
// 前置条件：事件链含两个 fakeEventReporter
// 预期结果：两个 reporter 各收到一次相同事件
func TestSendEvent_DeliversToChain(t *testing.T) {
	r1 := &fakeEventReporter{}
	r2 := &fakeEventReporter{}
	engine := &fakeReportEngine{eventChain: []events.EventReporter{r1, r2}}
	rc := &ResourceCounters{engineFlow: engine}

	eventInfo := &event.BaseEventImpl{
		EventType: event.CircuitBreakerEventType.EventTypeString(),
		EventName: event.CircuitBreakerOpen,
	}
	rc.sendEvent(eventInfo)

	assert.Len(t, r1.events, 1)
	assert.Len(t, r2.events, 1)
	assert.Equal(t, eventInfo, r1.events[0])
	assert.Equal(t, eventInfo, r2.events[0])
}

// TestReportCircuitBreakerEvent_BuildsAndDelivers 测试场景：reportCircuitBreakerEvent 构造事件并投递
// 前置条件：SERVICE 级资源 + 含 failure 块的规则 + 单 reporter 链
// 预期结果：reporter 收到一个 CircuitBreakerOpen 事件，状态与原因字段正确
func TestReportCircuitBreakerEvent_BuildsAndDelivers(t *testing.T) {
	svc := &model.ServiceKey{Namespace: "default", Service: "callee-svc"}
	resource, err := model.NewServiceResource(svc, nil)
	assert.NoError(t, err)

	reporter := &fakeEventReporter{}
	engine := &fakeReportEngine{eventChain: []events.EventReporter{reporter}}
	rule := &fault_tolerance.CircuitBreakerRule{Name: "rule-open"}
	rc := newRCWithResource(resource, rule, engine)

	rc.reportCircuitBreakerEvent(model.Close, model.Open, "ERROR_RATE:60% (threshold:50%)")

	assert.Len(t, reporter.events, 1)
	got, ok := reporter.events[0].(*event.BaseEventImpl)
	assert.True(t, ok)
	assert.Equal(t, event.CircuitBreakerOpen, got.GetEventName())
	assert.Equal(t, "OPEN", got.CurrentStatus)
	assert.Equal(t, "CLOSE", got.PreviousStatus)
	assert.Equal(t, "ERROR_RATE:60% (threshold:50%)", got.Reason)
}

// TestReportDestroyEvent_OverridesEventName 测试场景：reportDestroyEvent 覆写 EventName 为 Destroy
// 前置条件：资源已处于 Open 状态（statusRef 已存）+ 单 reporter 链
// 预期结果：reporter 收到事件，EventName=CircuitBreakerDestroy，PreviousStatus 为当前状态
func TestReportDestroyEvent_OverridesEventName(t *testing.T) {
	svc := &model.ServiceKey{Namespace: "default", Service: "callee-svc"}
	resource, err := model.NewServiceResource(svc, nil)
	assert.NoError(t, err)

	reporter := &fakeEventReporter{}
	engine := &fakeReportEngine{eventChain: []events.EventReporter{reporter}}
	rule := &fault_tolerance.CircuitBreakerRule{Name: "rule-destroy"}
	rc := newRCWithResource(resource, rule, engine)

	// 预置当前状态为 Open
	rc.statusRef = atomic.Value{}
	rc.statusRef.Store(&model.CircuitBreakerStatusWrapper{
		Val: model.NewCircuitBreakerStatus("rule-destroy", model.Open, time.Now()),
	})

	rc.reportDestroyEvent()

	assert.Len(t, reporter.events, 1)
	got, ok := reporter.events[0].(*event.BaseEventImpl)
	assert.True(t, ok)
	assert.Equal(t, event.CircuitBreakerDestroy, got.GetEventName())
	assert.Equal(t, "OPEN", got.PreviousStatus)
}

// TestReportDestroyEvent_NilStatusNoEvent 测试场景：无当前状态时不上报 Destroy
// 前置条件：statusRef 未存储任何状态（CurrentCircuitBreakerStatus 返回 nil）
// 预期结果：reporter 未收到任何事件
func TestReportDestroyEvent_NilStatusNoEvent(t *testing.T) {
	svc := &model.ServiceKey{Namespace: "default", Service: "callee-svc"}
	resource, err := model.NewServiceResource(svc, nil)
	assert.NoError(t, err)

	reporter := &fakeEventReporter{}
	engine := &fakeReportEngine{eventChain: []events.EventReporter{reporter}}
	rc := newRCWithResource(resource, &fault_tolerance.CircuitBreakerRule{Name: "r"}, engine)

	rc.reportDestroyEvent()

	assert.Empty(t, reporter.events)
}
