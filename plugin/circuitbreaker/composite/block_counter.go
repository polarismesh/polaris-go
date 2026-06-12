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

// Package composite 实现块级化的熔断计数与状态机
// 将每条 BlockConfig 拆分为独立的 blockCounter，块级错误条件互不串扰；
// 资源级状态机仍由 ResourceCounters 统一维护，任一块的任一 trigger 触发即让资源进入 Open。
package composite

import (
	"strconv"

	"github.com/polarismesh/specification/source/go/api/v1/fault_tolerance"
	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"

	"github.com/polarismesh/polaris-go/pkg/algorithm/match"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/plugin/circuitbreaker/composite/trigger"
)

// blockCounter 持有单个 BlockConfig 的错误条件与触发计数器集合
// 每个块独立按自身 errorConditions 判错并喂内部 trigger counter，块之间互不串扰；
// 整个资源仍共用 ResourceCounters 的状态机，任一块触发即让资源进入 Open。
type blockCounter struct {
	// name 计数器名，格式为 ruleName 或 ruleName#blockConfigName，用于状态切换日志
	name string
	// errorConditions 本块独立持有的错误条件列表
	// 优先级：块自身 ErrorConditions 非空时取自身；为空则在构造期回退至规则顶层；都空时留空
	errorConditions []*fault_tolerance.ErrorCondition
	// api 块级 API 匹配配置，非 nil 时仅当请求的 path/protocol/method 匹配才会计入本块
	// 为 nil 时表示无 API 限制，所有请求都计入（兼容老规则和非 METHOD 级规则）
	api *apimodel.API
	// counters 触发计数器列表，每个 TriggerCondition 对应一个
	counters []trigger.TriggerCounter
	// rc 反向引用 ResourceCounters，用于复用 regex 缓存与状态切换 handler
	rc *ResourceCounters
}

// newBlockCounter 构造一个块级计数器
// name           计数器名，作为状态切换时的标识
// api            块级 API 匹配配置，非 nil 时仅匹配该 API 的请求才计入本块，用于同一规则内多 path 独立计数
// errorConditions 该块生效的错误条件列表（已按优先级解析过）
// conditions     该块的触发条件列表，决定建几个 trigger counter
func newBlockCounter(rc *ResourceCounters, name string, api *apimodel.API,
	errorConditions []*fault_tolerance.ErrorCondition,
	conditions []*fault_tolerance.TriggerCondition) *blockCounter {
	bc := &blockCounter{
		name:            name,
		api:             api,
		errorConditions: errorConditions,
		rc:              rc,
	}
	for _, condition := range conditions {
		opt := trigger.Options{
			Resource:      rc.resource,
			Condition:     condition,
			StatusHandler: rc,
			DelayExecutor: rc.executor.DelayExecute,
			Log:           rc.logStat,
			RuleID:        rc.activeRule.Id,
			RuleRevision:  rc.activeRule.Revision,
		}
		switch condition.GetTriggerType() {
		case fault_tolerance.TriggerCondition_CONSECUTIVE_ERROR:
			bc.counters = append(bc.counters, trigger.NewConsecutiveCounter(name, &opt))
		case fault_tolerance.TriggerCondition_ERROR_RATE:
			bc.counters = append(bc.counters, trigger.NewErrRateCounter(name, &opt))
		}
	}
	return bc
}

// resolveBlockErrorConditions 计算单个 BlockConfig 实际生效的错误条件
// 优先级：BlockConfig.ErrorConditions 非空 → 直接使用；为空则回退到 Rule 顶层 ErrorConditions；
// 二者皆空时返回 nil，调用方据此走 stat.RetStatus 透传逻辑
func resolveBlockErrorConditions(rule *fault_tolerance.CircuitBreakerRule,
	bc *fault_tolerance.BlockConfig) []*fault_tolerance.ErrorCondition {
	if len(bc.GetErrorConditions()) > 0 {
		return bc.GetErrorConditions()
	}
	if len(rule.GetErrorConditions()) > 0 {
		return rule.GetErrorConditions()
	}
	return nil
}

// retCodeAbnormalSentinel SDK 内部约定的"异常哨兵值"
// 当 ResourceStat.RetCode 等于该值时，只要本块挂了 RET_CODE 类错误条件，
// 即直接计为 RetFail。OnError 路径下业务回调拿不到底层 HTTP 状态码、
// 网络错没有真实 retCode 时使用，避免被 RANGE/EXACT 类规则错过。
const retCodeAbnormalSentinel = "-1"

// parseRetStatus 仅依据本块自身的 errorConditions 判定本次调用是否失败
// 当本块 errorConditions 为空时，透传 stat.RetStatus，不做改写。
//
// 对挂有 RET_CODE 类错误条件的块，额外识别 retCode == "-1" 哨兵值：
// 该值不参与具体的 MatchString 判定，但只要落进任意一条 RET_CODE 条件下，
// 就直接计为 RetFail，用于覆盖 OnError 路径下的网络错、SDK 内部异常等场景。
func (b *blockCounter) parseRetStatus(stat *model.ResourceStat) model.RetStatus {
	if len(b.errorConditions) == 0 {
		return stat.RetStatus
	}
	for i := range b.errorConditions {
		errCondition := b.errorConditions[i]
		condition := errCondition.GetCondition()
		switch errCondition.GetInputType() {
		case fault_tolerance.ErrorCondition_RET_CODE:
			if match.MatchString(stat.RetCode, condition, b.rc.regexFunction) {
				return model.RetFail
			}
			// SDK 内部哨兵：retCode == "-1" 视为异常错误码，直接判 RetFail
			if stat.RetCode == retCodeAbnormalSentinel {
				return model.RetFail
			}
		case fault_tolerance.ErrorCondition_DELAY:
			delayVal, err := strconv.ParseInt(condition.GetValue().GetValue(), 10, 64)
			if err == nil && stat.Delay.Milliseconds() > delayVal {
				return model.RetTimeout
			}
		}
	}
	return model.RetSuccess
}

// matchAPI 判断当前请求的资源是否匹配本块的 API 配置
// api 为 nil 或无 path 限制时返回 true（兼容老规则和遗留路径）
// 仅 METHOD 级资源才做 path/protocol/method 组合匹配；非 METHOD 级直接放行
func (b *blockCounter) matchAPI(res model.Resource) bool {
	if b.api == nil {
		return true
	}
	if res.GetLevel() != fault_tolerance.Level_METHOD {
		return true
	}
	if b.api.GetPath() == nil {
		return true
	}
	methodRes := res.(*model.MethodResource)
	if !matchProtocolOrMethod(methodRes.Protocol, b.api.GetProtocol()) {
		return false
	}
	if !matchProtocolOrMethod(methodRes.Method, b.api.GetMethod()) {
		return false
	}
	return match.MatchString(methodRes.Path, b.api.GetPath(), b.rc.regexFunction)
}

// Report 把一次调用结果按本块的判错语义喂给本块内所有 trigger counter
func (b *blockCounter) Report(stat *model.ResourceStat) {
	retStatus := b.parseRetStatus(stat)
	isSuccess := retStatus != model.RetFail && retStatus != model.RetTimeout
	for _, c := range b.counters {
		c.Report(isSuccess)
	}
}

// Resume 让本块内全部 trigger counter 退出 suspended 状态
// 用于资源从 HalfOpen 切回 Close 时整体复位
func (b *blockCounter) Resume() {
	for _, c := range b.counters {
		c.Resume()
	}
}
