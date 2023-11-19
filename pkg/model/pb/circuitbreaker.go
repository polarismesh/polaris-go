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
	"github.com/polarismesh/specification/source/go/api/v1/service_manage"

	"github.com/polarismesh/polaris-go/pkg/model"
)

type CircuitBreakAssistant struct {
}

// ParseRuleValue 解析出具体的规则值
func (a *CircuitBreakAssistant) ParseRuleValue(resp *service_manage.DiscoverResponse) (proto.Message, string) {
	var revision string
	circuitBreakerValue := resp.CircuitBreaker
	if nil == circuitBreakerValue {
		return circuitBreakerValue, revision
	}
	revision = circuitBreakerValue.GetRevision().GetValue()
	return circuitBreakerValue, revision
}

// SetDefault 设置默认值
func (a *CircuitBreakAssistant) SetDefault(message proto.Message) {

}

// Validate 规则校验
func (a *CircuitBreakAssistant) Validate(message proto.Message, cache model.RuleCache) error {
	return nil
}

type FaultDetectAssistant struct {
}

// ParseRuleValue 解析出具体的规则值
func (a *FaultDetectAssistant) ParseRuleValue(resp *service_manage.DiscoverResponse) (proto.Message, string) {
	var revision string
	faultDetectValue := resp.FaultDetector
	if nil == faultDetectValue {
		return faultDetectValue, revision
	}
	revision = faultDetectValue.GetRevision()
	return faultDetectValue, revision
}

// SetDefault 设置默认值
func (a *FaultDetectAssistant) SetDefault(message proto.Message) {

}

// Validate 规则校验
func (a *FaultDetectAssistant) Validate(message proto.Message, cache model.RuleCache) error {
	return nil
}
