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

package setdivision

import (
	"fmt"
	"strings"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/servicerouter"
)

// SetEnableFilter 基于路由规则的服务实例过滤器
type SetEnableFilter struct {
	*plugin.PluginBase
	valueCtx    model.ValueContext
	nearbyIndex int32
}

// Type 插件类型，接口实现必须
func (g *SetEnableFilter) Type() common.Type {
	return common.TypeServiceRouter
}

// Name 插件名，一个类型下插件名唯一
func (g *SetEnableFilter) Name() string {
	return config.DefaultServiceRouterSetDivision
}

// Init 初始化插件
func (g *SetEnableFilter) Init(ctx *plugin.InitContext) error {
	g.PluginBase = plugin.NewPluginBase(ctx)
	g.valueCtx = ctx.ValueCtx
	nearbyIndex, _ := plugin.GetPluginId(common.TypeServiceRouter, config.DefaultServiceRouterNearbyBased)
	g.nearbyIndex = nearbyIndex
	return nil
}

//Destroy 销毁插件，可用于释放资源，暂时不需要
func (g *SetEnableFilter) Destroy() error {
	return nil
}

//init 注册插件
func init() {
	plugin.RegisterPlugin(&SetEnableFilter{})
}

// Enable 是否需要启动该服务插件
func (g *SetEnableFilter) Enable(routeInfo *servicerouter.RouteInfo, clusters model.ServiceClusters) bool {
	return true
}

//getallArea 获取set分组
func (g *SetEnableFilter) getallArea(setNameList []string, clusters model.ServiceClusters,
	withinCluster *model.Cluster) (bool, *model.Cluster) {
	targetCluster := model.NewCluster(clusters, withinCluster)
	instSet := withinCluster.GetClusterValue().GetInstancesSet(true, true)
	//是否匹配到了节点
	flag := false
	for _, instance := range instSet.GetRealInstances() {
		meta := instance.GetMetadata()
		if setEnable, ok := meta[setEnableKey]; ok {
			if setEnable == "Y" {
				targetCluster.AddMetadata(setEnableKey, setEnable)
				if setName, ok := meta[setNameKey]; ok {
					setNameSplit := strings.Split(setName, ".")
					if setNameSplit[0] == setNameList[0] && setNameSplit[1] == setNameList[1] {
						targetCluster.AddMetadata(setNameKey, setName)
						flag = true
					}

				}
			}
		}
	}
	targetCluster.ReloadComposeMetaValue()
	return flag, targetCluster
}

// 被调是否启用set分组判断
func (g *SetEnableFilter) calleeEnableSet(set string, withinCluster *model.Cluster) bool {
	instSet := withinCluster.GetClusterValue().GetInstancesSet(true, true)
	for _, instance := range instSet.GetRealInstances() {
		meta := instance.GetMetadata()
		if setEnable, ok := meta[setEnableKey]; ok {
			if setEnable == "Y" {
				if setName, ok := meta[setNameKey]; ok {
					setNameSplit := strings.Split(setName, ".")
					if set == setNameSplit[0] {
						return true
					}

				}
			}
		}
	}
	return false

}

// destinationSet 指定set进行调用
func (g *SetEnableFilter) destinationSet(dstSetName string,
	clusters model.ServiceClusters, withinCluster *model.Cluster) (*servicerouter.RouteResult, error) {
	targetCluster := model.NewCluster(clusters, withinCluster)
	targetCluster.AddMetadata(setNameKey, dstSetName)
	targetCluster.AddMetadata(setEnableKey, "Y")
	targetCluster.ReloadComposeMetaValue()
	result := servicerouter.PoolGetRouteResult(g.valueCtx)
	result.OutputCluster = targetCluster
	return result, nil

}

// sourceSet 按照主调设置的set进行调用
func (g *SetEnableFilter) sourceSet(routeInfo *servicerouter.RouteInfo, setName string, clusters model.ServiceClusters,
	withinCluster *model.Cluster) (*servicerouter.RouteResult, error) {
	targetCluster := model.NewCluster(clusters, withinCluster)
	setNameList := strings.Split(setName, ".")
	set := setNameList[0]
	setArea := setNameList[1]
	setGroup := setNameList[2]
	calleeEnableSet := g.calleeEnableSet(set, withinCluster)
	if !calleeEnableSet {
		//被调没有启用set，则启用就近路由规则
		result := servicerouter.PoolGetRouteResult(g.valueCtx)
		result.OutputCluster = targetCluster
		return result, nil
	}
	//被调也启用了set，这里要禁用就近路由插件
	routeInfo.SetRouterEnable(g.nearbyIndex, false)
	//set分组为*的处理逻辑
	if setGroup != "*" {
		targetCluster.AddMetadata(setNameKey, setName)
		targetCluster.ReloadComposeMetaValue()
		instSet := targetCluster.GetClusterValue().GetInstancesSet(true, true)

		if instSet.Count() == 0 {
			//本set内没有，试着匹配下相同set地区的服务，地区名为.*的服务
			fullSetName := set + "." + setArea + ".*"
			targetCluster = model.NewCluster(clusters, withinCluster)
			targetCluster.AddMetadata(setNameKey, fullSetName)
			targetCluster.ReloadComposeMetaValue()
			result := servicerouter.PoolGetRouteResult(g.valueCtx)
			result.OutputCluster = targetCluster
			return result, nil
		}
		result := servicerouter.PoolGetRouteResult(g.valueCtx)
		result.OutputCluster = targetCluster
		return result, nil
	}
	//获取所有set分组
	flag, targetCluster := g.getallArea(setNameList, clusters, withinCluster)
	if flag {
		result := servicerouter.PoolGetRouteResult(g.valueCtx)
		result.OutputCluster = targetCluster
		return result, nil
	}
	errorText := fmt.Sprintf("route set division with set group rule  not match, "+
		"source set name is %s, not instances found in this set group,please check", setName)
	log.GetBaseLogger().Errorf(errorText)
	return nil, model.NewSDKError(model.ErrCodeAPIInstanceNotFound, nil, errorText)

}

// GetFilteredInstances 进行服务实例过滤，并返回过滤后的实例列表
func (g *SetEnableFilter) GetFilteredInstances(routeInfo *servicerouter.RouteInfo,
	clusters model.ServiceClusters, withinCluster *model.Cluster) (*servicerouter.RouteResult, error) {
	//判断是否启用destination set的匹配，如果匹配
	if routeInfo.DestService != nil {
		dstMetaData := routeInfo.DestService.GetMetadata()
		if dstSetEnable, ok := dstMetaData[setEnableKey]; ok {
			if dstSetEnable == "Y" {
				if dstSetName, ok := dstMetaData[setNameKey]; ok {
					routeInfo.SetRouterEnable(g.nearbyIndex, false)
					return g.destinationSet(dstSetName, clusters, withinCluster)
				}
			}

		}
	}
	//判断是否启用set，如果启用了set，则过滤对应的set实例
	if routeInfo.SourceService != nil {
		srcMetaData := routeInfo.SourceService.GetMetadata()
		if setEnable, ok := srcMetaData[setEnableKey]; ok {
			if setEnable == "Y" {
				//主调启用了set，去过滤被调set实例
				if setName, ok := srcMetaData[setNameKey]; ok {
					// 先匹配本set内的服务
					return g.sourceSet(routeInfo, setName, clusters, withinCluster)
				}

			}
		}
	}
	//就近路由
	targetCluster := model.NewCluster(clusters, withinCluster)
	result := servicerouter.PoolGetRouteResult(g.valueCtx)
	result.OutputCluster = targetCluster
	return result, nil

}
