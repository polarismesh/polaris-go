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

package composite

import (
	"github.com/polarismesh/specification/source/go/api/v1/fault_tolerance"
	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/model"
)

const (
	_defaultInstanceCircuitBreakerRuleName = "default-polaris-instance-circuit-breaker"
	_defaultFailureBlockConfigName         = "failure-block-config"
	_defaultRetCodeRange                   = "500~599"
)

type defaultInstanceCircuitBreakerConfig struct {
	// 是否启用默认熔断规则
	defaultRuleEnable bool
	// 连续错误数熔断器默认连续错误数
	defaultErrorCount int
	// 错误率熔断器默认错误率
	defaultErrorPercent int
	// 错误率熔断器默认统计周期（单位：毫秒）
	defaultInterval int
	// 错误率熔断器默认最小请求数
	defaultMinimumRequest int
	// 熔断打开到半开的时长, 单位秒
	sleepWindow int
	// 熔断器半开到关闭所必须的最少成功请求数
	successCountAfterHalfOpen int
}

func (c *RuleContainer) getCircuitBreakerRule(object *model.ServiceRuleResponse) *fault_tolerance.CircuitBreakerRule {
	rule := selectCircuitBreakerRule(c.res, object, c.regexFunction)
	if rule != nil {
		c.log.Debugf("[CircuitBreaker] found matched rule: %s for resource: %s", rule.Name, c.res.String())
		return rule
	}
	if !c.breaker.defaultInstanceCircuitBreakerConfig.defaultRuleEnable || c.res.GetLevel() !=
		fault_tolerance.Level_INSTANCE {
		c.log.Debugf("[CircuitBreaker] default rule disabled or level mismatch (defaultRuleEnable=%v, level=%s), "+
			"resource: %s", c.breaker.defaultInstanceCircuitBreakerConfig.defaultRuleEnable, c.res.GetLevel(),
			c.res.String())
		return nil
	}
	// 实例级熔断,如果没有找到规则，默认规则没有被关闭,则使用默认规则
	defaultInstanceCircuitBreakerRule := &fault_tolerance.CircuitBreakerRule{
		Enable:           true,
		Name:             _defaultInstanceCircuitBreakerRuleName,
		Level:            fault_tolerance.Level_INSTANCE,
		RuleMatcher:      &fault_tolerance.RuleMatcher{},
		BlockConfigs:     make([]*fault_tolerance.BlockConfig, 0),
		ErrorConditions:  make([]*fault_tolerance.ErrorCondition, 0),
		TriggerCondition: make([]*fault_tolerance.TriggerCondition, 0),
	}
	// 主调服务
	if c.res.GetCallerService() != nil {
		defaultInstanceCircuitBreakerRule.RuleMatcher.Source = &fault_tolerance.RuleMatcher_SourceService{
			Namespace: c.res.GetCallerService().Namespace,
			Service:   c.res.GetCallerService().Service,
		}
	}
	// 被调服务
	if c.res.GetService() != nil {
		defaultInstanceCircuitBreakerRule.RuleMatcher.Destination = &fault_tolerance.RuleMatcher_DestinationService{
			Namespace: c.res.GetService().Namespace,
			Service:   c.res.GetService().Service,
		}
	}
	// 错误判断条件
	errorCondition := &fault_tolerance.ErrorCondition{
		InputType: fault_tolerance.ErrorCondition_RET_CODE,
		Condition: &apimodel.MatchString{
			Type:  apimodel.MatchString_RANGE,
			Value: &wrapperspb.StringValue{Value: _defaultRetCodeRange},
		},
	}
	// 熔断触发条件
	triggerConditions := []*fault_tolerance.TriggerCondition{
		// 错误率触发条件
		{
			TriggerType:    fault_tolerance.TriggerCondition_ERROR_RATE,
			ErrorPercent:   uint32(c.breaker.defaultInstanceCircuitBreakerConfig.defaultErrorPercent),
			Interval:       uint32(c.breaker.defaultInstanceCircuitBreakerConfig.defaultInterval),
			MinimumRequest: uint32(c.breaker.defaultInstanceCircuitBreakerConfig.defaultMinimumRequest),
		},
		// 连续错误触发条件
		{
			TriggerType: fault_tolerance.TriggerCondition_CONSECUTIVE_ERROR,
			ErrorCount:  uint32(c.breaker.defaultInstanceCircuitBreakerConfig.defaultErrorCount),
		},
	}
	// 熔断恢复策略
	defaultInstanceCircuitBreakerRule.RecoverCondition = &fault_tolerance.RecoverCondition{
		SleepWindow:        uint32(c.breaker.defaultInstanceCircuitBreakerConfig.sleepWindow),
		ConsecutiveSuccess: uint32(c.breaker.defaultInstanceCircuitBreakerConfig.successCountAfterHalfOpen),
	}
	blockConf := &fault_tolerance.BlockConfig{
		Name:            _defaultFailureBlockConfigName,
		ErrorConditions: []*fault_tolerance.ErrorCondition{errorCondition},
	}
	blockConf.TriggerConditions = triggerConditions
	defaultInstanceCircuitBreakerRule.BlockConfigs = append(defaultInstanceCircuitBreakerRule.BlockConfigs, blockConf)
	defaultInstanceCircuitBreakerRule.TriggerCondition = append(defaultInstanceCircuitBreakerRule.TriggerCondition,
		triggerConditions...)
	defaultInstanceCircuitBreakerRule.ErrorConditions = append(defaultInstanceCircuitBreakerRule.ErrorConditions,
		errorCondition)
	c.log.Infof("[CircuitBreaker] successfully created default instance circuit breaker rule: %s for resource: %s, "+
		"default rule details: %s", defaultInstanceCircuitBreakerRule.Name, c.res.String(),
		defaultInstanceCircuitBreakerRule.String())
	return defaultInstanceCircuitBreakerRule
}
