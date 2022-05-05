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
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang/protobuf/ptypes/duration"
	"github.com/golang/protobuf/ptypes/wrappers"
	"github.com/google/uuid"
	"github.com/modern-go/reflect2"

	"github.com/polarismesh/polaris-go/pkg/clock"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/flow/data"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

const (
	Disabled      = "rateLimit disabled"
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
	// 通过配置获取的远程集群标识
	remoteClusterByConfig config.ServerClusterConfig
	// 用来控制最大窗口数量的配置项
	windowCount        int32
	maxWindowSize      int32
	windowCountLogCtrl uint64
	// 超时淘汰周期
	purgeIntervalMilli int64
	// 本地配置的限流规则
	localRules map[model.ServiceKey]model.ServiceRule
}

func (f *FlowQuotaAssistant) AsyncRateLimitConnector() AsyncRateLimitConnector {
	return f.asyncRateLimitConnector
}

func (f *FlowQuotaAssistant) Destroy() {
	atomic.StoreUint32(&f.destroyed, 1)
	f.asyncRateLimitConnector.Destroy()
}

func (f *FlowQuotaAssistant) IsDestroyed() bool {
	return atomic.LoadUint32(&f.destroyed) > 0
}

func (f *FlowQuotaAssistant) AddWindowCount() {
	atomic.AddInt32(&f.windowCount, 1)
}

func (f *FlowQuotaAssistant) DelWindowCount() {
	atomic.AddInt32(&f.windowCount, -1)
}

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
	f.mutex = &sync.Mutex{}
	f.svcToWindowSet = &sync.Map{}
	localRules := cfg.GetProvider().GetRateLimit().GetRules()
	if len(localRules) > 0 {
		if f.localRules, err = rateLimitRuleConversion(localRules); err != nil {
			return err
		}
	}
	return nil
}

// rateLimitRuleConversion 获取配额分配窗口集合
func rateLimitRuleConversion(rules []config.RateLimitRule) (map[model.ServiceKey]model.ServiceRule, error) {
	svcRules := make(map[model.ServiceKey]*namingpb.RateLimit)
	for _, rule := range rules {
		svcKey := model.ServiceKey{
			Namespace: rule.Namespace,
			Service:   rule.Service,
		}
		totalRule, ok := svcRules[svcKey]
		if !ok {
			totalRule = &namingpb.RateLimit{}
			svcRules[svcKey] = totalRule
		}
		namingRule := &namingpb.Rule{
			Id:        &wrappers.StringValue{Value: uuid.New().String()},
			Service:   &wrappers.StringValue{Value: rule.Service},
			Namespace: &wrappers.StringValue{Value: rule.Namespace},
			Resource:  namingpb.Rule_QPS,
			Type:      namingpb.Rule_LOCAL,
			Amounts: []*namingpb.Amount{{
				MaxAmount:     &wrappers.UInt32Value{Value: uint32(rule.MaxAmount)},
				ValidDuration: &duration.Duration{Seconds: int64(rule.ValidDuration / time.Second)},
			}},
			Action: &wrappers.StringValue{Value: config.DefaultRejectRateLimiter},
		}
		if len(rule.Labels) > 0 {
			namingRule.Labels = make(map[string]*namingpb.MatchString)
			for key, matcher := range rule.Labels {
				var matcherType = namingpb.MatchString_EXACT
				if strings.ToUpper(matcher.Type) ==
					namingpb.MatchString_MatchStringType_name[int32(namingpb.MatchString_REGEX)] {
					matcherType = namingpb.MatchString_REGEX
				}
				namingRule.Labels[key] = &namingpb.MatchString{
					Type:  matcherType,
					Value: &wrappers.StringValue{Value: matcher.Value},
				}
			}
		}
		totalRule.Rules = append(totalRule.Rules, namingRule)
	}
	values := make(map[model.ServiceKey]model.ServiceRule, len(svcRules))
	for svcKey, totalRule := range svcRules {
		respInProto := &namingpb.DiscoverResponse{
			Type:      namingpb.DiscoverResponse_RATE_LIMIT,
			RateLimit: totalRule,
			Service: &namingpb.Service{
				Name:      &wrappers.StringValue{Value: svcKey.Service},
				Namespace: &wrappers.StringValue{Value: svcKey.Namespace},
			},
		}
		svcRule := pb.NewServiceRuleInProto(respInProto)
		if err := svcRule.ValidateAndBuildCache(); err != nil {
			return nil, errors.New(fmt.Sprintf("fail to validate config local rule, err is %v", err))
		}
		values[svcKey] = svcRule
	}
	return values, nil
}

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
func (f *FlowQuotaAssistant) GetRateLimitWindow(svcKey model.ServiceKey, rule *namingpb.Rule,
	label string) (*RateLimitWindowSet, *RateLimitWindow) {
	windowSet := f.GetRateLimitWindowSet(svcKey, true)
	return windowSet, windowSet.GetRateLimitWindow(rule, label)
}

// 服务更新回调，找到具体的限流窗口集合，然后触发更新
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
		return model.NewQuotaFuture(
			model.WithQuotaFutureReq(data.ConvertToQuotaRequest(commonRequest)),
			model.WithQuotaFutureResp(resp),
			model.WithQuotaFutureDeadline(clock.GetClock().Now()),
			model.WithQuotaFutureHooks(f.reportRateLimitGauga)), nil
	}
	window, err := f.lookupRateLimitWindow(commonRequest)
	if err != nil {
		return nil, err
	}
	if nil == window {
		// 没有限流规则，直接放通
		resp := &model.QuotaResponse{
			Code: model.QuotaResultOk,
			Info: RuleNotExists,
		}
		gauge := &RateLimitGauge{
			EmptyInstanceGauge: model.EmptyInstanceGauge{},
			Window:             nil,
			Labels:             commonRequest.Labels,
			Namespace:          commonRequest.DstService.Namespace,
			Service:            commonRequest.DstService.Service,
			Type:               QuotaGranted,
		}
		f.engine.SyncReportStat(model.RateLimitStat, gauge)
		return model.NewQuotaFuture(
			model.WithQuotaFutureReq(data.ConvertToQuotaRequest(commonRequest)),
			model.WithQuotaFutureResp(resp),
			model.WithQuotaFutureDeadline(clock.GetClock().Now()),
			model.WithQuotaFutureHooks(f.reportRateLimitGauga)), nil
	}
	window.Init()
	return window.AllocateQuota(commonRequest)
}

// lookupRateLimitWindow 计算限流窗口
func (f *FlowQuotaAssistant) lookupRateLimitWindow(
	commonRequest *data.CommonRateLimitRequest) (*RateLimitWindow, error) {
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
	var svcRule model.ServiceRule
	svcRule = commonRequest.RateLimitRule
	var rule *namingpb.Rule
	var hasContent bool
	hasContent, rule, err = lookupRule(svcRule, commonRequest.Labels)
	if err != nil {
		return nil, err
	}
	if !hasContent {
		// 远程规则没有内容，则匹配本地规则
		svcRule = f.localRules[commonRequest.DstService]
		_, rule, err = lookupRule(svcRule, commonRequest.Labels)
		if err != nil {
			return nil, err
		}
	}
	if nil == rule {
		return nil, nil
	}
	commonRequest.Criteria.DstRule = rule
	// 2.获取已有的QuotaWindow
	labelStr := commonRequest.FormatLabelToStr(rule)
	windowSet, window := f.GetRateLimitWindow(commonRequest.DstService, rule, labelStr)
	if nil != window {
		// 已经存在限流窗口，则直接分配
		return window, nil
	}

	// 检查是否达到最大限流窗口数量
	nowWindowCount := f.GetWindowCount()
	log.GetBaseLogger().Tracef("RateLimit nowWindowCount:%d %d", nowWindowCount, f.maxWindowSize)
	if nowWindowCount >= f.maxWindowSize {
		count := atomic.LoadUint64(&f.windowCountLogCtrl)
		if count%10000 == 0 {
			log.GetBaseLogger().Infof("RateLimit reach maxWindowSize nowCount:%d maxCount:%d",
				nowWindowCount, f.maxWindowSize)
		}
		atomic.AddUint64(&f.windowCountLogCtrl, 1)
		return nil, nil
	}
	// 3.创建限流窗口
	return windowSet.AddRateLimitWindow(commonRequest, rule, labelStr)
}

func (f *FlowQuotaAssistant) reportRateLimitGauga(req *model.QuotaRequestImpl, resp *model.QuotaResponse) {
	stat := &model.RateLimitGauge{
		EmptyInstanceGauge: model.EmptyInstanceGauge{},
		Namespace:          req.GetNamespace(),
		Service:            req.GetService(),
		Result:             resp.Code,
		Labels:             req.GetLabels(),
	}

	f.engine.SyncReportStat(model.RateLimitStat, stat)
}

// lookupRule 寻址规则
func lookupRule(svcRule model.ServiceRule, labels map[string]string) (bool, *namingpb.Rule, error) {
	if reflect2.IsNil(svcRule) {
		// 规则集为空
		return false, nil, nil
	}
	if reflect2.IsNil(svcRule.GetValue()) {
		// 没有配置限流规则
		return false, nil, nil
	}
	validateErr := svcRule.GetValidateError()
	if nil != validateErr {
		return true, nil, model.NewSDKError(model.ErrCodeInvalidRule, validateErr,
			"invalid rateLimit rule, please check rule for (namespace=%s, service=%s)",
			svcRule.GetNamespace(), svcRule.GetService())
	}
	ruleCache := svcRule.GetRuleCache()
	rateLimiting := svcRule.GetValue().(*namingpb.RateLimit)
	return true, matchRuleByLabels(labels, rateLimiting, ruleCache), nil
}

// matchRuleByLabels通过业务标签来匹配规则
func matchRuleByLabels(
	labels map[string]string, ruleSet *namingpb.RateLimit, ruleCache model.RuleCache) *namingpb.Rule {
	if len(ruleSet.Rules) == 0 {
		return nil
	}
	for _, rule := range ruleSet.Rules {
		if nil != rule.GetDisable() && rule.GetDisable().GetValue() {
			// 规则被停用
			continue
		}
		if len(rule.Labels) == 0 {
			// 没有业务标签，代表全匹配
			return rule
		}
		var allLabelsMatched = true
		for labelKey, labelValue := range rule.Labels {
			if labelKey == pb.MatchAll {
				continue
			}
			if !matchLabels(labelKey, labelValue, labels, ruleCache) {
				allLabelsMatched = false
				break
			}
		}
		if allLabelsMatched {
			return rule
		}
	}
	return nil
}
