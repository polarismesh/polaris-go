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
	regexp "github.com/dlclark/regexp2"
	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	apisecurity "github.com/polarismesh/specification/source/go/api/v1/security"

	"github.com/polarismesh/polaris-go/pkg/algorithm/match"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/plugin/authenticator"
)

const (
	// matchAll 匹配所有的特殊值
	matchAll = "*"
	// rejectInfo 鉴权拒绝时返回的描述
	rejectInfo = "blocked by block-allow-list rule"
)

// Authenticate 黑白名单鉴权入口。规则获取失败或无规则时按通过处理
func (p *BlockAllowListAuthenticator) Authenticate(info *authenticator.AuthInfo) *authenticator.AuthResult {
	if info == nil {
		return &authenticator.AuthResult{Code: authenticator.AuthResultOk}
	}
	rules := p.getBlockAllowListRules(info)
	if len(rules) > 0 && !p.checkAllow(info, rules) {
		return &authenticator.AuthResult{
			Code: authenticator.AuthResultForbidden,
			Info: rejectInfo,
		}
	}
	return &authenticator.AuthResult{Code: authenticator.AuthResultOk}
}

// getBlockAllowListRules 从 LocalRegistry 拉取目标服务的黑白名单规则
func (p *BlockAllowListAuthenticator) getBlockAllowListRules(info *authenticator.AuthInfo) []*apisecurity.BlockAllowListRule {
	if p.engine == nil {
		// engine 还没注入时直接返回空，按通过处理
		return nil
	}
	req := &model.GetServiceRuleRequest{
		Namespace: info.Namespace,
		Service:   info.Service,
	}
	resp, err := p.engine.SyncGetServiceRule(model.EventBlockAllowRule, req)
	if err != nil {
		if p.log != nil {
			p.log.Debugf("[BlockAllowListAuthenticator] get rule fail, namespace=%s service=%s err=%v",
				info.Namespace, info.Service, err)
		}
		return nil
	}
	if resp == nil || resp.GetValue() == nil {
		return nil
	}
	wrapper, ok := resp.GetValue().(*pb.BlockAllowRuleWrapper)
	if !ok {
		return nil
	}
	return wrapper.Rules
}

// checkAllow 检查鉴权是否允许
//  1. 全是白名单 → 任一匹配则通过；
//  2. 全是黑名单 → 任一匹配则拒绝；
//  3. 混合策略 → 任一白名单匹配则通过；都不匹配但存在白名单时拒绝。
func (p *BlockAllowListAuthenticator) checkAllow(
	info *authenticator.AuthInfo, rules []*apisecurity.BlockAllowListRule) bool {
	containsAllowList := false
	for _, rule := range rules {
		if rule == nil || !rule.GetEnable() {
			continue
		}
		for _, cfg := range rule.GetBlockAllowConfig() {
			if cfg == nil {
				continue
			}
			if cfg.GetBlockAllowPolicy() == apisecurity.BlockAllowConfig_ALLOW_LIST {
				containsAllowList = true
			}
			if !matchMethod(info, cfg.GetApi()) {
				continue
			}
			if matchArguments(info, cfg.GetArguments()) {
				return cfg.GetBlockAllowPolicy() == apisecurity.BlockAllowConfig_ALLOW_LIST
			}
		}
	}
	return !containsAllowList
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
func matchArguments(info *authenticator.AuthInfo, args []*apisecurity.BlockAllowConfig_MatchArgument) bool {
	if len(args) == 0 {
		return true
	}
	for _, arg := range args {
		if arg == nil {
			continue
		}
		labelValue := getLabelValue(info, arg)
		if !match.MatchString(labelValue, arg.GetValue(), regexToPattern) {
			return false
		}
	}
	return true
}

// getLabelValue 从 AuthInfo 中按 MatchArgument 类型取值。
// 取值规则：HEADER/QUERY/CALLER_IP 来自请求消息（Arguments），
// CALLER_SERVICE 来自主调服务名，CALLER_METADATA 来自主调服务 Metadata，CUSTOM 优先取 Arguments，再退回 Metadata。
func getLabelValue(info *authenticator.AuthInfo, arg *apisecurity.BlockAllowConfig_MatchArgument) string {
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
		// 当前 polaris-go 鉴权流程不携带被调服务实例的 Metadata，统一返回空字符串
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

// findArgumentValue 从 Arguments 中查找指定类型/key 的取值，未找到返回空字符串
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
