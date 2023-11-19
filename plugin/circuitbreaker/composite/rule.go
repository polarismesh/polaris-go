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
	"context"
	"sort"
	"strings"
	"time"

	regexp "github.com/dlclark/regexp2"
	"github.com/polarismesh/specification/source/go/api/v1/fault_tolerance"

	"github.com/polarismesh/polaris-go/pkg/algorithm/match"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
)

const (
	_triggerCircuitBreaker = 1
	_triggerFaultDetect    = 0
)

// RuleContainer
type RuleContainer struct {
	// res
	res model.Resource
	// breaker
	breaker *CompositeCircuitBreaker
	// regexFunction
	regexFunction func(string) *regexp.Regexp
	// engineFlow
	engineFlow model.Engine
	// log
	log log.Logger
	//
	executor *TaskExecutor
}

func newRuleContainer(ctx context.Context, res model.Resource, breaker *CompositeCircuitBreaker) *RuleContainer {
	c := &RuleContainer{
		res:     res,
		breaker: breaker,
		regexFunction: func(s string) *regexp.Regexp {
			return breaker.loadOrStoreCompiledRegex(s)
		},
		engineFlow: breaker.engineFlow,
		log:        breaker.log,
		executor:   breaker.executor,
	}
	c.scheduleCircuitBreaker()
	return c
}

func (c *RuleContainer) scheduleCircuitBreaker() {
	c.executor.AffinityExecute(c.res.String(), c.realRefreshCircuitBreaker)
}

func (c *RuleContainer) scheduleHealthCheck() {
	c.executor.AffinityExecute(c.res.String(), c.realRefreshHealthCheck)
}

func (c *RuleContainer) realRefreshCircuitBreaker() {
	engineFlow := c.engineFlow
	resp, err := engineFlow.SyncGetServiceRule(model.EventCircuitBreaker, &model.GetServiceRuleRequest{
		Namespace: c.res.GetService().Namespace,
		Service:   c.res.GetService().Service,
	})
	if err != nil {
		c.log.Errorf("[CircuitBreaker] get %s rule fail: %+v", c.res.GetService().String(), err)
		return
	}
	resourceCounters := c.breaker.getLevelResourceCounters(c.res.GetLevel())
	cbRule := selectCircuitBreakerRule(c.res, resp, c.regexFunction)
	if cbRule == nil {
		if _, exist := resourceCounters.remove(c.res); exist {
			c.scheduleHealthCheck()
		}
		return
	}
	counters, exist := resourceCounters.get(c.res)
	if exist {
		activeRule := counters.CurrentActiveRule()
		if activeRule.Id == cbRule.Id && activeRule.Revision == cbRule.Revision {
			return
		}
	}
	counters, err = newResourceCounters(c.res, cbRule, c.breaker)
	if err != nil {
		c.log.Errorf("[CircuitBreaker] new resource counters fail: %+v", err)
		return
	}
	resourceCounters.put(c.res, counters)
	c.scheduleHealthCheck()
}

func (c *RuleContainer) realRefreshHealthCheck() {
	c.log.Infof("[FaultDetect] start to pull fault detect rule for resource=%s", c.res.String())
	counters, exist := c.breaker.getLevelResourceCounters(c.res.GetLevel()).get(c.res)
	faultDetectEnabled := false
	var currentActiveRule *fault_tolerance.CircuitBreakerRule
	if exist {
		currentActiveRule = counters.CurrentActiveRule()
	}
	if currentActiveRule != nil && currentActiveRule.Enable && currentActiveRule.GetFallbackConfig().Enable {
		engineFlow := c.engineFlow
		resp, err := engineFlow.SyncGetServiceRule(model.EventFaultDetect, &model.GetServiceRuleRequest{
			Namespace: c.res.GetService().Namespace,
			Service:   c.res.GetService().Service,
		})
		if err != nil {
			c.log.Errorf("[FaultDetect] get %s rule fail: %+v", c.res.GetService().String(), err)
			c.executor.AffinityDelayExecute(c.res.String(), 5*time.Second, c.realRefreshHealthCheck)
			return
		}
		if faultDetector := selectFaultDetector(c.res, resp, c.regexFunction); faultDetector != nil {
			if curChecker, ok := c.breaker.getResourceHealthChecker(c.res); ok {
				curRule := curChecker.faultDetector
				if curRule.Revision == faultDetector.Revision {
					return
				}
				curChecker.stop()
			}
			checker := NewResourceHealthChecker(c.res, faultDetector, c.breaker)
			c.breaker.setResourceHealthChecker(c.res, checker)
			if c.res.GetLevel() != fault_tolerance.Level_INSTANCE {
				svcKey := c.res.GetService()
				resourceHealthCheckerMap := c.breaker.loadOrStoreServiceHealthCheck(*svcKey)
				resourceHealthCheckerMap.put(c.res, checker)
			}
			faultDetectEnabled = true
		}
	}
	if !faultDetectEnabled {
		c.log.Infof("[FaultDetect] health check for resource=%s is disabled, now stop the previous checker", c.res.String())
		if checker, ok := c.breaker.delResourceHealthChecker(c.res); ok {
			checker.stop()
		}
		if c.res.GetLevel() != fault_tolerance.Level_INSTANCE {
			svcKey := c.res.GetService()
			resourceHealthCheckerMap := c.breaker.loadOrStoreServiceHealthCheck(*svcKey)
			resourceHealthCheckerMap.remove(c.res)
			if resourceHealthCheckerMap.isEmpty() {
				c.breaker.delServiceHealthCheck(*svcKey)
			}
		}
	}
}

func selectCircuitBreakerRule(res model.Resource, object *model.ServiceRuleResponse, regexFunc func(string) *regexp.Regexp) *fault_tolerance.CircuitBreakerRule {
	if object == nil {
		return nil
	}
	if object.Value == nil {
		return nil
	}
	circuitBreaker := object.Value.(*fault_tolerance.CircuitBreaker)
	rules := circuitBreaker.Rules
	if len(rules) == 0 {
		return nil
	}
	sortedRules := sortCircuitBreakerRules(rules)
	for i := range sortedRules {
		cbRule := sortedRules[i]
		if !cbRule.Enable {
			continue
		}
		if cbRule.Level != res.GetLevel() {
			continue
		}
		ruleMatcher := cbRule.RuleMatcher
		destination := ruleMatcher.Destination
		if !match.MatchService(res.GetService(), destination.Namespace, destination.Service) {
			continue
		}
		source := ruleMatcher.Source
		if !match.MatchService(res.GetCallerService(), source.Namespace, source.Service) {
			continue
		}
		if ok := matchMethod(res, destination.GetMethod(), regexFunc); !ok {
			continue
		}
		return cbRule
	}
	return nil
}

func sortCircuitBreakerRules(rules []*fault_tolerance.CircuitBreakerRule) []*fault_tolerance.CircuitBreakerRule {
	ret := make([]*fault_tolerance.CircuitBreakerRule, 0, len(rules))
	ret = append(ret, rules...)
	sort.Slice(ret, func(i, j int) bool {
		rule1 := ret[i]
		rule2 := ret[j]

		// 1. compare destination service
		destNamespace1 := rule1.RuleMatcher.Destination.Namespace
		destService1 := rule1.RuleMatcher.Destination.Service
		destMethod1 := rule1.RuleMatcher.Destination.Method.Value.Value

		destNamespace2 := rule2.RuleMatcher.Destination.Namespace
		destService2 := rule2.RuleMatcher.Destination.Service
		destMethod2 := rule2.RuleMatcher.Destination.Method.Value.Value

		svcResult := compareService(destNamespace1, destService1, destNamespace2, destService2)
		if svcResult != 0 {
			return svcResult < 0
		}
		if rule1.Level == fault_tolerance.Level_METHOD && rule1.Level == rule2.Level {
			methodResult := compareStringValue(destMethod1, destMethod2)
			if methodResult != 0 {
				return methodResult < 0
			}
		}

		// 2. compare source service
		srcNamespace1 := rule1.RuleMatcher.Source.Namespace
		srcService1 := rule1.RuleMatcher.Source.Service
		srcNamespace2 := rule2.RuleMatcher.Source.Namespace
		srcService2 := rule2.RuleMatcher.Source.Service
		return compareService(srcNamespace1, srcService1, srcNamespace2, srcService2) < 0
	})
	return ret
}

func selectFaultDetector(res model.Resource, object *model.ServiceRuleResponse, regexFunc func(string) *regexp.Regexp) *fault_tolerance.FaultDetector {
	if object == nil {
		return nil
	}
	if object.Value == nil {
		return nil
	}
	return object.Value.(*fault_tolerance.FaultDetector)
}

func sortFaultDetectRules(srcRules []*fault_tolerance.FaultDetectRule) []*fault_tolerance.FaultDetectRule {
	rules := make([]*fault_tolerance.FaultDetectRule, 0, len(srcRules))
	copy(rules, srcRules)
	sort.Slice(rules, func(i, j int) bool {
		rule1 := rules[i]
		rule2 := rules[j]

		targetSvc1 := rule1.GetTargetService()
		destNamespace1 := targetSvc1.GetNamespace()
		destService1 := targetSvc1.GetService()
		destMethod1 := targetSvc1.GetMethod().GetValue().GetValue()

		targetSvc2 := rule2.GetTargetService()
		destNamespace2 := targetSvc2.GetNamespace()
		destService2 := targetSvc2.GetService()
		destMethod2 := targetSvc2.GetMethod().GetValue().GetValue()

		if v := compareService(destNamespace1, destService1, destNamespace2, destService2); v != 0 {
			return v < 0
		}
		return compareStringValue(destMethod1, destMethod2) < 0
	})
	return rules
}

func compareService(ns1, svc1, ns2, svc2 string) int {
	if v := compareStringValue(ns1, ns2); v != 0 {
		return v
	}
	return compareStringValue(svc1, svc2)
}

func compareStringValue(v1, v2 string) int {
	isMatchAllV1 := match.IsMatchAll(v1)
	isMatchAllV2 := match.IsMatchAll(v2)

	if isMatchAllV1 && isMatchAllV2 {
		return 0
	}
	if isMatchAllV1 {
		return 1
	}
	if isMatchAllV2 {
		return -1
	}
	return strings.Compare(v1, v2)
}
