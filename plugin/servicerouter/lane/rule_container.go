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

package lane

import (
	"sort"
	"strings"

	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
)

// laneRuleItem 用于排序的泳道规则条目
type laneRuleItem struct {
	group      *apitraffic.LaneGroup
	rule       *apitraffic.LaneRule
	isEntry    bool
	stainLabel string
}

// laneRuleContainer 泳道规则容器，维护排序后的规则列表以及快速查找索引
type laneRuleContainer struct {
	// sortedItems 按优先级排序后的规则条目（流量入口优先）
	sortedItems []*laneRuleItem
	// stainLabelIndex stainLabel -> laneRuleItem 快速查找
	stainLabelIndex map[string]*laneRuleItem
}

// newLaneRuleContainer 创建泳道规则容器
// 排序规则：流量入口(isEntry=true) 优先 → priority 升序 → ctime 升序
func newLaneRuleContainer(groups []*apitraffic.LaneGroup, sourceService model.ServiceMetadata) *laneRuleContainer {
	c := &laneRuleContainer{
		stainLabelIndex: make(map[string]*laneRuleItem),
	}
	for _, group := range groups {
		for _, rule := range group.GetRules() {
			if !rule.GetEnable() {
				continue
			}
			item := &laneRuleItem{
				group:      group,
				rule:       rule,
				isEntry:    isTrafficEntry(group.GetEntries(), sourceService),
				stainLabel: buildStainLabel(rule),
			}
			c.sortedItems = append(c.sortedItems, item)
			c.stainLabelIndex[item.stainLabel] = item
		}
	}
	sort.SliceStable(c.sortedItems, func(i, j int) bool {
		a, b := c.sortedItems[i], c.sortedItems[j]
		if a.isEntry != b.isEntry {
			return a.isEntry
		}
		if a.rule.GetPriority() != b.rule.GetPriority() {
			return a.rule.GetPriority() < b.rule.GetPriority()
		}
		return a.rule.GetCtime() < b.rule.GetCtime()
	})
	return c
}

// matchByStainLabel 根据染色标签直接查找规则。
// 支持两种格式：
//  1. 完整格式 "{groupName}/{ruleName}" — 优先在 stainLabelIndex 中精确匹配
//  2. 短格式 "{laneValue}" — 当精确匹配失败时，回退到匹配 rule.DefaultLabelValue
//  3. 歧义消解：如果存在多个匹配的规则，优先返回与 destService 相关的规则，其次返回第一个匹配的规则
func (c *laneRuleContainer) matchByStainLabel(
	stainLabel string,
	sourceService, destService model.ServiceMetadata,
) *laneRuleItem {
	// 1. 精确匹配（完整格式）
	if item := c.stainLabelIndex[stainLabel]; item != nil {
		return item
	}
	// 2. 短格式回退 + 歧义消解
	var firstFallback, firstRelevant *laneRuleItem
	for _, item := range c.sortedItems {
		if item.rule.GetDefaultLabelValue() != stainLabel {
			continue
		}
		if firstFallback == nil {
			firstFallback = item
		}
		// destService 必须在该泳道组的 destinations 中，否则本组与当前调用无关。
		if destService != nil && !checkServiceInLane(item.group, destService) {
			continue
		}
		if firstRelevant == nil {
			firstRelevant = item
		}
		if matchTrafficRule(item.rule.GetTrafficMatchRule(), sourceService) {
			return item
		}
	}
	if firstRelevant != nil {
		return firstRelevant
	}
	return firstFallback
}

// matchByRouteInfo 根据路由信息匹配规则（流量识别阶段）
// 只对流量入口（isEntry=true）的规则做 TrafficMatchRule 匹配，
// 非入口服务不应做首次流量识别，只响应上游已透传的染色标签。
func (c *laneRuleContainer) matchByRouteInfo(sourceService model.ServiceMetadata) *laneRuleItem {
	for _, item := range c.sortedItems {
		if !item.isEntry {
			continue
		}
		if matchTrafficRule(item.rule.GetTrafficMatchRule(), sourceService) {
			return item
		}
	}
	return nil
}

// buildStainLabel 构建染色标签：{groupName}/{ruleName}
func buildStainLabel(rule *apitraffic.LaneRule) string {
	return rule.GetGroupName() + "/" + rule.GetName()
}

// matchTrafficRule 匹配 TrafficMatchRule。
// 无规则或无匹配参数时返回 false（不匹配），避免空规则意外染色所有流量。
func matchTrafficRule(rule *apitraffic.TrafficMatchRule, sourceService model.ServiceMetadata) bool {
	if rule == nil {
		return false
	}
	args := rule.GetArguments()
	if len(args) == 0 {
		return false
	}
	routeLogger := log.GetRouteLogger()
	debugEnabled := routeLogger.IsLevelEnabled(log.DebugLog)
	isAND := rule.GetMatchMode() == apitraffic.TrafficMatchRule_AND
	for i, arg := range args {
		trafficValue := findTrafficValue(arg, sourceService)
		matched := matchStringValue(arg.GetValue(), trafficValue)
		if debugEnabled {
			routeLogger.Debugf("[Router][Lane] matchTrafficRule: arg[%d] type=%s, key=%q, "+
				"expected=%q(%s), actual=%q, matched=%v, mode=%s",
				i, arg.GetType(), arg.GetKey(),
				arg.GetValue().GetValue().GetValue(), arg.GetValue().GetType(),
				trafficValue, matched, rule.GetMatchMode())
		}
		if isAND && !matched {
			return false
		}
		if !isAND && matched {
			return true
		}
	}
	// AND 模式全部匹配返回 true；OR 模式全部不匹配返回 false
	return isAND
}

// matchStringValue 匹配 MatchString 规则
func matchStringValue(ms *apimodel.MatchString, value string) bool {
	if ms == nil {
		return true
	}
	pattern := ms.GetValue().GetValue()
	switch ms.GetType() {
	case apimodel.MatchString_EXACT:
		return value == pattern
	case apimodel.MatchString_NOT_EQUALS:
		return value != pattern
	case apimodel.MatchString_REGEX:
		re, err := getCompiledRegex(pattern)
		if err != nil {
			return false
		}
		return re.MatchString(value)
	case apimodel.MatchString_IN:
		for _, part := range strings.Split(pattern, ",") {
			if strings.TrimSpace(part) == value {
				return true
			}
		}
		return false
	case apimodel.MatchString_NOT_IN:
		for _, part := range strings.Split(pattern, ",") {
			if strings.TrimSpace(part) == value {
				return false
			}
		}
		return true
	default:
		return value == pattern
	}
}
