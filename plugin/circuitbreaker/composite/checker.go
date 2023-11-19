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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	regexp "github.com/dlclark/regexp2"
	"github.com/polarismesh/specification/source/go/api/v1/fault_tolerance"
	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	"github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/algorithm/match"
	"github.com/polarismesh/polaris-go/pkg/clock"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	pb "github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/plugin/healthcheck"
)

const (
	defaultCheckInterval = 10 * time.Second
)

type ResourceHealthChecker struct {
	resource       model.Resource
	faultDetector  *fault_tolerance.FaultDetector
	stopped        int32
	healthCheckers map[fault_tolerance.FaultDetectRule_Protocol]healthcheck.HealthChecker
	circuitBreaker *CompositeCircuitBreaker
	// regexFunction
	regexFunction func(string) *regexp.Regexp
	// lock
	lock sync.RWMutex
	// instances
	instances map[string]*ProtocolInstance
	// instanceExpireIntervalMill .
	instanceExpireIntervalMill int64
	// executor
	executor *TaskExecutor
	// log
	log log.Logger
}

func NewResourceHealthChecker(res model.Resource, faultDetector *fault_tolerance.FaultDetector,
	breaker *CompositeCircuitBreaker) *ResourceHealthChecker {
	checker := &ResourceHealthChecker{
		resource:       res,
		faultDetector:  faultDetector,
		circuitBreaker: breaker,
		regexFunction: func(s string) *regexp.Regexp {
			return breaker.loadOrStoreCompiledRegex(s)
		},
		healthCheckers: breaker.healthCheckers,
		instances:      make(map[string]*ProtocolInstance, 16),
	}
	if insRes, ok := res.(*model.InstanceResource); ok {
		checker.addInstance(insRes, false)
	}
	checker.start()
	return checker
}

func (c *ResourceHealthChecker) start() {
	protocol2Rules := c.selectFaultDetectRules(c.resource, c.faultDetector)
	for protocol, rule := range protocol2Rules {
		checkFunc := c.createCheckJob(protocol, rule)
		interval := defaultCheckInterval
		if rule.GetInterval() > 0 {
			interval = time.Duration(rule.GetInterval()) * time.Second
		}
		c.log.Infof("[CircuitBreaker] schedule task: resource=%s, protocol=%s, interval=%+v, rule=%s",
			c.resource.String(), protocol, interval, rule.GetName())
		c.executor.IntervalExecute(interval, checkFunc)
	}
	if c.resource.GetLevel() != fault_tolerance.Level_INSTANCE {
		checkPeriod := c.circuitBreaker.checkPeriod
		c.log.Infof("[CircuitBreaker] schedule expire task: resource=%s, interval=%+v", c.resource.String(), checkPeriod)
		c.executor.IntervalExecute(checkPeriod, c.cleanInstances)
	}
}

func (c *ResourceHealthChecker) stop() {
	c.log.Infof("[CircuitBreaker] health checker for resource=%s has stopped", c.resource.String())
	atomic.StoreInt32(&c.stopped, 1)
}

func (c *ResourceHealthChecker) isStopped() bool {
	return atomic.LoadInt32(&c.stopped) == 1
}

func (c *ResourceHealthChecker) cleanInstances() {
	curTimeMill := clock.CurrentMillis()
	expireIntervalMill := c.instanceExpireIntervalMill

	waitDel := make([]string, 0, 4)
	func() {
		c.lock.RLock()
		defer c.lock.RUnlock()

		for k, v := range c.instances {
			if v.isCheckSuccess() {
				continue
			}
			lastReportMilli := v.getLastReportMilli()
			if curTimeMill-lastReportMilli >= expireIntervalMill {
				waitDel = append(waitDel, k)
				c.log.Infof("[CircuitBreaker] clean instance from health check tasks, resource=%s, expired node=%s, lastReportMilli=%d",
					c.resource.String(), k, lastReportMilli)
			}
		}
	}()

	c.lock.Lock()
	defer c.lock.Unlock()
	for k := range waitDel {
		delete(c.instances, waitDel[k])
	}
}

func (c *ResourceHealthChecker) createCheckJob(protocol string, rule *fault_tolerance.FaultDetectRule) func() {
	return func() {
		if c.isStopped() {
			return
		}
		name := fault_tolerance.FaultDetectRule_Protocol_value[protocol]
		c.checkResource(fault_tolerance.FaultDetectRule_Protocol(name), rule)
	}
}

func (c *ResourceHealthChecker) checkResource(protocol fault_tolerance.FaultDetectRule_Protocol, rule *fault_tolerance.FaultDetectRule) {
	port := rule.GetPort()
	if port > 0 {
		hosts := map[string]struct{}{}
		c.lock.RLock()
		defer c.lock.RUnlock()
		for k, v := range c.instances {
			if _, ok := hosts[k]; ok {
				continue
			}
			hosts[k] = struct{}{}
			ins := pb.NewInstanceInProto(&service_manage.Instance{
				Host: wrapperspb.String(v.insRes.GetNode().Host),
				Port: wrapperspb.UInt32(v.insRes.GetNode().Port),
			}, defaultServiceKey(v.insRes.GetService()), nil)
			isSuccess := c.doCheck(ins, v.protocol, rule)
			v.setCheckResult(isSuccess)
		}
		return
	}
	c.lock.RLock()
	defer c.lock.RUnlock()
	for _, v := range c.instances {
		curProtocol := v.protocol
		if !(curProtocol == fault_tolerance.FaultDetectRule_UNKNOWN || curProtocol == protocol) {
			continue
		}
		ins := pb.NewInstanceInProto(&service_manage.Instance{
			Host: wrapperspb.String(v.insRes.GetNode().Host),
			Port: wrapperspb.UInt32(v.insRes.GetNode().Port),
		}, defaultServiceKey(v.insRes.GetService()), nil)
		isSuccess := c.doCheck(ins, v.protocol, rule)
		v.setCheckResult(isSuccess)
	}
}

func (c *ResourceHealthChecker) doCheck(ins model.Instance, protocol fault_tolerance.FaultDetectRule_Protocol,
	rule *fault_tolerance.FaultDetectRule) bool {
	checker, ok := c.healthCheckers[protocol]
	if !ok {
		c.log.Infof("plugin not found, skip health check for instance=%s:%d, resource=%s, protocol=%s",
			ins.GetHost(), ins.GetPort(), c.resource.String(), protocol.String())
		return false
	}
	ret, err := checker.DetectInstance(ins)
	if err != nil {
		return false
	}
	stat := &model.ResourceStat{
		Resource:  c.resource,
		RetCode:   ret.GetCode(),
		Delay:     ret.GetDelay(),
		RetStatus: ret.GetRetStatus(),
	}
	if err := c.circuitBreaker.Report(stat); err != nil {
		c.log.Errorf("[CircuitBreaker] report resource stat error, resource=%s, err=%s", c.resource.String(), err.Error())
	}
	return stat.RetStatus == model.RetSuccess
}

func (c *ResourceHealthChecker) addInstance(res *model.InstanceResource, record bool) {
	c.lock.Lock()
	defer c.lock.Unlock()
	saveIns, ok := c.instances[res.GetNode().String()]
	if !ok {
		c.instances[res.GetNode().String()] = &ProtocolInstance{
			protocol:        parseProtocol(res.GetProtocol()),
			insRes:          res,
			lastReportMilli: clock.CurrentMillis(),
		}
		return
	}
	if record {
		saveIns.doReport()
	}
}

func (c *ResourceHealthChecker) selectFaultDetectRules(res model.Resource,
	faultDetector *fault_tolerance.FaultDetector) map[string]*fault_tolerance.FaultDetectRule {
	sortedRules := sortFaultDetectRules(faultDetector.GetRules())
	matchRule := map[string]*fault_tolerance.FaultDetectRule{}

	for i := range sortedRules {
		rule := sortedRules[i]
		targetService := rule.GetTargetService()
		if !match.MatchService(res.GetService(), targetService.Namespace, targetService.Service) {
			continue
		}
		if res.GetLevel() == fault_tolerance.Level_METHOD {
			if !matchMethod(res, targetService.GetMethod(), c.regexFunction) {
				continue
			}
		} else {
			if !match.IsMatchAll(targetService.GetMethod().GetValue().Value) {
				continue
			}
		}
		if _, ok := matchRule[rule.GetProtocol().String()]; !ok {
			matchRule[rule.GetProtocol().String()] = rule
		}
	}
	return matchRule
}

func matchMethod(res model.Resource, val *apimodel.MatchString, regexFunc func(string) *regexp.Regexp) bool {
	if res.GetLevel() != fault_tolerance.Level_METHOD {
		return true
	}
	methodRes := res.(*model.MethodResource)
	return match.MatchString(methodRes.Method, val, regexFunc)
}

type ProtocolInstance struct {
	protocol        fault_tolerance.FaultDetectRule_Protocol
	insRes          *model.InstanceResource
	lastReportMilli int64
	checkSuccess    int32
}

func (p *ProtocolInstance) getLastReportMilli() int64 {
	return atomic.LoadInt64(&p.lastReportMilli)
}

func (p *ProtocolInstance) isCheckSuccess() bool {
	return atomic.LoadInt32(&p.checkSuccess) == 1
}

func (p *ProtocolInstance) setCheckResult(v bool) {
	if v {
		atomic.StoreInt32(&p.checkSuccess, 1)
	} else {
		atomic.StoreInt32(&p.checkSuccess, 0)
	}
}

func (p *ProtocolInstance) doReport() {
	atomic.StoreInt64(&p.lastReportMilli, clock.CurrentMillis())
}

func parseProtocol(s string) fault_tolerance.FaultDetectRule_Protocol {
	s = strings.ToLower(s)
	if s == "http" || strings.HasPrefix(s, "http/") || strings.HasSuffix(s, "/http") {
		return fault_tolerance.FaultDetectRule_HTTP
	}
	if s == "udp" || strings.HasPrefix(s, "udp/") || strings.HasSuffix(s, "/udp") {
		return fault_tolerance.FaultDetectRule_UDP
	}
	if s == "tcp" || strings.HasPrefix(s, "tcp/") || strings.HasSuffix(s, "/tcp") {
		return fault_tolerance.FaultDetectRule_TCP
	}
	return fault_tolerance.FaultDetectRule_UNKNOWN
}

func defaultServiceKey(v *model.ServiceKey) *model.ServiceKey {
	if v == nil {
		return &model.ServiceKey{}
	}
	return v
}
