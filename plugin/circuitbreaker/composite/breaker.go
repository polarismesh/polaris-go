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
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	regexp "github.com/dlclark/regexp2"
	"github.com/polarismesh/specification/source/go/api/v1/fault_tolerance"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/healthcheck"
	"github.com/polarismesh/polaris-go/pkg/plugin/localregistry"
	"github.com/polarismesh/polaris-go/pkg/sdk"
)

const (
	defaultCheckPeriod         = 60 * time.Second
	defaultCheckPeriodMultiple = 20
	// defaultRuleCheckInterval 周期性规则复检默认间隔；与服务端规则下发延迟相匹配的兜底节奏
	defaultRuleCheckInterval = 60 * time.Second
)

type CompositeCircuitBreaker struct {
	*plugin.PluginBase
	// pluginCtx
	pluginCtx *plugin.InitContext
	// countersCache
	countersCache map[fault_tolerance.Level]*CountersBucket
	// healthCheckers .
	healthCheckers map[fault_tolerance.FaultDetectRule_Protocol]healthcheck.HealthChecker
	// healthCheckCache map[model.Resource]*ResourceHealthChecker
	healthCheckCache *sync.Map
	// serviceHealthCheckCache map[model.ServiceKey]map[model.Resource]*ResourceHealthChecker
	serviceHealthCheckCache *sync.Map
	// containers model.Resource -> *RuleContainer
	containers *sync.Map
	// ruleDict 服务规则三级索引（Level → ServiceKey → []*Rule）
	// 由 RuleContainer 拉规则后写入；OnEvent 与定时复检场景下统一查询规则
	ruleDict *CircuitBreakerRuleDictionary
	// reportedServiceKeys 记录已收到 Report 的 ServiceKey 集合
	// 由定时复检任务遍历，主动拉一次规则做兜底；首次写入时打 INFO 日志
	reportedServiceKeys *sync.Map
	// engineFlow
	engineFlow sdk.Engine
	// regexpCache regexp -> *regexp.Regexp
	rlock sync.RWMutex
	// regexpCache
	regexpCache map[string]*regexp.Regexp
	// checkPeriod
	checkPeriod time.Duration
	// ruleCheckInterval 周期性规则复检间隔
	ruleCheckInterval time.Duration
	// healthCheckInstanceExpireInterval
	healthCheckInstanceExpireInterval time.Duration
	// defaultInstanceCircuitBreakerConfig
	defaultInstanceCircuitBreakerConfig defaultInstanceCircuitBreakerConfig
	// localCache
	localCache localregistry.LocalRegistry
	// 上下文日志
	logCtx *log.ContextLogger
	// cbLog 熔断日志记录器
	cbLog log.Logger
	// start
	start int32
	// destroy .
	destroy int32
	// cancel
	cancel context.CancelFunc
	// taskCtx
	taskCtx context.Context
	// executor
	executor *TaskExecutor
}

// Init 初始化插件
func (c *CompositeCircuitBreaker) Init(ctx *plugin.InitContext) error {
	c.PluginBase = plugin.NewPluginBase(ctx)
	c.pluginCtx = ctx
	// 监听规则
	callbackHandler := common.PluginEventHandler{
		Callback: c.OnEvent,
	}
	c.pluginCtx.Plugins.RegisterEventSubscriber(common.OnServiceAdded, callbackHandler)
	c.pluginCtx.Plugins.RegisterEventSubscriber(common.OnServiceUpdated, callbackHandler)
	c.pluginCtx.Plugins.RegisterEventSubscriber(common.OnServiceDeleted, callbackHandler)
	return nil
}

// Start 启动插件，对于需要依赖外部资源，以及启动协程的操作，在Start方法里面做
func (c *CompositeCircuitBreaker) Start() error {
	c.taskCtx, c.cancel = context.WithCancel(context.Background())
	c.countersCache = make(map[fault_tolerance.Level]*CountersBucket)
	c.healthCheckers = make(map[fault_tolerance.FaultDetectRule_Protocol]healthcheck.HealthChecker)
	c.healthCheckCache = &sync.Map{}
	c.serviceHealthCheckCache = &sync.Map{}
	c.containers = &sync.Map{}
	c.reportedServiceKeys = &sync.Map{}
	c.regexpCache = make(map[string]*regexp.Regexp)
	c.executor = newTaskExecutor(8, c.logCtx)
	c.checkPeriod = c.pluginCtx.Config.GetConsumer().GetCircuitBreaker().GetCheckPeriod()
	if c.checkPeriod == 0 {
		c.checkPeriod = defaultCheckPeriod
	}
	c.ruleCheckInterval = defaultRuleCheckInterval
	c.healthCheckInstanceExpireInterval = c.checkPeriod * defaultCheckPeriodMultiple
	c.defaultInstanceCircuitBreakerConfig = defaultInstanceCircuitBreakerConfig{
		defaultRuleEnable:   c.pluginCtx.Config.GetConsumer().GetCircuitBreaker().IsDefaultRuleEnable(),
		defaultErrorCount:   c.pluginCtx.Config.GetConsumer().GetCircuitBreaker().GetDefaultErrorCount(),
		defaultErrorPercent: c.pluginCtx.Config.GetConsumer().GetCircuitBreaker().GetDefaultErrorPercent(),
		defaultInterval: int(c.pluginCtx.Config.GetConsumer().GetCircuitBreaker().GetDefaultInterval().
			Seconds()),
		defaultMinimumRequest:     c.pluginCtx.Config.GetConsumer().GetCircuitBreaker().GetDefaultMinimumRequest(),
		sleepWindow:               int(c.pluginCtx.Config.GetConsumer().GetCircuitBreaker().GetSleepWindow().Seconds()),
		successCountAfterHalfOpen: c.pluginCtx.Config.GetConsumer().GetCircuitBreaker().GetSuccessCountAfterHalfOpen(),
	}
	c.engineFlow = c.pluginCtx.ValueCtx.GetEngine()
	c.start = 1

	c.countersCache[fault_tolerance.Level_SERVICE] = newCountersBucket()
	c.countersCache[fault_tolerance.Level_METHOD] = newCountersBucket()
	c.countersCache[fault_tolerance.Level_INSTANCE] = newCountersBucket()
	c.countersCache[fault_tolerance.Level_GROUP] = newCountersBucket()

	plugins, err := c.pluginCtx.Plugins.GetPlugins(common.TypeHealthCheck)
	if err != nil {
		return err
	}
	for i := range plugins {
		item := plugins[i]
		checker := item.(healthcheck.HealthChecker)
		c.healthCheckers[checker.Protocol()] = checker
	}
	registryPlugin, err := c.pluginCtx.Plugins.GetPlugin(common.TypeLocalRegistry, c.pluginCtx.Config.GetConsumer().GetLocalCache().GetType())
	if err != nil {
		return err
	}
	c.localCache = registryPlugin.(localregistry.LocalRegistry)
	c.logCtx = c.pluginCtx.ValueCtx.GetContextLogger()
	c.cbLog = c.logCtx.GetCircuitBreakerLogger()
	// 初始化规则字典：复用插件级 regex 缓存避免重复编译
	c.ruleDict = newCircuitBreakerRuleDictionary(c.loadOrStoreCompiledRegex, c.cbLog)
	// 启动周期性规则复检兜底任务
	c.executor.IntervalExecute(c.ruleCheckInterval, c.checkRules)
	return nil
}

// Destroy 销毁插件，可用于释放资源。
// 通过 CAS 保证幂等：仅首次调用（destroy 从 0 翻为 1）执行实际销毁；
// 重复调用时 CAS 失败，直接返回 nil。
func (c *CompositeCircuitBreaker) Destroy() error {
	if !atomic.CompareAndSwapInt32(&c.destroy, 0, 1) {
		return nil
	}
	c.cancel()
	c.healthCheckCache.Range(func(key, value interface{}) bool {
		checker, ok := value.(*ResourceHealthChecker)
		if !ok {
			return true
		}
		checker.stop()
		return true
	})
	return nil
}

// CheckResource get the resource circuitbreaker status
func (c *CompositeCircuitBreaker) CheckResource(res model.Resource) model.CircuitBreakerStatus {
	counters, exist := c.getResourceCounters(res)
	if !exist {
		return nil
	}
	return counters.CurrentCircuitBreakerStatus()
}

// Report report resource invoke result stat
func (c *CompositeCircuitBreaker) Report(stat *model.ResourceStat) error {
	return c.doReport(stat, true)
}

// reportFaultDetectStat 上报主动探测结果。
// 与业务请求上报（Report）的区别在于 record=false：探测结果只参与熔断状态机统计，
// 不再触发 addInstanceForHealthCheck 把实例重新注册进探测集合，避免探测自身扩充探测目标的自循环。
// stat 探测结果统计；返回 doReport 的处理结果（当前恒为 nil，错误仅记录日志）。
func (c *CompositeCircuitBreaker) reportFaultDetectStat(stat *model.ResourceStat) error {
	return c.doReport(stat, false)
}

func (c *CompositeCircuitBreaker) doReport(stat *model.ResourceStat, record bool) error {
	// 第一层：检查 stat 是否为 nil
	if stat == nil {
		c.cbLog.Errorf("[CircuitBreaker] doReport failed: stat is nil")
		return nil
	}
	// 第二层：检查 stat.Resource 接口是否为 nil
	if stat.Resource == nil {
		c.cbLog.Errorf("[CircuitBreaker] doReport failed: stat.Resource is nil, stat=%+v", stat)
		return nil
	}
	resource := stat.Resource
	// 第三层：使用反射检查接口底层值是否为 nil（避免 Go 接口陷阱）
	rv := reflect.ValueOf(resource)
	if !rv.IsValid() {
		c.cbLog.Errorf("[CircuitBreaker] doReport failed: resource reflect value is invalid, "+
			"resource type=%T", resource)
		return nil
	}
	if rv.IsNil() {
		c.cbLog.Errorf("[CircuitBreaker] doReport failed: resource underlying value is nil (Go "+
			"interface trap), resource type=%T", resource)
		return nil
	}
	// 第四层：检查 Service 是否为 nil
	service := resource.GetService()
	if service == nil {
		c.cbLog.Errorf("[CircuitBreaker] doReport failed: resource.GetService() returns nil, "+
			"resource=%s, level=%v", resource.String(), resource.GetLevel())
		return nil
	}
	// 第五层：检查 Level 是否有效
	level := resource.GetLevel()
	if level == fault_tolerance.Level_UNKNOWN {
		c.cbLog.Errorf("[CircuitBreaker] doReport failed: resource level is UNKNOWN, resource=%s, "+
			"service=%s", resource.String(), service.String())
		return nil
	}

	retStatus := stat.RetStatus
	// 因为限流、熔断被拒绝的请求，不需要进入熔断数据上报
	if retStatus == model.RetReject || retStatus == model.RetFlowControl {
		return nil
	}

	counters, exist := c.getResourceCounters(resource)
	if !exist {
		c.loadOrStoreContainer(resource)
	} else {
		counters.Report(stat)
	}
	c.recordReportedService(*service)
	c.addInstanceForHealthCheck(resource, record)
	return nil
}

// recordReportedService 把已收到 Report 的服务追加进 reportedServiceKeys
// 仅首次写入时打 INFO 日志，避免高频 Report 反复刷屏；定时复检任务从这里取需要主动拉规则的服务列表。
func (c *CompositeCircuitBreaker) recordReportedService(svc model.ServiceKey) {
	if c.reportedServiceKeys == nil {
		return
	}
	if _, loaded := c.reportedServiceKeys.LoadOrStore(svc, struct{}{}); !loaded {
		c.cbLog.Infof("[CircuitBreaker] start tracking service for periodic rule recheck: %s", svc.String())
	}
}

// loadOrStoreContainer 惰性获取或创建给定 resource 的 RuleContainer。
// newRuleContainer 本身已无副作用（不再在构造时触发规则刷新），首次拉取规则的调度
// 推迟到这里：仅当 LoadOrStore 确认本 goroutine 的实例被真正存入（loaded==false）时
// 才调 scheduleCircuitBreaker()。并发抢占场景下，落败 goroutine 构造的 container 是
// 一个未调度的空壳，会被 GC 回收，不会产生多余的远程规则刷新与计数器重复 initialized。
func (c *CompositeCircuitBreaker) loadOrStoreContainer(res model.Resource) {
	key := res.String()
	if _, ok := c.containers.Load(key); ok {
		return
	}
	container := newRuleContainer(c.taskCtx, res, c)
	if _, loaded := c.containers.LoadOrStore(key, container); !loaded {
		container.scheduleCircuitBreaker()
	}
}

// checkRules 周期性规则复检
// 遍历 reportedServiceKeys 中所有曾被 Report 过的服务，主动调 SyncGetServiceRule 拉一次熔断规则；
// 拉到的规则统一写入 ruleDict 做兜底；具体的 ResourceCounters 重建仍走 OnEvent 通道，
// 这里只解决"push 通道临时丢失"或"默认实例规则误占位"导致字典里没有最新规则的场景。
func (c *CompositeCircuitBreaker) checkRules() {
	if c.isDestroyed() || c.start == 0 {
		return
	}
	if c.engineFlow == nil || c.ruleDict == nil || c.reportedServiceKeys == nil {
		return
	}
	c.reportedServiceKeys.Range(func(key, _ interface{}) bool {
		svc, ok := key.(model.ServiceKey)
		if !ok {
			return true
		}
		resp, err := c.engineFlow.SyncGetServiceRule(model.EventCircuitBreaker,
			&model.GetServiceRuleRequest{
				Namespace: svc.Namespace,
				Service:   svc.Service,
			})
		if err != nil {
			c.cbLog.Warnf("[CircuitBreaker] periodic rule recheck failed for %s: %+v",
				svc.String(), err)
			return true
		}
		c.ruleDict.PutServiceRule(svc, resp)
		return true
	})
}

func (c *CompositeCircuitBreaker) addInstanceForHealthCheck(res model.Resource, record bool) {
	insRes, ok := res.(*model.InstanceResource)
	if !ok {
		return
	}
	checkers, exist := c.loadServiceHealthCheck(*insRes.GetService())
	if !exist {
		return
	}
	checkers.foreach(func(rhc *ResourceHealthChecker) {
		rhc.addInstance(insRes, true)
	})
}

func (c *CompositeCircuitBreaker) loadOrStoreCompiledRegex(s string) *regexp.Regexp {
	c.rlock.Lock()
	defer c.rlock.Unlock()

	if val, ok := c.regexpCache[s]; ok {
		return val
	}

	val := regexp.MustCompile(s, regexp.RE2)
	c.regexpCache[s] = val
	return val
}

func (c *CompositeCircuitBreaker) getResourceCounters(res model.Resource) (*ResourceCounters, bool) {
	levelCache := c.getLevelResourceCounters(res.GetLevel())
	return levelCache.get(res)
}

func (c *CompositeCircuitBreaker) getLevelResourceCounters(level fault_tolerance.Level) *CountersBucket {
	return c.countersCache[level]
}

// Type 插件类型
func (c *CompositeCircuitBreaker) Type() common.Type {
	return common.TypeCircuitBreaker
}

// Name 插件名，一个类型下插件名唯一
func (c *CompositeCircuitBreaker) Name() string {
	return "composite"
}

func (c *CompositeCircuitBreaker) OnEvent(event *common.PluginEvent) error {
	if c.isDestroyed() || c.start == 0 {
		c.cbLog.Debugf("[CircuitBreaker] OnEvent ignored, destroyed: %v, started: %v", c.isDestroyed(),
			c.start)
		return nil
	}

	var (
		eventObject *common.ServiceEventObject
		ok          bool
	)
	if eventObject, ok = event.EventObject.(*common.ServiceEventObject); !ok {
		c.cbLog.Debugf("[CircuitBreaker] OnEvent ignored, event object is not ServiceEventObject")
		return nil
	}
	if eventObject.SvcEventKey.Type != model.EventCircuitBreaker && eventObject.SvcEventKey.Type !=
		model.EventFaultDetect {
		c.cbLog.Debugf("[CircuitBreaker] OnEvent ignored, event type: %v",
			eventObject.SvcEventKey.Type)
		return nil
	}
	c.cbLog.Infof("[CircuitBreaker] OnEvent processing, namespace: %s, service: %s, eventType: %v",
		eventObject.SvcEventKey.Namespace, eventObject.SvcEventKey.Service, eventObject.SvcEventKey.Type)
	// 服务规则变化时同步清字典，避免 Lookup 命中已过期的规则
	if eventObject.SvcEventKey.Type == model.EventCircuitBreaker && c.ruleDict != nil {
		c.ruleDict.OnServiceChanged(model.ServiceKey{
			Namespace: eventObject.SvcEventKey.Namespace,
			Service:   eventObject.SvcEventKey.Service,
		})
	}
	c.doSchedule(eventObject.SvcEventKey)
	return nil
}

func (c *CompositeCircuitBreaker) doSchedule(expectKey model.ServiceEventKey) {
	c.cbLog.Debugf("[CircuitBreaker] doSchedule started, namespace: %s, service: %s, eventType: %v",
		expectKey.Namespace, expectKey.Service, expectKey.Type)
	c.containers.Range(func(key, value interface{}) bool {
		ruleC := value.(*RuleContainer)
		resource := ruleC.res
		actualKey := resource.GetService()
		if actualKey.Namespace == expectKey.Namespace && actualKey.Service == expectKey.Service {
			c.cbLog.Debugf("[CircuitBreaker] doSchedule matched resource: %s, eventType: %v",
				resource.String(), expectKey.Type)
			switch expectKey.Type {
			case model.EventCircuitBreaker:
				c.cbLog.Debugf("[CircuitBreaker] doSchedule triggering scheduleCircuitBreaker for "+
					"resource: %s", resource.String())
				ruleC.scheduleCircuitBreaker()
			case model.EventFaultDetect:
				c.cbLog.Debugf("[CircuitBreaker] doSchedule triggering scheduleHealthCheck for "+
					"resource: %s", resource.String())
				ruleC.scheduleHealthCheck()
			}
		}
		return true
	})
}

func (c *CompositeCircuitBreaker) loadServiceHealthCheck(key model.ServiceKey) (*HealthCheckersBucket, bool) {
	val, ok := c.serviceHealthCheckCache.Load(key)
	if !ok {
		return nil, false
	}
	return val.(*HealthCheckersBucket), true
}

func (c *CompositeCircuitBreaker) loadOrStoreServiceHealthCheck(key model.ServiceKey) *HealthCheckersBucket {
	c.serviceHealthCheckCache.LoadOrStore(key, newHealthCheckersBucket())
	val, _ := c.serviceHealthCheckCache.Load(key)
	return val.(*HealthCheckersBucket)
}

func (c *CompositeCircuitBreaker) delServiceHealthCheck(key model.ServiceKey) {
	c.serviceHealthCheckCache.LoadAndDelete(key)
}

func (c *CompositeCircuitBreaker) getResourceHealthChecker(res model.Resource) (*ResourceHealthChecker, bool) {
	v, ok := c.healthCheckCache.Load(res)
	if !ok {
		return nil, false
	}
	return v.(*ResourceHealthChecker), true
}

func (c *CompositeCircuitBreaker) delResourceHealthChecker(res model.Resource) (*ResourceHealthChecker, bool) {
	v, ok := c.healthCheckCache.LoadAndDelete(res)
	if !ok {
		return nil, false
	}
	return v.(*ResourceHealthChecker), true
}

func (c *CompositeCircuitBreaker) setResourceHealthChecker(res model.Resource, checker *ResourceHealthChecker) {
	c.healthCheckCache.Store(res, checker)
}

func (c *CompositeCircuitBreaker) isDestroyed() bool {
	return atomic.LoadInt32(&c.destroy) == 1
}

// init 注册插件信息.
func init() {
	plugin.RegisterConfigurablePlugin(&CompositeCircuitBreaker{}, &circuitbreakConfig{})
}

func newCountersBucket() *CountersBucket {
	return &CountersBucket{m: make(map[string]*ResourceCounters)}
}

type CountersBucket struct {
	lock sync.RWMutex
	m    map[string]*ResourceCounters
}

func (c *CountersBucket) get(key model.Resource) (*ResourceCounters, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()

	v, ok := c.m[key.String()]
	return v, ok
}

func (c *CountersBucket) put(key model.Resource, counter *ResourceCounters) {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.m[key.String()] = counter
}

func (c *CountersBucket) remove(key model.Resource) (*ResourceCounters, bool) {
	c.lock.Lock()
	defer c.lock.Unlock()

	v, ok := c.m[key.String()]
	delete(c.m, key.String())
	return v, ok
}

func newHealthCheckersBucket() *HealthCheckersBucket {
	return &HealthCheckersBucket{m: make(map[string]*ResourceHealthChecker)}
}

type HealthCheckersBucket struct {
	lock sync.RWMutex
	m    map[string]*ResourceHealthChecker
}

func (c *HealthCheckersBucket) get(key model.Resource) (*ResourceHealthChecker, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()

	v, ok := c.m[key.String()]
	return v, ok
}

func (c *HealthCheckersBucket) put(key model.Resource, counter *ResourceHealthChecker) {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.m[key.String()] = counter
}

func (c *HealthCheckersBucket) remove(key model.Resource) (*ResourceHealthChecker, bool) {
	c.lock.Lock()
	defer c.lock.Unlock()

	v, ok := c.m[key.String()]
	delete(c.m, key.String())
	return v, ok
}

func (c *HealthCheckersBucket) foreach(f func(*ResourceHealthChecker)) {
	c.lock.RLock()
	defer c.lock.RUnlock()

	for _, v := range c.m {
		f(v)
	}
}

func (c *HealthCheckersBucket) isEmpty() bool {
	c.lock.Lock()
	defer c.lock.Unlock()

	return len(c.m) == 0
}
