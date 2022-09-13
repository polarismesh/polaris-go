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
	"github.com/polarismesh/polaris-go/pkg/model"
	"sort"
	"strings"
	"time"

	"github.com/golang/protobuf/jsonpb"
	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/ptypes/wrappers"

	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

// RateLimitingAssistant 限流解析助手
type RateLimitingAssistant struct {
}

const (
	ruleServiceLevel = 1

	ruleMethodLevel = 2

	ruleArgumentLevel = 3

	MatchAll = "*"
)

func IsMatchAllValue(ruleMetaValue *namingpb.MatchString) bool {
	return isMatchAllValueString(ruleMetaValue.GetValue().GetValue())
}

func isMatchAllValueString(value string) bool {
	return len(value) == 0 || value == MatchAll
}

// ParseRuleValue 解析出具体的规则值
func (r *RateLimitingAssistant) ParseRuleValue(resp *namingpb.DiscoverResponse) (proto.Message, string) {
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

func getRuleLevel(rule *namingpb.Rule) int {
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

func unifiedRules(inRules []*namingpb.Rule) []*namingpb.Rule {
	var outRules = make([]*namingpb.Rule, 0, len(inRules))
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
		var matchArguments = make([]*namingpb.MatchArgument, 0)
		for labelKey, labelValue := range inRule.Labels {
			if labelKey == model.LabelKeyMethod {
				matchArguments = append(matchArguments, &namingpb.MatchArgument{
					Type:  namingpb.MatchArgument_METHOD,
					Value: labelValue,
				})
			} else if labelKey == model.LabelKeyCallerIp {
				matchArguments = append(matchArguments, &namingpb.MatchArgument{
					Type:  namingpb.MatchArgument_CALLER_IP,
					Value: labelValue,
				})
			} else if strings.HasPrefix(labelKey, model.LabelKeyHeader) {
				matchArguments = append(matchArguments, &namingpb.MatchArgument{
					Type:  namingpb.MatchArgument_HEADER,
					Key:   labelKey[len(model.LabelKeyHeader):],
					Value: labelValue,
				})
			} else if strings.HasPrefix(labelKey, model.LabelKeyQuery) {
				matchArguments = append(matchArguments, &namingpb.MatchArgument{
					Type:  namingpb.MatchArgument_QUERY,
					Key:   labelKey[len(model.LabelKeyQuery):],
					Value: labelValue,
				})
			} else if strings.HasPrefix(labelKey, model.LabelKeyCallerService) {
				matchArguments = append(matchArguments, &namingpb.MatchArgument{
					Type:  namingpb.MatchArgument_CALLER_SERVICE,
					Key:   labelKey[len(model.LabelKeyCallerService):],
					Value: labelValue,
				})
			} else {
				matchArguments = append(matchArguments, &namingpb.MatchArgument{
					Type:  namingpb.MatchArgument_CUSTOM,
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
type rateLimitRules []*namingpb.Rule

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
	rateLimiting := message.(*namingpb.RateLimit)
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
			rule.Report = &namingpb.Report{}
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
	rateLimiting := message.(*namingpb.RateLimit)
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
			return fmt.Errorf("behavior plugin %s not registered", behaviorName)
		}
		ruleCache.SetMessageCache(rule, &RateLimitRuleCache{
			MaxDuration: maxDuration})
	}
	return nil
}

const minAmountDuration = 1 * time.Second

// validateAmount 校验配额总量
func validateAmount(amounts []*namingpb.Amount) error {
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
func GetMaxValidDuration(rule *namingpb.Rule) (time.Duration, error) {
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
