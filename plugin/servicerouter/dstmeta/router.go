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

package dstmeta

import (
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/servicerouter"
)

//基于目标服务元数据的服务路由插件
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
	return config.DefaultServiceRouterDstMeta
}

//Init 初始化插件
func (g *InstancesFilter) Init(ctx *plugin.InitContext) error {
	// 获取最小返回实例比例
	g.PluginBase = plugin.NewPluginBase(ctx)
	g.percentOfMinInstances = ctx.Config.GetConsumer().GetServiceRouter().GetPercentOfMinInstances()
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

	dstMetadata := routeInfo.DestService.GetMetadata()
	targetCluster := g.getTargetCluster(clusters, withinCluster, dstMetadata)

	if len(dstMetadata) > 0 {
		instSet := targetCluster.GetClusterValue().GetInstancesSet(true, true)
		if instSet.Count() > 0 {
			return g.getResult(targetCluster), nil
		}

		targetCluster.PoolPut()
		if routeInfo.EnableFailOverDefaultMeta {
			targetCluster, err := g.failOverDefaultMetaHandler(clusters, withinCluster, routeInfo)
			if err != nil {
				return nil, err
			}
			routeInfo.SetIgnoreFilterOnlyOnEndChain(true)
			return g.getResult(targetCluster), nil
		}

		return nil, g.metaNotMatchError(routeInfo)
	}

	return g.getResult(targetCluster), nil
}

//元数据匹配不到时处理自定义匹配规则
func (g *InstancesFilter) failOverDefaultMetaHandler(clusters model.ServiceClusters,
	withinCluster *model.Cluster, routeInfo *servicerouter.RouteInfo) (*model.Cluster, error) {

	if routeInfo.FailOverDefaultMeta.Type == model.GetOneHealth {
		return g.getOneHealthHandler(clusters, withinCluster, routeInfo)
	} else if routeInfo.FailOverDefaultMeta.Type == model.NotContainMetaKey {
		return g.notContainMetaKeyHandler(clusters, withinCluster, routeInfo)
	} else if routeInfo.FailOverDefaultMeta.Type == model.CustomMeta {
		return g.customMetaHandler(clusters, withinCluster, routeInfo)
	}

	return nil, model.NewSDKError(model.ErrCodeAPIInvalidArgument,
		fmt.Errorf("failOverDefaultMeta Type not match"),
		"fail to enable failOverDefaultMeta")
}

//通配所有可用ip实例，等于关闭meta路由
func (g *InstancesFilter) getOneHealthHandler(clusters model.ServiceClusters, withinCluster *model.Cluster,
	routeInfo *servicerouter.RouteInfo) (*model.Cluster, error) {

	targetCluster := g.getTargetCluster(clusters, withinCluster, nil)
	clusterValue := targetCluster.GetClusterValue()
	instSet := g.getInstSet(clusterValue)
	return targetCluster, g.validateInstSet(instSet, routeInfo)
}

//匹配不带 metaData key路由
func (g *InstancesFilter) notContainMetaKeyHandler(clusters model.ServiceClusters, withinCluster *model.Cluster,
	routeInfo *servicerouter.RouteInfo) (*model.Cluster, error) {

	targetCluster := g.getTargetCluster(clusters, withinCluster, routeInfo.DestService.GetMetadata())
	clusterValue := targetCluster.GetNotContainMetaKeyClusterValue()
	instSet := g.getInstSet(clusterValue)
	return targetCluster, g.validateInstSet(instSet, routeInfo)
}

//匹配自定义meta
func (g *InstancesFilter) customMetaHandler(clusters model.ServiceClusters, withinCluster *model.Cluster,
	routeInfo *servicerouter.RouteInfo) (*model.Cluster, error) {

	if err := validateEmptyKey(routeInfo.FailOverDefaultMeta.Meta); err != nil {
		return nil, err
	}
	targetCluster := g.getTargetCluster(clusters, withinCluster, routeInfo.FailOverDefaultMeta.Meta)
	clusterValue := targetCluster.GetClusterValue()
	instSet := g.getInstSet(clusterValue)
	return targetCluster, g.validateInstSet(instSet, routeInfo)
}

func (g *InstancesFilter) getTargetCluster(clusters model.ServiceClusters, withinCluster *model.Cluster,
	dstMetadata map[string]string) *model.Cluster {

	targetCluster := model.NewCluster(clusters, withinCluster)
	if len(dstMetadata) > 0 {
		for metaKey, metaValue := range dstMetadata {
			targetCluster.AddMetadata(metaKey, metaValue)
		}
		targetCluster.ReloadComposeMetaValue()
	}
	return targetCluster
}

func (g *InstancesFilter) getInstSet(clusterValue *model.ClusterValue) *model.InstanceSet {
	instSet := clusterValue.GetInstancesSet(false, true)
	if instSet.Count() == 0 {
		instSet = clusterValue.GetInstancesSet(true, true)
	}
	return instSet
}

func (g *InstancesFilter) getResult(cluster *model.Cluster) *servicerouter.RouteResult {
	result := servicerouter.PoolGetRouteResult(g.valueCtx)
	result.OutputCluster = cluster
	return result
}

func (g *InstancesFilter) validateInstSet(instSet *model.InstanceSet, routeInfo *servicerouter.RouteInfo) error {
	if instSet.Count() == 0 {
		return g.metaNotMatchError(routeInfo)
	}
	return nil
}

func (g *InstancesFilter) metaNotMatchError(routeInfo *servicerouter.RouteInfo) error {
	errorText := fmt.Sprintf(
		"dstmeta not match, dstService %s(namespace %s), metadata is %v",
		routeInfo.DestService.GetService(), routeInfo.DestService.GetNamespace(),
		routeInfo.DestService.GetMetadata())
	log.GetBaseLogger().Errorf(errorText)
	return model.NewSDKError(model.ErrCodeDstMetaMismatch, nil, errorText)
}

func validateEmptyKey(m map[string]string) error {
	if len(m) == 0 {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument,
			fmt.Errorf("failOverDefaultMeta is empty"),
			"fail to validate GetOneInstanceRequest")
	}
	for k := range m {
		if len(k) == 0 {
			return model.NewSDKError(model.ErrCodeAPIInvalidArgument,
				fmt.Errorf("failOverDefaultMeta has empty key"),
				"fail to validate GetOneInstanceRequest")
		}
	}
	return nil
}

//init 注册插件
func init() {
	plugin.RegisterPlugin(&InstancesFilter{})
}

//是否需要启动规则路由
func (g *InstancesFilter) Enable(routeInfo *servicerouter.RouteInfo, clusters model.ServiceClusters) bool {
	if len(routeInfo.DestService.GetMetadata()) == 0 {
		return false
	}
	return true
}
