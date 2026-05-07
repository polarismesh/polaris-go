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
	"regexp"
	"sync"
	"time"

	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
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

// extractNsSvc 从 ServiceMetadata 中提取 namespace 与 service 名,nil 安全。
func extractNsSvc(svc model.ServiceMetadata) (string, string) {
	if svc == nil {
		return "", ""
	}
	return svc.GetNamespace(), svc.GetService()
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

// findTrafficValue 根据 SourceMatch 类型从调用方元数据中提取流量值.
//
// 与 polaris-java LaneUtils.findTrafficValue (polaris-plugins/polaris-plugins-router/
// router-lane/src/main/java/com/tencent/polaris/plugins/router/lane/LaneUtils.java) 保持
// 相同的 7 类匹配维度: HEADER / CUSTOM / METHOD / CALLER_IP / COOKIE / QUERY / PATH,
// 外加 Go 特有的 CALLER_METADATA (从 SourceService.Metadata 直接取 arg.Key).
//
// 查询策略:
//
//	api.go / api/consumer.go 的 convert() 已经用 Argument.ToLabels 把 Arguments
//	摊平到 SourceService.Metadata 的带前缀 label key (如 "$header.user" / "$method"
//	/ "$caller_ip"), 因此这里 lane router 和 rulebase 一样, 只读 SourceService.Metadata,
//	按 SourceMatch 类型拼出同样的前缀 key 回查即可。
//
// 类型驱动的好处:
//   - METHOD / CALLER_IP / PATH 三类匹配维度直接用固定 label key (arg.Key 为空), 不会相互覆盖;
//   - HEADER / QUERY / COOKIE 依靠不同前缀天然分开, 即便同名 key (如都叫 user) 也独立.
func findTrafficValue(arg *apitraffic.SourceMatch, sourceService model.ServiceMetadata) string {
	// 读取 sourceService.Metadata 的指定 label key
	lookup := func(labelKey string) string {
		if labelKey == "" || sourceService == nil {
			return ""
		}
		meta := sourceService.GetMetadata()
		if v, ok := meta[labelKey]; ok {
			return v
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
		// CALLER_METADATA 直接读调用方的原始业务 metadata (不套前缀).
		if sourceService != nil {
			return sourceService.GetMetadata()[arg.GetKey()]
		}
		return ""
	default:
		// 未知类型兜底: 按原始 key 直查, 避免引入隐式默认行为.
		return lookup(arg.GetKey())
	}
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
