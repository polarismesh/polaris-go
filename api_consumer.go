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

package polaris

import (
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

// consumerAPI 调用者对外函数实现
type consumerAPI struct {
	rawAPI api.ConsumerAPI
}

// SDKContext 获取SDK上下文
func (c *consumerAPI) SDKContext() api.SDKContext {
	return c.rawAPI.SDKContext()
}

// GetOneInstance 同步获取单个服务
func (c *consumerAPI) GetOneInstance(req *GetOneInstanceRequest) (*model.OneInstanceResponse, error) {
	return c.rawAPI.GetOneInstance((*api.GetOneInstanceRequest)(req))
}

// GetInstances 同步获取可用的服务列表
func (c *consumerAPI) GetInstances(req *GetInstancesRequest) (*model.InstancesResponse, error) {
	return c.rawAPI.GetInstances((*api.GetInstancesRequest)(req))
}

// GetAllInstances 同步获取完整的服务列表
func (c *consumerAPI) GetAllInstances(req *GetAllInstancesRequest) (*model.InstancesResponse, error) {
	return c.rawAPI.GetAllInstances((*api.GetAllInstancesRequest)(req))
}

// GetRouteRule 同步获取服务路由规则
func (c *consumerAPI) GetRouteRule(req *GetServiceRuleRequest) (*model.ServiceRuleResponse, error) {
	return c.rawAPI.GetRouteRule((*api.GetServiceRuleRequest)(req))
}

// UpdateServiceCallResult 上报服务调用结果
func (c *consumerAPI) UpdateServiceCallResult(req *ServiceCallResult) error {
	return c.rawAPI.UpdateServiceCallResult((*api.ServiceCallResult)(req))
}

// WatchService 订阅服务消息
func (c *consumerAPI) WatchService(req *WatchServiceRequest) (*model.WatchServiceResponse, error) {
	return c.rawAPI.WatchService((*api.WatchServiceRequest)(req))
}

// GetServices 根据业务同步获取批量服务
func (c *consumerAPI) GetServices(req *GetServicesRequest) (*model.ServicesResponse, error) {
	return c.rawAPI.GetServices((*api.GetServicesRequest)(req))
}

// InitCalleeService 初始化服务运行中需要的被调服务
func (c *consumerAPI) InitCalleeService(req *InitCalleeServiceRequest) error {
	return c.rawAPI.InitCalleeService((*api.InitCalleeServiceRequest)(req))
}

// WatchAllInstances 监听服务实例变更事件
func (c *consumerAPI) WatchAllInstances(req *WatchAllInstancesRequest) (*model.WatchAllInstancesResponse, error) {
	return c.rawAPI.WatchAllInstances((*api.WatchAllInstancesRequest)(req))
}

// WatchAllServices 监听服务列表变更事件
func (c *consumerAPI) WatchAllServices(req *WatchAllServicesRequest) (*model.WatchAllServicesResponse, error) {
	return c.rawAPI.WatchAllServices((*api.WatchAllServicesRequest)(req))
}

// Destroy 销毁API，销毁后无法再进行调用
func (c *consumerAPI) Destroy() {
	c.rawAPI.Destroy()
}

// NewConsumerAPI 创建调用者API
func NewConsumerAPI() (ConsumerAPI, error) {
	c, err := api.NewConsumerAPI()
	if nil != err {
		return nil, err
	}
	return &consumerAPI{rawAPI: c}, nil
}

// NewConsumerAPIByFile 创建调用者API
func NewConsumerAPIByFile(path string) (ConsumerAPI, error) {
	c, err := api.NewConsumerAPIByFile(path)
	if nil != err {
		return nil, err
	}
	return &consumerAPI{rawAPI: c}, nil
}

// NewConsumerAPIByAddress 创建调用者API
func NewConsumerAPIByAddress(address ...string) (ConsumerAPI, error) {
	c, err := api.NewConsumerAPIByAddress(address...)
	if nil != err {
		return nil, err
	}
	return &consumerAPI{rawAPI: c}, nil
}

// NewConsumerAPIByContext 创建调用者API
func NewConsumerAPIByContext(context api.SDKContext) ConsumerAPI {
	c := api.NewConsumerAPIByContext(context)
	return &consumerAPI{rawAPI: c}
}

// NewConsumerAPIByConfig 创建调用者API
func NewConsumerAPIByConfig(cfg config.Configuration) (ConsumerAPI, error) {
	c, err := api.NewConsumerAPIByConfig(cfg)
	if nil != err {
		return nil, err
	}
	return &consumerAPI{rawAPI: c}, nil
}

// NewSDKContext 创建SDK上下文
func NewSDKContext() (api.SDKContext, error) {
	return api.InitContextByConfig(config.NewDefaultConfigurationWithDomain())
}

// NewSDKContextByAddress 根据address创建SDK上下文
func NewSDKContextByAddress(address ...string) (api.SDKContext, error) {
	return api.InitContextByConfig(config.NewDefaultConfiguration(address))
}

// NewSDKContextByConfig 根据配置创建SDK上下文
func NewSDKContextByConfig(cfg config.Configuration) (api.SDKContext, error) {
	return api.InitContextByConfig(cfg)
}
