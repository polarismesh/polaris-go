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

package filteronly

import (
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/servicerouter"
)

//RuleBasedInstancesFilter 基于路由规则的服务实例过滤器
type InstancesFilter struct {
	*plugin.PluginBase
	percentOfMinInstances float64
	valueCtx              model.ValueContext
	recoverAll            bool
}

//Type 插件类型
func (g *InstancesFilter) Type() common.Type {
	return common.TypeServiceRouter
}

//Name 插件名，一个类型下插件名唯一
func (g *InstancesFilter) Name() string {
	return config.DefaultServiceRouterFilterOnly
}

//Init 初始化插件
func (g *InstancesFilter) Init(ctx *plugin.InitContext) error {
	// 获取最小返回实例比例
	g.percentOfMinInstances = ctx.Config.GetConsumer().GetServiceRouter().GetPercentOfMinInstances()
	g.PluginBase = plugin.NewPluginBase(ctx)
	g.recoverAll = ctx.Config.GetConsumer().GetServiceRouter().IsEnableRecoverAll()
	g.valueCtx = ctx.ValueCtx
	return nil
}

//Destroy 销毁插件，可用于释放资源
func (g *InstancesFilter) Destroy() error {
	return nil
}

//GetFilteredInstances 插件模式进行服务实例过滤，并返回过滤后的实例列表
func (g *InstancesFilter) GetFilteredInstances(routeInfo *servicerouter.RouteInfo,
	clusters model.ServiceClusters, withinCluster *model.Cluster) (*servicerouter.RouteResult, error) {
	return GetFilteredInstances(g.valueCtx, routeInfo, clusters, g.percentOfMinInstances, withinCluster, g.recoverAll)
}

//是否需要启动规则路由
func (g *InstancesFilter) Enable(routeInfo *servicerouter.RouteInfo, clusters model.ServiceClusters) bool {
	return true
}

//GetFilteredInstances 进行服务实例过滤，并返回过滤后的实例列表
func GetFilteredInstances(ctx model.ValueContext, routeInfo *servicerouter.RouteInfo, clusters model.ServiceClusters,
	percentOfMinInstances float64, withinCluster *model.Cluster, recoverAll bool) (*servicerouter.RouteResult, error) {
	outCluster := model.NewCluster(clusters, withinCluster)
	clsValue := outCluster.GetClusterValue()
	// 至少要返回多少实例
	allInstancesCount := clsValue.Count()
	minInstances := int(percentOfMinInstances * float64(allInstancesCount))
	healthyInstances := clsValue.GetInstancesSet(false, false)
	healthyCount := healthyInstances.Count()
	hasLimitedInstances := false
	if recoverAll && allInstancesCount > 0 && healthyCount <= minInstances {
		//全死全活
		hasLimitedInstances = true
	}
	outCluster.HasLimitedInstances = hasLimitedInstances
	result := servicerouter.PoolGetRouteResult(ctx)
	result.OutputCluster = outCluster
	routeInfo.SetIgnoreFilterOnlyOnEndChain(true)
	return result, nil
}

//init 注册插件
func init() {
	plugin.RegisterPlugin(&InstancesFilter{})
}
