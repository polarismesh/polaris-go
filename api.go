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
	"github.com/polarismesh/polaris-go/pkg/model"
)

// consumer API
type GetOneInstanceRequest api.GetOneInstanceRequest

type GetInstancesRequest api.GetInstancesRequest

type GetAllInstancesRequest api.GetAllInstancesRequest

type GetServiceRuleRequest api.GetServiceRuleRequest

type ServiceCallResult api.ServiceCallResult

type WatchServiceRequest api.WatchServiceRequest

// ConsumerAPI 主调端API方法
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
	// Destroy 销毁API，销毁后无法再进行调用
	Destroy()
}

// provider
type InstanceRegisterRequest api.InstanceRegisterRequest

type InstanceDeRegisterRequest api.InstanceDeRegisterRequest

type InstanceHeartbeatRequest api.InstanceHeartbeatRequest

// ProviderAPI CL5服务端API的主接口
type ProviderAPI interface {
	api.SDKOwner
	// Register
	// 同步注册服务，服务注册成功后会填充instance中的InstanceID字段
	// 用户可保持该instance对象用于反注册和心跳上报
	Register(instance *InstanceRegisterRequest) (*model.InstanceRegisterResponse, error)
	// Deregister
	// 同步反注册服务
	Deregister(instance *InstanceDeRegisterRequest) error
	// Heartbeat
	// 心跳上报
	Heartbeat(instance *InstanceHeartbeatRequest) error
	// Destroy
	// 销毁API，销毁后无法再进行调用
	Destroy()
}

// rate limiter
type QuotaRequest api.QuotaRequest

type QuotaFuture api.QuotaFuture

// LimitAPI 限流相关的API相关接口
type LimitAPI interface {
	api.SDKOwner
	// GetQuota 获取限流配额，一次接口只获取一个配额
	GetQuota(request QuotaRequest) (QuotaFuture, error)
	// Destroy 销毁API，销毁后无法再进行调用
	Destroy()
}

// NewQuotaRequest 创建配额查询请求
func NewQuotaRequest() QuotaRequest {
	return &model.QuotaRequestImpl{}
}
