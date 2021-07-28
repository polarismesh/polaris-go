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

package canary

import (
	"errors"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/servicerouter"
	"github.com/modern-go/reflect2"
)

const ()

//CanaryRouterFilter 金丝雀过滤器
type CanaryRouterFilter struct {
	*plugin.PluginBase
	percentOfMinInstances float64
	valueCtx              model.ValueContext
	recoverAll            bool
}

//Type 插件类型
func (g *CanaryRouterFilter) Type() common.Type {
	return common.TypeServiceRouter
}

//Name 插件名，一个类型下插件名唯一
func (g *CanaryRouterFilter) Name() string {
	return config.DefaultServiceRouterCanary
}

//Init 初始化插件
func (g *CanaryRouterFilter) Init(ctx *plugin.InitContext) error {
	// 获取最小返回实例比例
	g.PluginBase = plugin.NewPluginBase(ctx)
	g.percentOfMinInstances = ctx.Config.GetConsumer().GetServiceRouter().GetPercentOfMinInstances()
	g.recoverAll = ctx.Config.GetConsumer().GetServiceRouter().IsEnableRecoverAll()
	g.valueCtx = ctx.ValueCtx
	return nil
}

//Destroy 销毁插件，可用于释放资源
func (g *CanaryRouterFilter) Destroy() error {
	return nil
}

//CanaryRouterFilter 插件模式进行服务实例过滤，并返回过滤后的实例列表
func (g *CanaryRouterFilter) GetFilteredInstances(routeInfo *servicerouter.RouteInfo,
	clusters model.ServiceClusters, withinCluster *model.Cluster) (*servicerouter.RouteResult, error) {

	//enableCanary := clusters.IsCanaryEnabled()
	//if !enableCanary {
	//	result := servicerouter.PoolGetRouteResult(g.valueCtx)
	//	cls := model.NewCluster(clusters, withinCluster)
	//	result.OutputCluster = cls
	//	return result, nil
	//}
	canary := routeInfo.Canary
	var result *servicerouter.RouteResult
	var err error
	if canary != "" {
		result, err = g.canaryFilter(canary, clusters, withinCluster)
	} else {
		result, err = g.noCanaryFilter(clusters, withinCluster)
	}
	if err != nil {
		//返回给外层，靠filterOnly兜底
		if result == nil || reflect2.IsNil(result) {
			result = servicerouter.PoolGetRouteResult(g.valueCtx)
		}
		cls := model.NewCluster(clusters, withinCluster)
		result.OutputCluster = cls
		result.OutputCluster.HasLimitedInstances = true
		result.Status = servicerouter.DegradeToFilterOnly
		return result, nil
	} else {
		routeInfo.SetIgnoreFilterOnlyOnEndChain(true)
		return result, nil
	}
}

//带金丝雀标签的处理过滤
func (g *CanaryRouterFilter) canaryFilter(canaryValue string,
	clusters model.ServiceClusters, withinCluster *model.Cluster) (*servicerouter.RouteResult, error) {
	availableCluster, status, err := g.canaryAvailableFilter(canaryValue, clusters, withinCluster)
	if err == nil && availableCluster != nil {
		result := servicerouter.PoolGetRouteResult(g.valueCtx)
		result.OutputCluster = availableCluster
		result.Status = status
		return result, nil
	}
	limitedCluster, err := g.canaryLimitedFilter(canaryValue, clusters, withinCluster)
	if err == nil && limitedCluster != nil {
		result := servicerouter.PoolGetRouteResult(g.valueCtx)
		result.OutputCluster = limitedCluster
		result.Status = servicerouter.LimitedCanary
		return result, nil
	}
	return nil, errors.New("no instances after canaryFilter")
}

//带金丝雀过滤可用实例
func (g *CanaryRouterFilter) canaryAvailableFilter(canaryValue string, clusters model.ServiceClusters,
	withinCluster *model.Cluster) (*model.Cluster, servicerouter.RouteStatus, error) {
	targetCluster := model.NewCluster(clusters, withinCluster)
	targetCluster.AddMetadata(model.CanaryMetaKey, canaryValue)
	targetCluster.ReloadComposeMetaValue()
	// 返回带canary的可用实例
	instSet := targetCluster.GetClusterValue().GetInstancesSet(false, true)
	if instSet.Count() > 0 {
		return targetCluster, servicerouter.Normal, nil
	}
	defer targetCluster.PoolPut()
	// 返回不带canary的可用实例
	notContainMetaKeyCluster := model.NewCluster(clusters, withinCluster)
	notContainMetaKeyCluster.AddMetadata(model.CanaryMetaKey, "")
	notContainMetaKeyCluster.ReloadComposeMetaValue()
	notContainMetaKeyInstSet := notContainMetaKeyCluster.GetNotContainMetaKeyClusterValue().GetInstancesSet(false, true)
	if notContainMetaKeyInstSet.Count() > 0 {
		return notContainMetaKeyCluster, servicerouter.DegradeToNotCanary, nil
	}
	defer notContainMetaKeyCluster.PoolPut()
	// 找 不匹配特定金丝雀 的可用实例
	notMatchMetaKeyCluster := model.NewCluster(clusters, withinCluster)
	notMatchMetaKeyCluster.AddMetadata(model.CanaryMetaKey, canaryValue)
	notMatchMetaKeyCluster.ReloadComposeMetaValue()
	notMatchMetaKeyInstSet := notMatchMetaKeyCluster.GetContainNotMatchMetaKeyClusterValue().GetInstancesSet(false, true)
	if notMatchMetaKeyInstSet.Count() > 0 {
		return notMatchMetaKeyCluster, servicerouter.DegradeToNotMatchCanary, nil
	}
	defer notMatchMetaKeyCluster.PoolPut()
	return nil, servicerouter.Normal, errors.New("no available instances")
}

//带金丝雀过滤limited实例
func (g *CanaryRouterFilter) canaryLimitedFilter(canaryValue string, clusters model.ServiceClusters,
	withinCluster *model.Cluster) (*model.Cluster, error) {
	targetCluster := model.NewCluster(clusters, withinCluster)
	targetCluster.AddMetadata(model.CanaryMetaKey, canaryValue)
	targetCluster.ReloadComposeMetaValue()
	instSetAll := targetCluster.GetClusterValue().GetInstancesSetWhenSkipRouteFilter(true, true)
	if instSetAll.Count() > 0 {
		targetCluster.HasLimitedInstances = true
		return targetCluster, nil
	}
	defer targetCluster.PoolPut()
	return nil, errors.New("no available instances")
}

//不带金丝雀route
func (g *CanaryRouterFilter) noCanaryFilter(clusters model.ServiceClusters,
	withinCluster *model.Cluster) (*servicerouter.RouteResult, error) {
	availableCluster, status, err := g.noCanaryAvailableFilter(clusters, withinCluster)
	if err == nil && availableCluster != nil {
		result := servicerouter.PoolGetRouteResult(g.valueCtx)
		result.OutputCluster = availableCluster
		result.Status = status
		return result, nil
	}
	limitedCluster, err := g.noCanaryLimitedFilter(clusters, withinCluster)
	if err == nil && limitedCluster != nil {
		result := servicerouter.PoolGetRouteResult(g.valueCtx)
		result.OutputCluster = limitedCluster
		result.Status = servicerouter.LimitedNoCanary
		return result, nil
	}
	return nil, errors.New("no instances after canaryFilter")
}

//不带金丝雀过滤可用实例
func (g *CanaryRouterFilter) noCanaryAvailableFilter(clusters model.ServiceClusters,
	withinCluster *model.Cluster) (*model.Cluster, servicerouter.RouteStatus, error) {
	notContainMetaKeyCluster := model.NewCluster(clusters, withinCluster)
	notContainMetaKeyCluster.AddMetadata(model.CanaryMetaKey, "")
	notContainMetaKeyCluster.ReloadComposeMetaValue()
	noTargetInstSet := notContainMetaKeyCluster.GetNotContainMetaKeyClusterValue().GetInstancesSet(false, true)
	if noTargetInstSet.Count() > 0 {
		return notContainMetaKeyCluster, servicerouter.Normal, nil
	}
	defer notContainMetaKeyCluster.PoolPut()

	// 优先返回带canary的可用实例
	containMetaKeyCluster := model.NewCluster(clusters, withinCluster)
	containMetaKeyCluster.AddMetadata(model.CanaryMetaKey, "")
	containMetaKeyCluster.ReloadComposeMetaValue()
	// 返回带canary的可用实例
	instSet := containMetaKeyCluster.GetContainMetaKeyClusterValue().GetInstancesSet(false, true)
	if instSet.Count() > 0 {
		return containMetaKeyCluster, servicerouter.DegradeToCanary, nil
	}
	defer containMetaKeyCluster.PoolPut()
	return nil, servicerouter.Normal, errors.New("no available instances")
}

//不带金丝雀过滤limited实例
func (g *CanaryRouterFilter) noCanaryLimitedFilter(clusters model.ServiceClusters,
	withinCluster *model.Cluster) (*model.Cluster, error) {
	targetCluster := model.NewCluster(clusters, withinCluster)
	targetCluster.AddMetadata(model.CanaryMetaKey, "")
	targetCluster.ReloadComposeMetaValue()
	instSetAll := targetCluster.GetNotContainMetaKeyClusterValue().GetInstancesSetWhenSkipRouteFilter(true, true)
	if instSetAll.Count() > 0 {
		targetCluster.HasLimitedInstances = true
		return targetCluster, nil
	}
	defer targetCluster.PoolPut()
	return nil, errors.New("no available instances")
}

//是否需要启动规则路由
func (g *CanaryRouterFilter) Enable(routeInfo *servicerouter.RouteInfo, clusters model.ServiceClusters) bool {
	return clusters.IsCanaryEnabled()
}

//init 注册插件
func init() {
	plugin.RegisterPlugin(&CanaryRouterFilter{})
}
