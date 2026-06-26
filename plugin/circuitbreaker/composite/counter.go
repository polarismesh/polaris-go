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
	"sync"
	"sync/atomic"
	"time"

	regexp "github.com/dlclark/regexp2"
	"github.com/polarismesh/specification/source/go/api/v1/fault_tolerance"
	"github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/event"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/plugin/events"
	"github.com/polarismesh/polaris-go/pkg/plugin/localregistry"
	"github.com/polarismesh/polaris-go/pkg/sdk"
)

const (
	_stateCloseToOpen = iota
	_stateOpenToHalfOpen
	_stateHalfOpenToOpen
	_stateHalfOpenToClose
)

// ResourceCounters 资源级状态机
// 资源（service / method / instance）只持有单一 Open/HalfOpen/Close 状态机；
// 错误条件下沉到 blockCounter 层各自独立计数，任一块的任一 trigger 触发即让资源进入 Open。
// HalfOpen → Close 切换时会遍历所有块的 trigger counter 复位。
type ResourceCounters struct {
	circuitBreaker *CompositeCircuitBreaker
	lock           sync.RWMutex
	// activeRule 当前生效的熔断规则
	activeRule *fault_tolerance.CircuitBreakerRule
	// blocks 块级计数器列表，每个 BlockConfig 一个
	blocks []*blockCounter
	// legacyBlock BlockConfigs 为空时构造的兜底块，承载顶层弃用字段（兼容老规则与默认实例规则）
	legacyBlock *blockCounter
	// resource 当前规则作用的资源
	resource model.Resource
	// statusRef 状态机原子引用，承载 *model.CircuitBreakerStatusWrapper
	statusRef atomic.Value
	// fallbackInfo 降级响应信息，构造时一次性从规则的 FallbackConfig 解析缓存，
	// 仅在 toOpen() 注入到 OPEN 状态，含 code/body/headers
	fallbackInfo *model.FallbackInfo
	// regexFunction 正则编译函数，复用 CompositeCircuitBreaker 的 regex 缓存
	regexFunction func(string) *regexp.Regexp
	// engineFlow SDK 引擎入口
	engineFlow sdk.Engine
	// logStat 供 trigger 计数器和自身状态变化日志使用，统一输出到 circuitbreaker 目录
	logStat log.Logger
	// isInsRes 是否实例级资源；为 true 时状态切换会回写 localCache
	isInsRes bool
	// executor 任务执行器，负责状态切换的延迟与亲和性调度
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
		logStat:        circuitBreaker.logCtx.GetCircuitBreakerLogger(),
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

// init 初始化块级计数器列表
// 遍历规则中的每个 BlockConfig，按 BlockConfig × TriggerCondition 的笛卡尔积建立独立的
// trigger counter；每个块持有自身解析后的 errorConditions（块非空优先块、否则回退顶层）。
// counter 名称格式为 ruleName 或 ruleName#blockConfigName（当 blockConfigName 非空时）。
// 兼容老规则：当 BlockConfigs 为空时，使用顶层弃用字段构造单一 legacyBlock 兜底。
func (rc *ResourceCounters) init() error {
	for _, bc := range rc.activeRule.GetBlockConfigs() {
		ruleName := rc.activeRule.Name
		if bc.GetName() != "" {
			ruleName = ruleName + "#" + bc.GetName()
		}
		errConditions := resolveBlockErrorConditions(rc.activeRule, bc)
		rc.blocks = append(rc.blocks, newBlockCounter(rc, ruleName, bc.GetApi(),
			errConditions, bc.GetTriggerConditions()))
	}
	// 老规则兼容路径：BlockConfigs 为空时直接拿顶层 TriggerCondition / ErrorConditions
	if len(rc.blocks) == 0 {
		rc.legacyBlock = newBlockCounter(rc, rc.activeRule.Name, nil,
			rc.activeRule.GetErrorConditions(), rc.activeRule.GetTriggerCondition())
	}
	return nil
}

func (rc *ResourceCounters) CurrentActiveRule() *fault_tolerance.CircuitBreakerRule {
	return rc.activeRule
}

func (rc *ResourceCounters) updateCircuitBreakerStatus(status model.CircuitBreakerStatus) {
	rc.statusRef.Store(&model.CircuitBreakerStatusWrapper{
		Val: status,
	})
}

func (rc *ResourceCounters) CurrentCircuitBreakerStatus() model.CircuitBreakerStatus {
	val := rc.statusRef.Load()
	if val == nil {
		return nil
	}
	if wrapper, ok := val.(*model.CircuitBreakerStatusWrapper); ok {
		return wrapper.Val
	}
	return nil
}

func (rc *ResourceCounters) CloseToOpen(breaker string, reason string) {
	rc.lock.Lock()
	defer rc.lock.Unlock()

	status := rc.CurrentCircuitBreakerStatus()
	if status.GetStatus() == model.Close {
		rc.toOpen(status, breaker, reason)
	}
}

// toOpen 切换到 Open 态。reason 为触发原因描述（由 trigger 层构造），用于熔断事件上报；
// 半开重新打开（HalfOpenToOpen）等无 trigger 原因场景传空串。
func (rc *ResourceCounters) toOpen(before model.CircuitBreakerStatus, name string, reason string) {
	newStatus := model.NewCircuitBreakerStatus(name, model.Open, time.Now(),
		func(cbs model.CircuitBreakerStatus) {
			cbs.SetFallbackInfo(rc.fallbackInfo)
		})
	rc.updateCircuitBreakerStatus(newStatus)
	rc.reportCircuitStatus(newStatus)
	rc.logStat.Infof("[CircuitBreaker] status change: %s -> %s, resource(%s), rule(%s, id=%s, rev=%s)",
		before.GetStatus(), newStatus.GetStatus(), rc.resource.String(),
		before.GetCircuitBreaker(), rc.activeRule.Id, rc.activeRule.Revision)

	rc.reportCircuitBreakMetric(newStatus)
	rc.reportCircuitBreakerEvent(before.GetStatus(), model.Open, reason)

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
	rc.logStat.Infof("[CircuitBreaker] status change: %s -> %s, resource(%s), rule(%s, id=%s, rev=%s)",
		status.GetStatus(), halfOpenStatus.GetStatus(), rc.resource.String(),
		status.GetCircuitBreaker(), rc.activeRule.Id, rc.activeRule.Revision)
	rc.updateCircuitBreakerStatus(halfOpenStatus)
	rc.reportCircuitStatus(halfOpenStatus)

	rc.reportCircuitBreakMetric(halfOpenStatus)
	rc.reportCircuitBreakerEvent(model.Open, model.HalfOpen, "")
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
	rc.reportCircuitStatus(newStatus)
	rc.logStat.Infof("[CircuitBreaker] status change: %s -> %s, resource(%s), rule(%s, id=%s, rev=%s)",
		status.GetStatus(), newStatus.GetStatus(), rc.resource.String(),
		status.GetCircuitBreaker(), rc.activeRule.Id, rc.activeRule.Revision)

	rc.reportCircuitBreakMetric(newStatus)
	rc.reportCircuitBreakerEvent(model.HalfOpen, model.Close, "")

	rc.resumeAllBlocks()
}

func (rc *ResourceCounters) HalfOpenToOpen() {
	rc.lock.Lock()
	defer rc.lock.Unlock()

	status := rc.CurrentCircuitBreakerStatus()
	if status.GetStatus() == model.HalfOpen {
		rc.toOpen(status, status.GetCircuitBreaker(), "")
	}
}

func (rc *ResourceCounters) Report(stat *model.ResourceStat) {
	curStatus := rc.CurrentCircuitBreakerStatus()
	if curStatus != nil && curStatus.GetStatus() == model.HalfOpen {
		rc.handleHalfOpenReport(stat, curStatus)
		return
	}
	rc.logStat.Debugf("[CircuitBreaker] report resource stat to counter %s", stat.Resource.String())
	rc.dispatchToBlocks(stat)
}

// handleHalfOpenReport HalfOpen 态下使用资源级 isSuccess 判定，配额由 HalfOpenStatus 自身管理
// 失败/数量达阈值任一条件触发 → 从 HalfOpen 切到 Open；全部成功且达阈值 → 切到 Close
func (rc *ResourceCounters) handleHalfOpenReport(stat *model.ResourceStat,
	curStatus model.CircuitBreakerStatus) {
	isSuccess := rc.evaluateSuccess(stat)
	halfOpenStatus, ok := curStatus.(*model.HalfOpenStatus)
	if !ok {
		return
	}
	if !halfOpenStatus.Report(isSuccess) {
		return
	}
	switch halfOpenStatus.CalNextStatus() {
	case model.Close:
		rc.executor.AffinityExecute(rc.activeRule.Id, rc.HalfOpenToClose)
	case model.Open:
		rc.executor.AffinityExecute(rc.activeRule.Id, rc.HalfOpenToOpen)
	}
}

// dispatchToBlocks 把一次调用结果按块独立判错并喂给各块的 trigger counter
// 优先使用 blocks（BlockConfigs 解析出的块级计数器集合）；
// 当 BlockConfigs 为空时使用 legacyBlock 兜底，保持对老规则与默认实例规则的兼容。
func (rc *ResourceCounters) dispatchToBlocks(stat *model.ResourceStat) {
	if len(rc.blocks) > 0 {
		for _, b := range rc.blocks {
			if !b.matchAPI(stat.Resource) {
				continue
			}
			b.Report(stat)
		}
		return
	}
	if rc.legacyBlock != nil {
		rc.legacyBlock.Report(stat)
	}
}

// evaluateSuccess HalfOpen 态用块级判错合并视图判定本次调用是否成功
// 任一块判失败即视为整体失败，确保资源级状态机能即时反应错误条件
// 注意：RetReject / RetFlowControl 已在 CompositeCircuitBreaker.Report 入口被提前
// return（见 breaker.go doReport），不会进入本函数，因此本函数只关心 RetFail /
// RetTimeout 两种错误。
func (rc *ResourceCounters) evaluateSuccess(stat *model.ResourceStat) bool {
	if len(rc.blocks) > 0 {
		for _, b := range rc.blocks {
			if !b.matchAPI(stat.Resource) {
				continue
			}
			ret := b.parseRetStatus(stat)
			if ret == model.RetFail || ret == model.RetTimeout {
				return false
			}
		}
		return true
	}
	if rc.legacyBlock != nil {
		ret := rc.legacyBlock.parseRetStatus(stat)
		return ret != model.RetFail && ret != model.RetTimeout
	}
	return stat.RetStatus != model.RetFail && stat.RetStatus != model.RetTimeout
}

// resumeAllBlocks 让所有块内全部 trigger counter 退出 suspended 状态
func (rc *ResourceCounters) resumeAllBlocks() {
	for _, b := range rc.blocks {
		b.Resume()
	}
	if rc.legacyBlock != nil {
		rc.legacyBlock.Resume()
	}
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
	if err := rc.circuitBreaker.localCache.UpdateInstances(updateRequest); err != nil {
		rc.circuitBreaker.logCtx.GetCircuitBreakerLogger().Errorf("update instance circuitbreaker status fail, resource %s, "+
			"rule %s, err %s", insRes.String(), rc.activeRule.Id, err.Error())
	}
}

// reportCircuitBreakMetric 上报熔断状态指标到 StatReporter 链（Prometheus 等）。
// cbStatus 为切换后的目标状态；出错只记日志，不阻断状态机。
func (rc *ResourceCounters) reportCircuitBreakMetric(cbStatus model.CircuitBreakerStatus) {
	if rc.engineFlow == nil {
		return
	}
	gauge := rc.buildCircuitBreakGauge(cbStatus)
	if err := rc.engineFlow.SyncReportStat(model.CircuitBreakStat, gauge); err != nil {
		rc.logStat.Errorf("[CircuitBreaker] report metric failed, resource=%s, err=%v",
			rc.resource.String(), err)
	}
}

// buildCircuitBreakGauge 从资源与当前状态构造熔断监控指标数据源。
// 按资源级别填充被调服务、主调服务、方法、被熔断实例等维度。
func (rc *ResourceCounters) buildCircuitBreakGauge(
	cbStatus model.CircuitBreakerStatus) *model.CircuitBreakGauge {
	gauge := &model.CircuitBreakGauge{
		CBStatus:      cbStatus,
		Level:         levelToString(rc.resource.GetLevel()),
		RuleName:      rc.activeRule.GetName(),
		CalleeService: rc.resource.GetService(),
		CallerService: rc.resource.GetCallerService(),
	}
	switch rc.resource.GetLevel() {
	case fault_tolerance.Level_METHOD:
		if mr, ok := rc.resource.(*model.MethodResource); ok {
			gauge.Method = mr.Path
		}
	case fault_tolerance.Level_INSTANCE:
		if ir, ok := rc.resource.(*model.InstanceResource); ok {
			node := ir.GetNode()
			ins := pb.NewInstanceInProto(&service_manage.Instance{
				Host: wrapperspb.String(node.Host),
				Port: wrapperspb.UInt32(node.Port),
			}, defaultServiceKey(ir.GetService()), nil)
			gauge.ChangeInstance = ins
			gauge.Subset = ins.GetLogicSet()
		}
	}
	return gauge
}

// levelToString 把熔断资源级别枚举映射为监控/事件中的字符串表示。
func levelToString(l fault_tolerance.Level) string {
	switch l {
	case fault_tolerance.Level_SERVICE:
		return "SERVICE"
	case fault_tolerance.Level_METHOD:
		return "METHOD"
	case fault_tolerance.Level_INSTANCE:
		return "INSTANCE"
	default:
		return "UNKNOWN"
	}
}

// reportCircuitBreakerEvent 构造熔断事件并投递到 EventReporter 链。
// from/to 为状态转换前后状态；reason 无则传空串。出错只记日志，不阻断状态机。
func (rc *ResourceCounters) reportCircuitBreakerEvent(from, to model.Status, reason string) {
	eventInfo := event.BuildCircuitBreakerEvent(rc.resource, rc.activeRule, from, to, reason)
	rc.sendEvent(eventInfo)
}

// reportDestroyEvent 在规则被删除或替换、counters 被丢弃时上报销毁事件。
// model.Status 无 Destroy 枚举，故构造 to=Close 的事件后覆写 EventName 为 CircuitBreakerDestroy。
func (rc *ResourceCounters) reportDestroyEvent() {
	cur := rc.CurrentCircuitBreakerStatus()
	if cur == nil {
		return
	}
	eventInfo := event.BuildCircuitBreakerEvent(rc.resource, rc.activeRule, cur.GetStatus(), model.Close, "")
	eventInfo.EventName = event.CircuitBreakerDestroy
	rc.sendEvent(eventInfo)
}

// sendEvent 把事件逐个投递到 EventReporter 链。chain 为空或获取失败时静默返回。
func (rc *ResourceCounters) sendEvent(eventInfo *event.BaseEventImpl) {
	if rc.engineFlow == nil || eventInfo == nil {
		return
	}
	chainValue := rc.engineFlow.GetEventReportChain()
	if chainValue == nil {
		return
	}
	chain, ok := chainValue.([]events.EventReporter)
	if !ok || len(chain) == 0 {
		return
	}
	for _, reporter := range chain {
		if err := reporter.ReportEvent(eventInfo); err != nil {
			rc.logStat.Errorf("[CircuitBreaker] report event failed, resource=%s, eventName=%s, err=%v",
				rc.resource.String(), eventInfo.GetEventName(), err)
		}
	}
}

func buildFallbackInfo(rule *fault_tolerance.CircuitBreakerRule) *model.FallbackInfo {
	if rule == nil {
		return nil
	}
	if rule.GetLevel() != fault_tolerance.Level_METHOD && rule.GetLevel() != fault_tolerance.Level_SERVICE {
		return nil
	}
	// GetFallbackConfig() 在 proto 中即使规则未配置也会返回非 nil 零值 message，
	// 因此必须显式判断 GetEnable()，否则 enable=false 的规则会被误当作启用的降级
	// 配置，导致熔断时返回本不该返回的 code/body。
	fallbackConfig := rule.GetFallbackConfig()
	if fallbackConfig == nil || !fallbackConfig.GetEnable() {
		return nil
	}
	resp := fallbackConfig.GetResponse()
	if resp == nil {
		return nil
	}
	ret := &model.FallbackInfo{
		Code:    int(resp.GetCode()),
		Body:    resp.GetBody(),
		Headers: map[string]string{},
	}
	for _, header := range resp.GetHeaders() {
		ret.Headers[header.GetKey()] = header.GetValue()
	}
	return ret
}
