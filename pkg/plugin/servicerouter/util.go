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
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"sync"
)

//存放路由结果对象的全局池
var routeResultPool = &sync.Pool{}

//获取路由结果对象池
func GetRouteResultPool() *sync.Pool {
	return routeResultPool
}

// 根据服务路由链，过滤服务节点，返回对应的服务列表
func GetFilterInstances(ctx model.ValueContext, routers []ServiceRouter, routeInfo *RouteInfo,
	serviceInstances model.ServiceInstances) ([]model.Instance, *model.Cluster, *model.ServiceInfo, error) {
	if len(routers) == 0 {
		return serviceInstances.GetInstances(), nil, nil, nil
	}
	result, err := GetFilterCluster(ctx, routers, routeInfo, serviceInstances.GetServiceClusters())
	if nil != err {
		return nil, nil, nil, err
	}
	defer GetRouteResultPool().Put(result)
	if nil != result.RedirectDestService {
		return nil, nil, result.RedirectDestService, nil
	}
	instances, _ := result.OutputCluster.GetInstances()
	cls := result.OutputCluster
	//提供给外部的接口，因此无需进行复用
	cls.SetReuse(false)
	return instances, result.OutputCluster, nil, nil
}

//执行路由链
func processServiceRouters(ctx model.ValueContext, routers []ServiceRouter, routeInfo *RouteInfo,
	svcClusters model.ServiceClusters, cluster *model.Cluster) (*RouteResult, model.SDKError) {
	var result *RouteResult
	var err error
	for _, router := range routers {
		if !routeInfo.IsRouterEnable(router.ID()) || !router.Enable(routeInfo, svcClusters) {
			continue
		}
		if nil != result {
			//回收，下一步即将被新值替换
			GetRouteResultPool().Put(result)
		}
		result, err = router.GetFilteredInstances(routeInfo, svcClusters, cluster)
		// 判断result.OutputCluster是否是同一个地址，如果是同一个地址不要回收
		if result != nil && result.OutputCluster != cluster {
			cluster.PoolPut()
		}
		if nil != err {
			return nil, err.(model.SDKError)
		}
		if nil != result.RedirectDestService {
			//转发规则
			return result, nil
		}
		cluster = result.OutputCluster
	}
	if !routeInfo.ignoreFilterOnlyOnEndChain {
		//需要执行一遍全死全活
		if nil != result {
			//回收，下一步即将被新值替换
			GetRouteResultPool().Put(result)
		}
		result, err = routeInfo.FilterOnlyRouter.GetFilteredInstances(routeInfo, svcClusters, cluster)
		if result != nil && result.OutputCluster != cluster {
			cluster.PoolPut()
		}
		if nil != err {
			return nil, err.(model.SDKError)
		}
		cluster = result.OutputCluster
	}
	return result, nil
}

// 根据服务理由链，过滤服务节点，返回对应的cluster
func GetFilterCluster(ctx model.ValueContext, routers []ServiceRouter, routeInfo *RouteInfo,
	svcClusters model.ServiceClusters) (*RouteResult, model.SDKError) {
	if err := routeInfo.Validate(); nil != err {
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidArgument, err, "fail to validate routeInfo")
	}
	var result *RouteResult
	var err model.SDKError
	routerCount := len(routers)
	pluginsIf, _ := ctx.GetValue(model.ContextKeyPlugins)
	plugins := pluginsIf.(plugin.Supplier)
	cluster := model.NewCluster(svcClusters, nil)
	if routerCount > 0 {
		if nil == routeInfo.chainEnables {
			routeInfo.Init(plugins)
		}
		result, err = processServiceRouters(ctx, routers, routeInfo, svcClusters, cluster)
		if nil != err {
			return nil, err
		}
		if nil != result && nil != result.RedirectDestService {
			//重定向服务优先返回
			return result, nil
		}
	} else {
		//没有路由规则，则返回全量服务实例
		cluster.HasLimitedInstances = true
	}
	if nil == result {
		result = PoolGetRouteResult(ctx)
		result.OutputCluster = cluster
	}
	//初始化集群缓存
	result.OutputCluster.GetClusterValue()
	if routerCount > 0 {
		handlers := plugins.GetEventSubscribers(common.OnRoutedClusterReturned)
		if len(handlers) > 0 {
			eventObj := &common.PluginEvent{
				EventType:   common.OnRoutedClusterReturned,
				EventObject: result.OutputCluster,
			}
			for _, h := range handlers {
				h.Callback(eventObj)
			}
		}
	}
	return result, nil
}

//通过池子获取路由结果
func PoolGetRouteResult(ctx model.ValueContext) *RouteResult {
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
