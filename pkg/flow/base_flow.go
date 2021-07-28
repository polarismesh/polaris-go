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

package flow

import (
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/flow/data"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/localregistry"
	"github.com/polarismesh/polaris-go/pkg/plugin/servicerouter"
	"github.com/hashicorp/go-multierror"
)

//过滤器参数集合
type cacheFilters struct {
	//命名空间
	namespace string
	//服务名
	service string
	//源服务的路由filter，假如源服务为空，则为空
	sourceRouteFilter *localregistry.RuleFilter
	//目标服务的路由filter
	destRouteFilter *localregistry.RuleFilter
	//实例获取filter
	destInstancesFilter *localregistry.InstancesFilter
}

//转换为上下文查询标识
func (c *cacheFilters) toContextKey() (dstInstKey *ContextKey, srcRouterKey *ContextKey, dstRouterKey *ContextKey) {
	if nil != c.destInstancesFilter {
		dstInstKey = &ContextKey{
			ServiceKey: &model.ServiceKey{
				Namespace: c.destInstancesFilter.Namespace,
				Service:   c.destInstancesFilter.Service},
			Operation: keyDstInstances}
	}
	if nil != c.sourceRouteFilter {
		srcRouterKey = &ContextKey{
			ServiceKey: &model.ServiceKey{
				Namespace: c.sourceRouteFilter.Namespace,
				Service:   c.sourceRouteFilter.Service},
			Operation: keySourceRoute}
	}
	if nil != c.destRouteFilter {
		dstRouterKey = &ContextKey{
			ServiceKey: &model.ServiceKey{
				Namespace: c.destRouteFilter.Namespace,
				Service:   c.destRouteFilter.Service},
			Operation: keyDstRoute}
	}
	return dstInstKey, srcRouterKey, dstRouterKey
}

//实例请求转换为获取缓存的请求
func (e *Engine) instancesRequestToCacheFilters(request *model.GetInstancesRequest,
	redirectedService *model.ServiceInfo) *cacheFilters {
	filters := &cacheFilters{
		namespace: request.Namespace,
		service:   request.Service,
		destInstancesFilter: &localregistry.InstancesFilter{
			Service:           request.Service,
			Namespace:         request.Namespace,
			IsInternalRequest: false},
	}
	dstSvc := &model.ServiceKey{
		Service:   request.Service,
		Namespace: request.Namespace,
	}
	filters.destRouteFilter = &localregistry.RuleFilter{
		ServiceEventKey: model.ServiceEventKey{
			ServiceKey: *dstSvc,
			Type:       model.EventRouting,
		}}
	sourceInfo := request.SourceService
	if nil != sourceInfo && len(sourceInfo.Namespace) > 0 && len(sourceInfo.Service) > 0 {
		srcSvc := &model.ServiceKey{
			Service:   sourceInfo.Service,
			Namespace: sourceInfo.Namespace,
		}
		filters.sourceRouteFilter = &localregistry.RuleFilter{
			ServiceEventKey: model.ServiceEventKey{
				ServiceKey: *srcSvc,
				Type:       model.EventRouting,
			}}
	}
	if nil != redirectedService {
		buildRedirectedFilter(filters, redirectedService)
	}
	return filters
}

//构建重定向服务filter
func buildRedirectedFilter(filters *cacheFilters, redirectedService model.ServiceMetadata) {
	filters.service = redirectedService.GetService()
	filters.namespace = redirectedService.GetNamespace()
	filters.destInstancesFilter.Namespace = redirectedService.GetNamespace()
	filters.destInstancesFilter.Service = redirectedService.GetService()
	filters.destRouteFilter.Namespace = redirectedService.GetNamespace()
	filters.destRouteFilter.Service = redirectedService.GetService()
}

//同步加载缓存资源，包括实例以及规则
func getAndLoadCacheValues(registry localregistry.LocalRegistry,
	request model.CacheValueQuery, load bool) (*CombineNotifyContext, model.SDKError) {
	var notifiers []*SingleNotifyContext

	trigger := request.GetNotifierTrigger()
	dstService := request.GetDstService()
	srcService := request.GetSrcService()
	//先直接从内存数据获取
	if trigger.EnableDstInstances {
		instances := registry.GetInstances(dstService, false, false)
		if instances.IsInitialized() {
			request.SetDstInstances(instances)
			trigger.EnableDstInstances = false
		}
		if load && (instances.IsCacheLoaded() || !instances.IsInitialized()) {
			dstInstKey := &ContextKey{ServiceKey: dstService, Operation: keyDstInstances}
			log.GetBaseLogger().Debugf("value not initialized, scheduled context %s", dstInstKey)
			notifier, err := registry.LoadInstances(dstService)
			if nil != err {
				return nil, err.(model.SDKError)
			}
			notifiers = append(notifiers, NewSingleNotifyContext(dstInstKey, notifier))
		}
	}
	if trigger.EnableSrcRoute {
		routeRule := registry.GetServiceRouteRule(srcService, false)
		if routeRule.IsInitialized() {
			request.SetSrcRoute(routeRule)
			trigger.EnableSrcRoute = false
		}
		if load && (routeRule.IsCacheLoaded() || !routeRule.IsInitialized()) {
			srcRouterKey := &ContextKey{ServiceKey: srcService, Operation: keySourceRoute}
			log.GetBaseLogger().Debugf("value not initialized, scheduled context %s", srcRouterKey)
			notifier, err := registry.LoadServiceRouteRule(srcService)
			if nil != err {
				return nil, err.(model.SDKError)
			}
			notifiers = append(notifiers, NewSingleNotifyContext(srcRouterKey, notifier))
		}
	}
	if trigger.EnableDstRoute {
		routeRule := registry.GetServiceRouteRule(dstService, false)
		if routeRule.IsInitialized() {
			request.SetDstRoute(routeRule)
			trigger.EnableDstRoute = false
		}
		if load && (routeRule.IsCacheLoaded() || !routeRule.IsInitialized()) {
			dstRouterKey := &ContextKey{ServiceKey: dstService, Operation: keyDstRoute}
			log.GetBaseLogger().Debugf("value not initialized, scheduled context %s", dstRouterKey)
			notifier, err := registry.LoadServiceRouteRule(dstService)
			if nil != err {
				return nil, err.(model.SDKError)
			}
			notifiers = append(notifiers, NewSingleNotifyContext(dstRouterKey, notifier))
		}
	}
	if trigger.EnableDstRateLimit {
		rateLimitRule := registry.GetServiceRateLimitRule(dstService, false)
		if rateLimitRule.IsInitialized() {
			request.SetDstRateLimit(rateLimitRule)
			trigger.EnableDstRateLimit = false
		} else if load {
			dstRateLimitKey := &ContextKey{ServiceKey: dstService, Operation: keyDstRateLimit}
			log.GetBaseLogger().Debugf("value not initialized, scheduled context %s", dstRateLimitKey)
			notifier, err := registry.LoadServiceRateLimitRule(dstService)
			if nil != err {
				return nil, err.(model.SDKError)
			}
			notifiers = append(notifiers, NewSingleNotifyContext(dstRateLimitKey, notifier))
		}
	}
	if trigger.EnableMeshConfig {
		meshconfig := registry.GetMeshConfig(dstService, false)
		if meshconfig.IsInitialized() {
			request.SetMeshConfig(meshconfig)
			log.GetBaseLogger().Debugf("Mesh config IsInitialized")
			trigger.EnableMeshConfig = false
		} else {
			dstMeshConfigKey := &ContextKey{ServiceKey: dstService, Operation: keyDstMeshConfig}
			log.GetBaseLogger().Debugf("Mesh value not initialized, scheduled context %s", dstMeshConfigKey)
			notifier, err := registry.LoadMeshConfig(dstService)
			if nil != err {
				return nil, err.(model.SDKError)
			}
			notifiers = append(notifiers, NewSingleNotifyContext(dstMeshConfigKey, notifier))
		}
	}
	if trigger.EnableMesh {
		mesh := registry.GetMesh(dstService, false)
		if mesh.IsInitialized() {
			request.SetMeshConfig(mesh)
			log.GetBaseLogger().Debugf("Mesh IsInitialized")
			trigger.EnableMesh = false
		} else {
			dstMeshKey := &ContextKey{ServiceKey: dstService, Operation: keyDstMesh}
			log.GetBaseLogger().Debugf("Mesh value not initialized, scheduled context %s", dstMeshKey)
			notifier, err := registry.LoadMesh(dstService)
			if nil != err {
				return nil, err.(model.SDKError)
			}
			notifiers = append(notifiers, NewSingleNotifyContext(dstMeshKey, notifier))
		}
	}
	if trigger.EnableServices {
		services := registry.GetServicesByMeta(dstService, false)
		if services.IsInitialized() {
			//复用接口
			request.SetMeshConfig(services)
			log.GetBaseLogger().Debugf("services by meta IsInitialized")
			trigger.EnableServices = false
		} else {
			dstMeshConfigKey := &ContextKey{ServiceKey: dstService, Operation: keyDstServices}
			log.GetBaseLogger().Debugf("services value not initialized, scheduled context %s", dstMeshConfigKey)
			notifier, err := registry.LoadServices(dstService)
			if nil != err {
				return nil, err.(model.SDKError)
			}
			notifiers = append(notifiers, NewSingleNotifyContext(dstMeshConfigKey, notifier))
		}
	}
	//构造远程获取的复合上下文
	if len(notifiers) == 0 {
		return nil, nil
	}
	//deadline为空代表已经超时或者重试次数已经用完，因此无需继续重试获取
	return NewCombineNotifyContext(dstService, notifiers), nil
}

//尝试加载服务信息，来源包括缓存文件加载的信息
//返回值为是否成功加载了所需信息和这个过程中可能发生的错误
func tryGetServiceValuesFromCache(registry localregistry.LocalRegistry, request model.CacheValueQuery) (bool, error) {
	failNum := 0
	trigger := request.GetNotifierTrigger()
	dstService := request.GetDstService()
	srcService := request.GetSrcService()
	if trigger.EnableDstInstances {
		_, err := registry.LoadInstances(dstService)
		if nil != err {
			return false, err.(model.SDKError)
		}
		instances := registry.GetInstances(dstService, true, false)
		if instances.IsInitialized() {
			request.SetDstInstances(instances)
			trigger.EnableDstInstances = false
		} else {
			failNum++
		}
	}
	if trigger.EnableSrcRoute {
		_, err := registry.LoadServiceRouteRule(srcService)
		if nil != err {
			return false, err.(model.SDKError)
		}
		routeRule := registry.GetServiceRouteRule(srcService, true)
		if routeRule.IsInitialized() {
			request.SetSrcRoute(routeRule)
			trigger.EnableSrcRoute = false
		} else {
			failNum++
		}
	}
	if trigger.EnableDstRoute {
		_, err := registry.LoadServiceRouteRule(dstService)
		if nil != err {
			return false, err.(model.SDKError)
		}
		routeRule := registry.GetServiceRouteRule(dstService, true)
		if routeRule.IsInitialized() {
			request.SetDstRoute(routeRule)
			trigger.EnableDstRoute = false
		} else {
			failNum++
		}
	}
	if trigger.EnableDstRateLimit {
		_, err := registry.LoadServiceRateLimitRule(dstService)
		if nil != err {
			return false, err.(model.SDKError)
		}
		routeRule := registry.GetServiceRateLimitRule(dstService, true)
		if routeRule.IsInitialized() {
			request.SetDstRateLimit(routeRule)
			trigger.EnableDstRateLimit = false
		} else {
			failNum++
		}
	}
	if trigger.EnableMeshConfig {
		log.GetBaseLogger().Debugf("tryGetServiceValuesFromCache meshconfig")
		_, err := registry.LoadMeshConfig(dstService)
		if nil != err {
			return false, err.(model.SDKError)
		}
		mc := registry.GetMeshConfig(dstService, true)
		log.GetBaseLogger().Debugf("tryGetServiceValuesFromCache mc:", mc)
		if mc.IsInitialized() {
			request.SetMeshConfig(mc)
			trigger.EnableMeshConfig = false
		} else {
			failNum++
		}
	}
	if trigger.EnableServices {
		log.GetBaseLogger().Debugf("tryGetServiceValuesFromCache services")
		_, err := registry.LoadServices(dstService)
		if nil != err {
			return false, err.(model.SDKError)
		}
		//复用网格接口
		services := registry.GetMeshConfig(dstService, true)
		if services.IsInitialized() {
			request.SetMeshConfig(services)
			trigger.EnableServices = false
		} else {
			failNum++
		}
	}
	if trigger.EnableMesh {
		log.GetBaseLogger().Debugf("tryGetServiceValuesFromCache mesh, %v", dstService)
		_, err := registry.LoadMesh(dstService)
		if nil != err {
			return false, err.(model.SDKError)
		}
		mc := registry.GetMesh(dstService, true)
		log.GetBaseLogger().Debugf("tryGetServiceValuesFromCache mc:", mc)
		if mc.IsInitialized() {
			request.SetMeshConfig(mc)
			trigger.EnableMesh = false
		} else {
			failNum++
		}
	}
	if failNum > 0 {
		return false, nil
	}
	return true, nil
}

//afterLazyGetInstances 懒加载后执行的服务实例筛选流程
func (e *Engine) afterLazyGetInstances(
	req *data.CommonInstancesRequest) (cls *model.Cluster, redirected *model.ServiceInfo, err model.SDKError) {
	var result *servicerouter.RouteResult
	req.RouteInfo.FilterOnlyRouter = e.filterOnlyRouter
	// 服务路由
	if !req.SkipRouteFilter {
		result, err = e.getServiceRoutedInstances(req)
		if nil != err {
			return nil, nil, err
		}
	} else {
		result, err = servicerouter.GetFilterCluster(e.globalCtx, nil, &req.RouteInfo,
			req.DstInstances.GetServiceClusters())
		if nil != err {
			return nil, nil, err
		}
	}
	cls = result.OutputCluster
	redirected = result.RedirectDestService
	servicerouter.GetRouteResultPool().Put(result)
	return cls, redirected, nil
}

//把多个SDK error合成一个error
func combineSDKErrors(sdkErrs map[ContextKey]model.SDKError) error {
	var errs error
	for key, sdkErr := range sdkErrs {
		errs = multierror.Append(errs, fmt.Errorf("SDKError for %s, detail is %s", key, sdkErr))
	}
	return errs
}
