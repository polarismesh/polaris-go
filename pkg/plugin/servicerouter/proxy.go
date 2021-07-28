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

//proxy of ServiceRouter
type Proxy struct {
	ServiceRouter
	engine model.Engine
}

//路由调用统计数据
type RouteGauge struct {
	model.EmptyInstanceGauge
	PluginID         int32
	SrcService       model.ServiceKey
	RetCode          model.ErrCode
	Cluster          *model.Cluster
	ServiceInstances model.ServiceInstances
	RouteRuleType    RuleType
	Status           RouteStatus
}

// 清理gauge
func (r *RouteGauge) clear() {
	r.PluginID = 0
	r.SrcService.Namespace = ""
	r.SrcService.Service = ""
	r.RetCode = 0
	r.Cluster = nil
	r.ServiceInstances = nil
	r.RouteRuleType = UnknownRule
	r.Status = Normal
}

//获取PluginMethodGauge的pool
var routeStatPool = &sync.Pool{}

//从pluginStatPool中获取PluginMethodGauge
func getRouteStatFromPool() *RouteGauge {
	value := routeStatPool.Get()
	if nil == value {
		return &RouteGauge{}
	}
	res := value.(*RouteGauge)
	res.clear()
	return res
}

//从缓存池中获取路由统计信息结构
func poolPutRouteStat(gauge *RouteGauge) {
	routeStatPool.Put(gauge)
}

//上报路由调用信息
func (p *Proxy) reportRouteStat(routeInfo *RouteInfo, errCode model.ErrCode,
	svcInstances model.ServiceInstances, res *RouteResult) {
	gauge := getRouteStatFromPool()
	gauge.RetCode = errCode
	gauge.PluginID = p.ID()
	gauge.ServiceInstances = svcInstances
	if routeInfo.SourceService != nil {
		gauge.SrcService = model.ServiceKey{
			Namespace: routeInfo.SourceService.GetNamespace(),
			Service:   routeInfo.SourceService.GetService(),
		}
	}
	gauge.RouteRuleType = routeInfo.MatchRuleType
	if res != nil {
		gauge.Cluster = res.OutputCluster
		gauge.Status = res.Status
	}

	p.engine.SyncReportStat(model.RouteStat, gauge)
	poolPutRouteStat(gauge)
}

//设置
func (p *Proxy) SetRealPlugin(plug plugin.Plugin, engine model.Engine) {
	p.ServiceRouter = plug.(ServiceRouter)
	p.engine = engine
}

//proxy ServiceRouter GetFilteredInstances
func (p *Proxy) GetFilteredInstances(
	routeInfo *RouteInfo, serviceClusters model.ServiceClusters, withinCluster *model.Cluster) (*RouteResult, error) {
	result, err := p.ServiceRouter.GetFilteredInstances(routeInfo, serviceClusters, withinCluster)
	p.reportRouteStat(routeInfo, model.GetErrorCodeFromError(err),
		withinCluster.GetClusters().GetServiceInstances(), result)
	return result, err
}

//注册proxy
func init() {
	plugin.RegisterPluginProxy(common.TypeServiceRouter, &Proxy{})
}
