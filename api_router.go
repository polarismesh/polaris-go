/**
 * Tencent is pleased to support the open source community by making Polaris available.
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

package polaris

import (
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

type routerAPI struct {
	sdkCtx api.SDKContext
}

// process routers to filter instances
func (r *routerAPI) ProcessRouters(request *ProcessRoutersRequest) (*model.InstancesResponse, error) {
	if err := api.CheckAvailable(r); err != nil {
		return nil, err
	}
	if err := request.Validate(); err != nil {
		return nil, err
	}
	return r.sdkCtx.GetEngine().ProcessRouters(&request.ProcessRoutersRequest)
}

// process load balancer to get the target instances
func (r *routerAPI) ProcessLoadBalance(request *ProcessLoadBalanceRequest) (*model.OneInstanceResponse, error) {
	if err := api.CheckAvailable(r); err != nil {
		return nil, err
	}
	if err := request.Validate(); err != nil {
		return nil, err
	}
	return r.sdkCtx.GetEngine().ProcessLoadBalance(&request.ProcessLoadBalanceRequest)
}

// SDKContext 获取SDK上下文
func (r *routerAPI) SDKContext() api.SDKContext {
	return r.sdkCtx
}

// NewRouterAPI 通过以默认域名为埋点server的默认配置创建RouterAPI
func NewRouterAPI() (RouterAPI, error) {
	return NewRouterAPIByConfig(config.NewDefaultConfigurationWithDomain())
}

// NewProviderAPIByFile 通过配置文件创建SDK RouterAPI对象
func NewRouterAPIByFile(path string) (RouterAPI, error) {
	context, err := api.InitContextByFile(path)
	if err != nil {
		return nil, err
	}
	return &routerAPI{context}, nil
}

// newRouterAPIByConfig 通过配置对象创建SDK RouterAPI对象
func NewRouterAPIByConfig(cfg config.Configuration) (RouterAPI, error) {
	context, err := api.InitContextByConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &routerAPI{context}, nil
}

// NewRouterAPIByContext 通过上下文创建SDK RouterAPI对象
func NewRouterAPIByContext(context api.SDKContext) RouterAPI {
	return &routerAPI{context}
}

// NewRouterAPIByAddress 通过address创建RouterAPI
func NewRouterAPIByAddress(address ...string) (RouterAPI, error) {
	conf := config.NewDefaultConfiguration(address)
	return NewRouterAPIByConfig(conf)
}
