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
	"fmt"
	"strings"

	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"
)

// RuleID 返回规则的展示标识，仅用于日志可读性：
//   - 优先返回 rule.id；id 为空时 fallback 到 rule.name；
//   - rule == nil 时返回空字符串，避免日志中出现 panic.
//
// 三个限流插件（reject / reject_concurrency / unirate）共享此实现，
// 避免在每个 bucket 文件里重复同一段防御性逻辑.
func RuleID(rule *apitraffic.Rule) string {
	if rule == nil {
		return ""
	}
	if id := rule.GetId().GetValue(); id != "" {
		return id
	}
	return rule.GetName().GetValue()
}

// FormatRuleSummary 把规则的关键字段格式化为一段 key=value 串，便于在 bucket 创建日志中
// 一次性查到"规则下发但未生效"类问题的全部信号；这些字段均不在 windowKey 中体现：
//   - resource     ：区分 QPS / CONCURRENCY，验证规则被路由到了正确的限流插件；
//   - action       ：限流动作（REJECT / UNIRATE / 自定义），用于验证插件选择；
//   - amountMode   ：阈值模式（GLOBAL_TOTAL / SHARE_EQUALLY），影响多实例阈值计算；
//   - failover     ：远程不可用时的降级策略（FAILOVER_LOCAL / FAILOVER_PASS）；
//   - disable      ：规则是否被禁用（true 时整条规则不参与匹配）；
//   - regexCombine ：method/argument REGEX 是否合并阈值（多 path 共享 vs 独享）；
//   - labels / args：维度数量，便于快速判断规则是否带条件；
//   - cluster      ：远程限流集群（仅在 GLOBAL 类型规则下有值）.
//
// 返回值无前后空格；调用方按需拼到 logger.Infof 的尾部即可.
func FormatRuleSummary(rule *apitraffic.Rule) string {
	if rule == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "resource=%s action=%s amountMode=%s failover=%s",
		rule.GetResource().String(),
		rule.GetAction().GetValue(),
		rule.GetAmountMode().String(),
		rule.GetFailover().String(),
	)
	// Disable / RegexCombine 都是 *wrappers.BoolValue，nil 安全地走 GetValue() 返回 false.
	fmt.Fprintf(&b, " disable=%t regexCombine=%t",
		rule.GetDisable().GetValue(),
		rule.GetRegexCombine().GetValue(),
	)
	fmt.Fprintf(&b, " labels=%d args=%d",
		len(rule.GetLabels()),
		len(rule.GetArguments()),
	)
	if cluster := FormatCluster(rule); cluster != "" {
		fmt.Fprintf(&b, " cluster=%s", cluster)
	}
	return b.String()
}

// FormatCluster 把 rule.cluster（远程限流集群）格式化为 "<namespace>/<service>"；
// 当 ns 与 svc 都为空（即规则使用本地限流，不依赖远程集群）时返回空串，
// 让 FormatRuleSummary 可以省略整个 cluster=... 段，减少日志噪音.
func FormatCluster(rule *apitraffic.Rule) string {
	ns := rule.GetCluster().GetNamespace().GetValue()
	svc := rule.GetCluster().GetService().GetValue()
	if ns == "" && svc == "" {
		return ""
	}
	return ns + "/" + svc
}
