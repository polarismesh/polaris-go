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

package quota

import (
	"testing"

	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"
	"github.com/stretchr/testify/assert"
	wrappers "google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/config"
)

// TestResolveRateLimiterName_ConcurrencyResource_ShouldUseConcurrencyPlugin 验证:
// Resource=CONCURRENCY 时强制走 concurrency 插件，无视 Rule.Action.
func TestResolveRateLimiterName_ConcurrencyResource_ShouldUseConcurrencyPlugin(t *testing.T) {
	rule := &apitraffic.Rule{
		Resource: apitraffic.Rule_CONCURRENCY,
		Action:   &wrappers.StringValue{Value: "reject"}, // 即便 Action 是 reject 也应被覆盖
	}

	name := resolveRateLimiterName(rule)

	assert.Equal(t, config.DefaultConcurrencyRateLimiter, name)
	assert.Equal(t, "concurrency", name)
}

// TestResolveRateLimiterName_QpsResource_ShouldUseAction 验证:
// Resource=QPS 时使用 Rule.Action 指定的插件名（reject / unirate / 自定义）.
func TestResolveRateLimiterName_QpsResource_ShouldUseAction(t *testing.T) {
	tests := []struct {
		name     string
		action   string
		expected string
	}{
		{name: "reject_action", action: "reject", expected: "reject"},
		{name: "unirate_action", action: "unirate", expected: "unirate"},
		{name: "custom_action", action: "my-custom-limiter", expected: "my-custom-limiter"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := &apitraffic.Rule{
				Resource: apitraffic.Rule_QPS,
				Action:   &wrappers.StringValue{Value: tt.action},
			}
			assert.Equal(t, tt.expected, resolveRateLimiterName(rule))
		})
	}
}

// TestResolveRateLimiterName_DefaultResource_ShouldUseAction 验证:
// Resource 未设置（默认 0 = QPS）时使用 Action.
func TestResolveRateLimiterName_DefaultResource_ShouldUseAction(t *testing.T) {
	rule := &apitraffic.Rule{
		Action: &wrappers.StringValue{Value: "reject"},
	}

	assert.Equal(t, "reject", resolveRateLimiterName(rule))
}
