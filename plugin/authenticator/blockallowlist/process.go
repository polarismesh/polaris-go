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

package blockallowlist

import (
	"fmt"

	regexp "github.com/dlclark/regexp2"
	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	apisecurity "github.com/polarismesh/specification/source/go/api/v1/security"

	"github.com/polarismesh/polaris-go/pkg/algorithm/match"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/plugin/authenticator"
)

const (
	// matchAll 匹配所有的特殊值
	matchAll = "*"
	// rejectInfo 鉴权拒绝时返回的描述
	rejectInfo = "blocked by block-allow-list rule"
	// logPrefix 统一日志前缀，便于 grep
	logPrefix = "[BlockAllow]"
)

// Authenticate 黑白名单鉴权入口。规则获取失败或无规则时按通过处理。
//
// 日志策略：
//   - Info：仅在「拒绝」分支打 1 条审计日志（不打 Allow，避免日志爆量）；
//   - Debug：详细的规则评估过程（getRules → checkAllow → matchMethod / matchArguments），
//     生产环境默认关闭，调试时把 polaris.yaml 的 auth log level 切到 debug 即可看到。
func (p *BlockAllowListAuthenticator) Authenticate(info *authenticator.AuthInfo) *authenticator.AuthResult {
	if info == nil {
		return &authenticator.AuthResult{Code: authenticator.AuthResultOk}
	}
	rules := p.getBlockAllowListRules(info)
	if len(rules) == 0 {
		// 无规则按通过处理（与 polaris-java 行为一致）。Debug 级输出便于排查"明明配了规则却没生效"的场景
		if p.log != nil && p.log.IsLevelEnabled(log.DebugLog) {
			p.log.Debugf("%s no rules for namespace=%s service=%s, allow by default",
				logPrefix, info.Namespace, info.Service)
		}
		return &authenticator.AuthResult{Code: authenticator.AuthResultOk}
	}
	allowed, reason := p.checkAllow(info, rules)
	if !allowed {
		// 拒绝是审计场景：每次都打 Info，包含足够的上下文以便定位"为什么这个调用被拒"。
		// reason.summary 携带具体命中的规则名/cfg 下标或"白名单全不命中"等明确原因，
		// 让运维仅凭单行 Info 日志就能定位问题，无需切到 Debug 级别重启。
		if p.log != nil {
			p.log.Infof("%s deny: namespace=%s service=%s method=%s path=%s protocol=%s "+
				"caller=%s/%s reason=%s: %s",
				logPrefix, info.Namespace, info.Service, info.Method, info.Path, info.Protocol,
				sourceNamespace(info), sourceService(info), rejectInfo, reason.summary)
		}
		return &authenticator.AuthResult{
			Code: authenticator.AuthResultForbidden,
			Info: rejectInfo,
		}
	}
	if p.log != nil && p.log.IsLevelEnabled(log.DebugLog) {
		p.log.Debugf("%s allow: namespace=%s service=%s method=%s path=%s caller=%s/%s",
			logPrefix, info.Namespace, info.Service, info.Method, info.Path,
			sourceNamespace(info), sourceService(info))
	}
	return &authenticator.AuthResult{Code: authenticator.AuthResultOk}
}

// sourceNamespace / sourceService 安全提取 SourceService 字段，nil 时返回 "<unknown>"。
// 用于审计日志格式化，避免在打印路径里散落 nil 判断。
func sourceNamespace(info *authenticator.AuthInfo) string {
	if info == nil || info.SourceService == nil {
		return "<unknown>"
	}
	return info.SourceService.GetNamespace()
}

func sourceService(info *authenticator.AuthInfo) string {
	if info == nil || info.SourceService == nil {
		return "<unknown>"
	}
	return info.SourceService.GetService()
}

// getBlockAllowListRules 从 LocalRegistry 拉取目标服务的黑白名单规则
func (p *BlockAllowListAuthenticator) getBlockAllowListRules(info *authenticator.AuthInfo) []*apisecurity.BlockAllowListRule {
	if p.engine == nil {
		// engine 还没注入时直接返回空，按通过处理
		if p.log != nil {
			p.log.Warnf("%s engine not injected, skip rule fetch namespace=%s service=%s",
				logPrefix, info.Namespace, info.Service)
		}
		return nil
	}
	req := &model.GetServiceRuleRequest{
		Namespace: info.Namespace,
		Service:   info.Service,
	}
	resp, err := p.engine.SyncGetServiceRule(model.EventBlockAllowRule, req)
	if err != nil {
		// 拉规则失败是异常但不致命（按通过处理），生产环境保留 Warn 级让运维能感知到
		if p.log != nil {
			p.log.Warnf("%s get rule fail, namespace=%s service=%s err=%v",
				logPrefix, info.Namespace, info.Service, err)
		}
		return nil
	}
	if resp == nil || resp.GetValue() == nil {
		if p.log != nil && p.log.IsLevelEnabled(log.DebugLog) {
			p.log.Debugf("%s rule resp empty, namespace=%s service=%s",
				logPrefix, info.Namespace, info.Service)
		}
		return nil
	}
	wrapper, ok := resp.GetValue().(*pb.BlockAllowRuleWrapper)
	if !ok {
		// 类型不匹配通常是 SDK / 服务端版本不一致，Warn 级提醒
		if p.log != nil {
			p.log.Warnf("%s rule wrapper type mismatch, namespace=%s service=%s actual_type=%T",
				logPrefix, info.Namespace, info.Service, resp.GetValue())
		}
		return nil
	}
	if p.log != nil && p.log.IsLevelEnabled(log.DebugLog) {
		p.log.Debugf("%s loaded rules from LocalRegistry: namespace=%s service=%s rules_count=%d revision=%s",
			logPrefix, info.Namespace, info.Service, len(wrapper.Rules), wrapper.Revision.GetValue())
	}
	return wrapper.Rules
}

// denyReason 描述本次鉴权拒绝的具体原因。仅在 checkAllow 返回 allowed=false 时填充；
// allowed=true 时调用方不应读取该结构。
//
// 之所以把决策原因从 checkAllow 透传出来：原本只有 Debug 级日志能还原命中规则，生产环境
// 默认 Info 级，运维想定位"为什么这个调用被拒"必须切日志级别，成本高。这里把已经计算过
// 的命中信息显式回传，让 Authenticate 拒绝分支的 Info 日志一行就够。
type denyReason struct {
	// ruleIndex 命中规则在 rules 切片中的下标；"含白名单但都未命中" 这种兜底拒绝时为 -1
	ruleIndex int
	// ruleName 命中规则的 Name（来自服务端配置）；兜底拒绝时为空
	ruleName string
	// cfgIndex 命中 cfg 在规则的 BlockAllowConfig 切片中的下标；兜底拒绝时为 -1
	cfgIndex int
	// policy 命中 cfg 的策略类型；兜底拒绝时为零值（ALLOW_LIST）
	policy apisecurity.BlockAllowConfig_BlockAllowPolicy
	// summary 给人读的一句话原因，直接拼到日志的 reason= 后面
	summary string
}

// checkAllow 检查鉴权是否允许，并返回拒绝时的具体原因。
//  1. 全是白名单 → 任一匹配则通过；
//  2. 全是黑名单 → 任一匹配则拒绝；
//  3. 混合策略 → 任一白名单匹配则通过；都不匹配但存在白名单时拒绝。
//
// Debug 日志按层级输出：每条规则的 cfg 数量与 path → 每个 cfg 的 matchMethod 结果 → 命中
// matchMethod 后再交给 matchArguments 打详细决策。这能完整还原"哪条规则的哪个 cfg 在什么地方
// 命中或不命中"，是排查规则配错与残留版本的主要手段。
func (p *BlockAllowListAuthenticator) checkAllow(
	info *authenticator.AuthInfo, rules []*apisecurity.BlockAllowListRule) (bool, denyReason) {
	debug := p.log != nil && p.log.IsLevelEnabled(log.DebugLog)
	if debug {
		p.log.Debugf("%s checkAllow start: path=%s method=%s protocol=%s total_rules=%d",
			logPrefix, info.Path, info.Method, info.Protocol, len(rules))
	}
	containsAllowList := false
	for ri, rule := range rules {
		if rule == nil || !rule.GetEnable() {
			if debug {
				p.log.Debugf("%s   rules[%d] skipped: nil_or_disabled enable=%v",
					logPrefix, ri, rule != nil && rule.GetEnable())
			}
			continue
		}
		for ci, cfg := range rule.GetBlockAllowConfig() {
			if cfg == nil {
				continue
			}
			policy := cfg.GetBlockAllowPolicy()
			if policy == apisecurity.BlockAllowConfig_ALLOW_LIST {
				containsAllowList = true
			}
			methodOK := matchMethod(info, cfg.GetApi())
			if debug {
				apiPath := ""
				if cfg.GetApi() != nil && cfg.GetApi().GetPath() != nil {
					apiPath = cfg.GetApi().GetPath().GetValue().GetValue()
				}
				p.log.Debugf("%s   rules[%d=%s].cfg[%d] policy=%v api_path=%s method_match=%v args_len=%d",
					logPrefix, ri, rule.GetName(), ci, policy, apiPath, methodOK, len(cfg.GetArguments()))
			}
			if !methodOK {
				continue
			}
			if p.matchArguments(info, cfg.GetArguments()) {
				hit := policy == apisecurity.BlockAllowConfig_ALLOW_LIST
				reason := denyReason{
					ruleIndex: ri,
					ruleName:  rule.GetName(),
					cfgIndex:  ci,
					policy:    policy,
					summary: fmt.Sprintf("hit %s rule[%d]='%s' cfg[%d]",
						policy, ri, rule.GetName(), ci),
				}
				if debug {
					p.log.Debugf("%s   rules[%d=%s].cfg[%d] HIT → final_allow=%v reason=%s",
						logPrefix, ri, rule.GetName(), ci, hit, reason.summary)
				}
				return hit, reason
			}
		}
	}
	final := !containsAllowList
	reason := denyReason{ruleIndex: -1, cfgIndex: -1}
	if !final {
		reason.summary = "no allow-list rule matched (containsAllowList=true)"
	}
	if debug {
		p.log.Debugf("%s checkAllow end: no rule hit, containsAllowList=%v final_allow=%v",
			logPrefix, containsAllowList, final)
	}
	return final, reason
}

// matchMethod 检查 protocol/method/path 是否与规则的 API 配置匹配。
// 缺省的协议、方法字段为 "" 或 "*" 时视为匹配所有。
func matchMethod(info *authenticator.AuthInfo, api *apimodel.API) bool {
	if api == nil {
		return true
	}
	if !matchPlain(info.Protocol, api.GetProtocol()) {
		return false
	}
	if !matchPlain(info.Method, api.GetMethod()) {
		return false
	}
	pathMatch := api.GetPath()
	if pathMatch != nil {
		if !match.MatchString(info.Path, pathMatch, regexToPattern) {
			return false
		}
	}
	return true
}

// matchPlain 普通字符串匹配：规则值为空或 "*" 视为匹配所有
func matchPlain(actual, expected string) bool {
	if expected == "" || expected == matchAll {
		return true
	}
	return actual == expected
}

// matchArguments 检查所有 MatchArgument 是否都满足。任一不满足返回 false；全部匹配返回 true。
//
// AND 关系：args 数组中的多个 MatchArgument 之间是逻辑与，**任一不满足整体即不命中**。
// Debug 日志逐条打出 type/key/expect_value/match_type/actual_label/matched，足够还原决策。
func (p *BlockAllowListAuthenticator) matchArguments(
	info *authenticator.AuthInfo, args []*apisecurity.BlockAllowConfig_MatchArgument) bool {
	if len(args) == 0 {
		return true
	}
	debug := p.log != nil && p.log.IsLevelEnabled(log.DebugLog)
	for i, arg := range args {
		if arg == nil {
			continue
		}
		labelValue := p.getLabelValue(info, arg)
		matched := match.MatchString(labelValue, arg.GetValue(), regexToPattern)
		if debug {
			p.log.Debugf("%s     args[%d] type=%v key=%s expect=%q match_type=%v actual=%q matched=%v",
				logPrefix, i, arg.GetType(), arg.GetKey(), arg.GetValue().GetValue().GetValue(),
				arg.GetValue().GetType(), labelValue, matched)
		}
		if !matched {
			return false
		}
	}
	return true
}

// getLabelValue 从 AuthInfo / Engine 本端实例缓存中按 MatchArgument 类型取值。
// 取值规则：
//   - HEADER/QUERY/CALLER_IP 来自请求消息（info.Arguments）；
//   - CALLER_SERVICE 来自主调服务名（按命名空间过滤）；
//   - CALLER_METADATA 来自主调服务 Metadata；
//   - CALLEE_METADATA 来自本端通过 ProviderAPI.Register 登记的实例 Metadata；
//     由 Engine 在注册成功时缓存，反注册时清理；
//   - CUSTOM/default 优先取 Arguments(Custom)，再退回主调服务 Metadata。
func (p *BlockAllowListAuthenticator) getLabelValue(
	info *authenticator.AuthInfo, arg *apisecurity.BlockAllowConfig_MatchArgument) string {
	switch arg.GetType() {
	case apisecurity.BlockAllowConfig_MatchArgument_HEADER:
		return findArgumentValue(info.Arguments, model.ArgumentTypeHeader, arg.GetKey())
	case apisecurity.BlockAllowConfig_MatchArgument_QUERY:
		return findArgumentValue(info.Arguments, model.ArgumentTypeQuery, arg.GetKey())
	case apisecurity.BlockAllowConfig_MatchArgument_CALLER_SERVICE:
		if info.SourceService == nil {
			return ""
		}
		// arg.Key 为主调命名空间，"*" 表示匹配所有命名空间
		if arg.GetKey() == matchAll || arg.GetKey() == info.SourceService.GetNamespace() {
			return info.SourceService.GetService()
		}
		return ""
	case apisecurity.BlockAllowConfig_MatchArgument_CALLER_IP:
		return findArgumentValue(info.Arguments, model.ArgumentTypeCallerIP, "")
	case apisecurity.BlockAllowConfig_MatchArgument_CALLER_METADATA:
		if info.SourceService == nil {
			return ""
		}
		return info.SourceService.GetMetadata()[arg.GetKey()]
	case apisecurity.BlockAllowConfig_MatchArgument_CALLEE_METADATA:
		// 从本端注册的实例 metadata 中取值。同 (ns, svc) 下可能有多个实例，
		// 任一实例命中即返回；engine 未注入或无登记记录时返回空字符串。
		if p.engine == nil {
			if p.log != nil && p.log.IsLevelEnabled(log.DebugLog) {
				p.log.Debugf("%s CALLEE_METADATA miss: engine not injected namespace=%s service=%s key=%s",
					logPrefix, info.Namespace, info.Service, arg.GetKey())
			}
			return ""
		}
		locals := p.engine.GetLocalInstanceMetadata(info.Namespace, info.Service)
		for _, md := range locals {
			if v, ok := md[arg.GetKey()]; ok && v != "" {
				if p.log != nil && p.log.IsLevelEnabled(log.DebugLog) {
					p.log.Debugf("%s CALLEE_METADATA hit: namespace=%s service=%s key=%s value=%s",
						logPrefix, info.Namespace, info.Service, arg.GetKey(), v)
				}
				return v
			}
		}
		if p.log != nil && p.log.IsLevelEnabled(log.DebugLog) {
			p.log.Debugf("%s CALLEE_METADATA miss: namespace=%s service=%s key=%s local_instances=%d",
				logPrefix, info.Namespace, info.Service, arg.GetKey(), len(locals))
		}
		return ""
	case apisecurity.BlockAllowConfig_MatchArgument_CUSTOM:
		fallthrough
	default:
		// 优先取自定义 Argument，再退回到主调服务 Metadata
		if v := findArgumentValue(info.Arguments, model.ArgumentTypeCustom, arg.GetKey()); v != "" {
			return v
		}
		if info.SourceService != nil {
			return info.SourceService.GetMetadata()[arg.GetKey()]
		}
		return ""
	}
}

// findArgumentValue 从 Arguments 中查找指定类型/key 的取值，未找到返回空字符串。
// Argument 是值类型 struct，零值表示空 Argument（argumentType=0）；
// 这里用 ArgumentType() 自然过滤掉零值（除非 argType 也是 0，但 argType 0 是 Custom，
// 业务上不会去查零值 Custom）。
func findArgumentValue(args []model.Argument, argType int, key string) string {
	for _, a := range args {
		if a.ArgumentType() != argType {
			continue
		}
		if argType == model.ArgumentTypeMethod ||
			argType == model.ArgumentTypeCallerIP ||
			argType == model.ArgumentTypePath {
			return a.Value()
		}
		if a.Key() == key {
			return a.Value()
		}
	}
	return ""
}

// regexToPattern 简单正则编译器（不带缓存，与黑白名单调用频次相对低，可接受性能开销）
func regexToPattern(expr string) *regexp.Regexp {
	r, err := regexp.Compile(expr, 0)
	if err != nil {
		return nil
	}
	return r
}
