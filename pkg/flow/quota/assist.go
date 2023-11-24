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

package quota

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/modern-go/reflect2"
	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/flow/data"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

const (
	// Disabled is a constant for disabled quota.
	Disabled = "rateLimit disabled"
	// RuleNotExists is a constant for rules not exist.
	RuleNotExists = "quota rule not exists"
)

// FlowQuotaAssistant 限额流程的辅助类
type FlowQuotaAssistant struct {
	// 销毁标识
	destroyed uint32
	// 是否启用限流，如果不启用，默认都会放通
	enable bool
	// 流程执行引擎
	engine model.Engine
	// 插件工厂
	supplier plugin.Supplier
	// 并发锁，控制windowSet的加入
	mutex *sync.Mutex
	// 服务到windowSet的映射
	svcToWindowSet *sync.Map
	// 限流server连接器
	asyncRateLimitConnector AsyncRateLimitConnector
	// 任务列表
	taskValues model.TaskValues
	// 用来控制最大窗口数量的配置项
	windowCount        int32
	maxWindowSize      int32
	windowCountLogCtrl uint64
	// 超时淘汰周期
	purgeIntervalMilli int64

	remoteNamespace string
	remoteService   string
}

// AsyncRateLimitConnector 异步限流连接器
func (f *FlowQuotaAssistant) AsyncRateLimitConnector() AsyncRateLimitConnector {
	return f.asyncRateLimitConnector
}

// Destroy 销毁
func (f *FlowQuotaAssistant) Destroy() {
	atomic.StoreUint32(&f.destroyed, 1)
	f.asyncRateLimitConnector.Destroy()
}

// IsDestroyed 是否已销毁
func (f *FlowQuotaAssistant) IsDestroyed() bool {
	return atomic.LoadUint32(&f.destroyed) > 0
}

// AddWindowCount 添加窗口数量
func (f *FlowQuotaAssistant) AddWindowCount() {
	atomic.AddInt32(&f.windowCount, 1)
}

// DelWindowCount 减少窗口数量
func (f *FlowQuotaAssistant) DelWindowCount() {
	atomic.AddInt32(&f.windowCount, -1)
}

// GetWindowCount 获取窗口数量
func (f *FlowQuotaAssistant) GetWindowCount() int32 {
	return atomic.LoadInt32(&f.windowCount)
}

// TaskValues 获取调度任务
func (f *FlowQuotaAssistant) TaskValues() model.TaskValues {
	return f.taskValues
}

// Init 初始化限额辅助
func (f *FlowQuotaAssistant) Init(engine model.Engine, cfg config.Configuration, supplier plugin.Supplier) error {
	f.engine = engine
	f.supplier = supplier
	f.asyncRateLimitConnector = NewAsyncRateLimitConnector(engine.GetContext(), cfg)
	f.enable = cfg.GetProvider().GetRateLimit().IsEnable()
	if !f.enable {
		return nil
	}
	callback, err := NewRemoteQuotaCallback(cfg, supplier, engine, f.asyncRateLimitConnector)
	if err != nil {
		return err
	}
	period := config.MinRateLimitReportInterval
	_, taskValues := engine.ScheduleTask(&model.PeriodicTask{
		Name:       "quota-metric",
		CallBack:   callback,
		Period:     period,
		DelayStart: true,
	})
	f.taskValues = taskValues
	supplier.RegisterEventSubscriber(common.OnServiceUpdated,
		common.PluginEventHandler{Callback: f.OnServiceUpdated})
	supplier.RegisterEventSubscriber(common.OnServiceDeleted,
		common.PluginEventHandler{Callback: f.OnServiceDeleted})
	f.destroyed = 0
	f.windowCount = 0
	f.maxWindowSize = int32(cfg.GetProvider().GetRateLimit().GetMaxWindowSize())
	f.windowCountLogCtrl = 0
	f.purgeIntervalMilli = model.ToMilliSeconds(cfg.GetProvider().GetRateLimit().GetPurgeInterval())
	f.remoteNamespace = cfg.GetProvider().GetRateLimit().GetLimiterNamespace()
	f.remoteService = cfg.GetProvider().GetRateLimit().GetLimiterService()
	f.mutex = &sync.Mutex{}
	f.svcToWindowSet = &sync.Map{}
	return nil
}

// DeleteRateLimitWindowSet 删除窗口集合
func (f *FlowQuotaAssistant) DeleteRateLimitWindowSet(svcKey model.ServiceKey) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.svcToWindowSet.Delete(svcKey)
}

// CountRateLimitWindowSet 获取分配窗口集合数量,只用于测试
func (f *FlowQuotaAssistant) CountRateLimitWindowSet() int {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	var count int
	f.svcToWindowSet.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	return count
}

// GetRateLimitWindowSet 获取配额分配窗口集合
func (f *FlowQuotaAssistant) GetRateLimitWindowSet(svcKey model.ServiceKey, create bool) *RateLimitWindowSet {
	value, ok := f.svcToWindowSet.Load(svcKey)
	if ok {
		return value.(*RateLimitWindowSet)
	}
	if !create {
		return nil
	}
	f.mutex.Lock()
	defer f.mutex.Unlock()
	value, ok = f.svcToWindowSet.Load(svcKey)
	if ok {
		return value.(*RateLimitWindowSet)
	}
	windowSet := NewRateLimitWindowSet(f)
	f.svcToWindowSet.Store(svcKey, windowSet)
	return windowSet
}

// GetAllWindowSets 获取当前所有的限流窗口集合
func (f *FlowQuotaAssistant) GetAllWindowSets() map[model.ServiceKey]*RateLimitWindowSet {
	res := make(map[model.ServiceKey]*RateLimitWindowSet)
	f.svcToWindowSet.Range(func(k, v interface{}) bool {
		svcKey := k.(model.ServiceKey)
		windowSet := v.(*RateLimitWindowSet)
		res[svcKey] = windowSet
		return true
	})
	return res
}

// GetRateLimitWindow 获取配额分配窗口
func (f *FlowQuotaAssistant) GetRateLimitWindow(svcKey model.ServiceKey, rule *apitraffic.Rule,
	label string) (*RateLimitWindowSet, *RateLimitWindow) {
	windowSet := f.GetRateLimitWindowSet(svcKey, true)
	return windowSet, windowSet.GetRateLimitWindow(rule, label)
}

// OnServiceUpdated 服务更新回调，找到具体的限流窗口集合，然后触发更新
func (f *FlowQuotaAssistant) OnServiceUpdated(event *common.PluginEvent) error {
	svcEventObject := event.EventObject.(*common.ServiceEventObject)
	if svcEventObject.SvcEventKey.Type != model.EventRateLimiting {
		return nil
	}
	newValue := svcEventObject.NewValue.(model.RegistryValue)
	if !newValue.IsInitialized() {
		return nil
	}
	svcKey := svcEventObject.SvcEventKey.ServiceKey
	windowSet := f.GetRateLimitWindowSet(svcKey, false)
	if nil == windowSet {
		return nil
	}
	windowSet.OnServiceUpdated(svcEventObject)
	return nil
}

// OnServiceDeleted 服务删除回调
func (f *FlowQuotaAssistant) OnServiceDeleted(event *common.PluginEvent) error {
	svcEventObject := event.EventObject.(*common.ServiceEventObject)
	if svcEventObject.SvcEventKey.Type != model.EventRateLimiting {
		return nil
	}
	svcKey := svcEventObject.SvcEventKey.ServiceKey
	f.DeleteRateLimitWindowSet(svcKey)
	return nil
}

// GetQuota 获取配额
func (f *FlowQuotaAssistant) GetQuota(commonRequest *data.CommonRateLimitRequest) (*model.QuotaFutureImpl, error) {
	if !f.enable {
		// 没有限流规则，直接放通
		resp := &model.QuotaResponse{
			Code: model.QuotaResultOk,
			Info: Disabled,
		}
		return model.QuotaFutureWithResponse(resp), nil
	}
	windows, err := f.lookupRateLimitWindow(commonRequest)
	if err != nil {
		return nil, err
	}
	if len(windows) == 0 {
		// 没有限流规则，直接放通
		resp := &model.QuotaResponse{
			Code: model.QuotaResultOk,
			Info: RuleNotExists,
		}
		return model.QuotaFutureWithResponse(resp), nil
	}
	var maxWaitMs int64 = 0
	var releaseFuncs = make([]model.ReleaseFunc, 0, len(windows))
	for _, window := range windows {
		window.Init()
		quotaResult := window.AllocateQuota(commonRequest)
		if quotaResult == nil {
			continue
		}
		for i := range quotaResult.ReleaseFuncs {
			releaseFuncs = append(releaseFuncs, quotaResult.ReleaseFuncs[i])
		}
		// 触发限流，提前返回
		if quotaResult.Code == model.QuotaResultLimited {
			// 先释放资源
			for i := range releaseFuncs {
				releaseFuncs[i](0)
			}
			return model.QuotaFutureWithResponse(quotaResult), nil
		}
		// 未触发限流，记录令牌桶的最大排队时间
		if quotaResult.WaitMs > maxWaitMs {
			maxWaitMs = quotaResult.WaitMs
		}
	}
	return model.QuotaFutureWithResponse(&model.QuotaResponse{
		Code:         model.QuotaResultOk,
		WaitMs:       maxWaitMs,
		ReleaseFuncs: releaseFuncs,
	}), nil
}

// lookupRateLimitWindow 计算限流窗口
func (f *FlowQuotaAssistant) lookupRateLimitWindow(
	commonRequest *data.CommonRateLimitRequest) ([]*RateLimitWindow, error) {
	var err error
	// 1. 并发获取被调服务信息和限流配置，服务不存在，返回错误
	if err = f.engine.SyncGetResources(commonRequest); err != nil {
		sdkErr, ok := err.(model.SDKError)
		if !ok {
			return nil, err
		}
		if sdkErr.ErrorCode() != model.ErrCodeServiceNotFound {
			return nil, err
		}
	}
	// 2. 寻找匹配的规则
	rules := lookupRules(commonRequest.RateLimitRule, commonRequest.Method, commonRequest.Arguments)
	if len(rules) == 0 {
		return nil, nil
	}
	windows := make([]*RateLimitWindow, 0, len(rules))
	for _, rule := range rules {
		// 2.获取已有的QuotaWindow
		labelStr, regexSpread := FormatLabelToStr(commonRequest, rule)
		windowSet, window := f.GetRateLimitWindow(commonRequest.DstService, rule, labelStr)
		if nil != window {
			// 已经存在限流窗口，则直接分配
			windows = append(windows, window)
		} else {
			// 3.创建限流窗口
			window = windowSet.AddRateLimitWindow(commonRequest, rule, labelStr, regexSpread)
			windows = append(windows, window)
		}
	}
	return windows, nil
}

func matchStringValue(matchString *apimodel.MatchString, value string, ruleCache model.RuleCache) bool {
	if pb.IsMatchAllValue(matchString) {
		return true
	}
	matchType := matchString.GetType()
	matchValue := matchString.GetValue().GetValue()

	switch matchType {
	case apimodel.MatchString_EXACT:
		return value == matchValue
	case apimodel.MatchString_REGEX:
		regexObj, err := ruleCache.GetRegexMatcher(matchValue)
		if nil != err {
			log.GetBaseLogger().Errorf("regex compile error. ruleMetaValueStr: %s, value: %s, errors: %s",
				matchValue, value, err)
			return false
		}
		m, err := regexObj.FindStringMatch(value)
		if err != nil {
			log.GetBaseLogger().Errorf("regex match error. ruleMetaValueStr: %s, value: %s, errors: %s",
				matchValue, value, err)
			return false
		}
		if m == nil || m.String() == "" {
			return false
		}
		return true
	case apimodel.MatchString_NOT_EQUALS:
		return value != matchValue
	case apimodel.MatchString_IN:
		tokens := strings.Split(matchValue, ",")
		for _, token := range tokens {
			if token == value {
				return true
			}
		}
		return false
	case apimodel.MatchString_NOT_IN:
		tokens := strings.Split(matchValue, ",")
		for _, token := range tokens {
			if token == value {
				return false
			}
		}
		return true
	}
	return false
}

// lookupRule 寻址规则
func lookupRules(svcRule model.ServiceRule, method string, arguments map[int]map[string]string) []*apitraffic.Rule {
	if reflect2.IsNil(svcRule) || reflect2.IsNil(svcRule.GetValue()) {
		// 规则集为空
		return nil
	}
	validateErr := svcRule.GetValidateError()
	if nil != validateErr {
		return nil
	}
	ruleCache := svcRule.GetRuleCache()
	rateLimiting := svcRule.GetValue().(*apitraffic.RateLimit)
	rulesList := rateLimiting.Rules
	if len(rulesList) == 0 {
		return nil
	}
	matchRules := make([]*apitraffic.Rule, 0)
	for _, rule := range rulesList {
		if nil != rule.GetDisable() && rule.GetDisable().GetValue() {
			// 规则被停用
			continue
		}
		if len(rule.Amounts) == 0 {
			continue
		}
		methodMatcher := rule.Method
		if nil != methodMatcher {
			matchMethod := matchStringValue(methodMatcher, method, ruleCache)
			if !matchMethod {
				continue
			}
		}
		argumentMatchers := rule.Arguments
		matched := true
		if len(argumentMatchers) > 0 {
			for _, argumentMatcher := range argumentMatchers {
				stringStringMap := arguments[int(argumentMatcher.Type)]
				if len(stringStringMap) == 0 {
					matched = false
					break
				}
				labelValue, ok := getLabelValue(argumentMatcher, stringStringMap)
				if !ok {
					matched = false
				} else {
					matched = matchStringValue(argumentMatcher.GetValue(), labelValue, ruleCache)
				}
				if !matched {
					break
				}
			}
		}
		if matched {
			matchRules = append(matchRules, rule)
		}
	}
	return matchRules
}

// FormatLabelToStr 格式化字符串
func FormatLabelToStr(request *data.CommonRateLimitRequest, rule *apitraffic.Rule) (string, bool) {
	methodMatcher := rule.GetMethod()
	regexCombine := rule.GetRegexCombine().GetValue()
	methodValue := ""
	var regexSpread bool
	if nil != methodMatcher && !pb.IsMatchAllValue(methodMatcher) {
		if regexCombine && methodMatcher.GetType() != apimodel.MatchString_EXACT {
			methodValue = methodMatcher.GetValue().GetValue()
		} else {
			methodValue = request.Method
			if methodMatcher.GetType() != apimodel.MatchString_EXACT {
				regexSpread = true
			}
		}
	}
	argumentsList := rule.GetArguments()
	var tmpList []string
	for _, argumentMatcher := range argumentsList {
		var labelValue string
		valueMatcher := argumentMatcher.GetValue()
		if regexCombine && valueMatcher.GetType() != apimodel.MatchString_EXACT {
			labelValue = valueMatcher.GetValue().GetValue()
		} else {
			stringStringMap := request.Arguments[int(argumentMatcher.GetType())]
			labelValue, _ = getLabelValue(argumentMatcher, stringStringMap)
			if valueMatcher.GetType() != apimodel.MatchString_EXACT {
				regexSpread = true
			}
		}
		labelEntry := getLabelEntry(argumentMatcher, labelValue)
		if len(labelEntry) > 0 {
			tmpList = append(tmpList, labelEntry)
		}
	}
	sort.Strings(tmpList)
	return methodValue + config.DefaultMapKVTupleSeparator + strings.Join(tmpList, config.DefaultMapKVTupleSeparator), regexSpread
}

func getLabelValue(matchArgument *apitraffic.MatchArgument, stringStringMap map[string]string) (string, bool) {
	switch matchArgument.GetType() {
	case apitraffic.MatchArgument_CUSTOM, apitraffic.MatchArgument_HEADER, apitraffic.MatchArgument_QUERY, apitraffic.MatchArgument_CALLER_SERVICE:
		value, ok := stringStringMap[matchArgument.GetKey()]
		return value, ok
	case apitraffic.MatchArgument_METHOD, apitraffic.MatchArgument_CALLER_IP:
		var value string
		var ok bool
		for _, v := range stringStringMap {
			value = v
			ok = true
			break
		}
		return value, ok
	default:
		value, ok := stringStringMap[matchArgument.GetKey()]
		return value, ok
	}
}

func getLabelEntry(matchArgument *apitraffic.MatchArgument, labelValue string) string {
	switch matchArgument.GetType() {
	case apitraffic.MatchArgument_CUSTOM, apitraffic.MatchArgument_HEADER, apitraffic.MatchArgument_QUERY, apitraffic.MatchArgument_CALLER_SERVICE:
		return matchArgument.GetType().String() + config.DefaultMapKeyValueSeparator + matchArgument.GetKey() + config.DefaultMapKeyValueSeparator + labelValue
	case apitraffic.MatchArgument_METHOD, apitraffic.MatchArgument_CALLER_IP:
		return matchArgument.GetType().String() + config.DefaultMapKeyValueSeparator + labelValue
	default:
		return ""
	}
}
