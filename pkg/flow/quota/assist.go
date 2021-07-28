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
	"github.com/polarismesh/polaris-go/pkg/clock"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/flow/data"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/serverconnector"
	"github.com/modern-go/reflect2"
	"sync"
	"sync/atomic"
)

const (
	Disabled      = "rateLimit disabled"
	RuleNotExists = "quota rule not exists"
)

//限额流程的辅助类
type FlowQuotaAssistant struct {
	//销毁标识
	destroyed uint32
	//是否启用限流，如果不启用，默认都会放通
	enable bool
	//流程执行引擎
	engine model.Engine
	//插件工厂
	supplier plugin.Supplier
	//并发锁，控制windowSet的加入
	mutex *sync.Mutex
	//服务到windowSet的映射
	svcToWindowSet *sync.Map
	//限流server连接器
	asyncRateLimitConnector serverconnector.AsyncRateLimitConnector
	//任务列表
	taskValues model.TaskValues
	//通过配置获取的远程集群标识
	remoteClusterByConfig config.ServerClusterConfig
	//用来控制最大窗口数量的配置项
	windowCount        int32
	maxWindowSize      int32
	windowCountLogCtrl uint64
	//超时淘汰周期
	purgeIntervalMilli int64
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

//获取调度任务
func (f *FlowQuotaAssistant) TaskValues() model.TaskValues {
	return f.taskValues
}

//初始化限额辅助
func (f *FlowQuotaAssistant) Init(engine model.Engine, cfg config.Configuration, supplier plugin.Supplier) error {
	f.engine = engine
	f.supplier = supplier
	connector, err := data.GetServerConnector(cfg, supplier)
	if nil != err {
		return err
	}
	f.asyncRateLimitConnector = connector.GetAsyncRateLimitConnector()
	f.enable = cfg.GetProvider().GetRateLimit().IsEnable()
	if !f.enable {
		return nil
	}
	callback, err := NewRemoteQuotaCallback(cfg, supplier, engine)
	if nil != err {
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
	f.remoteClusterByConfig = cfg.GetProvider().GetRateLimit().GetRateLimitCluster()
	f.purgeIntervalMilli = model.ToMilliSeconds(cfg.GetProvider().GetRateLimit().GetPurgeInterval())
	f.mutex = &sync.Mutex{}
	f.svcToWindowSet = &sync.Map{}
	return nil
}

//获取配额分配窗口集合
func (f *FlowQuotaAssistant) DeleteRateLimitWindowSet(svcKey model.ServiceKey) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.svcToWindowSet.Delete(svcKey)
}

//获取分配窗口集合数量,只用于测试
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

//获取配额分配窗口集合
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

// 获取当前所有的限流窗口集合
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

//获取配额分配窗口
func (f *FlowQuotaAssistant) GetRateLimitWindow(svcKey model.ServiceKey, rule *namingpb.Rule,
	label string) (*RateLimitWindowSet, *RateLimitWindow) {
	windowSet := f.GetRateLimitWindowSet(svcKey, true)
	return windowSet, windowSet.GetRateLimitWindow(rule, label)
}

//服务更新回调，找到具体的限流窗口集合，然后触发更新
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

//服务删除回调
func (f *FlowQuotaAssistant) OnServiceDeleted(event *common.PluginEvent) error {
	svcEventObject := event.EventObject.(*common.ServiceEventObject)
	if svcEventObject.SvcEventKey.Type != model.EventRateLimiting {
		return nil
	}
	svcKey := svcEventObject.SvcEventKey.ServiceKey
	f.DeleteRateLimitWindowSet(svcKey)
	return nil
}

//获取配额
func (f *FlowQuotaAssistant) GetQuota(commonRequest *data.CommonRateLimitRequest) (*model.QuotaFutureImpl, error) {
	if !f.enable {
		//没有限流规则，直接放通
		resp := &model.QuotaResponse{
			Code: model.QuotaResultOk,
			Info: Disabled,
		}
		return model.NewQuotaFuture(resp, clock.GetClock().Now(), nil), nil
	}
	window, err := f.lookupRateLimitWindow(commonRequest)
	if nil != err {
		return nil, err
	}
	if nil == window {
		//没有限流规则，直接放通
		resp := &model.QuotaResponse{
			Code: model.QuotaResultOk,
			Info: RuleNotExists,
		}
		gauge := &RateLimitGauge{
			EmptyInstanceGauge: model.EmptyInstanceGauge{},
			Window:             nil,
			Namespace:          commonRequest.DstService.Namespace,
			Service:            commonRequest.DstService.Service,
			Type:               QuotaGranted,
		}
		f.engine.SyncReportStat(model.RateLimitStat, gauge)
		return model.NewQuotaFuture(resp, clock.GetClock().Now(), nil), nil
	}
	window.Init()
	return window.AllocateQuota()
}

//计算限流窗口
func (f *FlowQuotaAssistant) lookupRateLimitWindow(
	commonRequest *data.CommonRateLimitRequest) (*RateLimitWindow, error) {
	var err error
	// 1. 并发获取被调服务信息和限流配置，服务不存在，返回错误
	if err = f.engine.SyncGetResources(commonRequest); nil != err {
		return nil, err
	}
	// 2. 寻找匹配的规则
	rule, err := lookupRule(commonRequest.RateLimitRule, commonRequest.Labels)
	if nil != err {
		return nil, err
	}
	if nil == rule {
		return nil, nil
	}
	commonRequest.Criteria.DstRule = rule
	// 2.获取已有的QuotaWindow
	labelStr := commonRequest.FormatLabelToStr(rule)
	windowSet, window := f.GetRateLimitWindow(commonRequest.DstService, rule, labelStr)
	if nil != window {
		//已经存在限流窗口，则直接分配
		return window, nil
	}

	//检查是否达到最大限流窗口数量
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

// 寻址规则
func lookupRule(svcRule model.ServiceRule, labels map[string]string) (*namingpb.Rule, error) {
	if reflect2.IsNil(svcRule.GetValue()) {
		// 没有配置限流规则
		return nil, nil
	}
	validateErr := svcRule.GetValidateError()
	if nil != validateErr {
		return nil, model.NewSDKError(model.ErrCodeInvalidRule, validateErr,
			"invalid rateLimit rule, please check rule for (namespace=%s, service=%s)",
			svcRule.GetNamespace(), svcRule.GetService())
	}
	ruleCache := svcRule.GetRuleCache()
	rateLimiting := svcRule.GetValue().(*namingpb.RateLimit)
	return matchRuleByLabels(labels, rateLimiting, ruleCache), nil
}

//通过业务标签来匹配规则
func matchRuleByLabels(
	labels map[string]string, ruleSet *namingpb.RateLimit, ruleCache model.RuleCache) *namingpb.Rule {
	if len(ruleSet.Rules) == 0 {
		return nil
	}
	for _, rule := range ruleSet.Rules {
		if nil != rule.GetDisable() && rule.GetDisable().GetValue() {
			//规则被停用
			continue
		}
		if len(rule.Labels) == 0 {
			//没有业务标签，代表全匹配
			return rule
		}
		var allLabelsMatched = true
		for labelKey, labelValue := range rule.Labels {
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
