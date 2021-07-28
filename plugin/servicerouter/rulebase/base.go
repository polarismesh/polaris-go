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
	"github.com/polarismesh/polaris-go/pkg/algorithm/rand"
	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/pkg/plugin/servicerouter"
	"github.com/modern-go/reflect2"
	"os"
	"regexp"
	"sort"
)

//服务路由匹配结果
type matchResult int

//ToString方法
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
	//单个分组
	singleSubset weightedSubset
	// 实例分组列表
	subsets []weightedSubset
	// 实例分组的总权重
	totalWeight uint32
}

//获取节点累积的权重
func (p *prioritySubsets) GetValue(index int) uint64 {
	return uint64(p.subsets[index].weight)
}

//获取总权重值
func (p *prioritySubsets) TotalWeight() int {
	return int(p.totalWeight)
}

//获取数组成员数
func (p *prioritySubsets) Count() int {
	return len(p.subsets)
}

//重置subset数据
func (p *prioritySubsets) reset() {
	p.singleSubset.cluster = nil
	p.singleSubset.weight = 0
	p.subsets = nil
	p.totalWeight = 0
}

//通过池子来获取subset结构对象
func (g *RuleBasedInstancesFilter) poolGetPrioritySubsets() *prioritySubsets {
	value := g.prioritySubsetPool.Get()
	if reflect2.IsNil(value) {
		return &prioritySubsets{}
	}
	subSet := value.(*prioritySubsets)
	subSet.reset()
	return subSet
}

//归还subset结构对象进池子
func (g *RuleBasedInstancesFilter) poolReturnPrioritySubsets(set *prioritySubsets) {
	g.prioritySubsetPool.Put(set)
}

// 匹配metadata
func (g *RuleBasedInstancesFilter) matchSourceMetadata(ruleMeta map[string]*namingpb.MatchString,
	routeInfo *servicerouter.RouteInfo, ruleCache model.RuleCache) (match bool,
	invalidRegex string, invalidRegexError error) {
	var srcMeta map[string]string
	if routeInfo.SourceService != nil {
		srcMeta = routeInfo.SourceService.GetMetadata()
	}
	// 如果规则metadata不为空, 待匹配规则为空, 直接返回失败
	if len(srcMeta) == 0 {
		return false, "", nil
	}
	var err error
	// metadata是否全部匹配
	allMetaMatched := true
	for ruleMetaKey, ruleMetaValue := range ruleMeta {
		if srcMetaValue, ok := srcMeta[ruleMetaKey]; ok {
			rawMetaValue, exist := g.getRuleMetaValueStr(routeInfo, ruleMetaKey, ruleMetaValue)
			if !exist {
				return false, "", nil
			}
			switch ruleMetaValue.Type {
			case namingpb.MatchString_REGEX:
				var matchExp *regexp.Regexp
				if ruleMetaValue.ValueType == namingpb.MatchString_TEXT {
					matchExp = ruleCache.GetRegexMatcher(rawMetaValue)
				} else {
					matchExp, err = regexp.Compile(rawMetaValue)
				}
				if err != nil {
					return false, rawMetaValue, err
				}
				if !matchExp.MatchString(srcMetaValue) {
					allMetaMatched = false
				}
			default:
				// 精确匹配
				if srcMetaValue != rawMetaValue {
					allMetaMatched = false
				}
			}
		} else {
			//假如不存在规则要求的KEY，则直接返回匹配失败
			allMetaMatched = false
		}
		if !allMetaMatched {
			break
		}
	}
	return allMetaMatched, "", nil
}

//获取规则variable
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

//往routeInfo中添加匹配到的环境变量
func addRouteInfoVariable(key, value string, routeInfo *servicerouter.RouteInfo) {
	if routeInfo.EnvironmentVariables == nil {
		routeInfo.EnvironmentVariables = make(map[string]string)
	}
	routeInfo.EnvironmentVariables[key] = value
}

//非法正则表达式的信息
type invalidRegexInfo struct {
	invalidRegexSources      []*namingpb.Source
	invalidRegexDestinations []*namingpb.Destination
	invalidRegexErrors       map[string]string
}

// 匹配source规则
func (g *RuleBasedInstancesFilter) matchSource(sources []*namingpb.Source, routeInfo *servicerouter.RouteInfo,
	ruleMatchType int, ruleCache model.RuleCache) (success bool, matched *namingpb.Source,
	notMatched []*namingpb.Source, invalidRegexInfos *invalidRegexInfo) {
	if len(sources) == 0 {
		return true, nil, nil, nil
	}
	sourceService := routeInfo.SourceService
	var invalidRegexError error
	var invalidRegex string
	// source匹配成功标志
	//matched = true
	//invalidRegexes = false
	for _, source := range sources {
		// 对于inbound规则, 需要匹配source服务
		if ruleMatchType == dstRouteRuleMatch {
			if reflect2.IsNil(sourceService) {
				// 如果没有source服务信息, 判断rule是否支持全匹配
				if source.Namespace.GetValue() != matchAll || source.Service.GetValue() != matchAll {
					success = false
					notMatched = append(notMatched, source)
					continue
				}
			} else {
				// 如果有source服务信息, 需要匹配服务信息
				// 如果命名空间|服务不为"*"且不等于原服务, 则匹配失败
				if source.Namespace.GetValue() != matchAll &&
					source.Namespace.GetValue() != sourceService.GetNamespace() {
					success = false
					notMatched = append(notMatched, source)
					continue
				}
				if source.Service.GetValue() != matchAll &&
					source.Service.GetValue() != sourceService.GetService() {
					success = false
					notMatched = append(notMatched, source)
					continue
				}
			}
		}

		// 如果rule中metadata为空, 匹配成功, 结束
		if len(source.Metadata) == 0 {
			success = true
			matched = source
			break
		}

		// 如果没有源服务信息, 本次匹配失败
		if reflect2.IsNil(sourceService) {
			success = false
			notMatched = append(notMatched, source)
			continue
		}

		success, invalidRegex, invalidRegexError = g.matchSourceMetadata(source.Metadata, routeInfo, ruleCache)
		if success {
			matched = source
			break
		}
		//如果是正则表达式有问题的话，这个source放进 invalidRegexInfos
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
			//否则放进notMatched
			notMatched = append(notMatched, source)
		}
	}

	return success, matched, notMatched, invalidRegexInfos
}

//校验输入的元数据是否符合规则
func validateInMetadata(ruleMetaKey string, ruleMetaValue *namingpb.MatchString, ruleMetaValueStr string,
	metadata map[string]map[string]string, matcher *regexp.Regexp) bool {
	if len(metadata) == 0 {
		return true
	}
	var values map[string]string
	var ok bool
	if values, ok = metadata[ruleMetaKey]; !ok {
		//集成的路由规则不包含这个key，那就不冲突
		return true
	}
	switch ruleMetaValue.Type {
	case namingpb.MatchString_REGEX:
		for value := range values {
			if !matcher.MatchString(value) {
				return false
			}
		}
	default:
		_, ok = values[ruleMetaValueStr]
		return ok
	}
	return true
}

//匹配目标标签
func (g *RuleBasedInstancesFilter) matchDstMetadata(routeInfo *servicerouter.RouteInfo,
	ruleMeta map[string]*namingpb.MatchString, ruleCache model.RuleCache, svcCache model.ServiceClusters,
	inCluster *model.Cluster) (cls *model.Cluster, matched bool, invalidRegex string, invalidRegexError error) {
	cls = model.NewCluster(svcCache, inCluster)
	var metaChanged bool
	for ruleMetaKey, ruleMetaValue := range ruleMeta {
		ruleMetaValueStr, exist := g.getRuleMetaValueStr(routeInfo, ruleMetaKey, ruleMetaValue)
		if !exist {
			//首先如果元数据的value无法获取，直接匹配失败
			return nil, false, "", nil
		}
		metaValues := svcCache.GetInstanceMetaValues(cls.Location, ruleMetaKey)
		if len(metaValues) == 0 {
			//不匹配
			return nil, false, "", nil
		}
		switch ruleMetaValue.Type {
		case namingpb.MatchString_REGEX:
			//对于正则表达式，则可能匹配到多个value，
			// 需要把服务下面的所有的meta value都拿出来比较
			var regexObj *regexp.Regexp
			if ruleMetaValue.ValueType == namingpb.MatchString_TEXT {
				regexObj = ruleCache.GetRegexMatcher(ruleMetaValueStr)
			} else {
				var err error
				regexObj, err = regexp.Compile(ruleMetaValueStr)
				if err != nil {
					return nil, false, ruleMetaValueStr, err
				}
			}
			//校验从上一个路由插件继承下来的规则是否符合该目标规则
			if !validateInMetadata(ruleMetaKey, ruleMetaValue, ruleMetaValueStr, inCluster.Metadata, regexObj) {
				return nil, false, "", nil
			}
			var hasMatchedValue bool
			for value, composedValue := range metaValues {
				if !regexObj.MatchString(value) {
					continue
				}
				hasMatchedValue = true
				if cls.RuleAddMetadata(ruleMetaKey, value, composedValue) {
					metaChanged = true
				}
			}
			//假如没有找到一个匹配的，则证明该服务下没有规则匹配该元数据
			if !hasMatchedValue {
				return nil, false, "", nil
			}
		//parameter、variable、text 的 exact 最终都是要精确匹配，只是匹配的值来源不同
		default:
			//校验从上一个路由插件继承下来的规则是否符合该目标规则
			if !validateInMetadata(ruleMetaKey, ruleMetaValue, ruleMetaValueStr, inCluster.Metadata, nil) {
				return nil, false, "", nil
			}
			if composedValue, ok := metaValues[ruleMetaValueStr]; ok {
				if cls.RuleAddMetadata(ruleMetaKey, ruleMetaValueStr, composedValue) {
					metaChanged = true
				}
			} else {
				//没有找到对应的值
				return nil, false, "", nil
			}
		}
	}
	if metaChanged {
		cls.ReloadComposeMetaValue()
	}
	return cls, true, "", nil
}

//获取具体用于匹配的元数据的value
func (g *RuleBasedInstancesFilter) getRuleMetaValueStr(routeInfo *servicerouter.RouteInfo, ruleMetaKey string,
	ruleMetaValue *namingpb.MatchString) (string, bool) {
	var srcMeta map[string]string
	if routeInfo.SourceService != nil {
		srcMeta = routeInfo.SourceService.GetMetadata()
	}
	var ruleMetaValueStr string
	var exist bool
	switch ruleMetaValue.ValueType {
	case namingpb.MatchString_TEXT:
		ruleMetaValueStr = ruleMetaValue.GetValue().GetValue()
		exist = true
	case namingpb.MatchString_PARAMETER:
		if len(srcMeta) == 0 {
			exist = false
		} else {
			ruleMetaValueStr, exist = srcMeta[ruleMetaKey]
		}
	case namingpb.MatchString_VARIABLE:
		ruleMetaValueStr, exist = g.getVariable(ruleMetaValue.GetValue().GetValue())
		if exist {
			addRouteInfoVariable(ruleMetaValue.GetValue().GetValue(), ruleMetaValueStr, routeInfo)
		}
	default:
		ruleMetaValueStr = ruleMetaValue.GetValue().GetValue()
		exist = true
	}
	return ruleMetaValueStr, exist
}

// populateSubsetsFromDst 根据destination中的规则填充分组列表
// 返回是否存在匹配的实例
func (g *RuleBasedInstancesFilter) populateSubsetsFromDst(routeInfo *servicerouter.RouteInfo,
	svcCache model.ServiceClusters, ruleCache model.RuleCache, dst *namingpb.Destination,
	subsetsMap map[uint32]*prioritySubsets, inCluster *model.Cluster) (matched bool, invalidRegexInfos *invalidRegexInfo) {
	// 获取subset
	cluster, ok,
	invalidRegex, invalidRegexError := g.matchDstMetadata(routeInfo, dst.Metadata, ruleCache, svcCache, inCluster)
	if !ok {
		var invalidInfo *invalidRegexInfo
		if invalidRegexError != nil {
			invalidInfo = &invalidRegexInfo{
				invalidRegexErrors: map[string]string{
					invalidRegex: invalidRegexError.Error(),
				},
				invalidRegexDestinations: []*namingpb.Destination{dst},
			}
		}
		return false, invalidInfo
	}

	// 根据优先级填充subset列表
	priority := dst.Priority.GetValue()
	weight := dst.Weight.GetValue()
	weightedSubsets, ok := subsetsMap[priority]
	if !ok {
		pSubSet := g.poolGetPrioritySubsets()
		pSubSet.singleSubset.weight = weight
		pSubSet.singleSubset.cluster = cluster
		pSubSet.totalWeight = weight
		subsetsMap[priority] = pSubSet
	} else {
		weightedSubsets.totalWeight += weight
		if len(weightedSubsets.subsets) == 0 {
			weightedSubsets.subsets = append(weightedSubsets.subsets, weightedSubsets.singleSubset)
		}
		weightedSubsets.subsets = append(weightedSubsets.subsets, weightedSubset{
			cluster: cluster,
			weight:  weightedSubsets.totalWeight,
		})

	}
	return true, nil
}

//selectCluster 从subset中选取实例
func (g *RuleBasedInstancesFilter) selectCluster(subsetsMap map[uint32]*prioritySubsets) *model.Cluster {
	prioritySet := make([]uint32, 0, len(subsetsMap))
	for k := range subsetsMap {
		prioritySet = append(prioritySet, k)
	}
	if len(prioritySet) > 1 {
		// 从小到大排序, priority小的在前(越小越高)
		sort.Slice(prioritySet, func(i, j int) bool {
			return prioritySet[i] < prioritySet[j]
		})
	}
	//取优先级最高的
	priorityFirst := prioritySet[0]
	weightedSubsets := subsetsMap[priorityFirst]
	var retCluster *model.Cluster
	if len(weightedSubsets.subsets) == 0 {
		retCluster = weightedSubsets.singleSubset.cluster
	} else {
		index := rand.SelectWeightedRandItem(g.scalableRand, weightedSubsets)
		retCluster = weightedSubsets.subsets[index].cluster
	}
	//复用cluster
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
	return retCluster
}

// 根据路由规则进行服务实例过滤, 并返回过滤后的实例列表
func (g *RuleBasedInstancesFilter) getRoutesFromRule(routeInfo *servicerouter.RouteInfo,
	ruleMatchType int) []*namingpb.Route {

	// 跟据服务类型获取对应路由规则
	// 被调inbound
	if ruleMatchType == dstRouteRuleMatch {
		if reflect2.IsNil(routeInfo.DestRouteRule) || reflect2.IsNil(routeInfo.DestRouteRule.GetValue()) {
			return nil
		}
		routeRuleValue := routeInfo.DestRouteRule.GetValue()
		routing := routeRuleValue.(*namingpb.Routing)
		return routing.Inbounds
	}

	if reflect2.IsNil(routeInfo.SourceRouteRule) || reflect2.IsNil(routeInfo.SourceRouteRule.GetValue()) {
		return nil
	}

	// 主调outbound
	if reflect2.IsNil(routeInfo.SourceService) {
		return nil
	}
	routeRuleValue := routeInfo.SourceRouteRule.GetValue()
	routing := routeRuleValue.(*namingpb.Routing)
	return routing.Outbounds
}

//规则匹配的结果，用于后续日志输出
type ruleMatchSummary struct {
	matchedSource            []*namingpb.Source
	errorRegexes             map[string]string
	notMatchedSources        []*namingpb.Source
	invalidRegexSources      []*namingpb.Source
	invalidRegexDestinations []*namingpb.Destination
	notMatchedDestinations   []*namingpb.Destination
	weightZeroDestinations   []*namingpb.Destination
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
	svcCache model.ServiceClusters, routes []*namingpb.Route,
	inCluster *model.Cluster, summary *ruleMatchSummary) (*model.Cluster, error) {
	var ruleCache model.RuleCache
	if ruleMatchType == dstRouteRuleMatch {
		ruleCache = routeInfo.DestRouteRule.GetRuleCache()
	} else {
		ruleCache = routeInfo.SourceRouteRule.GetRuleCache()
	}
	for _, route := range routes {
		// 匹配source规则
		sourceMatched, match, notMatches, invalidRegex := g.matchSource(route.Sources, routeInfo, ruleMatchType, ruleCache)

		if invalidRegex != nil {
			//summary.invalidRegexSources = append(summary.invalidRegexSources, invalidRegex.invalidRegexes...)
			summary.appendErrorRegexes(invalidRegex)
		}

		if len(notMatches) > 0 {
			summary.notMatchedSources = append(summary.notMatchedSources, notMatches...)
		}

		if sourceMatched {
			summary.matchedSource = append(summary.matchedSource, match)
		} else {
			//没有匹配成功，继续下一轮匹配
			continue
		}

		// 如果source匹配成功, 继续匹配destination规则
		// 然后将结果写进map(key: 权重, value: 带权重的实例分组)
		subsetsMap := make(map[uint32]*prioritySubsets)
		for _, dst := range route.Destinations {
			// 对于outbound规则, 需要匹配DestService服务
			if ruleMatchType == sourceRouteRuleMatch {
				if dst.Namespace.GetValue() != matchAll &&
					dst.Namespace.GetValue() != routeInfo.DestService.GetNamespace() {
					summary.notMatchedDestinations = append(summary.notMatchedDestinations, dst)
					continue
				}

				if dst.Service.GetValue() != matchAll &&
					dst.Service.GetValue() != routeInfo.DestService.GetService() {
					summary.notMatchedDestinations = append(summary.notMatchedDestinations, dst)
					continue
				}
			}
			if dst.Weight.GetValue() == 0 {
				summary.weightZeroDestinations = append(summary.weightZeroDestinations, dst)
				continue
			}
			destMatched, invalidRegex := g.populateSubsetsFromDst(routeInfo, svcCache, ruleCache, dst, subsetsMap, inCluster)
			//判断实例的metadata信息，看是否符合
			if !destMatched {
				if invalidRegex != nil {
					//summary.invalidRegexDestinations = append(summary.invalidRegexDestinations, dst)
					summary.appendErrorRegexes(invalidRegex)
				} else {
					summary.notMatchedDestinations = append(summary.notMatchedDestinations, dst)
				}
			}
		}
		// 如果未匹配到分组, 继续匹配
		if len(subsetsMap) == 0 {
			continue
		}
		// 匹配到分组, 返回
		return g.selectCluster(subsetsMap), nil
	}

	// 全部匹配完成, 未匹配到任何分组, 返回空
	return nil, nil
}

// 在instance中全匹配被调服务metadata
func (g *RuleBasedInstancesFilter) searchMetadata(destServiceMetadata map[string]string,
	instanceMetadata map[string]string) bool {

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
