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
	"fmt"
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
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/plugin/healthcheck"
)

const (
	defaultCheckInterval = 10 * time.Second
	// detectStatusReportPeriod 探测持续失败时的定时汇报周期（不打扰状态变化立即打印的即时性）。
	detectStatusReportPeriod = 30 * time.Second
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
	// detectLastResult 记录每个实例的上一次探测结果（true=成功, false=失败），用于检测状态变化。
	detectLastResult map[string]bool
	// detectLastReportTime 记录每个实例上次定时打印失败日志的时间，用于收敛"连续失败"的汇报频率。
	detectLastReportTime map[string]time.Time
	// detectMu 保护 detectLastResult / detectLastReportTime 的并发访问。
	detectMu sync.RWMutex
	// lastDetectErr 记录每个实例的上一次探测底层异常错误信息，仅 err 内容变化时重新打印。
	lastDetectErr map[string]string
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
		// executor 必须复用熔断器的全局任务调度器，否则 start() 中的 IntervalExecute 会对 nil 解引用 panic
		executor:                   breaker.executor,
		instanceExpireIntervalMill: breaker.healthCheckInstanceExpireInterval.Milliseconds(),
		log:                        breaker.logCtx.GetCircuitBreakerLogger(),
		detectLastResult:           make(map[string]bool, 16),
		detectLastReportTime:       make(map[string]time.Time, 16),
		lastDetectErr:              make(map[string]string, 16),
	}
	if insRes, ok := res.(*model.InstanceResource); ok {
		checker.addInstance(insRes, false)
	}
	checker.start()
	return checker
}

func (c *ResourceHealthChecker) start() {
	if c.executor == nil {
		c.log.Errorf("[FaultDetect] health checker executor is nil, skip schedule for resource=%s",
			c.resource.String())
		return
	}
	protocol2Rules := c.selectFaultDetectRules(c.resource, c.faultDetector)
	for protocol, rule := range protocol2Rules {
		checkFunc := c.createCheckJob(protocol, rule)
		interval := defaultCheckInterval
		if rule.GetInterval() > 0 {
			interval = time.Duration(rule.GetInterval()) * time.Second
		}
		c.log.Infof("[FaultDetect] schedule task: resource=%s, protocol=%s, interval=%+v, rule=%s, id=%s, rev=%s",
			c.resource.String(), protocol, interval, rule.GetName(), rule.GetId(), rule.GetRevision())
		c.executor.IntervalExecute(interval, checkFunc)
	}
	if c.resource.GetLevel() != fault_tolerance.Level_INSTANCE {
		checkPeriod := c.circuitBreaker.checkPeriod
		c.log.Infof("[FaultDetect] schedule expire task: resource=%s, interval=%+v", c.resource.String(), checkPeriod)
		c.executor.IntervalExecute(checkPeriod, c.cleanInstances)
	}
}

func (c *ResourceHealthChecker) stop() {
	c.log.Infof("[FaultDetect] health checker for resource=%s has stopped", c.resource.String())
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
				// 清理过期实例：周期任务的内部状态变更日志，使用 Debug 级别。
				// 大规模实例过期（如容器批量重启）时可能产生大量输出，
				// 生产环境通常无需关注此信息，故采用 Debug 级别按需开启。
				c.log.Debugf("[FaultDetect] clean instance from health check tasks, resource=%s, expired node=%s, lastReportMilli=%d",
					c.resource.String(), k, lastReportMilli)
			}
		}
	}()

	c.lock.Lock()
	defer c.lock.Unlock()
	for _, k := range waitDel {
		delete(c.instances, k)
		// 同步清理探测状态追踪 map，避免容器 IP 漂移/实例下线后旧 entry 永久残留。
		c.detectMu.Lock()
		delete(c.detectLastResult, k)
		delete(c.detectLastReportTime, k)
		delete(c.lastDetectErr, k)
		c.detectMu.Unlock()
	}
}

func (c *ResourceHealthChecker) createCheckJob(protocol string, rule *fault_tolerance.FaultDetectRule) func() {
	// 周期性探测任务体：每个 interval 被调度器触发一次，属于周期级日志，使用 Debug 级别。
	return func() {
		if c.isStopped() {
			c.log.Debugf("[FaultDetect] check job skipped (checker stopped), resource=%s, protocol=%s, rule=%s",
				c.resource.String(), protocol, rule.GetName())
			return
		}
		name := fault_tolerance.FaultDetectRule_Protocol_value[protocol]
		c.log.Debugf("[FaultDetect] check job fired, resource=%s, protocol=%s, rule=%s",
			c.resource.String(), protocol, rule.GetName())
		c.checkResource(fault_tolerance.FaultDetectRule_Protocol(name), rule)
	}
}

func (c *ResourceHealthChecker) checkResource(protocol fault_tolerance.FaultDetectRule_Protocol, rule *fault_tolerance.FaultDetectRule) {
	port := rule.GetPort()
	if port > 0 {
		hosts := map[string]struct{}{}
		c.lock.RLock()
		defer c.lock.RUnlock()
		// 周期级日志：每次探测调度都会执行，统计待探实例数，使用 Debug 级别避免刷屏。
		c.log.Debugf("[FaultDetect] checkResource start (fixed port=%d), resource=%s, protocol=%s, instance_count=%d",
			port, c.resource.String(), protocol.String(), len(c.instances))
		for k, v := range c.instances {
			if _, ok := hosts[k]; ok {
				continue
			}
			hosts[k] = struct{}{}
			ins := pb.NewInstanceInProto(&service_manage.Instance{
				Host: wrapperspb.String(v.insRes.GetNode().Host),
				Port: wrapperspb.UInt32(v.insRes.GetNode().Port),
			}, defaultServiceKey(v.insRes.GetService()), nil)
			// 探测器按探测规则的 protocol 注册到 healthCheckers，故 doCheck 必须传规则 protocol；
			// 实例自身 protocol（v.protocol）通常为 UNKNOWN（注册时未声明），用它查 healthCheckers
			// 会命中 plugin not found 而跳过探测。
			isSuccess := c.doCheck(ins, protocol, rule)
			v.setCheckResult(isSuccess)
		}
		return
	}
	c.lock.RLock()
	defer c.lock.RUnlock()
	// 周期级日志：port=0 分支用实例自身端口探测，记录待探实例数。
	c.log.Debugf("[FaultDetect] checkResource start (instance port), resource=%s, protocol=%s, instance_count=%d",
		c.resource.String(), protocol.String(), len(c.instances))
	for _, v := range c.instances {
		curProtocol := v.protocol
		// 实例 protocol 仅用于过滤：UNKNOWN（未声明）或与规则 protocol 一致的实例才参与本规则探测。
		if !(curProtocol == fault_tolerance.FaultDetectRule_UNKNOWN || curProtocol == protocol) {
			c.log.Debugf("[FaultDetect] skip instance for protocol mismatch, resource=%s, instance=%s:%d, instance_protocol=%s, rule_protocol=%s",
				c.resource.String(), v.insRes.GetNode().Host, v.insRes.GetNode().Port, curProtocol.String(), protocol.String())
			continue
		}
		ins := pb.NewInstanceInProto(&service_manage.Instance{
			Host: wrapperspb.String(v.insRes.GetNode().Host),
			Port: wrapperspb.UInt32(v.insRes.GetNode().Port),
		}, defaultServiceKey(v.insRes.GetService()), nil)
		// 同 port>0 分支：探测执行用规则 protocol 选取探测器，不能用实例 protocol（多为 UNKNOWN）。
		isSuccess := c.doCheck(ins, protocol, rule)
		v.setCheckResult(isSuccess)
	}
}

// doCheck 执行一次实例探测并上报结果。
// 探测日志收敛策略（均带 rule 名，便于多条探测规则并存时区分日志来源）：
//   - 状态变化（成功→失败、失败→成功）：立即 INFO 打印，便于运维感知实例健康翻转（事件级）。
//   - 状态不变：
//   - 连续失败：按 detectStatusReportPeriod（30s）定时 Debug 汇总（周期级常态，避免刷屏淹没事件）。
//   - 连续成功：不打印（静默，避免噪声淹没关键事件）。
//   - 探测底层异常（err != nil）与 plugin not found：保持 Debug/Warn 即时打印（不在收敛范围内）。
//
// 收敛状态以 instanceKey = host:port 为粒度独立追踪。
func (c *ResourceHealthChecker) doCheck(ins model.Instance, protocol fault_tolerance.FaultDetectRule_Protocol,
	rule *fault_tolerance.FaultDetectRule) bool {
	checker, ok := c.healthCheckers[protocol]
	if !ok {
		c.log.Warnf("plugin not found, skip health check for instance=%s:%d, rule=%s, resource=%s, protocol=%s",
			ins.GetHost(), ins.GetPort(), rule.GetName(), c.resource.String(), protocol.String())
		return false
	}
	ret, err := checker.DetectInstance(ins, rule)
	if err != nil {
		instanceKey := fmt.Sprintf("%s:%d", ins.GetHost(), ins.GetPort())
		errMsg := err.Error()
		// 探测底层异常收敛 + 状态追踪更新，合并为一次 Lock。
		c.detectMu.Lock()
		lastErr, hasErrRecord := c.lastDetectErr[instanceKey]
		if !hasErrRecord || lastErr != errMsg {
			c.lastDetectErr[instanceKey] = errMsg
		}
		c.detectLastResult[instanceKey] = false
		c.detectMu.Unlock()
		if !hasErrRecord || lastErr != errMsg {
			c.log.Warnf("[FaultDetect] doCheck failed, rule=%s, resource=%s, instance=%s, protocol=%s, err=%v",
				rule.GetName(), c.resource.String(), instanceKey, protocol.String(), err)
		}
		return false
	}
	isSuccess := ret.GetRetStatus() == model.RetSuccess
	instanceKey := fmt.Sprintf("%s:%d", ins.GetHost(), ins.GetPort())
	now := time.Now()

	// 状态收敛逻辑
	c.detectMu.Lock()
	lastResult, hasRecord := c.detectLastResult[instanceKey]
	changed := !hasRecord || lastResult != isSuccess
	if changed {
		c.detectLastResult[instanceKey] = isSuccess
		delete(c.detectLastReportTime, instanceKey) // 状态变化后重置定时周期
	}
	c.detectMu.Unlock()

	if changed {
		// 状态翻转（成功↔失败）属事件级，立即 INFO 打印；带 rule 名便于多探测规则时区分来源。
		c.log.Infof("[FaultDetect] detect status change: rule=%s, instance=%s, resource=%s, protocol=%s, "+
			"success=%v, code=%s, delay=%+v",
			rule.GetName(), instanceKey, c.resource.String(), protocol.String(), isSuccess, ret.GetCode(), ret.GetDelay())
	} else if !isSuccess {
		c.detectMu.Lock()
		lastReport, hasReport := c.detectLastReportTime[instanceKey]
		if !hasReport || now.Sub(lastReport) >= detectStatusReportPeriod {
			c.detectLastReportTime[instanceKey] = now
			c.detectMu.Unlock()
			// 连续失败的定时汇报属周期级常态（状态未变），降级 Debug，避免长期故障实例每 30s 刷屏
			// 淹没真正的状态翻转事件；带 rule 名便于多探测规则时区分来源。
			c.log.Debugf("[FaultDetect] detect still failing: rule=%s, instance=%s, resource=%s, protocol=%s, "+
				"code=%s, delay=%+v (reported every %v)",
				rule.GetName(), instanceKey, c.resource.String(), protocol.String(), ret.GetCode(), ret.GetDelay(),
				detectStatusReportPeriod)
		} else {
			c.detectMu.Unlock()
		}
	}
	// 成功且状态不变 → 静默，不打印

	stat := &model.ResourceStat{
		Resource:  c.resource,
		RetCode:   ret.GetCode(),
		Delay:     ret.GetDelay(),
		RetStatus: ret.GetRetStatus(),
	}
	// 探测结果走 record=false 上报：仅参与熔断状态机统计，不触发实例重新注册进探测集合
	if err := c.circuitBreaker.reportFaultDetectStat(stat); err != nil {
		c.log.Errorf("[FaultDetect] report resource stat error, resource=%s, err=%s", c.resource.String(), err.Error())
	}
	return isSuccess
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
		// 探测目标集合新增实例属于事件级（懒启动填充），但触发频率受业务请求驱动，
		// 为避免高 QPS 下刷屏，统一用 Debug 级别。
		c.log.Debugf("[FaultDetect] add instance into health check set, resource=%s, node=%s, total=%d",
			c.resource.String(), res.GetNode().String(), len(c.instances))
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
		// 探测规则匹配：优先使用 targetService.api（protocol + method + path 三维匹配），
		// 对齐 polaris-java ResourceHealthChecker.matchResource。
		// api 为空时回退到 deprecated 的 targetService.method 做兼容。
		if res.GetLevel() == fault_tolerance.Level_METHOD {
			api := targetService.GetApi()
			if api != nil {
				if !matchMethodWithAPI(res, api, c.regexFunction) {
					continue
				}
			} else if !matchMethod(res, targetService.GetMethod(), c.regexFunction) {
				continue
			}
		} else {
			// SERVICE / INSTANCE 级：优先用 api.path，回退 method.value
			api := targetService.GetApi()
			if api != nil && api.GetPath() != nil {
				if !match.IsMatchAll(api.GetPath().GetValue().Value) {
					continue
				}
			} else if !match.IsMatchAll(targetService.GetMethod().GetValue().Value) {
				continue
			}
		}
		if _, ok := matchRule[rule.GetProtocol().String()]; !ok {
			matchRule[rule.GetProtocol().String()] = rule
		}
	}
	return matchRule
}

// matchMethod 旧版本接口路径匹配，仅比较 path 维度
// 适用场景：探测规则匹配（FaultDetectRule.targetService.method）以及
// METHOD 级熔断规则在 BlockConfigs 为空时的兼容回退路径
// res     必须为 MethodResource，否则视为非接口级直接放行
// val     待匹配的 MatchString，承载老规则中 destination.method / targetService.method 字段
// regexFunc 正则编译缓存函数
func matchMethod(res model.Resource, val *apimodel.MatchString, regexFunc func(string) *regexp.Regexp) bool {
	if res.GetLevel() != fault_tolerance.Level_METHOD {
		return true
	}
	methodRes := res.(*model.MethodResource)
	return match.MatchString(methodRes.Path, val, regexFunc)
}

// matchMethodWithAPI 接口级熔断 BlockConfig.Api 匹配
// 按 protocol + method + path 三维度联合验证，任一维度不匹配即失败；
// api 为 nil 时视为通配匹配所有接口
// res     被检查的资源；非 METHOD 级直接放行
// api     BlockConfig.Api 配置块，承载 protocol/method/path 三元组
// regexFunc 正则编译缓存函数
// 返回 true 表示该 BlockConfig 适用于当前资源
func matchMethodWithAPI(res model.Resource, api *apimodel.API, regexFunc func(string) *regexp.Regexp) bool {
	if res.GetLevel() != fault_tolerance.Level_METHOD {
		return true
	}
	if api == nil {
		return true
	}
	methodRes := res.(*model.MethodResource)
	if !matchProtocolOrMethod(methodRes.Protocol, api.GetProtocol()) {
		return false
	}
	if !matchProtocolOrMethod(methodRes.Method, api.GetMethod()) {
		return false
	}
	if api.GetPath() == nil {
		return true
	}
	return match.MatchString(methodRes.Path, api.GetPath(), regexFunc)
}

// matchProtocolOrMethod 单字段协议/HTTP 方法匹配
// target 为空或通配符（*）时视为放行；否则与 source 忽略大小写比较
func matchProtocolOrMethod(source, target string) bool {
	if target == "" || match.IsMatchAll(target) {
		return true
	}
	return strings.EqualFold(source, target)
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
