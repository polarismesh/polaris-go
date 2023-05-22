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

package zeroprotect

import (
	"strconv"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/servicerouter"
	"go.uber.org/zap"
)

const (
	MetadataInstanceLastHeartbeatTime = "internal-lastheartbeat"
)

// ZeroProtectFilter 基于路由规则的服务实例过滤器
type ZeroProtectFilter struct {
	*plugin.PluginBase
	valueCtx model.ValueContext
}

// Type 插件类型
func (g *ZeroProtectFilter) Type() common.Type {
	return common.TypeServiceRouter
}

// Name 插件名，一个类型下插件名唯一
func (g *ZeroProtectFilter) Name() string {
	return config.DefaultServiceRouterZeroProtect
}

// Init 初始化插件
func (g *ZeroProtectFilter) Init(ctx *plugin.InitContext) error {
	g.PluginBase = plugin.NewPluginBase(ctx)
	g.valueCtx = ctx.ValueCtx
	return nil
}

// Destroy 销毁插件，可用于释放资源
func (g *ZeroProtectFilter) Destroy() error {
	return nil
}

// GetFilteredInstances 插件模式进行服务实例过滤，并返回过滤后的实例列表
func (g *ZeroProtectFilter) GetFilteredInstances(routeInfo *servicerouter.RouteInfo,
	clusters model.ServiceClusters, withinCluster *model.Cluster) (*servicerouter.RouteResult, error) {
	outCluster := model.NewCluster(clusters, withinCluster)
	clsValue := outCluster.GetClusterValue()
	healthyInstances := clsValue.GetInstancesSet(false, false)
	healthyCount := healthyInstances.Count()
	if healthyCount == 0 {
		outCluster = model.NewCluster(g.doZeroProtect(outCluster), withinCluster)
		outCluster.ClearClusterValue()
		clsValue = outCluster.GetClusterValue()
	}
	result := servicerouter.PoolGetRouteResult(g.valueCtx)
	result.OutputCluster = outCluster
	routeInfo.SetIgnoreFilterOnlyOnEndChain(true)
	return result, nil
}

func (g *ZeroProtectFilter) doZeroProtect(curCluster *model.Cluster) model.ServiceClusters {
	lastBeat := int64(-1)
	instances, _ := curCluster.GetAllInstances()
	instanceLastBeatTimes := map[string]int64{}
	for i := range instances {
		ins := instances[i]
		metadata := ins.GetMetadata()
		if len(metadata) == 0 {
			continue
		}
		val, ok := metadata[MetadataInstanceLastHeartbeatTime]
		if !ok {
			continue
		}
		beatTime, _ := strconv.ParseInt(val, 10, 64)
		if beatTime >= int64(lastBeat) {
			lastBeat = beatTime
		}
		instanceLastBeatTimes[ins.GetId()] = beatTime
	}
	if lastBeat == -1 {
		return curCluster.GetClusters()
	}
	finalInstances := make([]model.Instance, 0, len(instances))
	zeroProtectIns := make([]model.Instance, 0, len(instances))
	for i := range instances {
		ins := instances[i]
		beatTime, ok := instanceLastBeatTimes[ins.GetId()]
		if !ok {
			continue
		}
		needProtect := NeedZeroProtect(lastBeat, beatTime, ins.GetTtl())
		if !needProtect {
			finalInstances = append(finalInstances, ins)
			continue
		}
		cloneIns := ins.DeepClone()
		cloneIns.SetHealthy(true)
		finalInstances = append(finalInstances, cloneIns)
		zeroProtectIns = append(zeroProtectIns, cloneIns)
	}

	if len(zeroProtectIns) != 0 {
		svcName := curCluster.GetClusters().GetServiceInstances().GetService()
		nsName := curCluster.GetClusters().GetServiceInstances().GetNamespace()
		log.GetStatLogger().Infof("[Router][ZeroProtect] namespace:%s service:%s zero protect", svcName, nsName,
			zap.Any("instances", zeroProtectIns))
	}

	finalCluster := model.NewServiceClusters(model.NewDefaultServiceInstances(model.ServiceInfo{
		Service:   curCluster.GetClusters().GetServiceInstances().GetService(),
		Namespace: curCluster.GetClusters().GetServiceInstances().GetNamespace(),
		Metadata:  curCluster.GetClusters().GetServiceInstances().GetMetadata(),
	}, finalInstances))
	return finalCluster
}

func NeedZeroProtect(lastBeat, beatTime, ttl int64) bool {
	return lastBeat-3*ttl > beatTime && beatTime <= lastBeat
}

// Enable 是否需要启动规则路由
func (g *ZeroProtectFilter) Enable(routeInfo *servicerouter.RouteInfo, clusters model.ServiceClusters) bool {
	return true
}

// init 注册插件
func init() {
	plugin.RegisterPlugin(&ZeroProtectFilter{})
}
