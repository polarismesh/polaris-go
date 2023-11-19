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
)

const (
	defaultCheckPeriod         = 60 * time.Second
	defaultCheckPeriodMultiple = 20
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
	// engineFlow
	engineFlow model.Engine
	// regexpCache regexp -> *regexp.Regexp
	rlock sync.RWMutex
	// regexpCache
	regexpCache map[string]*regexp.Regexp
	// checkPeriod
	checkPeriod time.Duration
	// healthCheckInstanceExpireInterval
	healthCheckInstanceExpireInterval time.Duration
	// localCache
	localCache localregistry.LocalRegistry
	// log .
	log log.Logger
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
	c.regexpCache = make(map[string]*regexp.Regexp)
	c.executor = newTaskExecutor(8)
	c.checkPeriod = c.pluginCtx.Config.GetConsumer().GetCircuitBreaker().GetCheckPeriod()
	if c.checkPeriod == 0 {
		c.checkPeriod = defaultCheckPeriod
	}
	c.healthCheckInstanceExpireInterval = c.checkPeriod * defaultCheckPeriodMultiple
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
	c.log = log.GetBaseLogger()
	return nil
}

// Destroy 销毁插件，可用于释放资源
func (c *CompositeCircuitBreaker) Destroy() error {
	if atomic.CompareAndSwapInt32(&c.destroy, 0, 1) {
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

func (c *CompositeCircuitBreaker) doReport(stat *model.ResourceStat, record bool) error {
	resource := stat.Resource
	if resource.GetLevel() == fault_tolerance.Level_UNKNOWN {
		return nil
	}
	retStatus := stat.RetStatus
	// 因为限流、熔断被拒绝的请求，不需要进入熔断数据上报
	if retStatus == model.RetReject || retStatus == model.RetFlowControl {
		return nil
	}
	counters, exist := c.getResourceCounters(resource)
	if !exist {
		c.containers.LoadOrStore(resource.String(), newRuleContainer(c.taskCtx, resource, c))
	} else {
		counters.Report(stat)
	}
	c.addInstanceForHealthCheck(resource, record)
	return nil
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
		return nil
	}

	var (
		eventObject *common.ServiceEventObject
		ok          bool
	)
	if eventObject, ok = event.EventObject.(*common.ServiceEventObject); !ok {
		return nil
	}
	if eventObject.SvcEventKey.Type != model.EventCircuitBreaker && eventObject.SvcEventKey.Type != model.EventFaultDetect {
		return nil
	}
	c.doSchedule(eventObject.SvcEventKey)
	return nil
}

func (c *CompositeCircuitBreaker) doSchedule(expectKey model.ServiceEventKey) {
	c.containers.Range(func(key, value interface{}) bool {
		ruleC := value.(*RuleContainer)
		resource := ruleC.res

		actualKey := resource.GetService()
		if actualKey.Namespace == expectKey.Namespace && actualKey.Service == expectKey.Service {
			switch expectKey.Type {
			case model.EventCircuitBreaker:
				ruleC.scheduleCircuitBreaker()
			case model.EventFaultDetect:
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
