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

package rulebase

import (
	"os"
	"sort"

	regexp "github.com/dlclark/regexp2"
	"github.com/modern-go/reflect2"
	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/algorithm/match"
	"github.com/polarismesh/polaris-go/pkg/algorithm/rand"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/servicerouter"
)

// 服务路由匹配结果
type matchResult int

// ToString方法
func (m matchResult) String() string {
	return matchResultToPresent[m]
}

var (
	matchResultToPresent = map[matchResult]string{
		noRouteRule:       "noRouteRule",
		dstRuleSuccess:    "dstRuleSuccess",
		dstRuleFail:       "dstRuleFail",
		sourceRuleSuccess: "sourceRuleSuccess",
		sourceRuleFail:    "sourceRuleFail",
	}
)

// 路由规则匹配状态
const (
	// 无路由策略
	noRouteRule matchResult = iota
	// 被调服务路由策略匹配成功
	dstRuleSuccess
	// 被调服务路由策略匹配失败
	dstRuleFail
	// 主调服务路由策略匹配成功
	sourceRuleSuccess
	// 主调服务路由策略匹配失败
	sourceRuleFail
)

// 路由规则匹配类型
const (
	// 主调服务规则匹配
	sourceRouteRuleMatch = iota
	// 被调服务匹配
	dstRouteRuleMatch
)

const (
	// 支持全匹配
	matchAll = "*"
)

// 带权重的实例subset
type weightedSubset struct {
	// 实例subset
	cluster *model.Cluster
	// subset列表
	weight uint32
}

// 同优先级的实例分组列表
type prioritySubsets struct {
	// 单个分组
	singleSubset weightedSubset
	// 实例分组列表
	subsets []weightedSubset
	// 实例分组的总权重
	totalWeight uint32
}

// GetValue 获取节点累积的权重
func (p *prioritySubsets) GetValue(index int) uint64 {
	return uint64(p.subsets[index].weight)
}

// TotalWeight 获取总权重值
func (p *prioritySubsets) TotalWeight() int {
	return int(p.totalWeight)
}

// Count 获取数组成员数
func (p *prioritySubsets) Count() int {
	return len(p.subsets)
}

// 重置subset数据
func (p *prioritySubsets) reset() {
	p.singleSubset.cluster = nil
	p.singleSubset.weight = 0
	p.subsets = nil
	p.totalWeight = 0
}

// 通过池子来获取subset结构对象
func (g *RuleBasedInstancesFilter) poolGetPrioritySubsets() *prioritySubsets {
	value := g.prioritySubsetPool.Get()
	if reflect2.IsNil(value) {
		return &prioritySubsets{}
	}
	subSet := value.(*prioritySubsets)
	subSet.reset()
	return subSet
}

// 归还subset结构对象进池子
func (g *RuleBasedInstancesFilter) poolReturnPrioritySubsets(set *prioritySubsets) {
	g.prioritySubsetPool.Put(set)
}

// 匹配metadata
func (g *RuleBasedInstancesFilter) matchSourceMetadata(ruleMeta map[string]*apimodel.MatchString,
	routeInfo *servicerouter.RouteInfo, ruleCache model.RuleCache) (bool, string, error) {
	var srcMeta map[string]string
	if routeInfo.SourceService != nil {
		srcMeta = routeInfo.SourceService.GetMetadata()
	}
	// 如果规则metadata不为空, 待匹配规则为空, 直接返回失败
	if len(srcMeta) == 0 {
		return false, "", nil
	}
	// metadata是否全部匹配
	allMetaMatched := true
	for ruleMetaKey, ruleMetaValue := range ruleMeta {
		if ruleMetaKey == matchAll {
			continue
		}
		if srcMetaValue, ok := srcMeta[ruleMetaKey]; ok {
			if ruleMetaValue.GetValue().GetValue() == matchAll {
				continue
			}
			rawMetaValue, exist := g.getRuleMetaValueForSource(routeInfo, ruleMetaKey, ruleMetaValue)
			if !exist {
				return false, "", nil
			}
			allMetaMatched = match.MatchString(srcMetaValue, &apimodel.MatchString{
				Type:  ruleMetaValue.Type,
				Value: wrapperspb.String(rawMetaValue),
			}, func(s string) *regexp.Regexp {
				matchExp, err := regexp.Compile(rawMetaValue, regexp.RE2)
				if err != nil {
					return nil
				}
				return matchExp
			})
		} else {
			// 假如不存在规则要求的KEY，则直接返回匹配失败
			allMetaMatched = false
		}
		if !allMetaMatched {
			break
		}
	}
	return allMetaMatched, "", nil
}

// 获取规则variable
func (g *RuleBasedInstancesFilter) getVariable(envKey string) (string, bool) {
	value, exist := g.systemCfg.GetVariable(envKey)
	if !exist {
		value = os.Getenv(envKey)
		if value != "" {
			exist = true
		}
	}
	return value, exist
}

// 往routeInfo中添加匹配到的环境变量
func addRouteInfoVariable(key, value string, routeInfo *servicerouter.RouteInfo) {
	if routeInfo.EnvironmentVariables == nil {
		routeInfo.EnvironmentVariables = make(map[string]string)
	}
	routeInfo.EnvironmentVariables[key] = value
}

// 非法正则表达式的信息
type invalidRegexInfo struct {
	invalidRegexSources      []*apitraffic.Source
	invalidRegexDestinations []*apitraffic.Destination
	invalidRegexErrors       map[string]string
}

// 匹配source规则
func (g *RuleBasedInstancesFilter) matchSource(sources []*apitraffic.Source, routeInfo *servicerouter.RouteInfo,
	ruleMatchType int, ruleCache model.RuleCache) (success bool, matched *apitraffic.Source,
	notMatched []*apitraffic.Source, invalidRegexInfos *invalidRegexInfo) {
	if len(sources) == 0 {
		if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			log.GetBaseLogger().Debugf("[RuleBasedRouter] matchSource: sources is empty, return matched")
		}
		return true, nil, nil, nil
	}
	sourceService := routeInfo.SourceService
	if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		sourceServiceInfo := model.ToStringService(sourceService, false)
		var sourceMetadata map[string]string
		if sourceService != nil {
			sourceMetadata = sourceService.GetMetadata()
		}
		log.GetBaseLogger().Debugf(
			"[RuleBasedRouter] matchSource start, sourcesCount: %d, sourceService: %s, sourceMetadata: %v, "+
				"ruleMatchType: %d", len(sources), sourceServiceInfo, sourceMetadata, ruleMatchType)
	}
	var invalidRegexError error
	var invalidRegex string
	// source匹配成功标志
	// matched = true
	// invalidRegexes = false
	for i, source := range sources {
		if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			log.GetBaseLogger().Debugf(
				"[RuleBasedRouter] matchSource: checking source[%d], ns=%s, svc=%s, metadataCount=%d, metadata=%v", i,
				source.Namespace.GetValue(), source.Service.GetValue(), len(source.Metadata), source.Metadata)
		}
		// 对于inbound规则, 需要匹配source服务
		if ruleMatchType == dstRouteRuleMatch {
			if reflect2.IsNil(sourceService) {
				// 如果没有source服务信息, 判断rule是否支持全匹配
				if source.Namespace.GetValue() != matchAll || source.Service.GetValue() != matchAll {
					if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
						log.GetBaseLogger().Debugf("[RuleBasedRouter] matchSource: source[%d] not matched, "+
							"sourceService is nil and rule not matchAll", i)
					}
					success = false
					notMatched = append(notMatched, source)
					continue
				}
			} else {
				// 如果有source服务信息, 需要匹配服务信息
				// 如果命名空间|服务不为"*"且不等于原服务, 则匹配失败
				if source.Namespace.GetValue() != matchAll &&
					source.Namespace.GetValue() != sourceService.GetNamespace() {
					if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
						log.GetBaseLogger().Debugf("[RuleBasedRouter] matchSource: source[%d] namespace not matched, "+
							"rule=%s, actual=%s", i, source.Namespace.GetValue(), sourceService.GetNamespace())
					}
					success = false
					notMatched = append(notMatched, source)
					continue
				}
				if source.Service.GetValue() != matchAll &&
					source.Service.GetValue() != sourceService.GetService() {
					if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
						log.GetBaseLogger().Debugf("[RuleBasedRouter] matchSource: source[%d] service not matched, "+
							"rule=%s, actual=%s", i, source.Service.GetValue(), sourceService.GetService())
					}
					success = false
					notMatched = append(notMatched, source)
					continue
				}
			}
		}

		// 如果rule中metadata为空, 匹配成功, 结束
		if len(source.Metadata) == 0 {
			if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
				log.GetBaseLogger().Debugf("[RuleBasedRouter] matchSource: source[%d] matched, metadata is empty", i)
			}
			success = true
			matched = source
			break
		}

		// 如果没有源服务信息, 本次匹配失败
		if reflect2.IsNil(sourceService) {
			if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
				log.GetBaseLogger().Debugf("[RuleBasedRouter] matchSource: source[%d] not matched, sourceService is "+
					"nil but metadata required", i)
			}
			success = false
			notMatched = append(notMatched, source)
			continue
		}

		success, invalidRegex, invalidRegexError = g.matchSourceMetadata(source.Metadata, routeInfo, ruleCache)
		if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			log.GetBaseLogger().Debugf("[RuleBasedRouter] matchSource: source[%d] metadata match result: success=%v, "+
				"invalidRegex=%s", i, success, invalidRegex)
		}
		if success {
			if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
				log.GetBaseLogger().Debugf("[RuleBasedRouter] matchSource: source[%d] matched successfully", i)
			}
			matched = source
			break
		}
		// 如果是正则表达式有问题的话，这个source放进 invalidRegexInfos
		if invalidRegexError != nil {
			if invalidRegexInfos == nil {
				invalidRegexInfos = &invalidRegexInfo{
					invalidRegexSources: nil,
					invalidRegexErrors:  make(map[string]string),
				}
			}
			invalidRegexInfos.invalidRegexSources = append(invalidRegexInfos.invalidRegexSources, source)
			invalidRegexInfos.invalidRegexErrors[invalidRegex] = invalidRegexError.Error()
		} else {
			// 否则放进notMatched
			notMatched = append(notMatched, source)
		}
	}

	if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		log.GetBaseLogger().Debugf("[RuleBasedRouter] matchSource finished, success=%v, notMatchedCount=%d", success,
			len(notMatched))
	}
	return success, matched, notMatched, invalidRegexInfos
}

// 校验输入的元数据是否符合规则
func validateInMetadata(ruleMetaKey string, ruleMetaValue *apimodel.MatchString, ruleMetaValueStr string,
	metadata map[string]map[string]string, matcher *regexp.Regexp) bool {
	if len(metadata) == 0 {
		return true
	}
	var values map[string]string
	var ok bool
	if values, ok = metadata[ruleMetaKey]; !ok {
		// 集成的路由规则不包含这个key，那就不冲突
		return true
	}
	switch ruleMetaValue.Type {
	case apimodel.MatchString_REGEX:
		for value := range values {
			m, err := matcher.FindStringMatch(value)
			if err != nil {
				log.GetBaseLogger().Errorf("regex match metadata error. ruleMetaKey: %s, value: %s, errors: %s",
					ruleMetaKey, value, err)
				return false
			}
			if m == nil || m.String() == "" {
				return false
			}
		}
	default:
		_, ok = values[ruleMetaValueStr]
		return ok
	}
	return true
}

// 匹配目标标签
func (g *RuleBasedInstancesFilter) matchDstMetadata(routeInfo *servicerouter.RouteInfo,
	ruleMeta map[string]*apimodel.MatchString, ruleCache model.RuleCache, svcCache model.ServiceClusters,
	inCluster *model.Cluster) (cls *model.Cluster, matched bool, invalidRegex string, invalidRegexError error) {
	cls = model.NewCluster(svcCache, inCluster)
	var metaChanged bool
	for ruleMetaKey, ruleMetaValue := range ruleMeta {
		ruleMetaValueStr, exist := g.getRuleMetaValueForDest(routeInfo, ruleMetaKey, ruleMetaValue)
		if !exist {
			// 首先如果元数据的value无法获取，直接匹配失败
			return nil, false, "", nil
		}

		// 全匹配类型直接放行
		if ruleMetaValueStr == matchAll && ruleMetaValue.ValueType == apimodel.MatchString_TEXT {
			continue
		}
		// 如果是“不等于”类型，需要单独处理
		var metaValues map[string]string
		if ruleMetaValue.Type != apimodel.MatchString_NOT_EQUALS {
			metaValues = svcCache.GetInstanceMetaValues(cls.Location, ruleMetaKey)
			if len(metaValues) == 0 {
				// 不匹配
				return nil, false, "", nil
			}
		}
		switch ruleMetaValue.Type {
		case apimodel.MatchString_REGEX:
			// 对于正则表达式，则可能匹配到多个value，
			// 需要把服务下面的所有的meta value都拿出来比较
			regexObj, err := ruleCache.GetRegexMatcher(ruleMetaValueStr)
			if err != nil {
				return nil, false, ruleMetaValueStr, err
			}
			// 校验从上一个路由插件继承下来的规则是否符合该目标规则
			if !validateInMetadata(ruleMetaKey, ruleMetaValue, ruleMetaValueStr, inCluster.Metadata, regexObj) {
				return nil, false, "", nil
			}
			var hasMatchedValue bool
			for value, composedValue := range metaValues {
				m, err := regexObj.FindStringMatch(value)
				if err != nil {
					log.GetBaseLogger().Errorf("regex match dst metadata error. ruleMetaValueStr: %s, value: %s, "+
						"errors: %s", ruleMetaValueStr, value, err)
					continue
				}
				if m == nil || m.String() == "" {
					continue
				}
				hasMatchedValue = true
				if cls.RuleAddMetadata(ruleMetaKey, value, composedValue) {
					metaChanged = true
				}
			}
			// 假如没有找到一个匹配的，则证明该服务下没有规则匹配该元数据
			if !hasMatchedValue {
				return nil, false, "", nil
			}

		case apimodel.MatchString_NOT_EQUALS:
			metaValues = svcCache.GetInstancesWithMetaValuesNotEqual(cls.Location, ruleMetaKey, ruleMetaValueStr)
			if len(metaValues) == 0 {
				return cls, false, "", nil
			}
			for k, v := range metaValues {
				cls.RuleAddMetadata(ruleMetaKey, k, v)
			}
			metaChanged = true

		// parameter、variable、text 的 exact 最终都是要精确匹配，只是匹配的值来源不同
		default:
			// 校验从上一个路由插件继承下来的规则是否符合该目标规则
			if !validateInMetadata(ruleMetaKey, ruleMetaValue, ruleMetaValueStr, inCluster.Metadata, nil) {
				return nil, false, "", nil
			}
			if composedValue, ok := metaValues[ruleMetaValueStr]; ok {
				if cls.RuleAddMetadata(ruleMetaKey, ruleMetaValueStr, composedValue) {
					metaChanged = true
				}
			} else {
				// 没有找到对应的值
				return nil, false, "", nil
			}
		}
	}
	if metaChanged {
		cls.ReloadComposeMetaValue()
	}
	return cls, true, "", nil
}

// getRuleMetaValueForSource 针对 Source 方向的标签 value 匹配获取
func (g *RuleBasedInstancesFilter) getRuleMetaValueForSource(routeInfo *servicerouter.RouteInfo, ruleMetaKey string,
	ruleMetaValue *apimodel.MatchString) (string, bool) {
	return g.getRuleMetaValueStr(routeInfo, ruleMetaKey, ruleMetaValue, false)
}

// getRuleMetaValueForDest 针对 Destination 方向的标签 value 匹配获取
func (g *RuleBasedInstancesFilter) getRuleMetaValueForDest(routeInfo *servicerouter.RouteInfo, ruleMetaKey string,
	ruleMetaValue *apimodel.MatchString) (string, bool) {
	return g.getRuleMetaValueStr(routeInfo, ruleMetaKey, ruleMetaValue, true)
}

// 获取具体用于匹配的元数据的value
func (g *RuleBasedInstancesFilter) getRuleMetaValueStr(routeInfo *servicerouter.RouteInfo, ruleMetaKey string,
	ruleMetaValue *apimodel.MatchString, forDest bool) (string, bool) {
	var srcMeta map[string]string
	if routeInfo.SourceService != nil {
		srcMeta = routeInfo.SourceService.GetMetadata()
	}
	var processedRuleMetaValue string
	var exist bool
	switch ruleMetaValue.ValueType {
	case apimodel.MatchString_TEXT:
		processedRuleMetaValue = ruleMetaValue.GetValue().GetValue()
		exist = true
	case apimodel.MatchString_PARAMETER:
		if forDest {
			if len(srcMeta) == 0 {
				exist = false
			} else {
				// 对于参数场景，实例标签的 value 来自 source metadata 中的 value
				processedRuleMetaValue, exist = srcMeta[ruleMetaValue.GetValue().GetValue()]
			}
		} else {
			// 如果是参数类型，并且当前是针对 Source 方向的标签匹配，默认直接放通
			exist = true
			processedRuleMetaValue = matchAll
		}
	case apimodel.MatchString_VARIABLE:
		processedRuleMetaValue, exist = g.getVariable(ruleMetaValue.GetValue().GetValue())
		if exist {
			addRouteInfoVariable(ruleMetaValue.GetValue().GetValue(), processedRuleMetaValue, routeInfo)
		}
	default:
		processedRuleMetaValue = ruleMetaValue.GetValue().GetValue()
		exist = true
	}
	return processedRuleMetaValue, exist
}

// populateSubsetsFromDst 根据destination中的规则填充分组列表
// 返回是否存在匹配的实例
func (g *RuleBasedInstancesFilter) populateSubsetsFromDst(routeInfo *servicerouter.RouteInfo,
	svcCache model.ServiceClusters, ruleCache model.RuleCache, dst *apitraffic.Destination,
	subsetsMap map[uint32]*prioritySubsets, inCluster *model.Cluster) (matched bool, invalidRegexInfos *invalidRegexInfo) {
	if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		log.GetBaseLogger().Debugf("[RuleBasedRouter] populateSubsetsFromDst 开始填充目标子集, dstMetadata: %v, "+
			"priority: %d, weight: %d", dst.Metadata, dst.Priority.GetValue(), dst.Weight.GetValue())
	}
	// 获取subset：根据目标metadata匹配实例集群
	cluster, ok, invalidRegex, invalidRegexError := g.matchDstMetadata(routeInfo, dst.Metadata, ruleCache, svcCache,
		inCluster)
	if !ok || cluster == nil {
		if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			log.GetBaseLogger().Debugf("[RuleBasedRouter] populateSubsetsFromDst 目标metadata匹配失败, "+
				"invalidRegex: %s, error: %v", invalidRegex, invalidRegexError)
		}
		var invalidInfo *invalidRegexInfo
		if invalidRegexError != nil {
			invalidInfo = &invalidRegexInfo{
				invalidRegexErrors: map[string]string{
					invalidRegex: invalidRegexError.Error(),
				},
				invalidRegexDestinations: []*apitraffic.Destination{dst},
			}
		}
		return false, invalidInfo
	}

	// 根据优先级填充subset列表：将匹配到的cluster按优先级和权重组织到subsetsMap中
	priority := dst.Priority.GetValue()
	weight := dst.Weight.GetValue()
	weightedSubsets, ok := subsetsMap[priority]
	if !ok {
		// 该优先级首次出现，创建新的prioritySubsets并设置为单一子集模式
		pSubSet := g.poolGetPrioritySubsets()
		pSubSet.singleSubset.weight = weight
		pSubSet.singleSubset.cluster = cluster
		pSubSet.totalWeight = weight
		subsetsMap[priority] = pSubSet
		if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			log.GetBaseLogger().Debugf("[RuleBasedRouter] populateSubsetsFromDst 创建新优先级子集, priority: %d, "+
				"weight: %d, clusterInstances: %d", priority, weight, cluster.GetClusterValue().Count())
		}
	} else {
		// 该优先级已存在，追加到子集列表中（用于权重选择）
		weightedSubsets.totalWeight += weight
		if len(weightedSubsets.subsets) == 0 {
			// 从单一子集模式转为多子集模式
			weightedSubsets.subsets = append(weightedSubsets.subsets, weightedSubsets.singleSubset)
		}
		weightedSubsets.subsets = append(weightedSubsets.subsets, weightedSubset{
			cluster: cluster,
			weight:  weightedSubsets.totalWeight,
		})
		if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			log.GetBaseLogger().Debugf("[RuleBasedRouter] populateSubsetsFromDst 追加子集到已有优先级, priority: %d, "+
				"weight: %d, totalWeight: %d, subsetsCount: %d", priority, weight, weightedSubsets.totalWeight,
				len(weightedSubsets.subsets))
		}
	}
	return true, nil
}

// selectCluster 从subset中选取实例
// 选择逻辑：1.按优先级排序(数值越小优先级越高) 2.取最高优先级的子集组 3.按权重随机选择一个cluster
func (g *RuleBasedInstancesFilter) selectCluster(subsetsMap map[uint32]*prioritySubsets) *model.Cluster {
	if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		log.GetBaseLogger().Debugf("[RuleBasedRouter] selectCluster 开始从%d个优先级组中选择cluster", len(subsetsMap))
	}
	// 步骤1: 提取所有优先级值
	prioritySet := make([]uint32, 0, len(subsetsMap))
	for k := range subsetsMap {
		prioritySet = append(prioritySet, k)
	}
	// 步骤2: 按优先级排序(数值越小优先级越高)
	if len(prioritySet) > 1 {
		// 从小到大排序, priority小的在前(越小越高)
		sort.Slice(prioritySet, func(i, j int) bool {
			return prioritySet[i] < prioritySet[j]
		})
	}
	// 步骤3: 取优先级最高的子集组
	priorityFirst := prioritySet[0]
	weightedSubsets := subsetsMap[priorityFirst]
	if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		log.GetBaseLogger().Debugf("[RuleBasedRouter] selectCluster 选中最高优先级: %d, 该优先级下子集数: %d, 总权重: %d",
			priorityFirst, len(weightedSubsets.subsets), weightedSubsets.totalWeight)
	}
	var retCluster *model.Cluster
	// 步骤4: 从子集中选择cluster(单一子集直接返回，多子集按权重随机选择)
	if len(weightedSubsets.subsets) == 0 {
		// 单一子集模式：直接返回该cluster
		retCluster = weightedSubsets.singleSubset.cluster
		if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			log.GetBaseLogger().Debugf("[RuleBasedRouter] selectCluster 单一子集模式, 直接选中cluster, 实例数: %d",
				retCluster.GetClusterValue().Count())
		}
	} else {
		// 多子集模式：按权重随机选择
		index := rand.SelectWeightedRandItem(g.scalableRand, weightedSubsets)
		retCluster = weightedSubsets.subsets[index].cluster
		if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			log.GetBaseLogger().Debugf("[RuleBasedRouter] selectCluster 多子集模式, 按权重随机选中index: %d, 实例数: %d",
				index, retCluster.GetClusterValue().Count())
		}
	}
	// 步骤5: 回收未选中的cluster到对象池(内存复用优化)
	for _, prioritySubset := range subsetsMap {
		if len(prioritySubset.subsets) == 0 {
			if retCluster != prioritySubset.singleSubset.cluster {
				prioritySubset.singleSubset.cluster.PoolPut()
			}
		} else {
			for _, subset := range prioritySubset.subsets {
				if retCluster != subset.cluster {
					subset.cluster.PoolPut()
				}
			}
		}
		g.poolReturnPrioritySubsets(prioritySubset)
	}
	if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		log.GetBaseLogger().Debugf("[RuleBasedRouter] selectCluster 完成, 最终选中cluster实例数: %d",
			retCluster.GetClusterValue().Count())
	}
	return retCluster
}

func ruleEmpty(svcRule model.ServiceRule) bool {
	return reflect2.IsNil(svcRule) || reflect2.IsNil(svcRule.GetValue()) || svcRule.GetValidateError() != nil
}

// 根据路由规则进行服务实例过滤, 并返回过滤后的实例列表
func (g *RuleBasedInstancesFilter) getRoutesFromRule(routeInfo *servicerouter.RouteInfo, ruleMatchType int) []*apitraffic.Route {
	// 跟据服务类型获取对应路由规则
	// 被调inbound
	if ruleMatchType == dstRouteRuleMatch {
		if ruleEmpty(routeInfo.DestRouteRule) {
			return nil
		}
		routeRuleValue := routeInfo.DestRouteRule.GetValue()
		routing := routeRuleValue.(*apitraffic.Routing)
		return routing.Inbounds
	}

	if ruleEmpty(routeInfo.SourceRouteRule) {
		return nil
	}

	// 主调outbound
	if reflect2.IsNil(routeInfo.SourceService) {
		return nil
	}
	routeRuleValue := routeInfo.SourceRouteRule.GetValue()
	routing := routeRuleValue.(*apitraffic.Routing)
	return routing.Outbounds
}

// 规则匹配的结果，用于后续日志输出
type ruleMatchSummary struct {
	matchedSource            []*apitraffic.Source
	errorRegexes             map[string]string
	notMatchedSources        []*apitraffic.Source
	invalidRegexSources      []*apitraffic.Source
	invalidRegexDestinations []*apitraffic.Destination
	notMatchedDestinations   []*apitraffic.Destination
	weightZeroDestinations   []*apitraffic.Destination
}

func (rms *ruleMatchSummary) appendErrorRegexes(invalid *invalidRegexInfo) {
	if invalid.invalidRegexSources != nil {
		rms.invalidRegexSources = append(rms.invalidRegexSources, invalid.invalidRegexSources...)
	}
	if invalid.invalidRegexDestinations != nil {
		rms.invalidRegexDestinations = append(rms.invalidRegexDestinations, invalid.invalidRegexDestinations...)
	}
	if rms.errorRegexes == nil {
		rms.errorRegexes = invalid.invalidRegexErrors
		return
	}
	for k, v := range invalid.invalidRegexErrors {
		rms.errorRegexes[k] = v
	}
}

// 根据路由规则进行服务实例过滤, 并返回过滤后的实例列表
func (g *RuleBasedInstancesFilter) getRuleFilteredInstances(ruleMatchType int, routeInfo *servicerouter.RouteInfo,
	svcCache model.ServiceClusters, routes []*apitraffic.Route,
	inCluster *model.Cluster, summary *ruleMatchSummary) (*model.Cluster, error) {
	var ruleCache model.RuleCache
	if ruleMatchType == dstRouteRuleMatch {
		ruleCache = routeInfo.DestRouteRule.GetRuleCache()
	} else {
		ruleCache = routeInfo.SourceRouteRule.GetRuleCache()
	}
	if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		log.GetBaseLogger().Debugf("[RuleBasedRouter] getRuleFilteredInstances start, ruleMatchType: %d, "+
			"routesCount: %d, destService: %s/%s", ruleMatchType, len(routes), routeInfo.DestService.GetNamespace(),
			routeInfo.DestService.GetService())
	}
	for i, route := range routes {
		// 匹配source规则
		if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			log.GetBaseLogger().Debugf("[RuleBasedRouter] matching route[%d], sourcesCount: %d, "+
				"destinationsCount: %d, route: %s", i, len(route.Sources), len(route.Destinations), route.String())
		}
		sourceMatched, matchSource, notMatches, invalidRegex := g.matchSource(route.Sources, routeInfo, ruleMatchType,
			ruleCache)
		if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			log.GetBaseLogger().Debugf("[RuleBasedRouter] route[%d] source match result: matched=%v, "+
				"notMatchedCount=%d", i, sourceMatched, len(notMatches))
		}

		if invalidRegex != nil {
			// summary.invalidRegexSources = append(summary.invalidRegexSources, invalidRegex.invalidRegexes...)
			summary.appendErrorRegexes(invalidRegex)
		}

		if len(notMatches) > 0 {
			summary.notMatchedSources = append(summary.notMatchedSources, notMatches...)
		}

		if sourceMatched {
			summary.matchedSource = append(summary.matchedSource, matchSource)
		} else {
			// 没有匹配成功，继续下一轮匹配
			continue
		}

		// 如果source匹配成功, 继续匹配destination规则
		// 然后将结果写进map(key: 权重, value: 带权重的实例分组)
		if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			log.GetBaseLogger().Debugf("[RuleBasedRouter] route[%d] source匹配成功, 开始匹配%d个destination规则", i,
				len(route.Destinations))
		}
		subsetsMap := make(map[uint32]*prioritySubsets)
		for dstIdx, dst := range route.Destinations {
			// 对于outbound规则, 需要匹配DestService服务
			if ruleMatchType == sourceRouteRuleMatch {
				if dst.Namespace.GetValue() != matchAll &&
					dst.Namespace.GetValue() != routeInfo.DestService.GetNamespace() {
					if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
						log.GetBaseLogger().Debugf("[RuleBasedRouter] route[%d] dst[%d] namespace不匹配: rule=%s, "+
							"actual=%s", i, dstIdx, dst.Namespace.GetValue(), routeInfo.DestService.GetNamespace())
					}
					summary.notMatchedDestinations = append(summary.notMatchedDestinations, dst)
					continue
				}

				if dst.Service.GetValue() != matchAll &&
					dst.Service.GetValue() != routeInfo.DestService.GetService() {
					if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
						log.GetBaseLogger().Debugf("[RuleBasedRouter] route[%d] dst[%d] service不匹配: rule=%s, "+
							"actual=%s", i, dstIdx, dst.Service.GetValue(), routeInfo.DestService.GetService())
					}
					summary.notMatchedDestinations = append(summary.notMatchedDestinations, dst)
					continue
				}
			}
			if dst.Weight.GetValue() == 0 {
				if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
					log.GetBaseLogger().Debugf("[RuleBasedRouter] route[%d] dst[%d] 权重为0, 跳过", i, dstIdx)
				}
				summary.weightZeroDestinations = append(summary.weightZeroDestinations, dst)
				continue
			}
			destMatched, invalidRegex := g.populateSubsetsFromDst(routeInfo, svcCache, ruleCache, dst, subsetsMap, inCluster)
			// 判断实例的metadata信息，看是否符合
			if !destMatched {
				if invalidRegex != nil {
					if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
						log.GetBaseLogger().Debugf("[RuleBasedRouter] route[%d] dst[%d] metadata匹配失败(正则错误): %v",
							i, dstIdx, invalidRegex.invalidRegexErrors)
					}
					// summary.invalidRegexDestinations = append(summary.invalidRegexDestinations, dst)
					summary.appendErrorRegexes(invalidRegex)
				} else {
					if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
						log.GetBaseLogger().Debugf("[RuleBasedRouter] route[%d] dst[%d] metadata匹配失败, "+
							"dstMetadata: %v", i, dstIdx, dst.Metadata)
					}
					summary.notMatchedDestinations = append(summary.notMatchedDestinations, dst)
				}
			} else {
				if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
					log.GetBaseLogger().Debugf("[RuleBasedRouter] route[%d] dst[%d] 匹配成功, priority: %d, weight: %d",
						i, dstIdx, dst.Priority.GetValue(), dst.Weight.GetValue())
				}
			}
		}
		// 如果未匹配到分组, 继续匹配
		if len(subsetsMap) == 0 {
			if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
				log.GetBaseLogger().Debugf("[RuleBasedRouter] route[%d] no matched subsets, continue to next route", i)
			}
			continue
		}
		// 匹配到分组, 返回
		if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			log.GetBaseLogger().Debugf("[RuleBasedRouter] route[%d] matched %d priority subsets, selecting cluster",
				i, len(subsetsMap))
		}
		return g.selectCluster(subsetsMap), nil
	}

	// 全部匹配完成, 未匹配到任何分组, 返回空
	if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		log.GetBaseLogger().Debugf("[RuleBasedRouter] getRuleFilteredInstances finished, no matched cluster found")
	}
	return nil, nil
}

// 在instance中全匹配被调服务metadata
func (g *RuleBasedInstancesFilter) searchMetadata(destServiceMetadata map[string]string, instanceMetadata map[string]string) bool {
	// metadata是否全部匹配
	allMetaMatched := true
	// instanceMetadata中找到的metadata个数, 用于辅助判断是否能匹配成功
	matchNum := 0
	for destMetaKey, destMetaValue := range destServiceMetadata {
		if insMetaValue, ok := instanceMetadata[destMetaKey]; ok {
			matchNum++

			if insMetaValue != destMetaValue {
				allMetaMatched = false
				break
			}
		}
	}

	// 如果一个metadata未找到, 匹配失败
	if matchNum == 0 {
		allMetaMatched = false
	}

	return allMetaMatched
}
