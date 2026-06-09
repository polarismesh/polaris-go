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

	regexp "github.com/dlclark/regexp2"
	"github.com/polarismesh/specification/source/go/api/v1/fault_tolerance"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
)

// CircuitBreakerRuleDictionary 三级规则索引，按 Level → ServiceKey → []*CircuitBreakerRule 缓存
// 用于以服务为粒度统一管理已拉到的规则集合：
//   - 在事件驱动的 RuleContainer 拉规则后写入；
//   - 让定时复检与 Lookup 调用方在不发起服务端请求的前提下命中规则；
//   - 服务规则变更（OnEvent）时一次性按 ServiceKey 清空，避免遗留旧规则误命中。
type CircuitBreakerRuleDictionary struct {
	lock sync.RWMutex
	// allRules level → serviceKey → 已按优先级排序的规则切片
	allRules map[fault_tolerance.Level]map[model.ServiceKey][]*fault_tolerance.CircuitBreakerRule
	// regexFn 正则编译函数，复用 CompositeCircuitBreaker 的缓存避免重复编译
	regexFn func(string) *regexp.Regexp
	// log 输出索引变更与匹配诊断信息
	log log.Logger
}

// newCircuitBreakerRuleDictionary 构造空索引
// regexFn 不允许为 nil，调用方应传入 CompositeCircuitBreaker.loadOrStoreCompiledRegex
func newCircuitBreakerRuleDictionary(regexFn func(string) *regexp.Regexp,
	logger log.Logger) *CircuitBreakerRuleDictionary {
	return &CircuitBreakerRuleDictionary{
		allRules: make(map[fault_tolerance.Level]map[model.ServiceKey][]*fault_tolerance.CircuitBreakerRule),
		regexFn:  regexFn,
		log:      logger,
	}
}

// PutServiceRule 把一份服务规则按 Level 拆分入库
// 调用方应保证 svc 与 resp 中规则的目的服务一致；同 ServiceKey 的旧记录将被替换。
// resp == nil 或不含规则时等价于清空该服务的所有 level 缓存。
func (d *CircuitBreakerRuleDictionary) PutServiceRule(svc model.ServiceKey, resp *model.ServiceRuleResponse) {
	rules := extractCircuitBreakerRules(resp)

	d.lock.Lock()
	defer d.lock.Unlock()

	// 先清掉该服务的所有 level 缓存，再按 level 重建，避免旧 level 残留
	d.removeServiceLocked(svc)
	if len(rules) == 0 {
		return
	}

	grouped := make(map[fault_tolerance.Level][]*fault_tolerance.CircuitBreakerRule)
	for _, r := range rules {
		grouped[r.GetLevel()] = append(grouped[r.GetLevel()], r)
	}
	for level, levelRules := range grouped {
		bucket, ok := d.allRules[level]
		if !ok {
			bucket = make(map[model.ServiceKey][]*fault_tolerance.CircuitBreakerRule)
			d.allRules[level] = bucket
		}
		bucket[svc] = sortCircuitBreakerRules(levelRules)
	}
	if d.log != nil {
		d.log.Debugf("[CircuitBreaker] dictionary put service: %s, total rules: %d",
			svc.String(), len(rules))
	}
}

// OnServiceChanged 清空指定服务的所有 level 规则缓存
// 调用方触发时机：收到 EventCircuitBreaker 服务规则变更事件，确保下次 Lookup 重新匹配新规则。
func (d *CircuitBreakerRuleDictionary) OnServiceChanged(svc model.ServiceKey) {
	d.lock.Lock()
	defer d.lock.Unlock()

	d.removeServiceLocked(svc)
	if d.log != nil {
		d.log.Infof("[CircuitBreaker] dictionary cleared service: %s on rule change", svc.String())
	}
}

// Lookup 在已缓存规则中匹配资源对应的有效规则
// 匹配流水线：destination 服务 → source 服务 → API（METHOD 级走 BlockConfigs.Api，其余 level 直接放行）。
// 命中即返回首条匹配规则；未命中返回 nil。
// 调用方应自行处理"未命中且 level=INSTANCE 时回退默认规则"的语义。
func (d *CircuitBreakerRuleDictionary) Lookup(res model.Resource) *fault_tolerance.CircuitBreakerRule {
	if res == nil {
		return nil
	}
	level := res.GetLevel()

	d.lock.RLock()
	bucket, ok := d.allRules[level]
	if !ok {
		d.lock.RUnlock()
		return nil
	}
	rules := bucket[*res.GetService()]
	// 拷贝一份切片再释放读锁，避免持锁时执行正则匹配
	candidates := make([]*fault_tolerance.CircuitBreakerRule, len(rules))
	copy(candidates, rules)
	d.lock.RUnlock()

	for _, cbRule := range candidates {
		if !cbRule.GetEnable() {
			continue
		}
		if cbRule.GetLevel() != level {
			continue
		}
		ruleMatcher := cbRule.GetRuleMatcher()
		if ruleMatcher == nil {
			continue
		}
		destination := ruleMatcher.GetDestination()
		if destination == nil {
			continue
		}
		if !matchService(res.GetService(), destination.GetNamespace(), destination.GetService()) {
			continue
		}
		source := ruleMatcher.GetSource()
		if source == nil {
			continue
		}
		if !matchService(res.GetCallerService(), source.GetNamespace(), source.GetService()) {
			continue
		}
		if !matchRuleAPI(res, cbRule, destination, d.regexFn) {
			continue
		}
		return cbRule
	}
	return nil
}

// removeServiceLocked 在持有写锁的前提下移除指定服务在所有 level 下的缓存
func (d *CircuitBreakerRuleDictionary) removeServiceLocked(svc model.ServiceKey) {
	for _, bucket := range d.allRules {
		delete(bucket, svc)
	}
}

// extractCircuitBreakerRules 从 ServiceRuleResponse 抽取规则切片
// 兼容 nil / 非熔断器类型 / 空规则等异常输入
func extractCircuitBreakerRules(resp *model.ServiceRuleResponse) []*fault_tolerance.CircuitBreakerRule {
	if resp == nil || resp.Value == nil {
		return nil
	}
	cb, ok := resp.Value.(*fault_tolerance.CircuitBreaker)
	if !ok || cb == nil {
		return nil
	}
	return cb.GetRules()
}

// matchService Lookup 内部专用的服务匹配；规则侧填空字符串视为通配
// 与 pkg/algorithm/match.MatchService 行为保持一致，但避免引入跨包正则函数差异。
func matchService(actual *model.ServiceKey, ruleNamespace, ruleService string) bool {
	if actual == nil {
		return false
	}
	return matchServiceField(actual.Namespace, ruleNamespace) &&
		matchServiceField(actual.Service, ruleService)
}

func matchServiceField(actual, rule string) bool {
	if rule == "" || rule == "*" {
		return true
	}
	return actual == rule
}
