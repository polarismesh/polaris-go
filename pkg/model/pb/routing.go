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
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/golang/protobuf/jsonpb"
	"github.com/golang/protobuf/proto"
	"github.com/modern-go/reflect2"
)

//兼容接口, trpc-go依赖项
func NewRoutingRuleInProto(resp *namingpb.DiscoverResponse) model.ServiceRule {
	return NewServiceRuleInProto(resp)
}

//路由规则解析助手
type RoutingAssistant struct {
}

//解析出具体的规则值
func (r *RoutingAssistant) ParseRuleValue(resp *namingpb.DiscoverResponse) (proto.Message, string) {
	var revision string
	routingValue := resp.Routing
	if nil != routingValue {
		revision = routingValue.GetRevision().GetValue()
	}
	return routingValue, revision
}

//规则校验
func (r *RoutingAssistant) Validate(message proto.Message, ruleCache model.RuleCache) error {
	if reflect2.IsNil(message) {
		return nil
	}
	routingValue := message.(*namingpb.Routing)
	var err error
	if err = r.validateRoute("inbound", routingValue.Inbounds, ruleCache); nil != err {
		return err
	}
	if err = r.validateRoute("outbound", routingValue.Outbounds, ruleCache); nil != err {
		return err
	}
	return nil
}

//校验路由规则
func (r *RoutingAssistant) validateRoute(direction string, routes []*namingpb.Route, ruleCache model.RuleCache) error {
	if len(routes) == 0 {
		return nil
	}
	for _, route := range routes {
		sources := route.GetSources()
		if len(sources) > 0 {
			for _, source := range sources {
				if err := buildCacheFromMatcher(source.GetMetadata(), ruleCache); nil != err {
					routeTxt, _ := (&jsonpb.Marshaler{}).MarshalToString(source)
					return fmt.Errorf("fail to validate %s source route, error is %v, route text is\n%s",
						direction, err, routeTxt)
				}
			}
		}
		destinations := route.GetDestinations()
		if len(destinations) > 0 {
			for _, destination := range destinations {
				if err := buildCacheFromMatcher(destination.GetMetadata(), ruleCache); nil != err {
					routeTxt, _ := (&jsonpb.Marshaler{}).MarshalToString(destination)
					return fmt.Errorf("fail to validate %s destination route, error is %v, route text is\n%s",
						direction, err, routeTxt)
				}
			}
		}
	}
	return nil
}

//设置默认值
func (r *RoutingAssistant) SetDefault(message proto.Message) {
	// do nothing
}
