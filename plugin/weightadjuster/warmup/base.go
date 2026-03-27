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

package warmup

import (
	"math"
	"strconv"
	"sync"
	"time"

	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

const (
	PluginName = "warmup"
	// 默认预热系数
	_defaultCurvature = 2
	// 实例权重缓存过期时间
	_instanceWeightCacheExpire = 5 * time.Second
)

// instanceWeightCacheEntry 实例权重缓存条目
type instanceWeightCacheEntry struct {
	weights      []*model.InstanceWeight
	instanceSize int
	expireAt     time.Time
}

// Adjuster 根据注册时间和预热系数来进行动态权重调整
type Adjuster struct {
	*plugin.PluginBase
	pluginCtx *plugin.InitContext
	// 缓存每个服务的实例权重计算结果
	instanceWeightCache sync.Map // map[model.ServiceKey]*instanceWeightCacheEntry
	// 每个服务的锁
	lockMap sync.Map // map[model.ServiceKey]*sync.Mutex
	log     log.Logger
}

// Type 插件类型
func (g *Adjuster) Type() common.Type {
	return common.TypeWeightAdjuster
}

// Name 插件名，一个类型下插件名唯一
func (g *Adjuster) Name() string {
	return PluginName
}

// Init 初始化插件
func (g *Adjuster) Init(ctx *plugin.InitContext) error {
	g.PluginBase = plugin.NewPluginBase(ctx)
	g.pluginCtx = ctx
	// 服务预热的日志统一放到无损上线的日志里面
	g.log = log.GetLosslessLogger()
	return nil
}

// Destroy 销毁插件，可用于释放资源
func (g *Adjuster) Destroy() error {
	return nil
}

// RealTimeAdjustDynamicWeight 实时上报健康状态，并判断是否需要立刻进行动态权重调整，用于流量削峰
func (g *Adjuster) RealTimeAdjustDynamicWeight(model.InstanceGauge) (bool, error) {
	return false, nil
}

// TimingAdjustDynamicWeight 进行动态权重调整，返回调整后的动态权重
// dynamicWeight 为前序 adjuster 计算得到的动态权重，用于多 adjuster 串联时的权重透传
func (g *Adjuster) TimingAdjustDynamicWeight(dynamicWeight map[string]*model.InstanceWeight,
	service model.ServiceInstances) ([]*model.InstanceWeight, error) {
	if service == nil || len(service.GetInstances()) == 0 ||
		!g.pluginCtx.Config.GetConsumer().GetWeightAdjust().IsEnable() {
		return nil, nil
	}
	svcKey := model.ServiceKey{
		Namespace: service.GetNamespace(),
		Service:   service.GetService(),
	}
	g.log.Debugf("[WarmupWeightAdjuster] TimingAdjustDynamicWeight called for service %s/%s, instance count: %d",
		svcKey.Namespace, svcKey.Service, len(service.GetInstances()))
	// 先检查缓存
	if cached, ok := g.instanceWeightCache.Load(svcKey); ok {
		entry := cached.(*instanceWeightCacheEntry)
		if time.Now().Before(entry.expireAt) && entry.instanceSize == len(service.GetInstances()) {
			g.log.Debugf("[WarmupWeightAdjuster] cache hit for service %s/%s, cached weights count: %d",
				svcKey.Namespace, svcKey.Service, len(entry.weights))
			return entry.weights, nil
		}
	}
	// 获取服务锁
	lockVal, _ := g.lockMap.LoadOrStore(svcKey, &sync.Mutex{})
	lock := lockVal.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	// double check 缓存
	if cached, ok := g.instanceWeightCache.Load(svcKey); ok {
		entry := cached.(*instanceWeightCacheEntry)
		if time.Now().Before(entry.expireAt) && entry.instanceSize == len(service.GetInstances()) {
			return entry.weights, nil
		}
	}
	// 获取lossless规则
	losslessRules := g.getLosslessRules(svcKey)
	if len(losslessRules) == 0 {
		return nil, nil
	}
	g.log.Debugf("[WarmupWeightAdjuster] cache miss for service %s/%s, recalculating weights, lossless rules count: %d",
		svcKey.Namespace, svcKey.Service, len(losslessRules))
	result, err := g.doTimingAdjustDynamicWeight(dynamicWeight, service, losslessRules)
	if err != nil {
		return nil, err
	}
	// 缓存计算结果
	g.instanceWeightCache.Store(svcKey, &instanceWeightCacheEntry{
		weights:      result,
		instanceSize: len(service.GetInstances()),
		expireAt:     time.Now().Add(_instanceWeightCacheExpire),
	})
	return result, nil
}

// getLosslessRules 获取lossless规则
// 每次都从 SDK 底层缓存获取最新规则，确保服务端规则变更时能及时感知
// SyncGetServiceRule 在规则已加载的情况下只是从本地 inmemory 缓存读取，性能开销很小
func (g *Adjuster) getLosslessRules(svcKey model.ServiceKey) []*apitraffic.LosslessRule {
	// 通过Engine获取lossless规则
	if g.pluginCtx.ValueCtx == nil {
		g.log.Warnf("[WarmupWeightAdjuster] getLosslessRules ValueCtx is nil for service %s/%s",
			svcKey.Namespace, svcKey.Service)
		return nil
	}
	engine := g.pluginCtx.ValueCtx.GetEngine()
	if engine == nil {
		g.log.Warnf("[WarmupWeightAdjuster] getLosslessRules engine is nil for service %s/%s",
			svcKey.Namespace, svcKey.Service)
		return nil
	}
	ruleReq := &model.GetServiceRuleRequest{
		Namespace: svcKey.Namespace,
		Service:   svcKey.Service,
	}
	ruleReq.SetTimeout(time.Second * 5)

	ruleResp, err := engine.SyncGetServiceRule(model.EventLossless, ruleReq)
	if err != nil {
		g.log.Warnf("[WarmupWeightAdjuster] get lossless rule failed for service %s/%s: %v",
			svcKey.Namespace, svcKey.Service, err)
		return nil
	}
	if ruleResp == nil || ruleResp.GetValue() == nil {
		g.log.Debugf("[WarmupWeightAdjuster] getLosslessRules response is nil for service %s/%s",
			svcKey.Namespace, svcKey.Service)
		return nil
	}
	wrapper, ok := ruleResp.GetValue().(*pb.LosslessRuleWrapper)
	if !ok || wrapper == nil {
		g.log.Warnf("[WarmupWeightAdjuster] getLosslessRules wrapper is nil or type mismatch for service %s/%s",
			svcKey.Namespace, svcKey.Service)
		return nil
	}
	rules := wrapper.Rules
	g.log.Debugf("[WarmupWeightAdjuster] getLosslessRules fetched for service %s/%s, rules count: %d",
		svcKey.Namespace, svcKey.Service, len(rules))

	return rules
}

// parseMetadataLosslessRules 解析规则中的元数据匹配信息
// 返回 map[metadataKey]map[metadataValue]*LosslessRule
func (g *Adjuster) parseMetadataLosslessRules(
	losslessRules []*apitraffic.LosslessRule) map[string]map[string]*apitraffic.LosslessRule {

	metadataRules := make(map[string]map[string]*apitraffic.LosslessRule)
	for _, rule := range losslessRules {
		metadata := rule.GetMetadata()
		if len(metadata) == 0 {
			g.log.Debugf("[WarmupWeightAdjuster] parseMetadataLosslessRules rule has no metadata, skip")
			continue
		}
		for key, value := range metadata {
			if _, ok := metadataRules[key]; !ok {
				metadataRules[key] = make(map[string]*apitraffic.LosslessRule)
			}
			metadataRules[key][value] = rule
			g.log.Debugf("[WarmupWeightAdjuster] parseMetadataLosslessRules added rule: metaKey=%s, metaValue=%s",
				key, value)
		}
	}
	g.log.Debugf("[WarmupWeightAdjuster] parseMetadataLosslessRules total metadata keys: %d", len(metadataRules))
	return metadataRules
}

// getMatchMetadataLosslessRule 根据实例的元数据匹配对应的lossless规则
func (g *Adjuster) getMatchMetadataLosslessRule(instance model.Instance,
	metadataRules map[string]map[string]*apitraffic.LosslessRule) *apitraffic.LosslessRule {

	instanceMeta := instance.GetMetadata()
	if len(instanceMeta) == 0 {
		g.log.Debugf("[WarmupWeightAdjuster] instance %s has no metadata, skip metadata rule matching", instance.GetId())
		return nil
	}
	for metaKey, valueRuleMap := range metadataRules {
		instanceValue, exists := instanceMeta[metaKey]
		if !exists {
			g.log.Debugf("[WarmupWeightAdjuster] instance %s metadata key %s not found, skip", instance.GetId(), metaKey)
			continue
		}
		if rule, ok := valueRuleMap[instanceValue]; ok {
			g.log.Debugf("[WarmupWeightAdjuster] instance %s matched metadata rule: key=%s, value=%s", instance.GetId(), metaKey, instanceValue)
			return rule
		}
		g.log.Debugf("[WarmupWeightAdjuster] instance %s metadata key=%s, value=%s not matched any rule", instance.GetId(), metaKey, instanceValue)
	}
	return nil
}

// doTimingAdjustDynamicWeight 执行动态权重调整
func (g *Adjuster) doTimingAdjustDynamicWeight(dynamicWeight map[string]*model.InstanceWeight,
	service model.ServiceInstances, losslessRules []*apitraffic.LosslessRule) ([]*model.InstanceWeight, error) {
	if len(losslessRules) == 0 {
		return nil, nil
	}

	// 解析元数据匹配规则
	metadataRules := g.parseMetadataLosslessRules(losslessRules)

	if len(metadataRules) == 0 {
		// 没有元数据匹配规则，使用第一条规则
		g.log.Debugf("[WarmupWeightAdjuster] no metadata rules, using first lossless rule, rule: %s",
			model.JsonString(losslessRules[0]))
		return g.getInstanceWeightFromLosslessRule(dynamicWeight, service, losslessRules[0])
	}
	// 有元数据匹配规则，按实例元数据匹配不同的规则
	g.log.Debugf("[WarmupWeightAdjuster] using metadata rules, metadata rule keys count: %d", len(metadataRules))
	return g.getInstanceWeightFromMetadataRule(dynamicWeight, service, metadataRules)
}

// getInstanceWeightFromMetadataRule 根据元数据规则获取实例权重
func (g *Adjuster) getInstanceWeightFromMetadataRule(dynamicWeight map[string]*model.InstanceWeight,
	service model.ServiceInstances,
	metadataRules map[string]map[string]*apitraffic.LosslessRule) ([]*model.InstanceWeight, error) {

	instances := service.GetInstances()
	currentTime := time.Now()

	needOverloadProtection := true
	var overloadProtectionThreshold int32
	warmupInstanceCount := 0

	var result []*model.InstanceWeight

	for _, instance := range instances {
		losslessRule := g.getMatchMetadataLosslessRule(instance, metadataRules)
		if losslessRule == nil {
			g.log.Debugf("[WarmupWeightAdjuster] instance %s no matching metadata lossless rule, skip", instance.GetId())
			continue
		}
		warmup := losslessRule.GetLosslessOnline().GetWarmup()
		if warmup == nil || !warmup.GetEnable() {
			g.log.Debugf("[WarmupWeightAdjuster] instance %s matched rule but warmup not enabled, skip", instance.GetId())
			continue
		}
		g.log.Debugf("[WarmupWeightAdjuster] instance %s matched rule, warmup config: interval=%ds, curvature=%d, "+
			"overloadProtection=%v, threshold=%d",
			instance.GetId(), warmup.GetIntervalSecond(), warmup.GetCurvature(),
			warmup.GetEnableOverloadProtection(), warmup.GetOverloadProtectionThreshold())
		// 任何一个规则关闭过载保护，就关闭过载保护, 那么预热就会生效
		if !warmup.GetEnableOverloadProtection() {
			needOverloadProtection = false
		}
		if warmup.GetOverloadProtectionThreshold() > overloadProtectionThreshold {
			overloadProtectionThreshold = warmup.GetOverloadProtectionThreshold()
		}

		weight := g.getInstanceWeight(dynamicWeight, instance, warmup, currentTime)
		if weight.IsDynamicWeightValid() {
			warmupInstanceCount++
			g.log.Debugf("[WarmupWeightAdjuster] instance %s is warming up, baseWeight=%d, dynamicWeight=%d",
				instance.GetId(), weight.BaseWeight, weight.DynamicWeight)
		}
		result = append(result, weight)
	}

	if needOverloadProtection && overloadProtectionThreshold > 0 {
		percentage := warmupInstanceCount * 100 / len(instances)
		if int32(percentage) > overloadProtectionThreshold {
			g.log.Infof("[WarmupWeightAdjuster] getInstanceWeightFromMetadataRule overload protection, "+
				"warmup count: %d, instance size: %d, threshold: %d",
				warmupInstanceCount, len(instances), overloadProtectionThreshold)
			return nil, nil
		}
	}

	g.log.Infof("[WarmupWeightAdjuster] getInstanceWeightFromMetadataRule warmup instance count: %d, result: %v",
		warmupInstanceCount, result)

	if warmupInstanceCount == 0 {
		return nil, nil
	}

	return result, nil
}

// getInstanceWeightFromLosslessRule 从lossless规则中获取实例权重
func (g *Adjuster) getInstanceWeightFromLosslessRule(dynamicWeight map[string]*model.InstanceWeight,
	service model.ServiceInstances, losslessRule *apitraffic.LosslessRule) ([]*model.InstanceWeight, error) {
	if losslessRule == nil || losslessRule.GetLosslessOnline() == nil {
		g.log.Debugf("[WarmupWeightAdjuster] getInstanceWeightFromLosslessRule losslessRule or losslessOnline is nil, skip")
		return nil, nil
	}

	warmup := losslessRule.GetLosslessOnline().GetWarmup()
	if warmup == nil || !warmup.GetEnable() {
		g.log.Debugf("[WarmupWeightAdjuster] getInstanceWeightFromLosslessRule warmup is nil or not enabled, skip")
		return nil, nil
	}

	instances := service.GetInstances()
	currentTime := time.Now()

	g.log.Debugf("[WarmupWeightAdjuster] getInstanceWeightFromLosslessRule warmup config: interval=%ds, curvature=%d, "+
		"overloadProtection=%v, threshold=%d",
		warmup.GetIntervalSecond(), warmup.GetCurvature(),
		warmup.GetEnableOverloadProtection(), warmup.GetOverloadProtectionThreshold())

	if warmup.GetEnableOverloadProtection() {
		needWarmupCount := g.countNeedWarmupInstances(instances, warmup, currentTime)
		threshold := warmup.GetOverloadProtectionThreshold()
		percentage := needWarmupCount * 100 / len(instances)
		g.log.Debugf("[WarmupWeightAdjuster] getInstanceWeightFromLosslessRule overload check: "+
			"needWarmupCount=%d, totalCount=%d, percentage=%d%%, threshold=%d",
			needWarmupCount, len(instances), percentage, threshold)
		if int32(percentage) >= threshold {
			g.log.Infof("[WarmupWeightAdjuster] overload protection triggered, needWarmupCount: %d, totalCount: %d, "+
				"percentage=%d%%, warmup config: interval=%ds, curvature=%d, overloadProtection=%v, threshold=%d%%",
				needWarmupCount, len(instances), percentage, warmup.GetIntervalSecond(), warmup.GetCurvature(),
				warmup.GetEnableOverloadProtection(), warmup.GetOverloadProtectionThreshold())
			return nil, nil
		}
	}

	var result []*model.InstanceWeight
	warmupInstanceCount := 0

	for _, instance := range instances {
		weight := g.getInstanceWeight(dynamicWeight, instance, warmup, currentTime)
		if weight.IsDynamicWeightValid() {
			warmupInstanceCount++
			g.log.Debugf("[WarmupWeightAdjuster] getInstanceWeightFromLosslessRule instance %s is warming up, "+
				"baseWeight=%d, dynamicWeight=%d", instance.GetId(), weight.BaseWeight, weight.DynamicWeight)
		}
		result = append(result, weight)
	}

	g.log.Debugf("[WarmupWeightAdjuster] getInstanceWeightFromLosslessRule warmup instance count: %d, result: %v",
		warmupInstanceCount, result)

	if warmupInstanceCount == 0 {
		return nil, nil
	}

	g.log.Infof("[WarmupWeightAdjuster] warmup instance count: %d, total:%d, result: %v, warmup config: interval=%ds, "+
		"curvature=%d, overloadProtection=%v, threshold=%d", warmupInstanceCount, len(result), result,
		warmup.GetIntervalSecond(), warmup.GetCurvature(), warmup.GetEnableOverloadProtection(),
		warmup.GetOverloadProtectionThreshold())

	return result, nil
}

// countNeedWarmupInstances 统计需要预热的实例数量
func (g *Adjuster) countNeedWarmupInstances(instances []model.Instance, warmup *apitraffic.Warmup,
	currentTime time.Time) int {
	count := 0
	intervalSecond := warmup.GetIntervalSecond()

	for _, instance := range instances {
		createTime := g.getInstanceCreateTime(instance)
		if createTime == 0 {
			g.log.Debugf("[WarmupWeightAdjuster] countNeedWarmupInstances instance %s createTime is 0, skip",
				instance.GetId())
			continue
		}

		uptime := currentTime.Unix() - createTime
		if uptime < 0 {
			g.log.Debugf("[WarmupWeightAdjuster] countNeedWarmupInstances instance %s has negative uptime=%ds, "+
				"createTime=%d, currentTime=%d, possible timezone issue, skip",
				instance.GetId(), uptime, createTime, currentTime.Unix())
			continue
		}

		if uptime < int64(intervalSecond) {
			count++
			g.log.Debugf("[WarmupWeightAdjuster] countNeedWarmupInstances instance %s needs warmup, "+
				"uptime=%ds, intervalSecond=%d", instance.GetId(), uptime, intervalSecond)
		}
	}

	g.log.Debugf("[WarmupWeightAdjuster] countNeedWarmupInstances total need warmup: %d / %d",
		count, len(instances))
	return count
}

// getInstanceWeight 计算单个实例的预热权重
// dynamicWeight 为前序 adjuster 的动态权重结果，若存在则以其作为基础权重（透传行为）
func (g *Adjuster) getInstanceWeight(dynamicWeight map[string]*model.InstanceWeight,
	instance model.Instance, warmup *apitraffic.Warmup,
	currentTime time.Time) *model.InstanceWeight {

	baseWeight := uint32(instance.GetWeight())
	if dynamicWeight != nil {
		if prev, ok := dynamicWeight[instance.GetId()]; ok {
			g.log.Debugf("[WarmupWeightAdjuster] getInstanceWeight instance %s using previous dynamic weight %d as base",
				instance.GetId(), prev.DynamicWeight)
			baseWeight = prev.DynamicWeight
		}
	}

	weight := &model.InstanceWeight{
		InstanceID:    instance.GetId(),
		DynamicWeight: baseWeight,
		BaseWeight:    baseWeight,
	}

	createTime := g.getInstanceCreateTime(instance)
	if createTime == 0 {
		g.log.Debugf("[WarmupWeightAdjuster] getInstanceWeight instance %s createTime is 0, return baseWeight=%d",
			instance.GetId(), baseWeight)
		return weight
	}

	uptime := currentTime.Unix() - createTime
	if uptime < 0 {
		g.log.Debugf("[WarmupWeightAdjuster] getInstanceWeight instance %s has negative uptime=%ds, "+
			"createTime=%d, currentTime=%d, possible timezone issue, treat as just started",
			instance.GetId(), uptime, createTime, currentTime.Unix())
		uptime = 0
	}

	intervalSecond := int64(warmup.GetIntervalSecond())
	if uptime > intervalSecond {
		g.log.Debugf("[WarmupWeightAdjuster] getInstanceWeight instance %s warmup completed, "+
			"uptime=%ds > interval=%ds, return baseWeight=%d",
			instance.GetId(), uptime, intervalSecond, baseWeight)
		return weight
	}

	curvature := warmup.GetCurvature()
	if curvature == 0 {
		curvature = _defaultCurvature
	}

	ratio := float64(uptime) / float64(intervalSecond)
	calcWeight := math.Ceil(math.Abs(math.Pow(ratio, float64(curvature)) * float64(baseWeight)))
	if calcWeight > float64(baseWeight) {
		calcWeight = float64(baseWeight)
	}

	g.log.Debugf("[WarmupWeightAdjuster] getInstanceWeight instance %s warming up: "+
		"uptime=%ds, interval=%ds, ratio=%.4f, curvature=%d, baseWeight=%d, calcWeight=%d",
		instance.GetId(), uptime, intervalSecond, ratio, curvature, baseWeight, uint32(calcWeight))

	weight.DynamicWeight = uint32(calcWeight)
	return weight
}

// 不带时区信息的 ctime 日期时间格式，需要使用 time.ParseInLocation 以本地时区解析
var _ctimeLocalLayouts = []string{
	"2006-01-02 15:04:05",
}

// 带时区信息的 ctime 日期时间格式，使用 time.Parse 即可正确解析
var _ctimeTimezoneLayouts = []string{
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05+08:00",
	"2006-01-02T15:04:05.000Z",
	time.RFC3339,
}

// getInstanceCreateTime 获取实例的创建时间（秒级时间戳）
func (g *Adjuster) getInstanceCreateTime(instance model.Instance) int64 {
	// InstanceInProto 嵌入了 *apiservice.Instance，可以通过类型断言获取Ctime
	if protoInst, ok := instance.(*pb.InstanceInProto); ok {
		ctime := protoInst.GetCtime()
		if ctime != nil && ctime.GetValue() != "" {
			ctimeStr := ctime.GetValue()
			// 优先尝试不带时区的格式，使用本地时区解析（服务端通常返回本地时间字符串）
			for _, layout := range _ctimeLocalLayouts {
				if t, err := time.ParseInLocation(layout, ctimeStr, time.Local); err == nil {
					g.log.Debugf("[WarmupWeightAdjuster] getInstanceCreateTime instance %s ctime=%s parsed with local timezone, unix=%d",
						instance.GetId(), ctimeStr, t.Unix())
					return t.Unix()
				}
			}
			// 尝试带时区信息的格式
			for _, layout := range _ctimeTimezoneLayouts {
				if t, err := time.Parse(layout, ctimeStr); err == nil {
					g.log.Debugf("[WarmupWeightAdjuster] getInstanceCreateTime instance %s ctime=%s parsed with timezone layout, unix=%d",
						instance.GetId(), ctimeStr, t.Unix())
					return t.Unix()
				}
			}
			// 回退：尝试解析为纯数字时间戳（秒级）
			if ts, err := strconv.ParseInt(ctimeStr, 10, 64); err == nil {
				g.log.Debugf("[WarmupWeightAdjuster] getInstanceCreateTime instance %s ctime=%s parsed as unix timestamp=%d",
					instance.GetId(), ctimeStr, ts)
				return ts
			}
			// 所有格式都无法解析
			g.log.Errorf("[WarmupWeightAdjuster] getInstanceCreateTime instance %s ctime parse failed: %s",
				instance.GetId(), ctimeStr)
		}
	}
	return 0
}

// init 注册插件
func init() {
	plugin.RegisterPlugin(&Adjuster{})
}
