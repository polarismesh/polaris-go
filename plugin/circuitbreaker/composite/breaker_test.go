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
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// newCompositeForTest 仅初始化纯逻辑测试关心的字段，避免触发 plugin.PluginBase 全套依赖
// 调用方按需补齐 reportedServiceKeys / ruleDict / engineFlow / start 标志位等字段。
func newCompositeForTest() *CompositeCircuitBreaker {
	return &CompositeCircuitBreaker{
		cbLog: noopLogger{},
	}
}

// TestCompositeCircuitBreaker_RecordReportedService_Idempotent 测试场景：同一服务重复 Report 时仅首次记录
// 前置条件：手工注入 reportedServiceKeys = sync.Map；多次调用 recordReportedService 同一 ServiceKey
// 预期结果：sync.Map 中仅存一条记录，后续调用不会再报"start tracking"日志（通过 LoadOrStore 二值返回判定）
func TestCompositeCircuitBreaker_RecordReportedService_Idempotent(t *testing.T) {
	cb := newCompositeForTest()
	cb.reportedServiceKeys = &sync.Map{}

	svc := model.ServiceKey{Namespace: "default", Service: "order"}
	cb.recordReportedService(svc)
	cb.recordReportedService(svc)
	cb.recordReportedService(svc)

	count := 0
	cb.reportedServiceKeys.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	assert.Equal(t, 1, count, "duplicate Report should record only one entry")

	// 再灌一个不同的服务，确认不同 key 互不影响
	cb.recordReportedService(model.ServiceKey{Namespace: "default", Service: "user"})
	count = 0
	cb.reportedServiceKeys.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	assert.Equal(t, 2, count, "different services should be tracked separately")
}

// TestCompositeCircuitBreaker_RecordReportedService_NoopWithoutMap 测试场景：reportedServiceKeys 为 nil 时安全退出
// 前置条件：未初始化 reportedServiceKeys（模拟 Start 之前或被销毁后）
// 预期结果：调用不 panic，函数静默返回
func TestCompositeCircuitBreaker_RecordReportedService_NoopWithoutMap(t *testing.T) {
	cb := newCompositeForTest()
	assert.NotPanics(t, func() {
		cb.recordReportedService(model.ServiceKey{Namespace: "default", Service: "order"})
	})
}

// TestCompositeCircuitBreaker_CheckRulesNoopWhenNotStarted 测试场景：start=0 时 checkRules 直接返回，不触发服务端调用
// 前置条件：start 与 destroy 均为 0；engineFlow / ruleDict / reportedServiceKeys 也未初始化
// 预期结果：调用不 panic、不访问 nil 字段
func TestCompositeCircuitBreaker_CheckRulesNoopWhenNotStarted(t *testing.T) {
	cb := newCompositeForTest()
	assert.NotPanics(t, cb.checkRules)
}

// TestCompositeCircuitBreaker_CheckRulesNoopWhenDestroyed 测试场景：destroy=1 时 checkRules 不再工作
// 前置条件：start=1 但 destroy=1；其余字段就绪
// 预期结果：调用不 panic、不调用 SyncGetServiceRule
func TestCompositeCircuitBreaker_CheckRulesNoopWhenDestroyed(t *testing.T) {
	cb := newCompositeForTest()
	cb.start = 1
	cb.destroy = 1
	cb.reportedServiceKeys = &sync.Map{}
	cb.reportedServiceKeys.Store(
		model.ServiceKey{Namespace: "default", Service: "order"}, struct{}{})

	assert.NotPanics(t, cb.checkRules)
}

// TestCompositeCircuitBreaker_CheckRulesSkipWithoutDeps 测试场景：依赖未初始化时跳过遍历
// 前置条件：start=1、destroy=0，但 ruleDict / engineFlow / reportedServiceKeys 任一为 nil
// 预期结果：函数静默返回，不 panic
func TestCompositeCircuitBreaker_CheckRulesSkipWithoutDeps(t *testing.T) {
	cb := newCompositeForTest()
	cb.start = 1
	cb.destroy = 0
	// 三依赖均为 nil，应直接返回
	assert.NotPanics(t, cb.checkRules)
}
