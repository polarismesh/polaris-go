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

package pb

import (
	"github.com/golang/protobuf/proto"
	"github.com/modern-go/reflect2"
	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
)

// NearbyRoutingAssistant 就近路由规则解析助手
type NearbyRoutingAssistant struct {
}

// ParseRuleValue 解析出具体的规则值
func (n *NearbyRoutingAssistant) ParseRuleValue(resp *apiservice.DiscoverResponse) (proto.Message, string) {
	var revision string
	serviceKey := ""
	if resp.Service != nil {
		serviceKey = resp.Service.GetNamespace().GetValue() + "/" + resp.Service.GetName().GetValue()
	}

	if resp.NearbyRouteRules == nil || len(resp.NearbyRouteRules) == 0 {
		// 当没有就近路由规则时，使用 service.revision
		revision = resp.GetService().GetRevision().GetValue()
		log.GetBaseLogger().Debugf("NearbyRoutingAssistant.ParseRuleValue: service=%s, no nearby route rules found, using service revision=%s",
			serviceKey, revision)
		return nil, revision
	}

	// 遍历规则列表，优先从开启的规则中获取revision
	var selectedRule *apitraffic.RouteRule
	var minPriorityRule *apitraffic.RouteRule

	for _, rule := range resp.NearbyRouteRules {
		// 如果找到开启的规则，直接使用
		if rule.GetEnable() {
			selectedRule = rule
			break
		}

		// 记录 priority 值最小的规则
		if minPriorityRule == nil || rule.GetPriority() < minPriorityRule.GetPriority() {
			minPriorityRule = rule
		}
	}

	// 如果没有开启的规则，使用 priority 值最小的规则
	if selectedRule == nil {
		selectedRule = minPriorityRule
	}

	// 使用服务级别的 revision，而不是单个规则的 revision
	// 这样可以避免因 revision 不匹配导致的循环刷新问题
	revision = resp.GetService().GetRevision().GetValue()
	routing := &apitraffic.Routing{
		Namespace: resp.Service.Namespace,
		Service:   resp.Service.Name,
		Rules:     resp.NearbyRouteRules,
		Revision:  wrapperspb.String(revision),
	}

	log.GetBaseLogger().Debugf("NearbyRoutingAssistant.ParseRuleValue: service=%s, rules=%d, revision=%s, enabled=%v, priority=%d",
		serviceKey, len(resp.NearbyRouteRules), revision, selectedRule.GetEnable(), selectedRule.GetPriority())

	return routing, revision
}

// Validate 规则校验
func (n *NearbyRoutingAssistant) Validate(message proto.Message, ruleCache model.RuleCache) error {
	if reflect2.IsNil(message) {
		return nil
	}
	// 就近路由规则不需要特殊校验，复用RoutingAssistant的校验逻辑
	routingValue := message.(*apitraffic.Routing)
	assistant := &RoutingAssistant{}
	return assistant.Validate(routingValue, ruleCache)
}

// SetDefault 设置默认值
func (n *NearbyRoutingAssistant) SetDefault(message proto.Message) {
	// do nothing
}
