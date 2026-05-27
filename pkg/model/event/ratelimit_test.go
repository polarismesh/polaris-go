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

	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// TestParseRateLimitStatus_Mapping 验证 QuotaResultCode → RateLimitStatus 的映射关系。
func TestParseRateLimitStatus_Mapping(t *testing.T) {
	assert.Equal(t, StatusLimited, ParseRateLimitStatus(model.QuotaResultLimited))
	assert.Equal(t, StatusUnlimited, ParseRateLimitStatus(model.QuotaResultOk))
	// 未知/越界结果码保留为 UNKNOWN，避免被静默归并为 UNLIMITED 掩盖异常.
	assert.Equal(t, StatusUnknown, ParseRateLimitStatus(model.QuotaResultCode(42)))
}

// TestParseRateLimitEventName_StateChange 验证仅在状态切换时返回事件名。
func TestParseRateLimitEventName_StateChange(t *testing.T) {
	tests := []struct {
		name     string
		current  RateLimitStatus
		previous RateLimitStatus
		want     EventName
	}{
		{
			name:     "unlimited→limited triggers RateLimitStart",
			current:  StatusLimited,
			previous: StatusUnlimited,
			want:     RateLimitStart,
		},
		{
			name:     "limited→unlimited triggers RateLimitEnd",
			current:  StatusUnlimited,
			previous: StatusLimited,
			want:     RateLimitEnd,
		},
		{
			name:     "limited→limited (no change) returns empty",
			current:  StatusLimited,
			previous: StatusLimited,
			want:     "",
		},
		{
			name:     "unlimited→unlimited (no change) returns empty",
			current:  StatusUnlimited,
			previous: StatusUnlimited,
			want:     "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseRateLimitEventName(tt.current, tt.previous)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestParseResourceType_Mapping 验证 Rule.Resource 枚举到字符串的映射。
func TestParseResourceType_Mapping(t *testing.T) {
	assert.Equal(t, "QPS", ParseResourceType(apitraffic.Rule_QPS))
	assert.Equal(t, "CONCURRENCY", ParseResourceType(apitraffic.Rule_CONCURRENCY))
}

// TestBuildRateLimitEvent_NoStateChange_ReturnsNil 验证当前与之前状态相同时不构造事件，避免重复上报。
func TestBuildRateLimitEvent_NoStateChange_ReturnsNil(t *testing.T) {
	rule := newDemoRule()
	got := BuildRateLimitEvent(
		model.ServiceKey{Namespace: "ns", Service: "svc"},
		rule,
		model.QuotaResultLimited, // previous: limited
		model.QuotaResultLimited, // current: limited（未变化）
		"src-ns", "src-svc",
		"label-foo", "info",
	)
	assert.Nil(t, got, "状态未变时不应产生事件")
}

// TestBuildRateLimitEvent_NilRule_ReturnsNil 验证 rule 为 nil 时安全返回 nil。
func TestBuildRateLimitEvent_NilRule_ReturnsNil(t *testing.T) {
	got := BuildRateLimitEvent(
		model.ServiceKey{Namespace: "ns", Service: "svc"},
		nil,
		model.QuotaResultOk,
		model.QuotaResultLimited,
		"", "", "", "",
	)
	assert.Nil(t, got)
}

// TestBuildRateLimitEvent_RateLimitStart_FieldsFilled 验证 UNLIMITED→LIMITED 切换时
// 构造的事件 EventName 为 RateLimitStart，且关键字段被正确填充。
func TestBuildRateLimitEvent_RateLimitStart_FieldsFilled(t *testing.T) {
	rule := newDemoRule()
	got := BuildRateLimitEvent(
		model.ServiceKey{Namespace: "ns", Service: "svc"},
		rule,
		model.QuotaResultOk,      // previous: unlimited
		model.QuotaResultLimited, // current: limited
		"src-ns", "src-svc",
		"caller_service:nsX:svcY", "limited by qps rule",
	)
	assert.NotNil(t, got)
	assert.Equal(t, RateLimitEventType.EventTypeString(), got.EventType)
	assert.Equal(t, RateLimitStart, got.EventName)
	assert.Equal(t, "ns", got.Namespace)
	assert.Equal(t, "svc", got.Service)
	assert.Equal(t, "/api/foo", got.APIPath)
	assert.Equal(t, "src-ns", got.SourceNamespace)
	assert.Equal(t, "src-svc", got.SourceService)
	assert.Equal(t, "caller_service:nsX:svcY", got.Labels)
	assert.Equal(t, string(StatusLimited), got.CurrentStatus)
	assert.Equal(t, string(StatusUnlimited), got.PreviousStatus)
	assert.Equal(t, "QPS", got.ResourceType)
	assert.Equal(t, "demo-rule", got.RuleName)
	assert.Equal(t, "limited by qps rule", got.Reason)
	assert.NotEmpty(t, got.EventTime)
}

// TestBuildRateLimitEvent_RateLimitEnd_FieldsFilled 验证 LIMITED→UNLIMITED 切换时
// 构造的事件 EventName 为 RateLimitEnd。
func TestBuildRateLimitEvent_RateLimitEnd_FieldsFilled(t *testing.T) {
	rule := newDemoRule()
	got := BuildRateLimitEvent(
		model.ServiceKey{Namespace: "ns", Service: "svc"},
		rule,
		model.QuotaResultLimited, // previous: limited
		model.QuotaResultOk,      // current: unlimited
		"", "", "", "",
	)
	assert.NotNil(t, got)
	assert.Equal(t, RateLimitEnd, got.EventName)
	assert.Equal(t, string(StatusUnlimited), got.CurrentStatus)
	assert.Equal(t, string(StatusLimited), got.PreviousStatus)
}

// newDemoRule 构造一条用于事件构造测试的限流规则。
func newDemoRule() *apitraffic.Rule {
	return &apitraffic.Rule{
		Id:       wrapperspb.String("rule-id-001"),
		Name:     wrapperspb.String("demo-rule"),
		Resource: apitraffic.Rule_QPS,
		Method: &apimodel.MatchString{
			Type:  apimodel.MatchString_EXACT,
			Value: wrapperspb.String("/api/foo"),
		},
	}
}
