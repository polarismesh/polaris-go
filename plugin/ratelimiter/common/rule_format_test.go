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

	"github.com/golang/protobuf/ptypes/wrappers"
	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"
	"github.com/stretchr/testify/assert"
)

// TestRuleID_Priority 校验 id 优先、name 兜底、nil 安全三种场景；这是日志可读性的基础保障.
func TestRuleID_Priority(t *testing.T) {
	t.Run("nil_rule_returns_empty", func(t *testing.T) {
		assert.Equal(t, "", RuleID(nil))
	})
	t.Run("id_present_takes_priority", func(t *testing.T) {
		rule := &apitraffic.Rule{
			Id:   &wrappers.StringValue{Value: "rule-id-123"},
			Name: &wrappers.StringValue{Value: "ignored-name"},
		}
		assert.Equal(t, "rule-id-123", RuleID(rule))
	})
	t.Run("falls_back_to_name_when_id_empty", func(t *testing.T) {
		rule := &apitraffic.Rule{Name: &wrappers.StringValue{Value: "rule-name"}}
		assert.Equal(t, "rule-name", RuleID(rule))
	})
}

// TestFormatCluster_OmitsEmpty 验证 type=LOCAL（无远程集群）时不会产出 "cluster=/" 这类噪声.
func TestFormatCluster_OmitsEmpty(t *testing.T) {
	t.Run("empty_cluster_returns_empty", func(t *testing.T) {
		rule := &apitraffic.Rule{}
		assert.Equal(t, "", FormatCluster(rule))
	})
	t.Run("with_cluster_returns_ns_slash_svc", func(t *testing.T) {
		rule := &apitraffic.Rule{
			Cluster: &apitraffic.RateLimitCluster{
				Namespace: &wrappers.StringValue{Value: "Polaris"},
				Service:   &wrappers.StringValue{Value: "polaris.limiter"},
			},
		}
		assert.Equal(t, "Polaris/polaris.limiter", FormatCluster(rule))
	})
}

// TestFormatRuleSummary_ContainsKeyFields 仅做结构性校验：保证关键 key 都出现在输出里，
// 避免将来字段被误删（不锁定具体格式以便后续微调）.
func TestFormatRuleSummary_ContainsKeyFields(t *testing.T) {
	rule := &apitraffic.Rule{
		Resource:     apitraffic.Rule_QPS,
		Action:       &wrappers.StringValue{Value: "REJECT"},
		AmountMode:   apitraffic.Rule_GLOBAL_TOTAL,
		Failover:     apitraffic.Rule_FAILOVER_LOCAL,
		Disable:      &wrappers.BoolValue{Value: false},
		RegexCombine: &wrappers.BoolValue{Value: true},
		Arguments:    []*apitraffic.MatchArgument{{}},
	}
	got := FormatRuleSummary(rule)
	for _, key := range []string{
		"resource=", "action=", "amountMode=", "failover=",
		"disable=", "regexCombine=", "labels=", "args=",
	} {
		assert.Contains(t, got, key, "缺少关键字段 %s", key)
	}
	assert.NotContains(t, got, "cluster=", "Local 规则不应出现 cluster=")
}

// TestFormatRuleSummary_NilSafe 防御 nil 入参：日志路径必须不 panic.
func TestFormatRuleSummary_NilSafe(t *testing.T) {
	assert.Equal(t, "", FormatRuleSummary(nil))
}
