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

//GetOneInstanceRequest 获取单个服务的请求对象
type GetOneInstanceRequest struct {
	model.GetOneInstanceRequest
}

//GetInstancesRequest 获取多个服务的请求对象
type GetInstancesRequest struct {
	model.GetInstancesRequest
}

//GetAllInstancesRequest 获取服务下所有实例的请求对象
type GetAllInstancesRequest struct {
	model.GetAllInstancesRequest
}

const (
	//调用成功
	RetSuccess = model.RetSuccess
	//调用失败
	RetFail = model.RetFail
)

const (
	EventInstance = model.EventInstance
)

//网格类型
const (
	MeshVirtualService  = model.MeshVirtualService
	MeshServiceEntry    = model.MeshServiceEntry
	MeshDestinationRule = model.MeshDestinationRule
	MeshEnvoyFilter     = model.MeshEnvoyFilter
	MeshGateway         = model.MeshGateway
)

//ServiceCallResult 服务调用结果
type ServiceCallResult struct {
	model.ServiceCallResult
}

//GetServiceRuleRequest 获取服务规则请求
type GetServiceRuleRequest struct {
	model.GetServiceRuleRequest
}

//获取网格规则请求
type GetMeshConfigRequest struct {
	model.GetMeshConfigRequest
}

//获取网格规则请求
type GetMeshRequest struct {
	model.GetMeshRequest
}

//获取批量服务请求
type GetServicesRequest struct {
	model.GetServicesRequest
}

// WatchService req
type WatchServiceRequest struct {
	model.WatchServiceRequest
}

type InitCalleeServiceRequest struct {
	model.InitCalleeServiceRequest
}

//ConsumerAPI 主调端API方法
type ConsumerAPI interface {
	SDKOwner
	// 同步获取单个服务
	GetOneInstance(req *GetOneInstanceRequest) (*model.OneInstanceResponse, error)
	// 同步获取可用的服务列表
	GetInstances(req *GetInstancesRequest) (*model.InstancesResponse, error)
	// 同步获取完整的服务列表
	GetAllInstances(req *GetAllInstancesRequest) (*model.InstancesResponse, error)
	// 同步获取服务路由规则
	GetRouteRule(req *GetServiceRuleRequest) (*model.ServiceRuleResponse, error)
	// 上报服务调用结果
	UpdateServiceCallResult(req *ServiceCallResult) error
	//销毁API，销毁后无法再进行调用
	Destroy()
	//订阅服务消息
	WatchService(req *WatchServiceRequest) (*model.WatchServiceResponse, error)
	// 同步获取网格规则
	GetMeshConfig(req *GetMeshConfigRequest) (*model.MeshConfigResponse, error)
	// 同步获取网格
	GetMesh(req *GetMeshRequest) (*model.MeshResponse, error)
	// 根据业务同步获取批量服务
	GetServicesByBusiness(req *GetServicesRequest) (*model.ServicesResponse, error)
	//初始化服务运行中需要的被调服务
	InitCalleeService(req *InitCalleeServiceRequest) error
}

var (
	//通过以默认域名为埋点server的默认配置创建ConsumerAPI
	NewConsumerAPI = newConsumerAPI
	//NewConsumerAPIByFile 通过配置文件创建SDK ConsumerAPI对象
	NewConsumerAPIByFile = newConsumerAPIByFile
	//NewConsumerAPIByFile 通过配置对象创建SDK ConsumerAPI对象
	NewConsumerAPIByConfig = newConsumerAPIByConfig
	//NewConsumerAPIByContext 通过上下文创建SDK ConsumerAPI对象
	NewConsumerAPIByContext = newConsumerAPIByContext
	//从系统默认配置文件中创建ConsumerAPI
	NewConsumerAPIByDefaultConfigFile = newConsumerAPIByDefaultConfigFile
	//创建上报对象
	NewServiceCallResult = newServiceCallResult
)
