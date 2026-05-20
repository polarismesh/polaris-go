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

	"github.com/polarismesh/polaris-go/pkg/model"
)

// TestBuildRemoteConfigMode_ConcurrencyGlobalRule_ShouldFallbackToLocalMode 验证修复:
// 即便 Rule.Type=GLOBAL 且 Cluster 已配置，CONCURRENCY 规则也必须落到 LocalMode，
// 防止下游异步调度 (DoAsyncRemoteInit/Acquire) 浪费连接和带宽.
func TestBuildRemoteConfigMode_ConcurrencyGlobalRule_ShouldFallbackToLocalMode(t *testing.T) {
	rule := &apitraffic.Rule{
		Resource: apitraffic.Rule_CONCURRENCY,
		Type:     apitraffic.Rule_GLOBAL,
		Cluster: &apitraffic.RateLimitCluster{
			Namespace: &wrappers.StringValue{Value: "Polaris"},
			Service:   &wrappers.StringValue{Value: "polaris.metric.v2.test"},
		},
		ConcurrencyAmount: &apitraffic.ConcurrencyAmount{MaxAmount: 8},
	}
	window := &RateLimitWindow{}

	window.buildRemoteConfigMode(nil, rule)

	assert.Equal(t, model.ConfigQuotaLocalMode, window.configMode,
		"CONCURRENCY 规则不应被识别为远程模式")
	assert.Empty(t, window.remoteCluster.Namespace,
		"CONCURRENCY 规则不应填充 remoteCluster")
	assert.Empty(t, window.remoteCluster.Service)
}

// TestBuildRemoteConfigMode_QpsGlobalRuleWithCluster_ShouldUseGlobalMode 保护回归:
// QPS+GLOBAL+Cluster 已配置时仍应走 GlobalMode（修复不应影响 QPS 限流路径）.
func TestBuildRemoteConfigMode_QpsGlobalRuleWithCluster_ShouldUseGlobalMode(t *testing.T) {
	rule := &apitraffic.Rule{
		Resource: apitraffic.Rule_QPS,
		Type:     apitraffic.Rule_GLOBAL,
		Cluster: &apitraffic.RateLimitCluster{
			Namespace: &wrappers.StringValue{Value: "Polaris"},
			Service:   &wrappers.StringValue{Value: "polaris.metric.v2.test"},
		},
	}
	window := &RateLimitWindow{}

	window.buildRemoteConfigMode(nil, rule)

	assert.Equal(t, model.ConfigQuotaGlobalMode, window.configMode)
	assert.Equal(t, "Polaris", window.remoteCluster.Namespace)
	assert.Equal(t, "polaris.metric.v2.test", window.remoteCluster.Service)
}

// TestBuildRemoteConfigMode_QpsLocalRule_ShouldUseLocalMode 保护回归:
// QPS+LOCAL 规则应走 LocalMode（修复不应改变现有 LOCAL 行为）.
func TestBuildRemoteConfigMode_QpsLocalRule_ShouldUseLocalMode(t *testing.T) {
	rule := &apitraffic.Rule{
		Resource: apitraffic.Rule_QPS,
		Type:     apitraffic.Rule_LOCAL,
	}
	window := &RateLimitWindow{}

	window.buildRemoteConfigMode(nil, rule)

	assert.Equal(t, model.ConfigQuotaLocalMode, window.configMode)
}
