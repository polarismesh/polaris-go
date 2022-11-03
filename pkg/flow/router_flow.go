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
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/flow/data"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/servicerouter"
)

// ProcessRouters 执行路由链过滤，返回经过路由后的实例列表
func (e *Engine) ProcessRouters(req *model.ProcessRoutersRequest) (*model.InstancesResponse, error) {
	routers, err := e.parseRouters(req.Routers)
	if nil != err {
		return nil, err
	}
	commonRequest := data.PoolGetCommonInstancesRequest(e.plugins)
	commonRequest.InitByProcessRoutersRequest(req, e.configuration, routers)
	resp, err := e.doSyncGetInstances(commonRequest)
	e.syncInstancesReportAndFinalize(commonRequest)
	return resp, err
}

func (e *Engine) parseRouters(routers []string) ([]servicerouter.ServiceRouter, error) {
	var svcRouters []servicerouter.ServiceRouter
	if len(routers) == 0 {
		return svcRouters, nil
	}
	// add the filter only plugin to do the unhealthy filter
	if routers[len(routers)-1] != config.DefaultServiceRouterFilterOnly {
		routers = append(routers, config.DefaultServiceRouterFilterOnly)
	}
	for _, router := range routers {
		targetPlugin, err := e.plugins.GetPlugin(common.TypeServiceRouter, router)
		if err != nil {
			return nil, err
		}
		svcRouters = append(svcRouters, targetPlugin.(servicerouter.ServiceRouter))
	}
	return svcRouters, nil
}

// ProcessLoadBalance 执行负载均衡策略，返回负载均衡后的实例
func (e *Engine) ProcessLoadBalance(req *model.ProcessLoadBalanceRequest) (*model.OneInstanceResponse, error) {
	// 方法开始时间
	commonRequest := data.PoolGetCommonInstancesRequest(e.plugins)
	commonRequest.InitByProcessLoadBalanceRequest(req, e.configuration)
	startTime := e.globalCtx.Now()
	resp, err := e.doLoadBalanceToOneInstance(startTime, commonRequest)
	e.syncInstancesReportAndFinalize(commonRequest)
	return resp, err
}
