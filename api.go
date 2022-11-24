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

// Package polaris api defines the interfaces for the external APIs.
package polaris

import (
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
)

// GetOneInstanceRequest is the request struct for GetOneInstance.
type GetOneInstanceRequest api.GetOneInstanceRequest

// GetInstancesRequest is the request struct for GetInstances.
type GetInstancesRequest api.GetInstancesRequest

// GetAllInstancesRequest is the request struct for GetAllInstances.
type GetAllInstancesRequest api.GetAllInstancesRequest

// GetServiceRuleRequest is the request struct for GetServiceRule.
type GetServiceRuleRequest api.GetServiceRuleRequest

// ServiceCallResult is the response struct for ServiceCall.
type ServiceCallResult api.ServiceCallResult

// WatchServiceRequest is the request struct for WatchService.
type WatchServiceRequest api.WatchServiceRequest

// GetServicesRequest is the request struct for GetServices.
type GetServicesRequest api.GetServicesRequest

// InitCalleeServiceRequest is the request struct for InitCalleeService.
type InitCalleeServiceRequest api.InitCalleeServiceRequest

// ConsumerAPI 主调端API方法.
type ConsumerAPI interface {
	api.SDKOwner
	// GetOneInstance 同步获取单个服务
	GetOneInstance(req *GetOneInstanceRequest) (*model.OneInstanceResponse, error)
	// GetInstances 同步获取可用的服务列表
	GetInstances(req *GetInstancesRequest) (*model.InstancesResponse, error)
	// GetAllInstances 同步获取完整的服务列表
	GetAllInstances(req *GetAllInstancesRequest) (*model.InstancesResponse, error)
	// GetRouteRule 同步获取服务路由规则
	GetRouteRule(req *GetServiceRuleRequest) (*model.ServiceRuleResponse, error)
	// UpdateServiceCallResult 上报服务调用结果
	UpdateServiceCallResult(req *ServiceCallResult) error
	// WatchService 订阅服务消息
	WatchService(req *WatchServiceRequest) (*model.WatchServiceResponse, error)
	// GetServices 根据业务同步获取批量服务
	GetServices(req *GetServicesRequest) (*model.ServicesResponse, error)
	// InitCalleeService 初始化服务运行中需要的被调服务
	InitCalleeService(req *InitCalleeServiceRequest) error
	// Destroy 销毁API，销毁后无法再进行调用
	Destroy()
}

// InstanceRegisterRequest 实例注册请求.
type InstanceRegisterRequest api.InstanceRegisterRequest

// InstanceDeRegisterRequest 实例注销请求.
type InstanceDeRegisterRequest api.InstanceDeRegisterRequest

// InstanceHeartbeatRequest 实例心跳请求.
type InstanceHeartbeatRequest api.InstanceHeartbeatRequest

// ProviderAPI CL5服务端API的主接口.
type ProviderAPI interface {
	api.SDKOwner
	// RegisterInstance
	// minimum supported version of polaris-server is v1.10.0
	RegisterInstance(instance *InstanceRegisterRequest) (*model.InstanceRegisterResponse, error)
	// Deprecated: Use RegisterInstance instead.
	// Register
	// 同步注册服务，服务注册成功后会填充instance中的InstanceID字段
	// 用户可保持该instance对象用于反注册和心跳上报
	Register(instance *InstanceRegisterRequest) (*model.InstanceRegisterResponse, error)
	// Deregister
	// 同步反注册服务
	Deregister(instance *InstanceDeRegisterRequest) error
	// Deprecated: Use RegisterInstance instead.
	// Heartbeat
	// 心跳上报
	Heartbeat(instance *InstanceHeartbeatRequest) error
	// Destroy
	// 销毁API，销毁后无法再进行调用
	Destroy()
}

// QuotaRequest rate limiter.
type QuotaRequest api.QuotaRequest

// QuotaFuture rate limiter.
type QuotaFuture api.QuotaFuture

// LimitAPI 限流相关的API相关接口.
type LimitAPI interface {
	api.SDKOwner
	// GetQuota 获取限流配额，一次接口只获取一个配额
	GetQuota(request QuotaRequest) (QuotaFuture, error)
	// Destroy 销毁API，销毁后无法再进行调用
	Destroy()
}

// NewQuotaRequest 创建配额查询请求.
func NewQuotaRequest() QuotaRequest {
	return &model.QuotaRequestImpl{}
}

// ConfigFile config
type ConfigFile model.ConfigFile

// ConfigAPI 配置文件的 API.
type ConfigAPI interface {
	api.SDKOwner
	// GetConfigFile 获取配置文件
	GetConfigFile(namespace, fileGroup, fileName string) (ConfigFile, error)
}

// RouterAPI 路由API方法
type RouterAPI interface {
	api.SDKOwner
	// ProcessRouters process routers to filter instances
	ProcessRouters(*ProcessRoutersRequest) (*model.InstancesResponse, error)
	// ProcessLoadBalance process load balancer to get the target instances
	ProcessLoadBalance(*ProcessLoadBalanceRequest) (*model.OneInstanceResponse, error)
}

// ProcessRoutersRequest process routers to filter instances
type ProcessRoutersRequest struct {
	model.ProcessRoutersRequest
}

func (r *ProcessRoutersRequest) convert() {
	if len(r.Arguments) == 0 {
		return
	}

	if len(r.SourceService.Metadata) == 0 {
		r.SourceService.Metadata = map[string]string{}
	}

	for i := range r.Arguments {
		arg := r.Arguments[i]
		arg.ToLabels(r.SourceService.Metadata)
	}
}

// ProcessLoadBalanceRequest process load balancer to get the target instances
type ProcessLoadBalanceRequest struct {
	model.ProcessLoadBalanceRequest
}
