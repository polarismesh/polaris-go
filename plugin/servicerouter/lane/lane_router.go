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

// Package lane 实现泳道路由（Lane Router）插件。
//
// 泳道路由通过 ServiceClusters 的 lane 元数据，将带有特定染色标签（service-lane）
// 的流量路由到对应的泳道实例；未染色的流量则按照 TrafficMatchRule 自动识别与染色，
// 或回退到基线实例。支持 STRICT / PERMISSIVE 两种匹配模式，以及 OnlyUntaggedInstance /
// ExcludeEnabledLaneInstance 两种基线选取策略。
//
// 该插件注册为 ServiceRouter 类型，默认在 consumer.serviceRouter.beforeChain 中启用，
// 与 rulebase / nearbybase / dstmeta 等后续路由插件串联工作。
package lane

import (
	"math"
	"math/rand"
	"time"

	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/servicerouter"
	"github.com/polarismesh/polaris-go/pkg/sdk"
)

const (
	// trafficStainLabel 跨进程染色复用时的 HTTP 协议字段名。
	// 真实链路：上游 SDK 染色后，业务侧通过 InstancesResponse 读到结果，作为 HTTP Header
	// 透传给下游进程；下游业务用 BuildHeaderArgument 把它包成 Argument 上报,
	// api.go / api/consumer.go 的 convert() 会把 Arguments 统一摊平到
	// SourceService.Metadata 的带前缀 label key ("$header.service-lane")，
	// lane router 和 rulebase 一样只读 SourceService.Metadata。
	// 非入口服务仅通过 "$header.service-lane" 存在性判断是否已染色，跳过流量识别。
	trafficStainLabel = "service-lane"
	// instanceLaneKey 实例元数据中表示泳道归属的默认 key
	instanceLaneKey = "lane"
	// gatewayEntryType 网关类型流量入口的 type 值
	gatewayEntryType = "polarismesh.cn/gateway/spring-cloud-gateway"
	// serviceEntryType 服务类型流量入口的 type 值
	serviceEntryType = "polarismesh.cn/service"
)

// tryStainCurrentTraffic 对当前请求流量进行染色判断
// 返回 true 表示本次流量应进入泳道，false 表示不进入
func (r *LaneRouter) tryStainCurrentTraffic(rule *apitraffic.LaneRule) bool {
	gray := rule.GetTrafficGray()
	if gray == nil {
		return true
	}
	switch gray.GetMode() {
	case apitraffic.TrafficGray_PERCENTAGE:
		return r.tryStainByPercentage(gray.GetPercentage())
	case apitraffic.TrafficGray_WARMUP:
		return r.tryStainByWarmup(rule, gray.GetWarmup())
	default:
		return true
	}
}

// tryStainByPercentage 按百分比进行流量染色
func (r *LaneRouter) tryStainByPercentage(pct *apitraffic.TrafficGray_Percentage) bool {
	if pct == nil {
		return true
	}
	percent := pct.GetPercent()
	if percent <= 0 {
		return false
	}
	if percent >= 100 {
		return true
	}
	roll := rand.Intn(100) //nolint:gosec
	stained := roll < int(percent)
	r.logCtx.GetRouteLogger().Debugf(
		"[Router][Lane] percentage stain: percent=%d, roll=%d, stained=%v",
		percent, roll, stained)
	return stained
}

// tryStainByWarmup 按预热模式进行流量染色
// probability = (uptime / warmupInterval) ^ curvature
func (r *LaneRouter) tryStainByWarmup(rule *apitraffic.LaneRule, warmup *apitraffic.TrafficGray_Warmup) bool {
	if warmup == nil {
		return true
	}
	intervalSec := warmup.GetIntervalSecond()
	curvature := warmup.GetCurvature()
	if intervalSec <= 0 {
		return true
	}

	// 解析规则启用时间（etime 字段存储预热开始时间）
	startTime := parseWarmupEtime(rule.GetEtime())
	if startTime.IsZero() {
		startTime = time.Now()
	}

	uptime := time.Since(startTime).Seconds()
	warmupInterval := float64(intervalSec)

	c := float64(curvature)
	if c <= 0 {
		c = 1
	}

	if uptime >= warmupInterval {
		if r.logCtx.GetRouteLogger().IsLevelEnabled(log.DebugLog) {
			r.logCtx.GetRouteLogger().Debugf(
				"[Router][Lane] warmup completed: uptime=%.1fs >= interval=%.1fs, probability=100%%, rule=%s",
				uptime, warmupInterval, rule.GetName())
		}
		return true
	}
	if uptime <= 0 {
		return false
	}

	probability := math.Pow(uptime/warmupInterval, c)
	roll := rand.Float64() //nolint:gosec
	stained := roll < probability
	if r.logCtx.GetRouteLogger().IsLevelEnabled(log.DebugLog) {
		r.logCtx.GetRouteLogger().Debugf(
			"[Router][Lane] warmup stain: uptime=%.1fs, interval=%.1fs, curvature=%.1f, "+
				"probability=%.4f, roll=%.4f, stained=%v, rule=%s",
			uptime, warmupInterval, c, probability, roll, stained, rule.GetName())
	}
	return stained
}

// LaneRouter 泳道路由插件
type LaneRouter struct {
	*plugin.PluginBase
	valueCtx sdk.ValueContext
	logCtx   *log.ContextLogger
	cfg      *Config
}

// Type 插件类型
func (r *LaneRouter) Type() common.Type {
	return common.TypeServiceRouter
}

// Name 插件名
func (r *LaneRouter) Name() string {
	return config.DefaultServiceRouterLane
}

// Init 初始化插件
func (r *LaneRouter) Init(ctx *plugin.InitContext) error {
	r.PluginBase = plugin.NewPluginBase(ctx)
	r.valueCtx = ctx.ValueCtx
	r.logCtx = ctx.ValueCtx.GetContextLogger()
	r.cfg = &Config{}
	r.cfg.SetDefault()
	cfgValue := ctx.Config.GetConsumer().GetServiceRouter().GetPluginConfig(r.Name())
	if cfgValue != nil {
		if c, ok := cfgValue.(*Config); ok {
			r.cfg = c
		}
	}
	return nil
}

// Destroy 销毁插件
func (r *LaneRouter) Destroy() error {
	return nil
}

// Enable 是否需要启动泳道路由。
//
// 实现说明：lane router **始终返回 true**（always-on），即使当前 callee 未关联任何
// 泳道组也参与路由链。原因如下：
//
//  1. 业界服务治理对“泳道”的核心约定：
//     - 带 `lane` 元数据标签的实例属于某条泳道，未染色的请求不应被路由到这些实例上；
//     - 没有标签的实例才是基线，承接默认流量。
//
//  2. 一个不在任何泳道组下的服务，如果其实例既有带 `lane` 标签的（如灰度遗留实例）
//     也有不带标签的，**默认（baseLaneMode=OnlyUntaggedInstance）**应只路由到不带
//     标签的实例上，否则未染色流量会被打散到泳道实例上，违背泳道隔离语义。
//
//  3. 旧实现下，`groups == nil` 时直接返回 false，lane router 整个跳过，
//     默认负载均衡会把请求随机分到所有实例（含带 lane 标签的），与上述约定不符。
//
// 通过让 Enable() 始终返回 true，GetFilteredInstances 会进入 routeToBaseline，
// 复用 OnlyUntaggedInstance 分支按 `lane` key 过滤掉带标签实例，对齐预期语义。
func (r *LaneRouter) Enable(routeInfo *servicerouter.RouteInfo, clusters model.ServiceClusters) bool {
	if r.logCtx.GetRouteLogger().IsLevelEnabled(log.DebugLog) {
		// clusters 理论上由调用方保证非 nil，但 Enable 作为公开接口方法加上防御性判断，
		// 避免上游改动或单测直传 nil 时日志格式化触发 panic。
		var serviceKey interface{} = "<nil>"
		if clusters != nil {
			serviceKey = clusters.GetServiceKey()
		}
		groups := r.getLaneGroups(routeInfo)
		r.logCtx.GetRouteLogger().Debugf("[Router][Lane] Enable: service=%s, always-on=true, laneGroups=%d",
			serviceKey, len(groups))
	}
	return true
}

// getLaneGroups 从 routeInfo 中获取泳道规则列表。
//
// 同时合并 SourceLaneRule(caller)与 DestLaneRule(callee)两侧的 LaneGroups，
// 按 group name 去重且 caller 优先：
//   - Polaris Server naming cache 按 service 维度独立刷新，当 callee 被移出某泳道组后，
//     callee 侧 cache 可能滞后返回旧规则（provider 仍在组里），而 caller 侧 cache
//     已感知到规则变更。以 caller 先到先得的顺序合并可规避该 cache 滞后问题。
//   - 对齐 polaris-java 的 LaneUtils.fetchLaneRules + LaneRuleContainer 合并语义。
func (r *LaneRouter) getLaneGroups(routeInfo *servicerouter.RouteInfo) []*apitraffic.LaneGroup {
	if routeInfo == nil {
		return nil
	}

	seen := make(map[string]struct{})
	var merged []*apitraffic.LaneGroup

	var callerCount, calleeCount, dupCount int

	appendGroups := func(rule model.ServiceRule, side string) {
		if rule == nil {
			return
		}
		ruleValue := rule.GetValue()
		if ruleValue == nil {
			return
		}
		wrapper, ok := ruleValue.(*pb.LaneWrapper)
		if !ok || wrapper == nil {
			return
		}
		for _, group := range wrapper.LaneGroups {
			if group == nil {
				continue
			}
			name := group.GetName()
			if _, dup := seen[name]; dup {
				dupCount++
				continue
			}
			seen[name] = struct{}{}
			merged = append(merged, group)
			if side == "caller" {
				callerCount++
			} else {
				calleeCount++
			}
		}
	}

	// 顺序关键：caller 先，callee 后。
	appendGroups(routeInfo.SourceLaneRule, "caller")
	appendGroups(routeInfo.DestLaneRule, "callee")

	if r.logCtx.GetRouteLogger().IsLevelEnabled(log.DebugLog) {
		r.logCtx.GetRouteLogger().Debugf("[Router][Lane] getLaneGroups merged: caller=%d, callee=%d, "+
			"duplicated=%d, total=%d", callerCount, calleeCount, dupCount, len(merged))
	}

	if len(merged) == 0 {
		return nil
	}
	return merged
}

// findMatchedRule 在规则容器中查找当前请求匹配的泳道规则。
// 如果已染色则走 stainLabel 索引查找，否则按 TrafficMatchRule 识别流量。
// 未命中返回 nil。该函数从 GetFilteredInstances 中抽出，仅为降低主函数体积。
func (r *LaneRouter) findMatchedRule(
	container *laneRuleContainer,
	alreadyStained bool,
	stainLabel string,
	sourceService, destService model.ServiceMetadata,
) *laneRuleItem {
	debugEnabled := r.logCtx.GetRouteLogger().IsLevelEnabled(log.DebugLog)
	if alreadyStained {
		matchedItem := container.matchByStainLabel(stainLabel, sourceService, destService)
		if debugEnabled {
			if matchedItem != nil {
				r.logCtx.GetRouteLogger().Debugf(
					"[Router][Lane] matched by stain label %q → group=%s, rule=%s",
					stainLabel, matchedItem.group.GetName(), matchedItem.rule.GetName())
			} else {
				r.logCtx.GetRouteLogger().Debugf(
					"[Router][Lane] stain label %q not found in rule index", stainLabel)
			}
		}
		return matchedItem
	}
	matchedItem := container.matchByRouteInfo(sourceService)
	if debugEnabled {
		if matchedItem != nil {
			r.logCtx.GetRouteLogger().Debugf(
				"[Router][Lane] matched by traffic rule → group=%s, rule=%s, stainLabel=%s",
				matchedItem.group.GetName(), matchedItem.rule.GetName(), matchedItem.stainLabel)
		} else {
			r.logCtx.GetRouteLogger().Debugf("[Router][Lane] no traffic rule matched")
		}
	}
	return matchedItem
}

// GetFilteredInstances 泳道路由过滤入口
func (r *LaneRouter) GetFilteredInstances(
	routeInfo *servicerouter.RouteInfo,
	clusters model.ServiceClusters,
	withinCluster *model.Cluster,
) (*servicerouter.RouteResult, error) {
	laneGroups := r.getLaneGroups(routeInfo)
	if len(laneGroups) == 0 {
		// 服务不在任何泳道组下：按 BaseLaneMode 决定基线选取方式。
		//
		//  - OnlyUntaggedInstance（默认 mode=0）：未染色流量不应被分发到带 `lane` 标签的实例
		//    → 走 routeToBaseline，由其第一分支过滤掉带标签实例。
		//  - ExcludeEnabledLaneInstance（mode=1）：基线 = 实例的 `lane` 值不在“已启用规则集合”
		//    内的实例。当前 callee 不属于任何泳道组，已启用规则集合为空，没有任何 lane 值需要
		//    排除 → 所有实例都符合基线定义，直通全量返回。
		debugEnabled := r.logCtx.GetRouteLogger().IsLevelEnabled(log.DebugLog)
		if r.cfg.BaseLaneMode == ExcludeEnabledLaneInstance {
			if debugEnabled {
				sourceNs, sourceSvc := extractNsSvc(routeInfo.SourceService)
				destNs, destSvc := extractNsSvc(routeInfo.DestService)
				r.logCtx.GetRouteLogger().Debugf(
					"[Router][Lane] no lane groups for %s/%s (caller=%s/%s), "+
						"baseLaneMode=ExcludeEnabledLaneInstance → enabled lane values empty, "+
						"pass through all instances",
					destNs, destSvc, sourceNs, sourceSvc)
			}
			return r.passThroughResult(clusters, withinCluster), nil
		}
		if debugEnabled {
			sourceNs, sourceSvc := extractNsSvc(routeInfo.SourceService)
			destNs, destSvc := extractNsSvc(routeInfo.DestService)
			r.logCtx.GetRouteLogger().Debugf(
				"[Router][Lane] no lane groups for %s/%s (caller=%s/%s), "+
					"baseLaneMode=OnlyUntaggedInstance → fallback to baseline to filter out tagged instances",
				destNs, destSvc, sourceNs, sourceSvc)
		}
		return r.routeToBaseline(routeInfo, clusters, withinCluster, laneGroups, instanceLaneKey), nil
	}

	// 染色检测: api.go / api/consumer.go 的 convert() 已经把请求中的 Arguments
	// (含从上游 HTTP Header 透传来的 service-lane) 摊平到 SourceService.Metadata,
	// 因此这里只需要按 HEADER 前缀读 "$header.service-lane" 判断是否已染色。
	// 不再检测裸 key 或 $query/$cookie 来源, 跟业务约定的 HTTP Header 透传路径对齐。
	var srcMeta map[string]string
	if routeInfo.SourceService != nil {
		srcMeta = routeInfo.SourceService.GetMetadata()
	}
	stainLabel, alreadyStained := srcMeta[model.LabelKeyHeader+trafficStainLabel]

	sourceNs, sourceSvc := extractNsSvc(routeInfo.SourceService)
	destNs, destSvc := extractNsSvc(routeInfo.DestService)

	if r.logCtx.GetRouteLogger().IsLevelEnabled(log.DebugLog) {
		r.logCtx.GetRouteLogger().Debugf(
			"[Router][Lane] start routing, source=%s/%s, dest=%s/%s, laneGroups=%d, stainLabel=%q, "+
				"alreadyStained=%v", sourceNs, sourceSvc, destNs, destSvc, len(laneGroups), stainLabel, alreadyStained)
	}

	// 构建泳道规则容器
	container := newLaneRuleContainer(laneGroups, routeInfo.SourceService)

	if r.logCtx.GetRouteLogger().IsLevelEnabled(log.DebugLog) {
		r.logCtx.GetRouteLogger().Debugf(
			"[Router][Lane] built rule container, totalRules=%d, stainLabelIndexSize=%d",
			len(container.sortedItems), len(container.stainLabelIndex))
		for i, item := range container.sortedItems {
			r.logCtx.GetRouteLogger().Debugf(
				"[Router][Lane]   rule[%d]: group=%s, rule=%s, isEntry=%v, stainLabel=%s, "+
					"priority=%d, matchMode=%s",
				i, item.group.GetName(), item.rule.GetName(),
				item.isEntry, item.stainLabel,
				item.rule.GetPriority(), item.rule.GetMatchMode())
		}
	}

	// 查找匹配的规则
	matchedItem := r.findMatchedRule(container, alreadyStained, stainLabel,
		routeInfo.SourceService, routeInfo.DestService)

	// 无匹配规则 → 回退基线
	if matchedItem == nil {
		if r.logCtx.GetRouteLogger().IsLevelEnabled(log.DebugLog) {
			r.logCtx.GetRouteLogger().Debugf(
				"[Router][Lane] no rule matched, fallback to baseline, source=%s/%s, dest=%s/%s",
				sourceNs, sourceSvc, destNs, destSvc)
		}
		return r.routeToBaseline(routeInfo, clusters, withinCluster, laneGroups, instanceLaneKey), nil
	}

	// isEntry 门禁染色:对齐 polaris-java LaneUtils.tryStainCurrentTraffic.
	// 只有当调用方是该泳道组的流量入口 (gateway/service entry) 时才能决定染色;
	// 非入口的中间服务仅透传已有染色标签,不做二次染色决策,避免:
	//   1. 网关外的 middle-hop 把业务请求误染色进泳道;
	//   2. 多泳道组共享 defaultLabelValue 时,短格式标签在非入口端产生歧义匹配.
	isEntryHit := matchedItem.isEntry

	// 首次流量(alreadyStained=false)场景:
	//   - 非入口 → 非法染色点,直接走基线 (与 Java `redirectToBase(...)` 一致);
	//   - 入口 → 走 percentage/warmup 染色概率,命中则写完整 stain label 到 RouteMetadata.
	if !alreadyStained {
		if !isEntryHit {
			if r.logCtx.GetRouteLogger().IsLevelEnabled(log.DebugLog) {
				r.logCtx.GetRouteLogger().Debugf(
					"[Router][Lane] caller %s/%s is NOT traffic entry of group %q, fallback to baseline",
					sourceNs, sourceSvc, matchedItem.group.GetName())
			}
			return r.routeToBaseline(routeInfo, clusters, withinCluster, laneGroups, instanceLaneKey), nil
		}
		// 入口染色概率检查 (percentage / warmup)
		if !r.tryStainCurrentTraffic(matchedItem.rule) {
			r.logCtx.GetRouteLogger().Infof(
				"[Router][Lane] traffic gray check rejected (not stained), group=%s, rule=%s, "+
					"grayMode=%s, fallback to baseline",
				matchedItem.group.GetName(), matchedItem.rule.GetName(),
				matchedItem.rule.GetTrafficGray().GetMode())
			return r.routeToBaseline(routeInfo, clusters, withinCluster, laneGroups, instanceLaneKey), nil
		}
		// 染色成功:把完整 stain label ({groupName}/{ruleName}) 写回 RouteMetadata.
		// 业务网关通过 InstancesResponse.RouteMetadata["service-lane"] 读取后以 HTTP Header
		// 形式透传给下游服务,下游 SDK 的 lane router 按精确匹配 (stainLabelIndex) 直接命中
		fullStainLabel := matchedItem.stainLabel
		routeInfo.SetRouteMetadata(trafficStainLabel, fullStainLabel)
		if r.logCtx.GetRouteLogger().IsLevelEnabled(log.DebugLog) {
			r.logCtx.GetRouteLogger().Debugf(
				"[Router][Lane] first-time stain at entry: %s=%s → RouteMetadata (for caller passthrough)",
				trafficStainLabel, fullStainLabel)
		}
	} else {
		// 已染色场景:无论是否入口都透传,保持链路染色一致性.
		// Java 实现对应 `calleeMsgContainer.setHeader(TRAFFIC_STAIN_LABEL, stainLabel, PASS_THROUGH)`.
		// 注意:透传的是上游给的原始 stainLabel (可能是完整格式或短格式),调用方 (gateway)
		// 读到什么就原样透传什么,不做格式转换,避免链路中途把完整格式降级回短格式.
		routeInfo.SetRouteMetadata(trafficStainLabel, stainLabel)
		if r.logCtx.GetRouteLogger().IsLevelEnabled(log.DebugLog) {
			r.logCtx.GetRouteLogger().Debugf(
				"[Router][Lane] passthrough stain label: %s=%s (matched group=%s, rule=%s)",
				trafficStainLabel, stainLabel,
				matchedItem.group.GetName(), matchedItem.rule.GetName())
		}
	}

	// 目标服务不在此泳道组的 destinations 中 → 回退基线
	if !checkServiceInLane(matchedItem.group, routeInfo.DestService) {
		r.logCtx.GetRouteLogger().Infof(
			"[Router][Lane] dest service %s/%s not in lane group %q destinations, fallback to baseline",
			destNs, destSvc, matchedItem.group.GetName())
		return r.routeToBaseline(routeInfo, clusters, withinCluster, laneGroups, instanceLaneKey), nil
	}

	laneKey := getLaneKey(matchedItem.rule)
	laneVal := matchedItem.rule.GetDefaultLabelValue()

	// 尝试路由到泳道实例
	// 使用 metadata 精确匹配筛选泳道实例。
	tmpLaneCls := model.NewCluster(clusters, withinCluster)
	tmpLaneCls.AddMetadata(laneKey, laneVal)
	tmpLaneCls.ReloadComposeMetaValue()
	laneInstSet := tmpLaneCls.GetClusterValue().GetInstancesSet(false, true)

	if laneInstSet.Count() > 0 {
		if r.logCtx.GetRouteLogger().IsLevelEnabled(log.DebugLog) {
			r.logCtx.GetRouteLogger().Debugf(
				"[Router][Lane] route to lane, group=%s, rule=%s, %s=%s, instances=%d, dest=%s/%s",
				matchedItem.group.GetName(), matchedItem.rule.GetName(),
				laneKey, laneVal, laneInstSet.Count(), destNs, destSvc)
		}
		result := servicerouter.PoolGetRouteResult(r.valueCtx)
		// 直接返回带 lane metadata 的 cluster，其 GetClusterValue() 已缓存正确的泳道实例集。
		// 设置 ignoreFilterOnlyOnEndChain 阻止 filterOnly 重建 cluster。
		routeInfo.SetIgnoreFilterOnlyOnEndChain(true)
		result.OutputCluster = tmpLaneCls
		result.Status = servicerouter.Normal
		return result, nil
	}

	// 无泳道实例
	if matchedItem.rule.GetMatchMode() == apitraffic.LaneRule_STRICT {
		r.logCtx.GetRouteLogger().Infof(
			"[Router][Lane] no lane instances for %s=%s (STRICT mode)，"+
				"return empty cluster to trigger HTTP 503，group=%s，rule=%s，dest=%s/%s",
			laneKey, laneVal, matchedItem.group.GetName(), matchedItem.rule.GetName(),
			destNs, destSvc)
		// STRICT 模式：SDK 语义要求"没有匹配的泳道实例时直接视为没有可用实例"。
		// 返回上面 metadata 过滤后的空 cluster（tmpLaneCls 的 ClusterValue 实例数=0），
		// 同时设置 ignoreFilterOnlyOnEndChain=true 阻止下游 filterOnly 基于原始 clusters
		// 重建出一个非空 cluster，否则 STRICT 会被意外降级成"全量实例"而丧失隔离性。
		// 外部调用方（LoadBalancer / HTTP gateway）看到空实例后会返回 HTTP 503，
		// 这正是 lane-test.sh 用例 1.4b/2.4b/3.4b/4.4b 的期望行为。
		//
		// ⚠ OutputCluster 生命周期说明：
		// tmpLaneCls 在被赋给 result.OutputCluster 后，不在此函数内 PoolPut。
		// 归还责任由上层调用方（InstancesResponse Finalize 流程）承担，与
		// routeToBaseline 的约定保持一致。因此函数开头的 `defer tmpLaneCls.PoolPut()`
		// 必须放在 STRICT 分支 return 之后、PERMISSIVE 分支之前（见下方）。
		routeInfo.SetIgnoreFilterOnlyOnEndChain(true)
		result := servicerouter.PoolGetRouteResult(r.valueCtx)
		result.OutputCluster = tmpLaneCls
		result.Status = servicerouter.DegradeToFilterOnly
		return result, nil
	}
	// PERMISSIVE 分支会走 routeToBaseline 构造新的 cluster，不再需要 tmpLaneCls。
	// 在 STRICT 早退之后再 PoolPut，避免 STRICT 路径误回收仍在对外输出的对象。
	defer tmpLaneCls.PoolPut()

	// PERMISSIVE 模式：回退基线
	r.logCtx.GetRouteLogger().Infof(
		"[Router][Lane] no lane instances for %s=%s (PERMISSIVE mode), "+
			"fallback to baseline, group=%s, rule=%s, dest=%s/%s",
		laneKey, laneVal, matchedItem.group.GetName(), matchedItem.rule.GetName(),
		destNs, destSvc)
	return r.routeToBaseline(routeInfo, clusters, withinCluster, laneGroups, laneKey), nil
}

// passThroughResult 直通（不过滤）结果。
// 当 callee 不在任何泳道组下且 BaseLaneMode=ExcludeEnabledLaneInstance 时，
// 已启用规则集合为空，没有任何 lane 值需要排除，所有实例都视为基线，直通返回。
func (r *LaneRouter) passThroughResult(clusters model.ServiceClusters, withinCluster *model.Cluster) *servicerouter.RouteResult {
	result := servicerouter.PoolGetRouteResult(r.valueCtx)
	result.OutputCluster = model.NewCluster(clusters, withinCluster)
	result.Status = servicerouter.Normal
	return result
}

// routeToBaseline 路由到基线实例
// laneKey 为当前泳道规则使用的实例元数据 key
//
// ⚠ OutputCluster 生命周期说明：
// 此函数把构造的 tmpCls / baselineCls 赋给 result.OutputCluster 直接返回，
// 不在本函数归还对象池。归还责任由上层调用方承担：
//   - RouteResult 经 pkg/flow/sync_flow.go 逐层向上传递，最终由 InstancesResponse
//     的 Finalize 流程统一回收（参见 PoolGetRouteResult / GetRouteResultPool 的使用者）。
//   - 若未来在本函数中间加入 early return 且未设置 OutputCluster，则必须显式 tmpCls.PoolPut()
//     避免泄漏。
func (r *LaneRouter) routeToBaseline(
	routeInfo *servicerouter.RouteInfo,
	clusters model.ServiceClusters,
	withinCluster *model.Cluster,
	laneGroups []*apitraffic.LaneGroup,
	laneKey string,
) *servicerouter.RouteResult {
	result := servicerouter.PoolGetRouteResult(r.valueCtx)

	// 优先选取无泳道 key 的实例（对应 OnlyUntaggedInstance 语义）
	tmpCls := model.NewCluster(clusters, withinCluster)
	tmpCls.AddMetadata(laneKey, "")
	tmpCls.ReloadComposeMetaValue()
	notTaggedInstSet := tmpCls.GetNotContainMetaKeyClusterValue().GetInstancesSet(false, true)
	if notTaggedInstSet.Count() > 0 {
		r.logCtx.GetRouteLogger().Debugf(
			"[Router][Lane] baseline: found %d instances without %q key",
			notTaggedInstSet.Count(), laneKey)
		// 直接返回 tmpCls，其 value 已通过 GetNotContainMetaKeyClusterValue() 设置。
		// 设置 ignoreFilterOnlyOnEndChain 阻止下游 filterOnly 重建 cluster 并丢失过滤结果。
		routeInfo.SetIgnoreFilterOnlyOnEndChain(true)
		result.OutputCluster = tmpCls
		result.Status = servicerouter.Normal
		return result
	}
	tmpCls.PoolPut()

	// ExcludeEnabledLaneInstance 模式：排除元数据值命中"已启用泳道规则集合"的实例,其余作为基线。
	//
	// 实现借助 Cluster 原生的 containNotMatchMetadata 语义:
	//   - 对 Cluster 添加 `laneKey=excludedVal1`, `laneKey=excludedVal2` ... 多条 metadata
	//     (MetaCount > 1 下,同一 key 下的多个 value 在 containNotMatchMetadata 中按"并集"处理);
	//   - 调用 GetContainNotMatchMetaKeyClusterValue 得到"包含 laneKey 但 value 不在 excluded 集合内"
	//     的实例集,正是 mode=1 期望的基线子集。
	//   - 这样复用 Cluster 机制既能命中 verifyCluster 的 revision 一致性检查,又不会被后续
	//     主链 filterOnly 基于原始 clusters 重建覆盖。
	if r.cfg.BaseLaneMode == ExcludeEnabledLaneInstance {
		enabledVals := buildEnabledLaneValues(laneGroups)
		if excluded, hasKey := enabledVals[laneKey]; hasKey && len(excluded) > 0 {
			excludeCls := model.NewCluster(clusters, withinCluster)
			for excludedVal := range excluded {
				excludeCls.AddMetadata(laneKey, excludedVal)
			}
			excludeCls.ReloadComposeMetaValue()
			baselineSet := excludeCls.GetContainNotMatchMetaKeyClusterValue().GetInstancesSet(false, true)
			if baselineSet.Count() > 0 {
				if r.logCtx.GetRouteLogger().IsLevelEnabled(log.DebugLog) {
					r.logCtx.GetRouteLogger().Debugf(
						"[Router][Lane] baseline (ExcludeEnabledLaneInstance): laneKey=%s excluded=%v, "+
							"%d instances survive",
						laneKey, excluded, baselineSet.Count())
				}
				// 设置 ignoreFilterOnlyOnEndChain,阻止后续主链 filterOnly 基于原始 clusters
				// 重建 cluster 并冲掉过滤结果。
				routeInfo.SetIgnoreFilterOnlyOnEndChain(true)
				result.OutputCluster = excludeCls
				result.Status = servicerouter.Normal
				return result
			}
			excludeCls.PoolPut()
		}
	}

	// 兜底：返回所有实例
	r.logCtx.GetRouteLogger().Debugf(
		"[Router][Lane] baseline: no dedicated baseline instances found, returning all instances")
	result.OutputCluster = model.NewCluster(clusters, withinCluster)
	result.Status = servicerouter.Normal
	return result
}

// init 注册插件
func init() {
	plugin.RegisterConfigurablePlugin(&LaneRouter{}, &Config{})
}
