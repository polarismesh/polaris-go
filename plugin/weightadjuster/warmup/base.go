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
)

// Adjuster 根据注册时间和预热系数来进行动态权重调整
type Adjuster struct {
	*plugin.PluginBase
	pluginCtx *plugin.InitContext
	// 缓存每个服务的lossless规则
	losslessRuleCache sync.Map // map[model.ServiceKey][]*apitraffic.LosslessRule
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
func (g *Adjuster) TimingAdjustDynamicWeight(service model.ServiceInstances) ([]*model.InstanceWeight, error) {
	if service == nil || len(service.GetInstances()) == 0 ||
		!g.pluginCtx.Config.GetConsumer().GetWeightAdjust().IsEnable() {
		return nil, nil
	}
	svcKey := model.ServiceKey{
		Namespace: service.GetNamespace(),
		Service:   service.GetService(),
	}
	// 获取lossless规则
	losslessRules := g.getLosslessRules(svcKey)
	if len(losslessRules) == 0 {
		return nil, nil
	}
	// 获取服务锁
	lockVal, _ := g.lockMap.LoadOrStore(svcKey, &sync.Mutex{})
	lock := lockVal.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	return g.doTimingAdjustDynamicWeight(service, losslessRules)
}

// getLosslessRules 获取lossless规则
func (g *Adjuster) getLosslessRules(svcKey model.ServiceKey) []*apitraffic.LosslessRule {
	// 先从缓存获取
	if cached, ok := g.losslessRuleCache.Load(svcKey); ok {
		return cached.([]*apitraffic.LosslessRule)
	}
	// 通过Engine获取lossless规则
	if g.pluginCtx.ValueCtx == nil {
		return nil
	}
	engine := g.pluginCtx.ValueCtx.GetEngine()
	if engine == nil {
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
		return nil
	}
	wrapper, ok := ruleResp.GetValue().(*pb.LosslessRuleWrapper)
	if !ok || wrapper == nil {
		return nil
	}
	rules := wrapper.Rules
	// 缓存规则
	g.losslessRuleCache.Store(svcKey, rules)

	return rules
}

// doTimingAdjustDynamicWeight 执行动态权重调整
func (g *Adjuster) doTimingAdjustDynamicWeight(service model.ServiceInstances,
	losslessRules []*apitraffic.LosslessRule) ([]*model.InstanceWeight, error) {
	if len(losslessRules) == 0 {
		return nil, nil
	}

	// 使用第一个匹配的规则
	losslessRule := losslessRules[0]
	return g.getInstanceWeightFromLosslessRule(service, losslessRule)
}

// getInstanceWeightFromLosslessRule 从lossless规则中获取实例权重
func (g *Adjuster) getInstanceWeightFromLosslessRule(service model.ServiceInstances,
	losslessRule *apitraffic.LosslessRule) ([]*model.InstanceWeight, error) {
	if losslessRule == nil || losslessRule.GetLosslessOnline() == nil {
		return nil, nil
	}

	warmup := losslessRule.GetLosslessOnline().GetWarmup()
	if warmup == nil || !warmup.GetEnable() || warmup.GetIntervalSecond() == 0 {
		return nil, nil
	}

	instances := service.GetInstances()
	currentTime := time.Now()

	// 检查过载保护
	if warmup.GetEnableOverloadProtection() {
		needWarmupCount := g.countNeedWarmupInstances(instances, warmup, currentTime)
		threshold := warmup.GetOverloadProtectionThreshold()
		percentage := needWarmupCount * 100 / len(instances)
		if int32(percentage) >= threshold {
			g.log.Infof("[WarmupWeightAdjuster] overload protection triggered, "+
				"needWarmupCount: %d, totalCount: %d, threshold: %d",
				needWarmupCount, len(instances), threshold)
			return nil, nil
		}
	}

	var result []*model.InstanceWeight
	warmupInstanceCount := 0

	for _, instance := range instances {
		weight := g.getInstanceWeight(instance, warmup, currentTime)
		if weight != nil {
			warmupInstanceCount++
			result = append(result, weight)
		}
	}

	if warmupInstanceCount > 0 {
		g.log.Infof("[WarmupWeightAdjuster] warmup instance count: %d, result: %v",
			warmupInstanceCount, result)
	}

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
			continue
		}

		uptime := currentTime.Unix() - createTime
		if uptime < 0 {
			uptime = -uptime
		}

		if uptime < int64(intervalSecond) {
			count++
		}
	}

	return count
}

// getInstanceWeight 计算单个实例的预热权重
func (g *Adjuster) getInstanceWeight(instance model.Instance, warmup *apitraffic.Warmup,
	currentTime time.Time) *model.InstanceWeight {
	createTime := g.getInstanceCreateTime(instance)
	if createTime == 0 {
		return nil
	}

	uptime := currentTime.Unix() - createTime
	if uptime < 0 {
		uptime = -uptime
	}

	intervalSecond := int64(warmup.GetIntervalSecond())
	// 如果已经超过预热时间，不需要调整权重
	if uptime >= intervalSecond {
		return nil
	}

	baseWeight := uint32(instance.GetWeight())
	curvature := warmup.GetCurvature()
	if curvature == 0 {
		curvature = 2 // 默认曲率为2
	}

	// 计算预热权重: (uptime/intervalSecond)^curvature * baseWeight
	ratio := float64(uptime) / float64(intervalSecond)
	dynamicWeight := math.Ceil(math.Abs(math.Pow(ratio, float64(curvature)) * float64(baseWeight)))

	// 确保权重不超过基础权重
	if dynamicWeight > float64(baseWeight) {
		dynamicWeight = float64(baseWeight)
	}
	// 确保权重至少为1
	if dynamicWeight < 1 {
		dynamicWeight = 1
	}

	return &model.InstanceWeight{
		InstanceID:    instance.GetId(),
		DynamicWeight: uint32(dynamicWeight),
	}
}

// getInstanceCreateTime 获取实例的创建时间（秒级时间戳）
func (g *Adjuster) getInstanceCreateTime(instance model.Instance) int64 {
	// 尝试从实例的元数据中获取创建时间
	// InstanceInProto 嵌入了 *apiservice.Instance，可以通过类型断言获取Ctime
	if protoInst, ok := instance.(*pb.InstanceInProto); ok {
		ctime := protoInst.GetCtime()
		if ctime != nil && ctime.GetValue() != "" {
			// Ctime 是字符串格式的时间戳
			if ts, err := strconv.ParseInt(ctime.GetValue(), 10, 64); err == nil {
				return ts
			}
		}
	}
	return 0
}

// init 注册插件
func init() {
	plugin.RegisterPlugin(&Adjuster{})
}
