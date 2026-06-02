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

package pb

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/golang/protobuf/jsonpb"
	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/ptypes/wrappers"
	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

// RateLimitingAssistant 限流解析助手
type RateLimitingAssistant struct {
}

// BehaviorNotRegisteredError 表示限流规则中引用的限流算法插件在当前 SDK 二进制中未注册。
//
// 触发场景：服务端下发的限流规则中 `action` 字段值（即 RateLimiter 插件名）在客户端
// 没有对应实现，例如某些发行版才包含的专有插件。规则解析时无法构造对应的限流算法。
//
// 该错误是限流规则校验中**可以容忍的一类错误**:相对于其他校验失败（amount 非法、
// validDuration 解析失败、reportAmount 越界等），它只表示客户端缺少处理这条规则的能力，
// 不代表规则数据本身损坏，因此调用方可以选择以 WARN 级别记录、并保留对应缓存项。
//
// **影响范围（重要）**：`RateLimitingAssistant.Validate` 在遍历规则数组时遇到本错误
// 会立即短路返回，因此 `*apitraffic.RateLimit` 中**整个规则集**都会因 `validateError`
// 非空而被 `pkg/flow/quota/assist.go::lookupRules` 整体跳过，包括同一规则集中其它
// 合法的规则。也就是说：如果服务有 10 条限流规则、其中 1 条 action 引用未注册插件，
// 另外 9 条合法规则也不会生效。这是与日志降级一并需要让上层调用方知晓的语义。
// 后续可以考虑改为"标记并跳过单条非法规则、保留其它合法规则"，但属于独立的语义改造，
// 不在当前修复范围内。
type BehaviorNotRegisteredError struct {
	// Behavior 未注册的限流算法插件名（即规则中的 `action` 字段值）
	Behavior string
}

// Error 返回错误描述。保持与历史版本完全一致的文案，便于既有监控/告警的关键字匹配继续生效。
func (e *BehaviorNotRegisteredError) Error() string {
	return fmt.Sprintf("behavior plugin %s not registered", e.Behavior)
}

const (
	ruleServiceLevel = 1

	ruleMethodLevel = 2

	ruleArgumentLevel = 3

	MatchAll = "*"
)

func IsMatchAllValue(ruleMetaValue *apimodel.MatchString) bool {
	return isMatchAllValueString(ruleMetaValue.GetValue().GetValue())
}

func isMatchAllValueString(value string) bool {
	return len(value) == 0 || value == MatchAll
}

// ParseRuleValue 解析出具体的规则值
func (r *RateLimitingAssistant) ParseRuleValue(resp *apiservice.DiscoverResponse, baseLogger log.Logger) (proto.Message, string) {
	var revision string
	rateLimitValue := resp.RateLimit
	if nil == rateLimitValue {
		return rateLimitValue, revision
	}
	revision = rateLimitValue.GetRevision().GetValue()
	outRules := unifiedRules(rateLimitValue.Rules)
	sort.SliceStable(outRules, func(i, j int) bool {
		rule1 := outRules[i]
		rule2 := outRules[j]
		type1 := rule1.Type
		type2 := rule2.Type
		if type1 != type2 {
			return !(type1 < type2)
		}
		return !(getRuleLevel(rule1) < getRuleLevel(rule2))
	})
	rateLimitValue.Rules = outRules
	return rateLimitValue, revision
}

func getRuleLevel(rule *apitraffic.Rule) int {
	arguments := rule.Arguments
	if len(arguments) > 0 {
		return ruleArgumentLevel + len(arguments)
	}
	method := rule.Method
	if nil != method || !IsMatchAllValue(method) {
		return ruleMethodLevel
	}
	return ruleServiceLevel
}

func unifiedRules(inRules []*apitraffic.Rule) []*apitraffic.Rule {
	var outRules = make([]*apitraffic.Rule, 0, len(inRules))
	if len(inRules) == 0 {
		return outRules
	}
	for _, inRule := range inRules {
		if len(inRule.Labels) == 0 {
			// not labels, nothing to convert
			outRules = append(outRules, inRule)
			continue
		}
		if len(inRule.Arguments) > 0 {
			// new server version, already transfer to arguments
			outRules = append(outRules, inRule)
			continue
		}
		// transfer the labels to arguments
		var matchArguments = make([]*apitraffic.MatchArgument, 0)
		for labelKey, labelValue := range inRule.Labels {
			if labelKey == model.LabelKeyMethod {
				matchArguments = append(matchArguments, &apitraffic.MatchArgument{
					Type:  apitraffic.MatchArgument_METHOD,
					Value: labelValue,
				})
			} else if labelKey == model.LabelKeyCallerIP {
				matchArguments = append(matchArguments, &apitraffic.MatchArgument{
					Type:  apitraffic.MatchArgument_CALLER_IP,
					Value: labelValue,
				})
			} else if strings.HasPrefix(labelKey, model.LabelKeyHeader) {
				matchArguments = append(matchArguments, &apitraffic.MatchArgument{
					Type:  apitraffic.MatchArgument_HEADER,
					Key:   labelKey[len(model.LabelKeyHeader):],
					Value: labelValue,
				})
			} else if strings.HasPrefix(labelKey, model.LabelKeyQuery) {
				matchArguments = append(matchArguments, &apitraffic.MatchArgument{
					Type:  apitraffic.MatchArgument_QUERY,
					Key:   labelKey[len(model.LabelKeyQuery):],
					Value: labelValue,
				})
			} else if strings.HasPrefix(labelKey, model.LabelKeyCallerService) {
				matchArguments = append(matchArguments, &apitraffic.MatchArgument{
					Type:  apitraffic.MatchArgument_CALLER_SERVICE,
					Key:   labelKey[len(model.LabelKeyCallerService):],
					Value: labelValue,
				})
			} else {
				matchArguments = append(matchArguments, &apitraffic.MatchArgument{
					Type:  apitraffic.MatchArgument_CUSTOM,
					Key:   labelKey,
					Value: labelValue,
				})
			}
		}
		inRule.Arguments = matchArguments
		outRules = append(outRules, inRule)
	}
	return outRules
}

// RateLimitRuleCache 规则PB缓存
type RateLimitRuleCache struct {
	MaxDuration time.Duration
}

// 限流规则集合
type rateLimitRules []*apitraffic.Rule

// Len 数组长度
func (rls rateLimitRules) Len() int {
	return len(rls)
}

// Less 比较数组成员大小
func (rls rateLimitRules) Less(i, j int) bool {
	// 先按照优先级来比较，数值小的优先级高
	if rls[i].GetPriority().GetValue() < rls[j].GetPriority().GetValue() {
		return true
	}
	if rls[i].GetPriority().GetValue() > rls[j].GetPriority().GetValue() {
		return false
	}
	// 按字母序升序排列
	return strings.Compare(rls[i].GetId().GetValue(), rls[j].GetId().GetValue()) < 0
}

// Swap 交换数组成员
func (rls rateLimitRules) Swap(i, j int) {
	rls[i], rls[j] = rls[j], rls[i]
}

const (
	// DefaultRejectRateLimiter 默认的reject限流器.
	DefaultRejectRateLimiter = "reject"
	// DefaultRateLimitReportAmountPresent 默认满足百分之80的请求后立刻限流上报.
	DefaultRateLimitReportAmountPresent = 80
	// MaxRateLimitReportAmountPresent 最大实时上报百分比.
	MaxRateLimitReportAmountPresent = 100
	// MinRateLimitReportAmountPresent 最小实时上报百分比.
	MinRateLimitReportAmountPresent = 0
)

// SetDefault 设置默认值
func (r *RateLimitingAssistant) SetDefault(message proto.Message) {
	rateLimiting := message.(*apitraffic.RateLimit)
	if len(rateLimiting.GetRules()) == 0 {
		return
	}
	var rules rateLimitRules = rateLimiting.GetRules()
	sort.Sort(rules)
	for _, rule := range rateLimiting.GetRules() {
		behaviorName := rule.GetAction().GetValue()
		if len(behaviorName) == 0 {
			rule.Action = &wrappers.StringValue{Value: DefaultRejectRateLimiter}
		} else {
			rule.Action.Value = strings.ToLower(behaviorName)
		}
		if nil == rule.GetReport() {
			rule.Report = &apitraffic.Report{}
		}
		if nil == rule.GetReport().GetAmountPercent() {
			rule.GetReport().AmountPercent = &wrappers.UInt32Value{
				Value: DefaultRateLimitReportAmountPresent,
			}
		}
	}
}

// Validate 规则校验
func (r *RateLimitingAssistant) Validate(message proto.Message, ruleCache model.RuleCache) error {
	rateLimiting := message.(*apitraffic.RateLimit)
	if len(rateLimiting.GetRules()) == 0 {
		return nil
	}
	for _, rule := range rateLimiting.GetRules() {
		if err := validateAmount(rule.GetAmounts()); err != nil {
			routeTxt, _ := (&jsonpb.Marshaler{}).MarshalToString(rule)
			return fmt.Errorf("fail to validate rate limit rule, error is %v, rule text is\n%s",
				err, routeTxt)
		}
		maxDuration, err := GetMaxValidDuration(rule)
		if err != nil {
			return fmt.Errorf("fail to parse validDuration in rate limit rule, error is %v", err)
		}
		amountPresent := rule.GetReport().GetAmountPercent().GetValue()
		if amountPresent < MinRateLimitReportAmountPresent ||
			amountPresent > MaxRateLimitReportAmountPresent {
			return fmt.Errorf(
				"fail to parse reportAmount in rate limit rule, value %d must in (0, 100]", amountPresent)
		}
		behaviorName := rule.GetAction().GetValue()
		if !plugin.IsPluginRegistered(common.TypeRateLimiter, behaviorName) {
			// 用 typed error 回传，便于调用方区分"客户端缺插件"这种可容忍的校验失败，
			// 从而以 WARN 而非 ERROR 记录日志（详见 BehaviorNotRegisteredError 的注释）。
			return &BehaviorNotRegisteredError{Behavior: behaviorName}
		}
		ruleCache.SetMessageCache(rule, &RateLimitRuleCache{
			MaxDuration: maxDuration})
	}
	return nil
}

const minAmountDuration = 1 * time.Second

// validateAmount 校验配额总量
func validateAmount(amounts []*apitraffic.Amount) error {
	if len(amounts) == 0 {
		return nil
	}
	for _, amount := range amounts {
		validDuration, err := ConvertDuration(amount.GetValidDuration())
		if err != nil {
			return err
		}
		if validDuration < minAmountDuration {
			return fmt.Errorf("amount.validDuration must be greater and equals to %v", minAmountDuration)
		}
	}
	return nil
}

// GetMaxValidDuration 获取最大校验周期
func GetMaxValidDuration(rule *apitraffic.Rule) (time.Duration, error) {
	var maxValidDura time.Duration
	amounts := rule.GetAmounts()
	if len(amounts) == 0 {
		return 0, nil
	}
	for _, amount := range amounts {
		validDura, err := ConvertDuration(amount.GetValidDuration())
		if err != nil {
			return validDura, err
		}
		if validDura == 0 {
			return validDura, fmt.Errorf("validDuration is empty for amount %v", amount.GetMaxAmount().GetValue())
		}
		if maxValidDura == 0 || maxValidDura < validDura {
			maxValidDura = validDura
		}
	}
	return maxValidDura, nil
}
