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

package api

import (
	"github.com/polarismesh/polaris-go/pkg/model"
)

const (
	// RetSuccess the call is successful
	RetSuccess = model.RetSuccess
	// RetFail call fails
	RetFail = model.RetFail
)

const (
	// EventInstance .
	EventInstance = model.EventInstance
)

const (
	// WatchModeLongPull watch model by long pulling, the invocation would be hang on until revision updated or timeout
	WatchModeLongPull = model.WatchModeLongPull
	// WatchModeNotify watch model by notify to listener
	WatchModeNotify = model.WatchModeNotify
)

// GetOneInstanceRequest 获取单个服务的请求对象
type GetOneInstanceRequest struct {
	model.GetOneInstanceRequest
}

func (r *GetOneInstanceRequest) convert() {
	if len(r.Arguments) == 0 {
		return
	}

	serviceInfo := r.SourceService
	if serviceInfo == nil {
		r.SourceService = &model.ServiceInfo{
			Metadata: map[string]string{},
		}
		serviceInfo = r.SourceService
	}

	for i := range r.Arguments {
		arg := r.Arguments[i]
		arg.ToLabels(serviceInfo.Metadata)
	}
}

// GetInstancesRequest 获取多个服务的请求对象
type GetInstancesRequest struct {
	model.GetInstancesRequest
}

func (r *GetInstancesRequest) convert() {
	if len(r.Arguments) == 0 {
		return
	}

	serviceInfo := r.SourceService
	if serviceInfo == nil {
		r.SourceService = &model.ServiceInfo{
			Metadata: map[string]string{},
		}
		serviceInfo = r.SourceService
	}

	for i := range r.Arguments {
		arg := r.Arguments[i]
		arg.ToLabels(serviceInfo.Metadata)
	}
}

// GetAllInstancesRequest 获取服务下所有实例的请求对象
type GetAllInstancesRequest struct {
	model.GetAllInstancesRequest
}

// ServiceCallResult 服务调用结果
type ServiceCallResult struct {
	model.ServiceCallResult
}

// GetServiceRuleRequest 获取服务规则请求
type GetServiceRuleRequest struct {
	model.GetServiceRuleRequest
}

// GetServicesRequest 获取批量服务请求
type GetServicesRequest struct {
	model.GetServicesRequest
}

// WatchServiceRequest WatchService req
type WatchServiceRequest struct {
	model.WatchServiceRequest
}

// InitCalleeServiceRequest .
type InitCalleeServiceRequest struct {
	model.InitCalleeServiceRequest
}

// WatchAllInstancesRequest .
type WatchAllInstancesRequest struct {
	model.WatchAllInstancesRequest
}

// WatchAllServicesRequest .
type WatchAllServicesRequest struct {
	model.WatchAllServicesRequest
}

// ConsumerAPI 主调端API方法
type ConsumerAPI interface {
	SDKOwner
	// GetOneInstance 获取单个服务（会执行路由链与负载均衡，获取负载均衡后的服务实例）
	GetOneInstance(req *GetOneInstanceRequest) (*model.OneInstanceResponse, error)
	// GetInstances 获取可用的服务列表（会执行路由链，默认去掉隔离以及不健康的服务实例）
	GetInstances(req *GetInstancesRequest) (*model.InstancesResponse, error)
	// GetAllInstances 获取完整的服务列表（包括隔离及不健康的服务实例）
	GetAllInstances(req *GetAllInstancesRequest) (*model.InstancesResponse, error)
	// GetRouteRule 同步获取服务路由规则
	GetRouteRule(req *GetServiceRuleRequest) (*model.ServiceRuleResponse, error)
	// GetCircuitBreakerRule 同步获取熔断规则
	GetCircuitBreakerRule(req *GetServiceRuleRequest) (*model.ServiceRuleResponse, error)
	// GetRateLimitRule 同步获取限流规则
	GetRateLimitRule(req *GetServiceRuleRequest) (*model.ServiceRuleResponse, error)
	// GetNearbyRouteRule 同步获取就近路由规则
	GetNearbyRouteRule(req *GetServiceRuleRequest) (*model.ServiceRuleResponse, error)
	// UpdateServiceCallResult 上报服务调用结果
	UpdateServiceCallResult(req *ServiceCallResult) error
	// Destroy 销毁API，销毁后无法再进行调用
	Destroy()
	// Deprecated: please use WatchAllInstances instead
	// WatchService 订阅服务消息
	WatchService(req *WatchServiceRequest) (*model.WatchServiceResponse, error)
	// GetServices 根据业务同步获取批量服务
	GetServices(req *GetServicesRequest) (*model.ServicesResponse, error)
	// InitCalleeService 初始化服务运行中需要的被调服务
	InitCalleeService(req *InitCalleeServiceRequest) error
	// WatchAllInstances 监听服务实例变更事件
	WatchAllInstances(req *WatchAllInstancesRequest) (*model.WatchAllInstancesResponse, error)
	// WatchAllServices 监听服务列表变更事件
	WatchAllServices(req *WatchAllServicesRequest) (*model.WatchAllServicesResponse, error)
}

var (
	// NewConsumerAPI 通过以默认域名为埋点server的默认配置创建ConsumerAPI
	NewConsumerAPI = newConsumerAPI
	// NewConsumerAPIByFile 通过配置文件创建SDK ConsumerAPI对象
	NewConsumerAPIByFile = newConsumerAPIByFile
	// NewConsumerAPIByConfig 通过配置对象创建SDK ConsumerAPI对象
	NewConsumerAPIByConfig = newConsumerAPIByConfig
	// NewConsumerAPIByContext 通过上下文创建SDK ConsumerAPI对象
	NewConsumerAPIByContext = newConsumerAPIByContext
	// NewConsumerAPIByDefaultConfigFile 从系统默认配置文件中创建ConsumerAPI
	NewConsumerAPIByDefaultConfigFile = newConsumerAPIByDefaultConfigFile
	// NewServiceCallResult 创建上报对象
	NewServiceCallResult = newServiceCallResult
	// NewConsumerAPIByAddress 通过address创建ConsumerAPI对象
	NewConsumerAPIByAddress = newConsumerAPIByAddress
)
