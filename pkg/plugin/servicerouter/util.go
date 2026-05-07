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

package servicerouter

import (
	"sync"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/sdk"
)

// 存放路由结果对象的全局池
var routeResultPool = &sync.Pool{}

// GetRouteResultPool 获取路由结果对象池
func GetRouteResultPool() *sync.Pool {
	return routeResultPool
}

// GetFilterInstances 根据服务路由链，过滤服务节点，返回对应的服务列表
func GetFilterInstances(ctx sdk.ValueContext, routers []ServiceRouter, routeInfo *RouteInfo,
	serviceInstances model.ServiceInstances) ([]model.Instance, *model.Cluster, *model.ServiceInfo, error) {
	if len(routers) == 0 {
		return serviceInstances.GetInstances(), nil, nil, nil
	}
	result, err := GetFilterCluster(ctx, routers, routeInfo, serviceInstances.GetServiceClusters())
	if err != nil {
		return nil, nil, nil, err
	}
	defer GetRouteResultPool().Put(result)
	if nil != result.RedirectDestService {
		return nil, nil, result.RedirectDestService, nil
	}
	instances, _ := result.OutputCluster.GetInstances()
	cls := result.OutputCluster
	// 提供给外部的接口，因此无需进行复用
	cls.SetReuse(false)
	return instances, result.OutputCluster, nil, nil
}

// processServiceRouters 执行路由链.
//
// runFilterOnlyFallback 控制是否在链尾追加一次 FilterOnlyRouter 作为全死全活兜底:
//   - 主链调用方应传 true, 与历史行为保持一致;
//   - 前置链 (beforeChain) 调用方必须传 false, 否则 FilterOnly 在前置链尾部运行时
//     会调用 SetIgnoreFilterOnlyOnEndChain(true), 导致上层 getServiceRoutedInstances
//     误判"前置链已出最终结果"从而跳过主链 (ruleBasedRouter / nearbyBasedRouter /
//     dstMetaRouter 等), 让用户侧看到"路由规则完全不生效"的 bug.
func processServiceRouters(ctx sdk.ValueContext, routers []ServiceRouter, routeInfo *RouteInfo,
	svcClusters model.ServiceClusters, cluster *model.Cluster, runFilterOnlyFallback bool) (*RouteResult, model.SDKError) {
	var result *RouteResult
	var err error
	logCtx := ctx.GetContextLogger()
	if logCtx.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		sourceStr := model.ToStringService(routeInfo.SourceService, true)
		destStr := model.ToStringService(routeInfo.DestService, true)
		instancesCount := cluster.GetClusterValue().GetInstancesSet(false, false).Count()
		logCtx.GetBaseLogger().Debugf("processServiceRouters: start, source=%s, dest=%s, routers=%d, instances=%v",
			sourceStr, destStr, len(routers), instancesCount)
	}

	for _, router := range routers {
		routerName := router.Name()
		isRouterEnabled := routeInfo.IsRouterEnable(router.ID())
		isEnabled := router.Enable(routeInfo, svcClusters)

		if !isRouterEnabled || !isEnabled {
			if logCtx.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
				logCtx.GetBaseLogger().Debugf("processServiceRouters: router=%v skipped (routerEnabled=%v, enabled=%v)",
					routerName, isRouterEnabled, isEnabled)
			}
			continue
		}

		if nil != result {
			// 回收，下一步即将被新值替换
			GetRouteResultPool().Put(result)
		}
		result, err = router.GetFilteredInstances(routeInfo, svcClusters, cluster)
		// 判断result.OutputCluster是否是同一个地址，如果是同一个地址不要回收
		if result != nil && result.OutputCluster != cluster {
			cluster.PoolPut()
		}
		if err != nil || result == nil {
			logCtx.GetBaseLogger().Errorf("processServiceRouters: router=%v failed, error=%v", routerName, err)
			return nil, err.(model.SDKError)
		}
		if nil != result.RedirectDestService {
			// 转发规则
			if logCtx.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
				redirectStr := model.ToStringService(result.RedirectDestService, true)
				logCtx.GetBaseLogger().Debugf("processServiceRouters: router=%v redirect to %s", routerName, redirectStr)
			}
			return result, nil
		}
		cluster = result.OutputCluster
		if logCtx.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			instances := cluster.GetClusterValue().GetInstancesSet(false, false).GetRealInstances()
			logCtx.GetBaseLogger().Debugf("processServiceRouters: router=%v done, instances=%s, status=%s", routerName,
				instances, result.Status.String())
		}
	}
	if runFilterOnlyFallback && !routeInfo.ignoreFilterOnlyOnEndChain {
		// 需要执行一遍全死全活
		if nil != result {
			// 回收，下一步即将被新值替换
			GetRouteResultPool().Put(result)
		}
		result, err = routeInfo.FilterOnlyRouter.GetFilteredInstances(routeInfo, svcClusters, cluster)
		if result != nil && result.OutputCluster != cluster {
			cluster.PoolPut()
		}
		if err != nil || result == nil {
			logCtx.GetBaseLogger().Errorf("processServiceRouters: FilterOnlyRouter failed, error=%v", err)
			return nil, err.(model.SDKError)
		}
		cluster = result.OutputCluster
		if logCtx.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			instances := cluster.GetClusterValue().GetInstancesSet(false, false).GetRealInstances()
			logCtx.GetBaseLogger().Debugf("processServiceRouters: FilterOnlyRouter done, instances=%s", instances)
		}
	}
	return result, nil
}

// GetFilterCluster 根据服务路由链，过滤服务节点，返回对应的cluster.
// 该入口用于主链场景, 会在链尾追加一次 FilterOnlyRouter 兜底, 保证全死全活语义.
// 前置链(beforeChain)场景请使用 GetFilterClusterBefore, 避免 FilterOnly 抢占 ignoreFilterOnlyOnEndChain
// 标记导致主链被跳过.
func GetFilterCluster(ctx sdk.ValueContext, routers []ServiceRouter, routeInfo *RouteInfo,
	svcClusters model.ServiceClusters) (*RouteResult, model.SDKError) {
	return getFilterClusterImpl(ctx, routers, routeInfo, svcClusters, nil, true)
}

// GetFilterClusterBefore 用于前置链(beforeChain)的路由链过滤.
// 与 GetFilterCluster 的差别仅在于: 链尾不追加 FilterOnlyRouter 兜底.
//
// 前置链若在链尾跑 FilterOnly, FilterOnly.GetFilteredInstances 内部会调用
// SetIgnoreFilterOnlyOnEndChain(true) (filteronly/router.go) 将 routeInfo 标记置位,
// 上层 getServiceRoutedInstances 看到此位为 true 会误以为"前置链已产出最终实例集"
// 从而整条主链被跳过 (ruleBasedRouter / nearbyBasedRouter / dstMetaRouter 都不会执行),
// 导致规则路由/就近路由/元数据路由全部失效(#issue: beforeChain 启用 laneRouter 后的回归问题).
func GetFilterClusterBefore(ctx sdk.ValueContext, routers []ServiceRouter, routeInfo *RouteInfo,
	svcClusters model.ServiceClusters) (*RouteResult, model.SDKError) {
	return getFilterClusterImpl(ctx, routers, routeInfo, svcClusters, nil, false)
}

// GetFilterClusterWithin 在指定的初始 cluster 范围内执行路由链过滤。
// withinCluster 为 nil 时自动创建新 cluster（等价于 GetFilterCluster）。
// 用于将前置链的输出 cluster 作为主链的输入，实现 beforeChain → chain 的串联。
func GetFilterClusterWithin(ctx sdk.ValueContext, routers []ServiceRouter, routeInfo *RouteInfo,
	svcClusters model.ServiceClusters, withinCluster *model.Cluster) (*RouteResult, model.SDKError) {
	return getFilterClusterImpl(ctx, routers, routeInfo, svcClusters, withinCluster, true)
}

// getFilterClusterImpl 是 GetFilterCluster / GetFilterClusterWithin / GetFilterClusterBefore 的共同实现.
// runFilterOnlyFallback 控制是否在主循环结束后追加一次 FilterOnlyRouter, 见
// processServiceRouters 的说明.
func getFilterClusterImpl(ctx sdk.ValueContext, routers []ServiceRouter, routeInfo *RouteInfo,
	svcClusters model.ServiceClusters, withinCluster *model.Cluster,
	runFilterOnlyFallback bool) (*RouteResult, model.SDKError) {
	if err := routeInfo.Validate(); err != nil {
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidArgument, err, "fail to validate routeInfo")
	}
	var result *RouteResult
	var err model.SDKError
	routerCount := len(routers)
	pluginsIf, _ := ctx.GetValue(sdk.ContextKeyPlugins)
	plugins := pluginsIf.(plugin.Supplier)
	cluster := withinCluster
	if cluster == nil {
		cluster = model.NewCluster(svcClusters, nil)
	}
	if routerCount > 0 {
		if nil == routeInfo.chainEnables {
			routeInfo.Init(plugins)
		}
		result, err = processServiceRouters(ctx, routers, routeInfo, svcClusters, cluster, runFilterOnlyFallback)
		if err != nil {
			return nil, err
		}
		if nil != result && nil != result.RedirectDestService {
			return result, nil
		}
	} else {
		cluster.HasLimitedInstances = true
	}
	if nil == result {
		result = PoolGetRouteResult(ctx)
		result.OutputCluster = cluster
	}
	result.OutputCluster.GetClusterValue()
	if routerCount > 0 {
		handlers := plugins.GetEventSubscribers(common.OnRoutedClusterReturned)
		if len(handlers) > 0 {
			eventObj := &common.PluginEvent{
				EventType:   common.OnRoutedClusterReturned,
				EventObject: result.OutputCluster,
			}
			for _, h := range handlers {
				_ = h.Callback(eventObj)
			}
		}
	}
	return result, nil
}

// PoolGetRouteResult 通过池子获取路由结果
func PoolGetRouteResult(ctx sdk.ValueContext) *RouteResult {
	value := GetRouteResultPool().Get()
	if nil == value {
		return &RouteResult{}
	}
	result := value.(*RouteResult)
	result.OutputCluster = nil
	result.RedirectDestService = nil
	result.Status = Normal
	return result
}
