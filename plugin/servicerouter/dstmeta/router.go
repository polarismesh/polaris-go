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
	"github.com/polarismesh/polaris-go/pkg/sdk"
)

// InstancesFilter 基于目标服务元数据的服务路由插件
type InstancesFilter struct {
	*plugin.PluginBase
	percentOfMinInstances float64
	valueCtx              sdk.ValueContext
	recoverAll            bool
	// 上下文日志
	logCtx *log.ContextLogger
}

// Type 插件类型
func (g *InstancesFilter) Type() common.Type {
	return common.TypeServiceRouter
}

// Name 插件名，一个类型下插件名唯一
func (g *InstancesFilter) Name() string {
	return config.DefaultServiceRouterDstMeta
}

// Init 初始化插件
func (g *InstancesFilter) Init(ctx *plugin.InitContext) error {
	// 获取最小返回实例比例
	g.PluginBase = plugin.NewPluginBase(ctx)
	g.percentOfMinInstances = ctx.Config.GetConsumer().GetServiceRouter().GetPercentOfMinInstances()
	g.recoverAll = ctx.Config.GetConsumer().GetServiceRouter().IsEnableRecoverAll()
	g.valueCtx = ctx.ValueCtx
	g.logCtx = ctx.ValueCtx.GetContextLogger()
	return nil
}

// Destroy 销毁插件，可用于释放资源
func (g *InstancesFilter) Destroy() error {
	return nil
}

// GetFilteredInstances 插件模式进行服务实例过滤，并返回过滤后的实例列表
func (g *InstancesFilter) GetFilteredInstances(
	routeInfo *servicerouter.RouteInfo,
	clusters model.ServiceClusters,
	withinCluster *model.Cluster) (*servicerouter.RouteResult, error) {
	dstMetadata := routeInfo.DestService.GetMetadata()
	debugEnabled := g.logCtx.GetRouteLogger().IsLevelEnabled(log.DebugLog)

	// 入口摘要:每次请求 1 行 DEBUG,记录目标服务 + 期望的 metadata。
	// 后续命中/未命中的细节由分支日志补充。
	if debugEnabled {
		g.logCtx.GetRouteLogger().Debugf(
			"[Router][DstMeta] start filtering, dest=%s/%s, dstMetadata=%v, "+
				"failOverEnabled=%v, withinClusterMeta=%v",
			routeInfo.DestService.GetNamespace(), routeInfo.DestService.GetService(),
			dstMetadata, routeInfo.EnableFailOverDefaultMeta, withinCluster.ComposeMetaValue)
	}

	targetCluster := g.getTargetCluster(clusters, withinCluster, dstMetadata)

	if len(dstMetadata) > 0 {
		instSet := targetCluster.GetClusterValue().GetInstancesSet(true, true)
		if instSet.Count() > 0 {
			// 出口摘要: Info 级,在不开 DEBUG_MODE 时也能看到 dstmeta 路由的输出。
			// (DEBUG 不再单独打印 "metadata matched",信息已被这条 INFO 完全覆盖)
			g.logCtx.GetRouteLogger().Infof(
				"[Router][DstMeta] result: dest=%s/%s, status=metadata-matched, metadata=%v, instances=%d",
				routeInfo.DestService.GetNamespace(), routeInfo.DestService.GetService(),
				dstMetadata, instSet.Count())
			return g.getResult(targetCluster), nil
		}

		targetCluster.PoolPut()
		if routeInfo.EnableFailOverDefaultMeta {
			// failover 诊断 DEBUG: 命中 0 实例 → 走 failover 的原因,默认 INFO 行无法体现
			// "源 metadata 没有匹配实例" 这层语义,所以保留 DEBUG。
			if debugEnabled {
				g.logCtx.GetRouteLogger().Debugf(
					"[Router][DstMeta] dstMetadata=%v matched 0 instances, fallback to failOverDefaultMeta type=%v",
					dstMetadata, routeInfo.FailOverDefaultMeta.Type)
			}
			targetCluster, err := g.failOverDefaultMetaHandler(clusters, withinCluster, routeInfo)
			if err != nil {
				return nil, err
			}
			routeInfo.SetIgnoreFilterOnlyOnEndChain(true)
			// 出口摘要: failover 路径也输出 Info,方便定位降级行为。
			foInstCount := targetCluster.GetClusterValue().GetInstancesSet(false, false).Count()
			g.logCtx.GetRouteLogger().Infof(
				"[Router][DstMeta] result: dest=%s/%s, status=failover, type=%v, instances=%d",
				routeInfo.DestService.GetNamespace(), routeInfo.DestService.GetService(),
				routeInfo.FailOverDefaultMeta.Type, foInstCount)
			return g.getResult(targetCluster), nil
		}

		// 元数据不匹配 + 未启用 failover → 报错路径,metaNotMatchError 内部已 Warn,
		// 此处不再重复输出 Info,以免与下方 Warnf 重复。
		return nil, g.metaNotMatchError(routeInfo)
	}

	// 兜底分支:Enable() 已经把空 metadata 的请求过滤掉,理论上不会到这里。
	// 仅在 DEBUG 级别打 1 行作为防御性日志,便于排查 Enable 与 GetFilteredInstances
	// 调用顺序异常的情况;不输出 INFO,避免空 metadata 误以为 dstmeta 路由生效。
	if debugEnabled {
		g.logCtx.GetRouteLogger().Debugf(
			"[Router][DstMeta] no dst metadata required, dest=%s/%s, pass through (Enable should have excluded this case)",
			routeInfo.DestService.GetNamespace(), routeInfo.DestService.GetService())
	}
	return g.getResult(targetCluster), nil
}

// 元数据匹配不到时处理自定义匹配规则
func (g *InstancesFilter) failOverDefaultMetaHandler(clusters model.ServiceClusters, withinCluster *model.Cluster, routeInfo *servicerouter.RouteInfo) (*model.Cluster, error) {
	if routeInfo.FailOverDefaultMeta.Type == model.GetOneHealth {
		return g.getOneHealthHandler(clusters, withinCluster, routeInfo)
	} else if routeInfo.FailOverDefaultMeta.Type == model.NotContainMetaKey {
		return g.notContainMetaKeyHandler(clusters, withinCluster, routeInfo)
	} else if routeInfo.FailOverDefaultMeta.Type == model.CustomMeta {
		return g.customMetaHandler(clusters, withinCluster, routeInfo)
	}

	return nil, model.NewSDKError(model.ErrCodeAPIInvalidArgument, fmt.Errorf("failOverDefaultMeta Type not match"), "fail to enable failOverDefaultMeta")
}

// 通配所有可用ip实例，等于关闭meta路由
func (g *InstancesFilter) getOneHealthHandler(clusters model.ServiceClusters, withinCluster *model.Cluster, routeInfo *servicerouter.RouteInfo) (*model.Cluster, error) {
	targetCluster := g.getTargetCluster(clusters, withinCluster, nil)
	clusterValue := targetCluster.GetClusterValue()
	instSet := g.getInstSet(clusterValue)
	return targetCluster, g.validateInstSet(instSet, routeInfo)
}

// 匹配不带 metaData key路由
func (g *InstancesFilter) notContainMetaKeyHandler(clusters model.ServiceClusters, withinCluster *model.Cluster, routeInfo *servicerouter.RouteInfo) (*model.Cluster, error) {
	targetCluster := g.getTargetCluster(clusters, withinCluster, routeInfo.DestService.GetMetadata())
	clusterValue := targetCluster.GetNotContainMetaKeyClusterValue()
	instSet := g.getInstSet(clusterValue)
	return targetCluster, g.validateInstSet(instSet, routeInfo)
}

// 匹配自定义meta
func (g *InstancesFilter) customMetaHandler(clusters model.ServiceClusters, withinCluster *model.Cluster, routeInfo *servicerouter.RouteInfo) (*model.Cluster, error) {
	if err := validateEmptyKey(routeInfo.FailOverDefaultMeta.Meta); err != nil {
		return nil, err
	}
	targetCluster := g.getTargetCluster(clusters, withinCluster, routeInfo.FailOverDefaultMeta.Meta)
	clusterValue := targetCluster.GetClusterValue()
	instSet := g.getInstSet(clusterValue)
	return targetCluster, g.validateInstSet(instSet, routeInfo)
}

func (g *InstancesFilter) getTargetCluster(clusters model.ServiceClusters, withinCluster *model.Cluster, dstMetadata map[string]string) *model.Cluster {
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
	g.logCtx.GetRouteLogger().Warnf("[Router][DstMeta] %s", errorText)
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

// init 注册插件
func init() {
	plugin.RegisterPlugin(&InstancesFilter{})
}

// Enable 是否需要启动规则路由。
//
// 仅当 DestService.Metadata 非空时启用。本方法不输出日志,因为:
//   - GetFilteredInstances 入口的 "start filtering" DEBUG 行已经包含了
//     dest 服务名 + dstMetadata,完全覆盖 Enable 这里的诊断信息;
//   - Enable 在每次路由链调用时都会被框架调用,额外打印只会刷屏。
func (g *InstancesFilter) Enable(routeInfo *servicerouter.RouteInfo, clusters model.ServiceClusters) bool {
	return len(routeInfo.DestService.GetMetadata()) != 0
}
