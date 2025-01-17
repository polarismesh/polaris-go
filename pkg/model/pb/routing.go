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
	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"
	wrapperspb "google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// NewRoutingRuleInProto 兼容接口, trpc-go依赖项
func NewRoutingRuleInProto(resp *apiservice.DiscoverResponse) model.ServiceRule {
	return NewServiceRuleInProto(resp)
}

// RoutingAssistant 路由规则解析助手
type RoutingAssistant struct {
}

// ParseRuleValue 解析出具体的规则值
func (r *RoutingAssistant) ParseRuleValue(resp *apiservice.DiscoverResponse) (proto.Message, string) {
	var revision string
	routingValue := resp.Routing
	if nil != routingValue {
		revision = routingValue.GetRevision().GetValue()
	}

	if resp.Routing == nil && len(resp.NearbyRouteRules) >= 1 {
		rule := resp.NearbyRouteRules[0]
		routing := &apitraffic.Routing{
			Namespace: wrapperspb.String(rule.GetNamespace()),
			Service:   resp.Service.Name,
			Rules:     resp.NearbyRouteRules,
		}

		return routing, rule.GetRevision()
	}

	return routingValue, revision
}

// Validate 规则校验
func (r *RoutingAssistant) Validate(message proto.Message, ruleCache model.RuleCache) error {
	if reflect2.IsNil(message) {
		return nil
	}
	routingValue := message.(*apitraffic.Routing)
	var err error
	if err = r.validateRoute("inbound", routingValue.Inbounds, ruleCache); err != nil {
		return err
	}
	if err = r.validateRoute("outbound", routingValue.Outbounds, ruleCache); err != nil {
		return err
	}
	return nil
}

// validateRoute 校验路由规则
func (r *RoutingAssistant) validateRoute(direction string, routes []*apitraffic.Route, ruleCache model.RuleCache) error {
	if len(routes) == 0 {
		return nil
	}
	for _, route := range routes {
		for _, source := range route.GetSources() {
			for _, matchValue := range source.GetMetadata() {
				if matchValue.GetType() == apimodel.MatchString_REGEX && len(matchValue.GetValue().GetValue()) > 0 {
					_, err := ruleCache.GetRegexMatcher(matchValue.GetValue().GetValue())
					if err != nil {
						return err
					}
				}
			}
		}
		for _, destination := range route.GetDestinations() {
			for _, matchValue := range destination.GetMetadata() {
				if matchValue.GetType() == apimodel.MatchString_REGEX && len(matchValue.GetValue().GetValue()) > 0 {
					_, err := ruleCache.GetRegexMatcher(matchValue.GetValue().GetValue())
					if err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// SetDefault 设置默认值
func (r *RoutingAssistant) SetDefault(message proto.Message) {
	// do nothing
}
