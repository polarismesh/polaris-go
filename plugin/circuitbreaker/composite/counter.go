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
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	regexp "github.com/dlclark/regexp2"

	"github.com/polarismesh/polaris-go/pkg/algorithm/match"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/localregistry"
	"github.com/polarismesh/polaris-go/plugin/circuitbreaker/composite/trigger"
	"github.com/polarismesh/specification/source/go/api/v1/fault_tolerance"
)

const (
	_stateCloseToOpen = iota
	_stateOpenToHalfOpen
	_stateHalfOpenToOpen
	_stateHalfOpenToClose
)

// ResourceCounters .
type ResourceCounters struct {
	circuitBreaker *CompositeCircuitBreaker
	lock           sync.RWMutex
	// activeRuleRef
	activeRule *fault_tolerance.CircuitBreakerRule
	// counters
	counters []trigger.TriggerCounter
	// resource
	resource model.Resource
	// statusRef
	statusRef atomic.Value
	// fallbackInfo
	fallbackInfo *model.FallbackInfo
	// regexFunction
	regexFunction func(string) *regexp.Regexp
	// engineFlow
	engineFlow model.Engine
	// log
	log log.Logger
	// isInsRes
	isInsRes bool
	//
	executor *TaskExecutor
}

func newResourceCounters(res model.Resource, activeRule *fault_tolerance.CircuitBreakerRule,
	circuitBreaker *CompositeCircuitBreaker) (*ResourceCounters, error) {

	_, isInsRes := res.(*model.InstanceResource)
	counters := &ResourceCounters{
		activeRule: activeRule,
		resource:   res,
		regexFunction: func(s string) *regexp.Regexp {
			if circuitBreaker == nil {
				return regexp.MustCompile(s, regexp.RE2)
			}
			return circuitBreaker.loadOrStoreCompiledRegex(s)
		},
		circuitBreaker: circuitBreaker,
		statusRef:      atomic.Value{},
		fallbackInfo:   buildFallbackInfo(activeRule),
		log:            log.GetCircuitBreakerEventLogger(),
		isInsRes:       isInsRes,
		executor:       circuitBreaker.executor,
	}
	counters.updateCircuitBreakerStatus(model.NewCircuitBreakerStatus(activeRule.Name, model.Close, time.Now()))
	if circuitBreaker != nil {
		counters.engineFlow = circuitBreaker.engineFlow
	}
	if err := counters.init(); err != nil {
		return nil, err
	}
	return counters, nil
}

func (rc *ResourceCounters) init() error {
	conditions := rc.activeRule.GetTriggerCondition()
	for i := range conditions {
		condition := conditions[i]
		opt := trigger.Options{
			Resource:      rc.resource,
			Condition:     condition,
			StatusHandler: rc,
			DelayExecutor: rc.executor.DelayExecute,
			Log:           rc.log,
		}

		switch condition.GetTriggerType() {
		case fault_tolerance.TriggerCondition_CONSECUTIVE_ERROR:
			rc.counters = append(rc.counters, trigger.NewConsecutiveCounter(rc.activeRule.Name, &opt))
		case fault_tolerance.TriggerCondition_ERROR_RATE:
			rc.counters = append(rc.counters, trigger.NewErrRateCounter(rc.activeRule.Name, &opt))
		}
	}
	return nil
}

func (rc *ResourceCounters) CurrentActiveRule() *fault_tolerance.CircuitBreakerRule {
	return rc.activeRule
}

func (rc *ResourceCounters) updateCircuitBreakerStatus(status model.CircuitBreakerStatus) {
	rc.statusRef.Store(status)
}

func (rc *ResourceCounters) CurrentCircuitBreakerStatus() model.CircuitBreakerStatus {
	val := rc.statusRef.Load()
	if val == nil {
		return nil
	}
	return val.(model.CircuitBreakerStatus)
}

func (rc *ResourceCounters) CloseToOpen(breaker string) {
	rc.lock.Lock()
	defer rc.lock.Unlock()

	status := rc.CurrentCircuitBreakerStatus()
	if status.GetStatus() == model.Close {
		rc.toOpen(status, breaker)
	}
}

func (rc *ResourceCounters) toOpen(before model.CircuitBreakerStatus, name string) {
	newStatus := model.NewCircuitBreakerStatus(name, model.Open, time.Now(),
		func(cbs model.CircuitBreakerStatus) {
			cbs.SetFallbackInfo(rc.fallbackInfo)
		})
	rc.updateCircuitBreakerStatus(newStatus)
	rc.reportCircuitStatus(newStatus)
	rc.log.Infof("previous status %s, current status %s, resource %s, rule %s", before.GetStatus(),
		newStatus.GetStatus(), rc.resource.String(), before.GetCircuitBreaker())
	sleepWindow := rc.activeRule.GetRecoverCondition().GetSleepWindow()
	delay := time.Duration(sleepWindow) * time.Second

	rc.executor.AffinityDelayExecute(rc.activeRule.Id, delay, rc.OpenToHalfOpen)
}

func (rc *ResourceCounters) OpenToHalfOpen() {
	rc.lock.Lock()
	defer rc.lock.Unlock()

	status := rc.CurrentCircuitBreakerStatus()
	if status.GetStatus() != model.Open {
		return
	}
	consecutiveSuccess := rc.activeRule.GetRecoverCondition().ConsecutiveSuccess
	halfOpenStatus := model.NewHalfOpenStatus(status.GetCircuitBreaker(), time.Now(), int(consecutiveSuccess))
	rc.log.Infof("previous status %s, current status %s, resource %s, rule %s", status.GetStatus(),
		halfOpenStatus.GetStatus(), rc.resource.String(), status.GetCircuitBreaker())
	rc.updateCircuitBreakerStatus(halfOpenStatus)
	rc.reportCircuitStatus(halfOpenStatus)
}

func (rc *ResourceCounters) HalfOpenToClose() {
	rc.lock.Lock()
	defer rc.lock.Unlock()

	status := rc.CurrentCircuitBreakerStatus()
	if status.GetStatus() != model.HalfOpen {
		return
	}
	newStatus := model.NewCircuitBreakerStatus(status.GetCircuitBreaker(), model.Close, time.Now())
	rc.updateCircuitBreakerStatus(newStatus)
	rc.log.Infof("previous status %s, current status %s, resource %s, rule %s", status.GetStatus(),
		newStatus.GetStatus(), rc.resource.String(), status.GetCircuitBreaker())
	rc.reportCircuitStatus(newStatus)
}

func (rc *ResourceCounters) HalfOpenToOpen() {
	rc.lock.Lock()
	defer rc.lock.Unlock()

	status := rc.CurrentCircuitBreakerStatus()
	if status.GetStatus() == model.HalfOpen {
		rc.toOpen(status, status.GetCircuitBreaker())
	}
}

func (rc *ResourceCounters) Report(stat *model.ResourceStat) {
	retStatus := rc.parseRetStatus(stat)
	isSuccess := retStatus != model.RetFail && retStatus != model.RetTimeout
	curStatus := rc.CurrentCircuitBreakerStatus()
	if curStatus != nil && curStatus.GetStatus() == model.HalfOpen {
		halfOpenStatus := curStatus.(*model.HalfOpenStatus)
		checked := halfOpenStatus.Report(isSuccess)
		if !checked {
			return
		}
		nextStatus := halfOpenStatus.CalNextStatus()
		switch nextStatus {
		case model.Close:
			rc.executor.AffinityExecute(rc.activeRule.Id, rc.HalfOpenToClose)
		case model.Open:
			rc.executor.AffinityExecute(rc.activeRule.Id, rc.HalfOpenToOpen)
		}
	} else {
		log.GetBaseLogger().Debugf("[CircuitBreaker] report resource stat to counter %s", stat.Resource.String())
		for _, counter := range rc.counters {
			counter.Report(isSuccess)
		}
	}
}

func (rc *ResourceCounters) parseRetStatus(stat *model.ResourceStat) model.RetStatus {
	errConditions := rc.activeRule.GetErrorConditions()
	if len(errConditions) == 0 {
		return stat.RetStatus
	}
	for i := range errConditions {
		errCondition := errConditions[i]
		condition := errCondition.GetCondition()
		switch errCondition.GetInputType() {
		case fault_tolerance.ErrorCondition_RET_CODE:
			codeMatched := match.MatchString(stat.RetCode, condition, rc.regexFunction)
			if codeMatched {
				return model.RetFail
			}
		case fault_tolerance.ErrorCondition_DELAY:
			delayVal, err := strconv.ParseInt(condition.GetValue().GetValue(), 10, 64)
			if err == nil {
				if stat.Delay.Milliseconds() > delayVal {
					return model.RetTimeout
				}
			}
		}
	}
	return model.RetSuccess
}

func (rc *ResourceCounters) reportCircuitStatus(newStatus model.CircuitBreakerStatus) {
	if !rc.isInsRes {
		return
	}
	insRes := rc.resource.(*model.InstanceResource)
	// 构造请求，更新探测结果
	updateRequest := &localregistry.ServiceUpdateRequest{
		ServiceKey: *insRes.GetService(),
		Properties: []localregistry.InstanceProperties{
			{
				Host:       insRes.GetNode().Host,
				Port:       insRes.GetNode().Port,
				Service:    insRes.GetService(),
				Properties: map[string]interface{}{localregistry.PropertyCircuitBreakerStatus: newStatus},
			},
		},
	}
	// 调用 localCache
	rc.circuitBreaker.localCache.UpdateInstances(updateRequest)
}

func buildFallbackInfo(rule *fault_tolerance.CircuitBreakerRule) *model.FallbackInfo {
	if rule == nil {
		return nil
	}
	if rule.GetLevel() != fault_tolerance.Level_METHOD && rule.GetLevel() != fault_tolerance.Level_SERVICE {
		return nil
	}
	fallbackInfo := rule.GetFallbackConfig()
	if fallbackInfo == nil {
		return nil
	}
	if fallbackInfo.GetResponse() == nil {
		return nil
	}
	ret := &model.FallbackInfo{
		Code:    int(fallbackInfo.GetResponse().GetCode()),
		Body:    fallbackInfo.GetResponse().GetBody(),
		Headers: map[string]string{},
	}

	headers := fallbackInfo.GetResponse().GetHeaders()
	for i := range headers {
		header := headers[i]
		ret.Headers[header.Key] = header.Value
	}
	return ret
}
