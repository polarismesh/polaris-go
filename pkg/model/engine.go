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

package model

import (
	"time"
)

// NotifyTrigger 通知开关，标识本次需要获取哪些资源
type NotifyTrigger struct {
	EnableDstInstances bool
	EnableDstRoute     bool
	EnableSrcRoute     bool
	EnableDstRateLimit bool
	EnableServices     bool
}

// Clear 清理缓存信息
func (n *NotifyTrigger) Clear() {
	n.EnableDstInstances = false
	n.EnableDstRoute = false
	n.EnableSrcRoute = false
	n.EnableDstRateLimit = false
	n.EnableServices = false
}

// ControlParam 单次查询的控制参数
type ControlParam struct {
	Timeout       time.Duration
	MaxRetry      int
	RetryInterval time.Duration
}

// CacheValueQuery 缓存查询请求对象
type CacheValueQuery interface {
	// GetDstService 获取目标服务
	GetDstService() *ServiceKey
	// GetSrcService 获取源服务
	GetSrcService() *ServiceKey
	// GetNotifierTrigger 获取缓存查询触发器
	GetNotifierTrigger() *NotifyTrigger
	// SetDstInstances 设置目标服务实例
	SetDstInstances(instances ServiceInstances)
	// SetDstRoute 设置目标服务路由规则
	SetDstRoute(rule ServiceRule)
	// SetDstRateLimit 设置目标服务限流规则
	SetDstRateLimit(rule ServiceRule)
	// SetSrcRoute 设置源服务路由规则
	SetSrcRoute(rule ServiceRule)
	// GetControlParam 获取API调用控制参数
	GetControlParam() *ControlParam
	// GetCallResult 获取API调用统计
	GetCallResult() *APICallResult
	// SetServices 设置服务列表
	SetServices(mc Services)
	// GetAuthToken 获取token
	GetAuthToken() string
}

// Engine 编排调度引擎，API相关逻辑在这里执行
type Engine interface {
	// Destroy 销毁流程引擎
	Destroy() error
	// SyncGetResources 同步加载资源，可通过配置参数指定一次同时加载多个资源
	SyncGetResources(req CacheValueQuery) error
	// SyncGetOneInstance 同步获取负载均衡后的服务实例
	SyncGetOneInstance(req *GetOneInstanceRequest) (*OneInstanceResponse, error)
	// SyncGetInstances 同步获取批量服务实例
	SyncGetInstances(req *GetInstancesRequest) (*InstancesResponse, error)
	// SyncGetAllInstances 同步获取全量服务实例
	SyncGetAllInstances(req *GetAllInstancesRequest) (*InstancesResponse, error)
	// SyncRegister 同步进行服务注册
	SyncRegister(instance *InstanceRegisterRequest) (*InstanceRegisterResponse, error)
	// SyncDeregister 同步进行服务反注册
	SyncDeregister(instance *InstanceDeRegisterRequest) error
	// SyncHeartbeat 同步进行心跳上报
	SyncHeartbeat(instance *InstanceHeartbeatRequest) error
	// SyncUpdateServiceCallResult 上报调用结果信息
	SyncUpdateServiceCallResult(result *ServiceCallResult) error
	// SyncReportStat 上报实例统计信息
	SyncReportStat(typ MetricType, stat InstanceGauge) error
	// SyncGetServiceRule 同步获取服务规则
	SyncGetServiceRule(
		eventType EventType, req *GetServiceRuleRequest) (*ServiceRuleResponse, error)
	// SyncGetServices 同步获取批量服务
	SyncGetServices(
		eventType EventType, req *GetServicesRequest) (*ServicesResponse, error)
	// AsyncGetQuota 同步获取配额信息
	AsyncGetQuota(request *QuotaRequestImpl) (*QuotaFutureImpl, error)
	// ScheduleTask 启动定时任务
	ScheduleTask(task *PeriodicTask) (chan<- *PriorityTask, TaskValues)
	// WatchService 监听服务的change
	WatchService(request *WatchServiceRequest) (*WatchServiceResponse, error)
	// GetContext 获取上下文
	GetContext() ValueContext
	// InitCalleeService 所需的被调初始化
	InitCalleeService(req *InitCalleeServiceRequest) error
	// SyncGetConfigFile 同步获取配置文件
	SyncGetConfigFile(req *GetConfigFileRequest) (ConfigFile, error)
	// SyncGetConfigGroup 同步获取配置文件
	SyncGetConfigGroup(namespace, fileGroup string) (ConfigFileGroup, error)
	// SyncCreateConfigFile 同步创建配置文件
	SyncCreateConfigFile(namespace, fileGroup, fileName, content string) error
	// SyncUpdateConfigFile 同步更新配置文件
	SyncUpdateConfigFile(namespace, fileGroup, fileName, content string) error
	// SyncPublishConfigFile 同步发布配置文件
	SyncPublishConfigFile(namespace, fileGroup, fileName string) error
	// ProcessRouters 执行路由链过滤，返回经过路由后的实例列表
	ProcessRouters(req *ProcessRoutersRequest) (*InstancesResponse, error)
	// ProcessLoadBalance 执行负载均衡策略，返回负载均衡后的实例
	ProcessLoadBalance(req *ProcessLoadBalanceRequest) (*OneInstanceResponse, error)
	// WatchAllInstances 监听实例变更事件
	WatchAllInstances(request *WatchAllInstancesRequest) (*WatchAllInstancesResponse, error)
	// WatchAllServices 监听服务列表变更事件
	WatchAllServices(request *WatchAllServicesRequest) (*WatchAllServicesResponse, error)
	// Check
	Check(Resource) (*CheckResult, error)
	// Report
	Report(*ResourceStat) error
	// MakeFunctionDecorator
	MakeFunctionDecorator(CustomerFunction, *RequestContext) DecoratorFunction
	// MakeInvokeHandler
	MakeInvokeHandler(*RequestContext) InvokeHandler
}
