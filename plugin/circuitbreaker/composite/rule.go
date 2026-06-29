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
	"github.com/polarismesh/polaris-go/pkg/sdk"
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
	engineFlow sdk.Engine
	// log
	log log.Logger
	//
	executor *TaskExecutor
}

// newRuleContainer 仅构造 RuleContainer，不产生任何副作用。
// 首次拉取规则的调度由调用方在确认其被存入 sync.Map 后显式调用 scheduleCircuitBreaker() 触发，
// 避免并发场景下构造了多余实例却仍触发一次无用的远程规则刷新。
func newRuleContainer(ctx context.Context, res model.Resource, breaker *CompositeCircuitBreaker) *RuleContainer {
	c := &RuleContainer{
		res:     res,
		breaker: breaker,
		regexFunction: func(s string) *regexp.Regexp {
			return breaker.loadOrStoreCompiledRegex(s)
		},
		engineFlow: breaker.engineFlow,
		log:        breaker.logCtx.GetCircuitBreakerLogger(),
		executor:   breaker.executor,
	}
	return c
}

func (c *RuleContainer) scheduleCircuitBreaker() {
	c.executor.AffinityExecute(c.res.String(), c.realRefreshCircuitBreaker)
}

func (c *RuleContainer) scheduleHealthCheck() {
	// 调度刷新探测规则，由规则变更/事件驱动（非每请求），使用 Debug 级别记录触发来源。
	c.log.Debugf("[FaultDetect] schedule health check refresh for resource=%s", c.res.String())
	c.executor.AffinityExecute(c.res.String(), c.realRefreshHealthCheck)
}

func (c *RuleContainer) realRefreshCircuitBreaker() {
	c.log.Debugf("[CircuitBreaker] refreshing circuit breaker rule for resource: %s", c.res.String())
	engineFlow := c.engineFlow
	resp, err := engineFlow.SyncGetServiceRule(model.EventCircuitBreaker, &model.GetServiceRuleRequest{
		Namespace: c.res.GetService().Namespace,
		Service:   c.res.GetService().Service,
	})
	if err != nil {
		c.log.Errorf("[CircuitBreaker] get %s rule fail: %+v", c.res.GetService().String(), err)
		return
	}
	// 同步刷新三级索引：以服务为粒度替换旧规则集合，便于其他资源 Lookup 复用
	if c.breaker.ruleDict != nil {
		c.breaker.ruleDict.PutServiceRule(*c.res.GetService(), resp)
	}
	resourceCounters := c.breaker.getLevelResourceCounters(c.res.GetLevel())
	cbRule := c.getCircuitBreakerRule(resp)
	if cbRule == nil {
		if old, exist := resourceCounters.remove(c.res); exist {
			// 规则被删除，旧 counters 即将被丢弃，上报熔断销毁事件
			old.reportDestroyEvent()
			c.log.Infof("[CircuitBreaker] removed counters for resource: %s, scheduling health check", c.res.String())
			c.scheduleHealthCheck()
		}
		return
	}
	c.log.Debugf("[CircuitBreaker] matched rule: %s (id: %s, revision: %s) for resource: %s",
		cbRule.Name, cbRule.Id, cbRule.Revision, c.res.String())
	oldCounters, exist := resourceCounters.get(c.res)
	if exist {
		activeRule := oldCounters.CurrentActiveRule()
		if activeRule.Id == cbRule.Id && activeRule.Revision == cbRule.Revision {
			c.log.Debugf("[CircuitBreaker] rule unchanged for resource: %s, skipping update", c.res.String())
			return
		}
		c.log.Infof("[CircuitBreaker] rule changed for resource: %s, old rule: %s (id: %s, revision: %s), "+
			"new rule: %s (id: %s, revision: %s)", c.res.String(), activeRule.Name, activeRule.Id,
			activeRule.Revision, cbRule.Name, cbRule.Id, cbRule.Revision)
	}
	counters, err := newResourceCounters(c.res, cbRule, c.breaker)
	if err != nil {
		c.log.Errorf("[CircuitBreaker] new resource counters fail: %+v", err)
		return
	}
	// 规则发生变更，旧 counters 即将被新 counters 覆盖，上报熔断销毁事件
	if exist {
		oldCounters.reportDestroyEvent()
	}
	resourceCounters.put(c.res, counters)
	c.log.Infof("[CircuitBreaker] created new counters, applied rule: %s (id: %s, revision: %s) for resource: %s",
		cbRule.Name, cbRule.Id, cbRule.Revision, c.res.String())
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
	// 主动探测的启动门控：熔断规则启用 且 其 faultDetectConfig.enable=true。
	// faultDetectConfig 是独立的主动探测开关，与 fallbackConfig（熔断后的降级响应）解耦；
	// 仅配置降级而未开启探测时不应启动探测任务，反之亦然。
	if currentActiveRule != nil && currentActiveRule.Enable && currentActiveRule.GetFaultDetectConfig() != nil &&
		currentActiveRule.GetFaultDetectConfig().GetEnable() {
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
		faultDetector := selectFaultDetector(c.res, resp, c.regexFunction)
		if faultDetector == nil {
			// 门控已开启但尚未匹配到 FaultDetectRule（常见于探测规则刚下发、SDK 还没拉取到），
			// 周期级日志记录，便于排查"探测门控通过但 checker 未建立"。
			c.log.Debugf("[FaultDetect] gate enabled but no matched fault detect rule yet for resource=%s",
				c.res.String())
		}
		if faultDetector != nil {
			if curChecker, ok := c.breaker.getResourceHealthChecker(c.res); ok {
				curRule := curChecker.faultDetector
				if curRule.Revision == faultDetector.Revision {
					c.log.Debugf("[FaultDetect] health checker unchanged (revision=%s) for resource=%s, skip rebuild",
						faultDetector.Revision, c.res.String())
					return
				}
				c.log.Debugf("[FaultDetect] fault detect rule revision changed (%s -> %s) for resource=%s, rebuild checker",
					curRule.Revision, faultDetector.Revision, c.res.String())
				curChecker.stop()
			}
			c.log.Debugf("[FaultDetect] build health checker for resource=%s, faultDetector revision=%s, rules=%d",
				c.res.String(), faultDetector.Revision, len(faultDetector.GetRules()))
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
		// 仅在真的停掉了一个已存在的 checker（探测从开启变为关闭，属状态变化事件）时打 INFO；
		// 否则（该资源本就没配探测，仅刷新时例行判定门控不通过）降级 DEBUG，避免每次规则
		// 复检/push 事件对未配探测的 METHOD/INSTANCE 资源重复刷屏淹没真正的状态切换事件。
		if checker, ok := c.breaker.delResourceHealthChecker(c.res); ok {
			c.log.Infof("[FaultDetect] health check for resource=%s is disabled, now stop the previous checker",
				c.res.String())
			checker.stop()
		} else {
			c.log.Debugf("[FaultDetect] health check for resource=%s is disabled (no active checker)", c.res.String())
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

func selectCircuitBreakerRule(res model.Resource, object *model.ServiceRuleResponse,
	regexFunc func(string) *regexp.Regexp, cbLogger log.Logger) *fault_tolerance.CircuitBreakerRule {
	if object == nil || object.Value == nil {
		return nil
	}
	circuitBreaker, ok := object.Value.(*fault_tolerance.CircuitBreaker)
	if !ok || circuitBreaker == nil {
		return nil
	}
	rules := circuitBreaker.Rules
	if len(rules) == 0 {
		return nil
	}
	sortedRules := sortCircuitBreakerRules(rules)
	for i := range sortedRules {
		cbRule := sortedRules[i]
		if !cbRule.Enable {
			cbLogger.Debugf("[CircuitBreaker] rule %s skipped: disabled", cbRule.Name)
			continue
		}
		if cbRule.Level != res.GetLevel() {
			cbLogger.Debugf("[CircuitBreaker] rule %s skipped: level mismatch (rule level: %v, resource "+
				"level: %v, resource: %s)", cbRule.Name, cbRule.Level, res.GetLevel(), res.String())
			continue
		}
		ruleMatcher := cbRule.RuleMatcher
		if ruleMatcher == nil {
			cbLogger.Debugf("[CircuitBreaker] rule %s skipped: nil rule matcher", cbRule.Name)
			continue
		}
		destination := ruleMatcher.Destination
		source := ruleMatcher.Source
		if destination == nil || source == nil {
			cbLogger.Debugf("[CircuitBreaker] rule %s skipped: nil destination or source in rule matcher", cbRule.Name)
			continue
		}
		if !match.MatchService(res.GetService(), destination.Namespace, destination.Service) {
			cbLogger.Debugf("[CircuitBreaker] rule %s skipped: destination service mismatch (rule: %s/%s, "+
				"resource: %s)",
				cbRule.Name, destination.Namespace, destination.Service, res.GetService().String())
			continue
		}
		if !match.MatchService(res.GetCallerService(), source.Namespace, source.Service) {
			cbLogger.Debugf("[CircuitBreaker] rule %s skipped: source service mismatch (rule: %s/%s, "+
				"resource caller: %s)", cbRule.Name, source.Namespace, source.Service, res.GetCallerService().String())
			continue
		}
		if !matchRuleAPI(res, cbRule, destination, regexFunc) {
			cbLogger.Debugf("[CircuitBreaker] rule %s skipped: api/method mismatch", cbRule.Name)
			continue
		}
		// 命中路径：每条业务请求 CheckResource 都会执行到这里，因此降级为 Debug
		// 避免 INFO 被请求级日志刷屏淹没真正的状态机切换 / 规则变更事件。
		cbLogger.Debugf("[CircuitBreaker] rule %s matched for resource: %s", cbRule.Name, res.String())
		return cbRule
	}
	return nil
}

// matchRuleAPI 判定一条规则的接口/方法维度是否命中当前资源
// 对 METHOD 级规则：优先遍历 BlockConfigs.Api，任一 BlockConfig 与资源匹配即命中；
// 若 BlockConfigs 为空（兼容老规则），回退到 destination.Method 单字段匹配。
// 对 SERVICE / INSTANCE 级规则：方法维度不参与匹配，直接放行。
// res         待匹配资源
// cbRule      候选熔断规则
// destination 规则的目标服务匹配块（用于回退路径取 destination.Method）
// regexFunc   正则编译缓存函数
func matchRuleAPI(res model.Resource, cbRule *fault_tolerance.CircuitBreakerRule,
	destination *fault_tolerance.RuleMatcher_DestinationService,
	regexFunc func(string) *regexp.Regexp) bool {
	if cbRule.Level != fault_tolerance.Level_METHOD {
		return true
	}
	blockConfigs := cbRule.GetBlockConfigs()
	if len(blockConfigs) > 0 {
		for _, bc := range blockConfigs {
			if matchMethodWithAPI(res, bc.GetApi(), regexFunc) {
				return true
			}
		}
		return false
	}
	// 兼容老规则：BlockConfigs 为空时回退到 destination.method 单字段匹配
	return matchMethod(res, destination.GetMethod(), regexFunc)
}

// sortCircuitBreakerRules 对熔断规则按优先级排序，数值越小优先级越高。
// 排序优先级：rule.Priority → destination service（精确匹配优先于通配匹配）→ rule.Id（字典序保证确定性）。
func sortCircuitBreakerRules(rules []*fault_tolerance.CircuitBreakerRule) []*fault_tolerance.CircuitBreakerRule {
	ret := make([]*fault_tolerance.CircuitBreakerRule, 0, len(rules))
	ret = append(ret, rules...)
	sort.Slice(ret, func(i, j int) bool {
		rule1 := ret[i]
		rule2 := ret[j]

		// 1. compare priority（数值越小优先级越高）
		if rule1.Priority != rule2.Priority {
			return rule1.Priority < rule2.Priority
		}

		// 2. compare destination service（精确匹配优先于通配匹配）
		destNs1 := rule1.RuleMatcher.GetDestination().GetNamespace()
		destSvc1 := rule1.RuleMatcher.GetDestination().GetService()
		destNs2 := rule2.RuleMatcher.GetDestination().GetNamespace()
		destSvc2 := rule2.RuleMatcher.GetDestination().GetService()
		svcResult := compareService(destNs1, destSvc1, destNs2, destSvc2)
		if svcResult != 0 {
			return svcResult < 0
		}

		// 3. compare rule ID（字典序保证确定性）
		return strings.Compare(rule1.Id, rule2.Id) < 0
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
	// 复制一份待排序切片，避免就地修改入参底层数组。
	// 注意：必须用 append（或先 make 出等长 slice 再 copy），不能用 make(..., 0, len)+copy——
	// copy 的拷贝条数受目标 slice 的 len 限制，len=0 时会把规则全部丢弃。
	rules := append([]*fault_tolerance.FaultDetectRule(nil), srcRules...)
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
