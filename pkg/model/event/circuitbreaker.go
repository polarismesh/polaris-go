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
	"fmt"
	"strconv"
	"time"

	"github.com/polarismesh/specification/source/go/api/v1/fault_tolerance"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// 熔断事件扩展参数键（写入 BaseEventImpl.AdditionalParams）
const (
	// IsolationObjectKey 被隔离对象的标识，按资源级别拼装
	IsolationObjectKey = "isolation_object"
	// FailureRateKey 错误率阈值（failure 块的 errorPercent）
	FailureRateKey = "failure_rate"
	// SlowCallDurationKey 慢调用率阈值（slow 块的 errorPercent）
	SlowCallDurationKey = "slow_call_duration"
)

// blockNameFailure / blockNameSlow 用于从 BlockConfig 区分错误率块与慢调用块
const (
	blockNameFailure = "failure"
	blockNameSlow    = "slow"
)

// BuildCircuitBreakerEvent 根据熔断状态转换构造事件 BaseEventImpl。
//
// 输入：
//   - resource 当前熔断资源（service/method/instance 级），不为 nil
//   - rule     当前生效的熔断规则，用于提取 failure/slow 阈值；nil 时阈值参数留空
//   - from     状态转换前的状态
//   - to       状态转换后的状态
//   - reason   触发原因；HalfOpen/Close 等无原因场景传空串
//
// 返回：
//   - 填充完整字段的 *BaseEventImpl，clientID/clientIP 由 EventReporter 在投递时注入，此处不填。
func BuildCircuitBreakerEvent(
	resource model.Resource,
	rule *fault_tolerance.CircuitBreakerRule,
	from, to model.Status,
	reason string,
) *BaseEventImpl {
	toStr := parseCircuitBreakerStatus(to)
	e := &BaseEventImpl{
		EventType:        CircuitBreakerEventType.EventTypeString(),
		EventName:        parseFlowEventName(toStr),
		EventTime:        time.Now().Format("2006-01-02 15:04:05"),
		Namespace:        resource.GetService().Namespace,
		Service:          resource.GetService().Service,
		CurrentStatus:    toStr,
		PreviousStatus:   parseCircuitBreakerStatus(from),
		RuleName:         circuitBreakerRuleName(rule),
		Reason:           reason,
		AdditionalParams: make(map[string]string),
	}

	if caller := resource.GetCallerService(); caller != nil {
		e.SourceNamespace = caller.Namespace
		e.SourceService = caller.Service
	}

	ns := resource.GetService().Namespace
	svc := resource.GetService().Service
	switch resource.GetLevel() {
	case fault_tolerance.Level_SERVICE:
		e.ResourceType = "SERVICE"
		e.AdditionalParams[IsolationObjectKey] = fmt.Sprintf("%s#%s", ns, svc)
	case fault_tolerance.Level_METHOD:
		if mr, ok := resource.(*model.MethodResource); ok {
			e.ResourceType = "METHOD"
			e.APIProtocol = mr.Protocol
			e.APIPath = mr.Path
			e.APIMethod = mr.Method
			e.AdditionalParams[IsolationObjectKey] =
				fmt.Sprintf("%s#%s#(%s,'%s')", ns, svc, mr.Path, mr.Method)
		}
	case fault_tolerance.Level_INSTANCE:
		if ir, ok := resource.(*model.InstanceResource); ok {
			node := ir.GetNode()
			e.ResourceType = "INSTANCE"
			e.Host = node.Host
			e.Port = strconv.Itoa(int(node.Port))
			e.AdditionalParams[IsolationObjectKey] = fmt.Sprintf("%s:%d", node.Host, node.Port)
		}
	}

	fillRateParams(e, rule)
	return e
}

// fillRateParams 从规则的 BlockConfigs 提取 failure / slow 块的错误率阈值，
// 写入 AdditionalParams 的 failure_rate / slow_call_duration。
// rule 为 nil 或对应块不存在时，相应键留空字符串。
func fillRateParams(e *BaseEventImpl, rule *fault_tolerance.CircuitBreakerRule) {
	e.AdditionalParams[FailureRateKey] = ""
	e.AdditionalParams[SlowCallDurationKey] = ""
	if rule == nil {
		return
	}
	for _, bc := range rule.GetBlockConfigs() {
		conditions := bc.GetTriggerConditions()
		if len(conditions) == 0 {
			continue
		}
		percent := strconv.Itoa(int(conditions[0].GetErrorPercent()))
		switch bc.GetName() {
		case blockNameFailure:
			e.AdditionalParams[FailureRateKey] = percent
		case blockNameSlow:
			e.AdditionalParams[SlowCallDurationKey] = percent
		}
	}
}

// circuitBreakerRuleName 安全获取规则名，rule 为 nil 时返回空串。
func circuitBreakerRuleName(rule *fault_tolerance.CircuitBreakerRule) string {
	if rule == nil {
		return ""
	}
	return rule.GetName()
}

// parseCircuitBreakerStatus 将熔断状态枚举映射为事件中的字符串表示。
// model.Status 无 Destroy 枚举，Destroy 事件由调用方覆写 EventName 实现，
// 因此本函数只覆盖 Open/HalfOpen/Close，其余归为 UNKNOWN。
func parseCircuitBreakerStatus(s model.Status) string {
	switch s {
	case model.Open:
		return "OPEN"
	case model.HalfOpen:
		return "HALF_OPEN"
	case model.Close:
		return "CLOSE"
	default:
		return "UNKNOWN"
	}
}

// parseFlowEventName 根据目标状态字符串推导熔断事件名。
func parseFlowEventName(toStatus string) EventName {
	switch toStatus {
	case "OPEN":
		return CircuitBreakerOpen
	case "HALF_OPEN":
		return CircuitBreakerHalfOpen
	case "CLOSE":
		return CircuitBreakerClose
	default:
		return CircuitBreakerDestroy
	}
}
