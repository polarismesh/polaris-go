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

package lane

import (
	"math"
	"math/rand"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
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
	// trafficStainLabel 流量染色标签的 key（在 EnvironmentVariables 中传递）
	trafficStainLabel = "service-lane"
	// instanceLaneKey 实例元数据中表示泳道归属的默认 key
	instanceLaneKey = "lane"
	// gatewayEntryType 网关类型流量入口的 type 值
	gatewayEntryType = "polarismesh.cn/gateway/spring-cloud-gateway"
	// serviceEntryType 服务类型流量入口的 type 值
	serviceEntryType = "polarismesh.cn/service"
)

// regexCache 缓存已编译的正则表达式，避免高频路由路径上重复编译
var regexCache sync.Map // map[string]*regexp.Regexp

// getCompiledRegex 获取编译后的正则，命中缓存直接返回
func getCompiledRegex(pattern string) (*regexp.Regexp, error) {
	if v, ok := regexCache.Load(pattern); ok {
		return v.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	actual, _ := regexCache.LoadOrStore(pattern, re)
	return actual.(*regexp.Regexp), nil
}

// laneRuleItem 用于排序的泳道规则条目
type laneRuleItem struct {
	group      *apitraffic.LaneGroup
	rule       *apitraffic.LaneRule
	isEntry    bool
	stainLabel string
}

// laneRuleContainer 泳道规则容器，维护排序后的规则列表以及快速查找索引
type laneRuleContainer struct {
	// sortedItems 按优先级排序后的规则条目（流量入口优先）
	sortedItems []*laneRuleItem
	// stainLabelIndex stainLabel -> laneRuleItem 快速查找
	stainLabelIndex map[string]*laneRuleItem
}

// newLaneRuleContainer 创建泳道规则容器
// 排序规则：流量入口(isEntry=true) 优先 → priority 升序 → ctime 升序
func newLaneRuleContainer(groups []*apitraffic.LaneGroup, sourceService model.ServiceMetadata) *laneRuleContainer {
	c := &laneRuleContainer{
		stainLabelIndex: make(map[string]*laneRuleItem),
	}
	for _, group := range groups {
		for _, rule := range group.GetRules() {
			if !rule.GetEnable() {
				continue
			}
			item := &laneRuleItem{
				group:      group,
				rule:       rule,
				isEntry:    isTrafficEntry(group.GetEntries(), sourceService),
				stainLabel: buildStainLabel(rule),
			}
			c.sortedItems = append(c.sortedItems, item)
			c.stainLabelIndex[item.stainLabel] = item
		}
	}
	sort.SliceStable(c.sortedItems, func(i, j int) bool {
		a, b := c.sortedItems[i], c.sortedItems[j]
		if a.isEntry != b.isEntry {
			return a.isEntry
		}
		if a.rule.GetPriority() != b.rule.GetPriority() {
			return a.rule.GetPriority() < b.rule.GetPriority()
		}
		return a.rule.GetCtime() < b.rule.GetCtime()
	})
	return c
}

// matchByStainLabel 根据染色标签直接查找规则。
// 支持两种格式：
//  1. 完整格式 "{groupName}/{ruleName}" — 优先在 stainLabelIndex 中精确匹配
//  2. 短格式 "{laneValue}" — 当精确匹配失败时，回退到匹配 rule.DefaultLabelValue
//
// 短格式用于兼容网关流量匹配场景：网关通过 TrafficMatchRule 确定泳道后，只能从实例元数据
// 中获取 lane 值（如 "gray"），无法构造完整的 stainLabel。下游服务收到短格式后，
// 通过 defaultLabelValue 匹配即可正确路由到对应泳道。
func (c *laneRuleContainer) matchByStainLabel(stainLabel string) *laneRuleItem {
	// 1. 精确匹配（完整格式）
	if item := c.stainLabelIndex[stainLabel]; item != nil {
		return item
	}
	// 2. 短格式回退：遍历规则，匹配 defaultLabelValue
	for _, item := range c.sortedItems {
		if item.rule.GetDefaultLabelValue() == stainLabel {
			return item
		}
	}
	return nil
}

// matchByRouteInfo 根据路由信息匹配规则（流量识别阶段）
// 只对流量入口（isEntry=true）的规则做 TrafficMatchRule 匹配，
// 非入口服务不应做首次流量识别，只响应上游已透传的染色标签。
func (c *laneRuleContainer) matchByRouteInfo(envVars map[string]string, sourceService model.ServiceMetadata) *laneRuleItem {
	for _, item := range c.sortedItems {
		if !item.isEntry {
			continue
		}
		if matchTrafficRule(item.rule.GetTrafficMatchRule(), envVars, sourceService) {
			return item
		}
	}
	return nil
}

// buildStainLabel 构建染色标签：{groupName}/{ruleName}
func buildStainLabel(rule *apitraffic.LaneRule) string {
	return rule.GetGroupName() + "/" + rule.GetName()
}

// isTrafficEntry 判断当前调用方是否为该泳道组的流量入口
func isTrafficEntry(entries []*apitraffic.TrafficEntry, sourceService model.ServiceMetadata) bool {
	if sourceService == nil {
		return false
	}
	routeLogger := log.GetRouteLogger()
	debugEnabled := routeLogger.IsLevelEnabled(log.DebugLog)
	for _, entry := range entries {
		switch entry.GetType() {
		case gatewayEntryType:
			sel := &apitraffic.ServiceGatewaySelector{}
			if err := entry.GetSelector().UnmarshalTo(sel); err != nil {
				if debugEnabled {
					routeLogger.Debugf("[Router][Lane] isTrafficEntry: failed to unmarshal gateway selector, err=%v", err)
				}
				continue
			}
			svcMatched := matchSelectorService(sel.GetNamespace(), sel.GetService(), sourceService)
			labelMatched := matchSelectorLabels(sel.GetLabels(), sourceService.GetMetadata())
			if debugEnabled {
				routeLogger.Debugf("[Router][Lane] isTrafficEntry: gateway entry check, "+
					"selector=%s/%s, source=%s/%s, svcMatched=%v, labelMatched=%v",
					sel.GetNamespace(), sel.GetService(),
					sourceService.GetNamespace(), sourceService.GetService(),
					svcMatched, labelMatched)
			}
			if svcMatched && labelMatched {
				return true
			}
		case serviceEntryType:
			sel := &apitraffic.ServiceSelector{}
			if err := entry.GetSelector().UnmarshalTo(sel); err != nil {
				if debugEnabled {
					routeLogger.Debugf("[Router][Lane] isTrafficEntry: failed to unmarshal service selector, err=%v", err)
				}
				continue
			}
			svcMatched := matchSelectorService(sel.GetNamespace(), sel.GetService(), sourceService)
			labelMatched := matchSelectorLabels(sel.GetLabels(), sourceService.GetMetadata())
			if debugEnabled {
				routeLogger.Debugf("[Router][Lane] isTrafficEntry: service entry check, "+
					"selector=%s/%s, source=%s/%s, svcMatched=%v, labelMatched=%v",
					sel.GetNamespace(), sel.GetService(),
					sourceService.GetNamespace(), sourceService.GetService(),
					svcMatched, labelMatched)
			}
			if svcMatched && labelMatched {
				return true
			}
		default:
			// 未知入口类型，跳过
			routeLogger.Warnf("[Router][Lane] isTrafficEntry: unknown entry type %q, skipped",
				entry.GetType())
		}
	}
	return false
}

// matchSelectorService 匹配 selector 中的 namespace/service 是否和调用方一致
func matchSelectorService(ns, svc string, sourceService model.ServiceMetadata) bool {
	if ns != "" && ns != sourceService.GetNamespace() {
		return false
	}
	if svc != "" && svc != sourceService.GetService() {
		return false
	}
	return true
}

// matchSelectorLabels 匹配 selector 标签
func matchSelectorLabels(labels map[string]*apimodel.MatchString, metadata map[string]string) bool {
	if len(labels) == 0 {
		return true
	}
	if metadata == nil {
		return false
	}
	for k, matchStr := range labels {
		v, ok := metadata[k]
		if !ok {
			return false
		}
		if !matchStringValue(matchStr, v) {
			return false
		}
	}
	return true
}

// matchTrafficRule 匹配 TrafficMatchRule。
// 无规则或无匹配参数时返回 false（不匹配），避免空规则意外染色所有流量。
func matchTrafficRule(rule *apitraffic.TrafficMatchRule, envVars map[string]string, sourceService model.ServiceMetadata) bool {
	if rule == nil {
		return false
	}
	args := rule.GetArguments()
	if len(args) == 0 {
		return false
	}
	routeLogger := log.GetRouteLogger()
	debugEnabled := routeLogger.IsLevelEnabled(log.DebugLog)
	isAND := rule.GetMatchMode() == apitraffic.TrafficMatchRule_AND
	for i, arg := range args {
		trafficValue := findTrafficValue(arg, envVars, sourceService)
		matched := matchStringValue(arg.GetValue(), trafficValue)
		if debugEnabled {
			routeLogger.Debugf("[Router][Lane] matchTrafficRule: arg[%d] type=%s, key=%q, "+
				"expected=%q(%s), actual=%q, matched=%v, mode=%s",
				i, arg.GetType(), arg.GetKey(),
				arg.GetValue().GetValue().GetValue(), arg.GetValue().GetType(),
				trafficValue, matched, rule.GetMatchMode())
		}
		if isAND && !matched {
			return false
		}
		if !isAND && matched {
			return true
		}
	}
	// AND 模式全部匹配返回 true；OR 模式全部不匹配返回 false
	return isAND
}

// findTrafficValue 根据 SourceMatch 类型从环境变量或调用方元数据中提取流量值.
//
// 与 polaris-java LaneUtils.findTrafficValue (polaris-plugins/polaris-plugins-router/
// router-lane/src/main/java/com/tencent/polaris/plugins/router/lane/LaneUtils.java) 保持
// 相同的 7 类匹配维度: HEADER / CUSTOM / METHOD / CALLER_IP / COOKIE / QUERY / PATH,
// 外加 Go 特有的 CALLER_METADATA (从 SourceService.Metadata 直接取).
//
// 查询策略:
//  1. 按维度类型拼出带前缀的 label key (如 "$header.user" / "$method" / "$caller_ip"),
//     先从 envVars 查;
//  2. 查不到时 fallback 到 sourceService.Metadata 的同一 key (api.go / api/consumer.go
//     的 convert() 已经用 Argument.ToLabels 保证了同样的前缀约定);
//  3. 都没有则返回 "".
//
// 修复:
//   - METHOD / CALLER_IP / PATH 三类 Argument 的 Key() 为空串, 以前被错误地按
//     envVars[""] 写入又按 envVars["method"] / envVars["caller_ip"] 读取, 永远读不到.
//   - HEADER / QUERY / COOKIE 以前都按短 key (arg.Key()) 入 envVars, "$header.user" 和
//     "$query.user" 之间互撞. 现在各自按前缀命名空间存取, 6 个维度相互独立.
func findTrafficValue(arg *apitraffic.SourceMatch, envVars map[string]string, sourceService model.ServiceMetadata) string {
	lookup := func(labelKey string) string {
		if labelKey == "" {
			return ""
		}
		if v, ok := envVars[labelKey]; ok && v != "" {
			return v
		}
		if sourceService != nil {
			meta := sourceService.GetMetadata()
			if v, ok := meta[labelKey]; ok {
				return v
			}
		}
		return ""
	}

	switch arg.GetType() {
	case apitraffic.SourceMatch_HEADER:
		return lookup(model.LabelKeyHeader + arg.GetKey())
	case apitraffic.SourceMatch_QUERY:
		return lookup(model.LabelKeyQuery + arg.GetKey())
	case apitraffic.SourceMatch_COOKIE:
		return lookup(model.LabelKeyCookie + arg.GetKey())
	case apitraffic.SourceMatch_METHOD:
		return lookup(model.LabelKeyMethod)
	case apitraffic.SourceMatch_CALLER_IP:
		return lookup(model.LabelKeyCallerIp)
	case apitraffic.SourceMatch_PATH:
		return lookup(model.LabelKeyPath)
	case apitraffic.SourceMatch_CUSTOM:
		// CUSTOM 维度的 Key 由用户自定义, 不套前缀 (与 Argument.ToLabels 的 Custom 分支一致).
		return lookup(arg.GetKey())
	case apitraffic.SourceMatch_CALLER_METADATA:
		if sourceService != nil {
			return sourceService.GetMetadata()[arg.GetKey()]
		}
		return ""
	default:
		// 未知类型兜底: 与 CUSTOM 同策略, 按原始 key 直查, 避免引入隐式默认行为.
		return lookup(arg.GetKey())
	}
}

// matchStringValue 匹配 MatchString 规则
func matchStringValue(ms *apimodel.MatchString, value string) bool {
	if ms == nil {
		return true
	}
	pattern := ms.GetValue().GetValue()
	switch ms.GetType() {
	case apimodel.MatchString_EXACT:
		return value == pattern
	case apimodel.MatchString_NOT_EQUALS:
		return value != pattern
	case apimodel.MatchString_REGEX:
		re, err := getCompiledRegex(pattern)
		if err != nil {
			return false
		}
		return re.MatchString(value)
	case apimodel.MatchString_IN:
		for _, part := range strings.Split(pattern, ",") {
			if strings.TrimSpace(part) == value {
				return true
			}
		}
		return false
	case apimodel.MatchString_NOT_IN:
		for _, part := range strings.Split(pattern, ",") {
			if strings.TrimSpace(part) == value {
				return false
			}
		}
		return true
	default:
		return value == pattern
	}
}

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

// parseWarmupEtime 解析规则启用时间字符串，返回 SDK 本地时区的 time.Time。
//
// Polaris 服务端返回的 etime 有两种常见格式：
//   - RFC3339（带时区，如 "2026-04-21T19:14:30+08:00"）—— 可直接 time.Parse
//   - 无时区的本地时间字符串（如 "2026-04-21 19:14:30"）—— 必须用 ParseInLocation
//     以 time.Local 解析，否则 Go 默认按 UTC 解析会把 SDK 所在本地时区的时间当成 UTC，
//     导致 uptime = now(本地) - startTime(被当 UTC) = 时差 × -1（如 CST 为 -8h），
//     永远 <= 0，tryStainByWarmup 永不染色。
//
// 无法解析时返回零值，调用方应回退到 time.Now()。
func parseWarmupEtime(etimeStr string) time.Time {
	if etimeStr == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, etimeStr); err == nil {
		return t
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
		if t, err := time.ParseInLocation(layout, etimeStr, time.Local); err == nil {
			return t
		}
	}
	return time.Time{}
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

// checkServiceInLane 检查目标服务是否在泳道组的 destinations 中
func checkServiceInLane(group *apitraffic.LaneGroup, destService model.ServiceMetadata) bool {
	if destService == nil {
		return false
	}
	routeLogger := log.GetRouteLogger()
	debugEnabled := routeLogger.IsLevelEnabled(log.DebugLog)
	dests := group.GetDestinations()
	for i, dest := range dests {
		destNs := dest.GetNamespace()
		destSvc := dest.GetService()
		if destNs != "" && destNs != destService.GetNamespace() {
			if debugEnabled {
				routeLogger.Debugf("[Router][Lane] checkServiceInLane: group=%s dest[%d] ns mismatch, "+
					"ruleNs=%q, actualNs=%q", group.GetName(), i, destNs, destService.GetNamespace())
			}
			continue
		}
		if destSvc != "" && destSvc != destService.GetService() {
			if debugEnabled {
				routeLogger.Debugf("[Router][Lane] checkServiceInLane: group=%s dest[%d] svc mismatch, "+
					"ruleSvc=%q, actualSvc=%q", group.GetName(), i, destSvc, destService.GetService())
			}
			continue
		}
		if debugEnabled {
			routeLogger.Debugf("[Router][Lane] checkServiceInLane: group=%s matched dest[%d]=%s/%s",
				group.GetName(), i, destNs, destSvc)
		}
		return true
	}
	if debugEnabled {
		routeLogger.Debugf("[Router][Lane] checkServiceInLane: group=%s no dest matched, "+
			"destsCount=%d, target=%s/%s",
			group.GetName(), len(dests), destService.GetNamespace(), destService.GetService())
	}
	return false
}

// getLaneKey 获取泳道规则对应的实例元数据 key
func getLaneKey(rule *apitraffic.LaneRule) string {
	if k := rule.GetLabelKey(); k != "" {
		return k
	}
	return instanceLaneKey
}

// buildEnabledLaneValues 构建所有已启用泳道规则的 defaultLabelValue 集合
// 返回：map[laneMetaKey]set(enabledLaneValue)
func buildEnabledLaneValues(groups []*apitraffic.LaneGroup) map[string]map[string]struct{} {
	result := make(map[string]map[string]struct{})
	for _, group := range groups {
		for _, rule := range group.GetRules() {
			if !rule.GetEnable() {
				continue
			}
			laneKey := getLaneKey(rule)
			laneVal := rule.GetDefaultLabelValue()
			if laneVal == "" {
				continue
			}
			if result[laneKey] == nil {
				result[laneKey] = make(map[string]struct{})
			}
			result[laneKey][laneVal] = struct{}{}
		}
	}
	return result
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

// Enable 是否需要启动泳道路由
func (r *LaneRouter) Enable(routeInfo *servicerouter.RouteInfo, clusters model.ServiceClusters) bool {
	groups := r.getLaneGroups(routeInfo)
	enabled := groups != nil
	if r.logCtx.GetRouteLogger().IsLevelEnabled(log.DebugLog) {
		r.logCtx.GetRouteLogger().Debugf("[Router][Lane] Enable: service=%s, enabled=%v, laneGroups=%d",
			clusters.GetServiceKey(), enabled, len(groups))
	}
	return enabled
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

// GetFilteredInstances 泳道路由过滤入口
func (r *LaneRouter) GetFilteredInstances(
	routeInfo *servicerouter.RouteInfo,
	clusters model.ServiceClusters,
	withinCluster *model.Cluster,
) (*servicerouter.RouteResult, error) {
	laneGroups := r.getLaneGroups(routeInfo)
	if len(laneGroups) == 0 {
		r.logCtx.GetRouteLogger().Debugf("[Router][Lane] no lane groups found, pass through")
		return r.passThroughResult(clusters, withinCluster), nil
	}

	envVars := routeInfo.EnvironmentVariables
	stainLabel := ""
	if envVars != nil {
		// 染色标签优先级:
		// 1) routeInfo.EnvironmentVariables[trafficStainLabel]: 上游 lane router 首次染色后主动写回的裸 key;
		// 2) envVars[$header.service-lane]: 外部(gateway / consumer)把请求头透传成 HeaderArgument,
		//    经 ToLabels 落到 "$header.service-lane" 这个带前缀的 key;
		// 3) envVars[$query.service-lane] / $cookie.service-lane: 兼容把染色标签放到 query / cookie 的
		//    非主流用法, 覆盖 Java LaneUtils 里 message container 多来源的语义。
		if v := envVars[trafficStainLabel]; v != "" {
			stainLabel = v
		} else if v := envVars[model.LabelKeyHeader+trafficStainLabel]; v != "" {
			stainLabel = v
		} else if v := envVars[model.LabelKeyQuery+trafficStainLabel]; v != "" {
			stainLabel = v
		} else if v := envVars[model.LabelKeyCookie+trafficStainLabel]; v != "" {
			stainLabel = v
		}
	}
	alreadyStained := stainLabel != ""

	sourceNs, sourceSvc := "", ""
	if routeInfo.SourceService != nil {
		sourceNs = routeInfo.SourceService.GetNamespace()
		sourceSvc = routeInfo.SourceService.GetService()
	}
	destNs, destSvc := "", ""
	if routeInfo.DestService != nil {
		destNs = routeInfo.DestService.GetNamespace()
		destSvc = routeInfo.DestService.GetService()
	}

	if r.logCtx.GetRouteLogger().IsLevelEnabled(log.DebugLog) {
		r.logCtx.GetRouteLogger().Debugf(
			"[Router][Lane] start routing, source=%s/%s, dest=%s/%s, laneGroups=%d, "+
				"stainLabel=%q, alreadyStained=%v, envVars=%v",
			sourceNs, sourceSvc, destNs, destSvc,
			len(laneGroups), stainLabel, alreadyStained, envVars)
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
	var matchedItem *laneRuleItem
	if alreadyStained {
		matchedItem = container.matchByStainLabel(stainLabel)
		if r.logCtx.GetRouteLogger().IsLevelEnabled(log.DebugLog) {
			if matchedItem != nil {
				r.logCtx.GetRouteLogger().Debugf(
					"[Router][Lane] matched by stain label %q → group=%s, rule=%s",
					stainLabel, matchedItem.group.GetName(), matchedItem.rule.GetName())
			} else {
				r.logCtx.GetRouteLogger().Debugf(
					"[Router][Lane] stain label %q not found in rule index", stainLabel)
			}
		}
	} else {
		matchedItem = container.matchByRouteInfo(envVars, routeInfo.SourceService)
		if r.logCtx.GetRouteLogger().IsLevelEnabled(log.DebugLog) {
			if matchedItem != nil {
				r.logCtx.GetRouteLogger().Debugf(
					"[Router][Lane] matched by traffic rule → group=%s, rule=%s, stainLabel=%s",
					matchedItem.group.GetName(), matchedItem.rule.GetName(), matchedItem.stainLabel)
			} else {
				r.logCtx.GetRouteLogger().Debugf("[Router][Lane] no traffic rule matched")
			}
		}
	}

	// 无匹配规则 → 回退基线
	if matchedItem == nil {
		if r.logCtx.GetRouteLogger().IsLevelEnabled(log.DebugLog) {
			r.logCtx.GetRouteLogger().Debugf(
				"[Router][Lane] no rule matched, fallback to baseline, source=%s/%s, dest=%s/%s",
				sourceNs, sourceSvc, destNs, destSvc)
		}
		return r.routeToBaseline(routeInfo, clusters, withinCluster, laneGroups, instanceLaneKey), nil
	}

	// 灰度染色判断（仅在首次染色时执行）
	if !alreadyStained && !r.tryStainCurrentTraffic(matchedItem.rule) {
		r.logCtx.GetRouteLogger().Infof(
			"[Router][Lane] traffic gray check rejected (not stained), group=%s, rule=%s, "+
				"grayMode=%s, fallback to baseline",
			matchedItem.group.GetName(), matchedItem.rule.GetName(),
			matchedItem.rule.GetTrafficGray().GetMode())
		return r.routeToBaseline(routeInfo, clusters, withinCluster, laneGroups, instanceLaneKey), nil
	}

	// 首次染色成功后，将完整 stainLabel 写回 EnvironmentVariables，
	// 供上层（如 Gateway/Consumer）读取并在下游请求头中透传。
	if !alreadyStained {
		if routeInfo.EnvironmentVariables == nil {
			routeInfo.EnvironmentVariables = make(map[string]string, 1)
		}
		routeInfo.EnvironmentVariables[trafficStainLabel] = matchedItem.stainLabel
		if r.logCtx.GetRouteLogger().IsLevelEnabled(log.DebugLog) {
			r.logCtx.GetRouteLogger().Debugf(
				"[Router][Lane] stain label set: %s=%s (first-time stain)",
				trafficStainLabel, matchedItem.stainLabel)
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

// passThroughResult 直通（不过滤）结果
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
