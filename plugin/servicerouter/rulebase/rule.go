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
	"encoding/json"
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/algorithm/rand"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/servicerouter"
	"github.com/golang/protobuf/jsonpb"
	"github.com/modern-go/reflect2"
	"sync"
)

//RuleBasedInstancesFilter 基于路由规则的服务实例过滤器
type RuleBasedInstancesFilter struct {
	*plugin.PluginBase
	percentOfMinInstances float64
	scalableRand          *rand.ScalableRand
	valueCtx              model.ValueContext
	recoverAll            bool
	prioritySubsetPool    *sync.Pool
	systemCfg             config.SystemConfig
}

//Type 插件类型
func (g *RuleBasedInstancesFilter) Type() common.Type {
	return common.TypeServiceRouter
}

//Name 插件名，一个类型下插件名唯一
func (g *RuleBasedInstancesFilter) Name() string {
	return config.DefaultServiceRouterRuleBased
}

//Init 初始化插件
func (g *RuleBasedInstancesFilter) Init(ctx *plugin.InitContext) error {
	// 获取最小返回实例比例
	g.percentOfMinInstances = ctx.Config.GetConsumer().GetServiceRouter().GetPercentOfMinInstances()
	g.PluginBase = plugin.NewPluginBase(ctx)
	g.recoverAll = ctx.Config.GetConsumer().GetServiceRouter().IsEnableRecoverAll()
	g.scalableRand = rand.NewScalableRand()
	g.valueCtx = ctx.ValueCtx
	g.prioritySubsetPool = &sync.Pool{}
	g.systemCfg = ctx.Config.GetGlobal().GetSystem()
	return nil
}

//Destroy 销毁插件，可用于释放资源
func (g *RuleBasedInstancesFilter) Destroy() error {
	return nil
}

//是否需要启动规则路由
func (g *RuleBasedInstancesFilter) Enable(routeInfo *servicerouter.RouteInfo, clusters model.ServiceClusters) bool {
	dstRoutes := g.getRoutesFromRule(routeInfo, dstRouteRuleMatch)
	sourceRoutes := g.getRoutesFromRule(routeInfo, sourceRouteRuleMatch)
	return len(dstRoutes) > 0 || len(sourceRoutes) > 0
}

//GetFilteredInstances 进行服务实例过滤，并返回过滤后的实例列表
func (g *RuleBasedInstancesFilter) GetFilteredInstances(routeInfo *servicerouter.RouteInfo,
	clusters model.ServiceClusters, withinCluster *model.Cluster) (*servicerouter.RouteResult, error) {
	var dstFilteredInstances, sourceFilteredInstances *model.Cluster
	var dstRoutes, sourceRoutes []*namingpb.Route
	var filteredInstances *model.Cluster
	var summary ruleMatchSummary

	// 检查输入参数
	if isValid, errInfo := g.validateParams(routeInfo); !isValid {
		return nil, errInfo
	}

	// 根据匹配过程修改状态, 默认无路由策略状态
	ruleStatus := noRouteRule

	// 优先匹配inbound规则, 成功则不需要继续匹配outbound规则 获取目标的入路由规则
	var err error
	dstRoutes = g.getRoutesFromRule(routeInfo, dstRouteRuleMatch)
	if len(dstRoutes) > 0 {
		routeInfo.MatchRuleType = servicerouter.DestRule
		filteredInstances, err = g.getRuleFilteredInstances(
			dstRouteRuleMatch, routeInfo, clusters, dstRoutes, withinCluster, &summary)
		if err != nil {
			return nil, err
		}
		dstFilteredInstances = filteredInstances
		if nil == dstFilteredInstances {
			ruleStatus = dstRuleFail
		} else {
			ruleStatus = dstRuleSuccess
		}
		goto finally
	}

	// 处理主调服务路由规则, 获取目标的出路由规则
	sourceRoutes = g.getRoutesFromRule(routeInfo, sourceRouteRuleMatch)
	if len(sourceRoutes) > 0 {
		routeInfo.MatchRuleType = servicerouter.SrcRule
		filteredInstances, err = g.getRuleFilteredInstances(
			sourceRouteRuleMatch, routeInfo, clusters, sourceRoutes, withinCluster, &summary)
		if err != nil {
			return nil, err
		}
		sourceFilteredInstances = filteredInstances
		if nil == sourceFilteredInstances {
			ruleStatus = sourceRuleFail
		} else {
			ruleStatus = sourceRuleSuccess
		}
	}

finally:
	var targetCluster *model.Cluster
	switch ruleStatus {
	case noRouteRule:
		// 如果没有路由规则, 则返回有效实例(有效个数不满足配置的量则返回全量)
		targetCluster = model.NewCluster(clusters, withinCluster)
	case sourceRuleSuccess:
		targetCluster = sourceFilteredInstances
	case dstRuleSuccess:
		targetCluster = dstFilteredInstances
	default:
		checkRule := routeInfo.DestService.GetNamespace() + ":" + routeInfo.DestService.GetService()
		if ruleStatus == sourceRuleFail {
			checkRule = routeInfo.SourceService.GetNamespace() + ":" + routeInfo.SourceService.GetService()
		}
		// 如果规则匹配失败, 返回错误
		notMatchedSrcText := getSourcesText(summary.notMatchedSources)
		matchedSrcText := getSourcesText(summary.matchedSource)
		invalidRegexSourceText := getSourcesText(summary.invalidRegexSources)
		notMatchedDstText := getNotMatchedDestinationText(summary.notMatchedDestinations)
		invalidRegexDstText := getNotMatchedDestinationText(summary.invalidRegexDestinations)
		weightZeroDstText := getNotMatchedDestinationText(summary.weightZeroDestinations)
		regexCompileErrText :=  getErrorRegexText(summary.errorRegexes)
		errorText := fmt.Sprintf("route rule not match, rule status: %s, sourceService %s, used variables %v,"+
			" dstService %s, notMatchedSource is %s, invalidRegexSource is %s, matchedSource is %s,"+
			" notMatchedDestination is %s, invalidRegexDestination is %s, zeroWeightDestination is %s," +
			" regexCompileErrors is %s, please check your route rule of service %s",
			ruleStatus.String(), model.ToStringService(routeInfo.SourceService, true), routeInfo.EnvironmentVariables,
			model.ToStringService(routeInfo.DestService, false), notMatchedSrcText, invalidRegexSourceText,
			matchedSrcText, notMatchedDstText, invalidRegexDstText, weightZeroDstText, regexCompileErrText, checkRule)
		log.GetBaseLogger().Errorf(errorText)
		return nil, model.NewSDKError(model.ErrCodeRouteRuleNotMatch, nil, errorText)
	}
	result := servicerouter.PoolGetRouteResult(g.valueCtx)
	result.OutputCluster = targetCluster
	return result, nil
}

//格式化不匹配的源规则
func getSourcesText(sources []*namingpb.Source) string {
	fakeRoute := &namingpb.Route{Sources: sources}
	jsonText, _ := (&jsonpb.Marshaler{}).MarshalToString(fakeRoute)
	return jsonText
}

//格式化不匹配的目标规则
func getNotMatchedDestinationText(notMatchedDestinations []*namingpb.Destination) string {
	fakeRoute := &namingpb.Route{Destinations: notMatchedDestinations}
	jsonText, _ := (&jsonpb.Marshaler{}).MarshalToString(fakeRoute)
	return jsonText
}

//将正则表达式的出错信息转化为 json 字符串
func getErrorRegexText(regexErrors map[string]string) string {
	if len(regexErrors) == 0 {
		return "{}"
	}
	res, _ := json.Marshal(regexErrors)
	return string(res)
}

// 检查请求参数
func (g *RuleBasedInstancesFilter) validateParams(routeInfo *servicerouter.RouteInfo) (bool, error) {

	// 规则实例为nil, 返回参数错误
	if reflect2.IsNil(routeInfo) {
		return false, model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"GetFilteredInstances param invalid, routeInfo can't be nil")
	}

	// 被调服务必须存在
	if reflect2.IsNil(routeInfo.DestService) {
		return false, model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"GetFilteredInstances param invalid, dstService must exist")
	}

	var err error
	// 被调规则如果存在, 主流程必须保证已初始化
	if err = checkRouteRule(routeInfo.DestRouteRule); nil != err {
		return false, err
	}

	// 主调规则如果存在, 主流程必须保证已初始化
	if err = checkRouteRule(routeInfo.SourceRouteRule); nil != err {
		return false, err
	}

	return true, nil
}

//校验路由规则
func checkRouteRule(routeRule model.ServiceRule) error {
	if reflect2.IsNil(routeRule) {
		return nil
	}
	if !routeRule.IsInitialized() {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"GetFilteredInstances param invalid, route rule for (namespace=%s, service=%s) not initialized",
			routeRule.GetService(), routeRule.GetNamespace())
	}
	validateErr := routeRule.GetValidateError()
	if nil != validateErr {
		return model.NewSDKError(model.ErrCodeInvalidRule, validateErr,
			"GetFilteredInstances param invalid, please check rule for (namespace=%s, service=%s)",
			routeRule.GetNamespace(), routeRule.GetService())
	}
	return nil
}

//init 注册插件
func init() {
	plugin.RegisterPlugin(&RuleBasedInstancesFilter{})
}
