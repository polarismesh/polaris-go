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
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/hashicorp/go-multierror"
)

//路由信息
type RouteInfo struct {
	//源服务信息
	SourceService model.ServiceMetadata
	//源路由规则
	SourceRouteRule model.ServiceRule
	//目标服务信息
	DestService model.ServiceMetadata
	//目标路由规则
	DestRouteRule model.ServiceRule
	//在路由匹配过程中使用到的环境变量
	EnvironmentVariables map[string]string
	//全死全活路由插件，用于做路由兜底
	//路由链不为空，但是没有走filterOnly，则使用该路由插件来进行兜底
	FilterOnlyRouter ServiceRouter
	//标识是否不需要执行全死全活兜底
	//对于全死全活插件，以及就近路由插件等，已经做了最小实例数检查，则可以设置该属性为true
	//需要插件内部进行设置
	ignoreFilterOnlyOnEndChain bool
	//可动态调整路由插件是否启用，不存在或者为true代表启用
	//key为路由插件的id
	chainEnables map[int32]bool
	//是否开启元数据匹配不到时启用自定义匹配规则，仅用于dstMetadata路由插件
	EnableFailOverDefaultMeta bool
	//自定义匹配规则，仅当EnableFailOverDefaultMeta为true时生效
	FailOverDefaultMeta model.FailOverDefaultMetaConfig
	//金丝雀
	Canary string
	//进行匹配的规则类型，如规则路由有入规则和出规则之分
	MatchRuleType RuleType
}

//初始化map
func (r *RouteInfo) Init(supplier plugin.Supplier) {
	registeredRouters := plugin.GetPluginCount(common.TypeServiceRouter)
	r.chainEnables = make(map[int32]bool, registeredRouters)
}

//设置是否需要执行全死全活插件兜底
func (r *RouteInfo) SetIgnoreFilterOnlyOnEndChain(run bool) {
	r.ignoreFilterOnlyOnEndChain = run
}

//清理值
func (r *RouteInfo) ClearValue() {
	r.DestService = nil
	r.SourceService = nil
	r.DestRouteRule = nil
	r.SourceService = nil
	r.FilterOnlyRouter = nil
	r.MatchRuleType = UnknownRule
	r.ignoreFilterOnlyOnEndChain = false
	for k := range r.chainEnables {
		r.chainEnables[k] = true
	}
}

//校验入参
func (r *RouteInfo) Validate() error {
	var errs error
	if nil == r.DestService {
		errs = multierror.Append(errs, fmt.Errorf("routeInfo: destService is required"))
	}
	if nil == r.FilterOnlyRouter {
		errs = multierror.Append(errs, fmt.Errorf("routeInfo: filterOnlyRouter is required"))
	}
	return errs
}

//路由插件是否启用
func (r *RouteInfo) IsRouterEnable(routerId int32) bool {
	var enable, ok bool
	if enable, ok = r.chainEnables[routerId]; !ok {
		return true
	}
	return enable
}

//设置是否启用路由插件
func (r *RouteInfo) SetRouterEnable(routerId int32, enable bool) {
	r.chainEnables[routerId] = enable
}

type RuleType int

const (
	UnknownRule RuleType = 0
	DestRule    RuleType = 1
	SrcRule     RuleType = 2
)

//路由结束状态
type RouteStatus int

const (
	//正常结束
	Normal RouteStatus = 0
	//全活
	RecoverAll RouteStatus = 1
	//降级到城市
	DegradeToCity RouteStatus = 2
	//降级到大区
	DegradeToRegion RouteStatus = 3
	//降级到全部区域
	DegradeToAll RouteStatus = 4
	//降级到不完全匹配的金丝雀节点
	DegradeToNotMatchCanary RouteStatus = 5
	//降级到非金丝雀节点
	DegradeToNotCanary RouteStatus = 6
	//正常环境降级到金丝雀环境
	DegradeToCanary RouteStatus = 7
	//金丝雀环境发生异常
	LimitedCanary RouteStatus = 8
	//非金丝雀环境发生异常
	LimitedNoCanary RouteStatus = 9
	//降级使用filterOnly
	DegradeToFilterOnly RouteStatus = 10
)

var routeStatusMap = map[RouteStatus]string{
	Normal:                  "Normal",
	RecoverAll:              "RecoverAll",
	DegradeToCity:           "DegradeToCity",
	DegradeToRegion:         "DegradeToRegion",
	DegradeToAll:            "DegradeToAll",
	DegradeToNotMatchCanary: "DegradeToNotMatchCanary",
	DegradeToNotCanary:      "DegradeToNotCanary",
	DegradeToCanary:         "DegradeToCanary",
	LimitedCanary:           "LimitedCanary",
	LimitedNoCanary:         "LimitedNoCanary",
	DegradeToFilterOnly:     "DegradeToFilterOnly",
}

func (rs RouteStatus) String() string {
	return routeStatusMap[rs]
}

//路由结果信息
type RouteResult struct {
	//根据路由规则重定向的目标服务，无需重定向则返回空
	RedirectDestService *model.ServiceInfo
	//根据路由规则过滤后的目标服务集群，假如重定向则列表为空
	OutputCluster *model.Cluster
	//路由结束状态
	Status RouteStatus
}

//服务路由链结构
type RouterChain struct {
	//服务路由链
	Chain []ServiceRouter
}

//ServiceRouter 【扩展点接口】服务路由
type ServiceRouter interface {
	plugin.Plugin
	//当前是否需要启动该服务路由插件
	Enable(routeInfo *RouteInfo, serviceClusters model.ServiceClusters) bool
	//获取通过规则过滤后的服务集群信息以及服务实例列表
	//routeInfo: 路由信息，包括源和目标服务的实例及路由规则
	//serviceClusters：服务级缓存信息
	//withinCluster：上一个环节过滤的集群信息，本次路由需要继承
	GetFilteredInstances(
		routeInfo *RouteInfo, serviceClusters model.ServiceClusters, withinCluster *model.Cluster) (*RouteResult, error)
}

//初始化
func init() {
	plugin.RegisterPluginInterface(common.TypeServiceRouter, new(ServiceRouter))
}
