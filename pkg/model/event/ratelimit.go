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
	"time"

	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// RateLimitStatus 限流状态。配合 ParseRateLimitEventName 推导出本次状态变化对应的事件名。
type RateLimitStatus string

const (
	// StatusUnlimited 未触发限流
	StatusUnlimited RateLimitStatus = "UNLIMITED"
	// StatusLimited 已触发限流
	StatusLimited RateLimitStatus = "LIMITED"
	// StatusUnknown 未知状态
	StatusUnknown RateLimitStatus = "UNKNOWN"
)

// ParseRateLimitStatus 从 QuotaResultCode 解析限流状态。
// QuotaResultLimited → LIMITED；QuotaResultOk → UNLIMITED；其他未知码 → UNKNOWN。
// 上层 ParseRateLimitEventName 仅在 UNLIMITED↔LIMITED 切换时构造事件，UNKNOWN 不会触发上报，
// 但保留 UNKNOWN 状态便于后续诊断（避免把异常码静默归并为 UNLIMITED 掩盖问题）。
func ParseRateLimitStatus(code model.QuotaResultCode) RateLimitStatus {
	switch code {
	case model.QuotaResultLimited:
		return StatusLimited
	case model.QuotaResultOk:
		return StatusUnlimited
	default:
		return StatusUnknown
	}
}

// ParseRateLimitEventName 根据当前与之前状态推导事件名称：
//   - UNLIMITED → LIMITED：RateLimitStart
//   - LIMITED → UNLIMITED：RateLimitEnd
//   - 其他（状态未变化）：返回空串，调用方据此跳过事件构造
func ParseRateLimitEventName(current, previous RateLimitStatus) EventName {
	if current == StatusLimited && previous == StatusUnlimited {
		return RateLimitStart
	}
	if current == StatusUnlimited && previous == StatusLimited {
		return RateLimitEnd
	}
	return ""
}

// ParseResourceType 将限流规则的 Resource 枚举转换为事件中的字符串表示。
func ParseResourceType(resource apitraffic.Rule_Resource) string {
	switch resource {
	case apitraffic.Rule_QPS:
		return "QPS"
	case apitraffic.Rule_CONCURRENCY:
		return "CONCURRENCY"
	default:
		return "UNKNOWN"
	}
}

// BuildRateLimitEvent 根据状态变化构造限流事件 BaseEventImpl。
//
// 输入：
//   - svcKey       被限流的目标服务（命名空间+服务名），不允许为零值
//   - rule         命中的限流规则；nil 时返回 nil（调用方应保证非 nil）
//   - previousCode 上一次配额分配的结果码
//   - currentCode  本次配额分配的结果码
//   - sourceNamespace / sourceService 主调来源信息，缺失时传空串
//   - labels       限流维度的标签字符串（FormatLabelToStr 输出格式）
//   - reason       附加原因，例如 QuotaResponse.Info；可为空
//
// 返回：
//   - 当 currentCode 与 previousCode 表示的状态相同（不构成 RateLimitStart/RateLimitEnd 事件）时，返回 nil
//   - 否则返回填充完整字段的 *BaseEventImpl，由调用方投递到 EventReporter 链
func BuildRateLimitEvent(
	svcKey model.ServiceKey,
	rule *apitraffic.Rule,
	previousCode model.QuotaResultCode,
	currentCode model.QuotaResultCode,
	sourceNamespace string,
	sourceService string,
	labels string,
	reason string,
) *BaseEventImpl {
	if rule == nil {
		return nil
	}
	currentStatus := ParseRateLimitStatus(currentCode)
	previousStatus := ParseRateLimitStatus(previousCode)
	eventName := ParseRateLimitEventName(currentStatus, previousStatus)
	if eventName == "" {
		// 状态未发生 UNLIMITED↔LIMITED 切换，不需要上报事件
		return nil
	}

	apiPath := ""
	if rule.GetMethod() != nil {
		apiPath = rule.GetMethod().GetValue().GetValue()
	}
	ruleName := ""
	if rule.GetName() != nil {
		ruleName = rule.GetName().GetValue()
	}

	return &BaseEventImpl{
		EventType:       RateLimitEventType.EventTypeString(),
		EventName:       eventName,
		EventTime:       time.Now().Format("2006-01-02 15:04:05"),
		Namespace:       svcKey.Namespace,
		Service:         svcKey.Service,
		APIPath:         apiPath,
		SourceNamespace: sourceNamespace,
		SourceService:   sourceService,
		Labels:          labels,
		CurrentStatus:   string(currentStatus),
		PreviousStatus:  string(previousStatus),
		ResourceType:    ParseResourceType(rule.GetResource()),
		RuleName:        ruleName,
		Reason:          reason,
	}
}
