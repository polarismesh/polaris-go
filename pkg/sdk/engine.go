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

package sdk

import (
	"github.com/polarismesh/polaris-go/pkg/model"
)

// CacheValueQuery 缓存查询请求对象
type CacheValueQuery interface {
	// GetDstService 获取目标服务
	GetDstService() *model.ServiceKey
	// GetSrcService 获取源服务
	GetSrcService() *model.ServiceKey
	// GetNotifierTrigger 获取缓存查询触发器
	GetNotifierTrigger() *model.NotifyTrigger
	// SetDstInstances 设置目标服务实例
	SetDstInstances(instances model.ServiceInstances)
	// SetDstRoute 设置目标服务路由规则
	SetDstRoute(rule model.ServiceRule)
	// SetDstNearbyRoute 设置目标服务就近路由规则
	SetDstNearbyRoute(rule model.ServiceRule)
	// SetDstRateLimit 设置目标服务限流规则
	SetDstRateLimit(rule model.ServiceRule)
	// SetSrcRoute 设置源服务路由规则
	SetSrcRoute(rule model.ServiceRule)
	// GetControlParam 获取API调用控制参数
	GetControlParam() *model.ControlParam
	// GetCallResult 获取API调用统计
	GetCallResult() *model.APICallResult
	// SetServices 设置服务列表
	SetServices(mc model.Services)
}

// RegisterState 注册状态管理器接口，用于避免循环导入
type RegisterState interface {
	// IsRegistered 检查实例是否已注册
	IsRegistered(instance *model.InstanceRegisterRequest) bool
}

// Admin 管理接口，用于避免循环导入
type Admin interface {
	// RegisterHandler 注册处理器
	RegisterHandler(handler *model.AdminHandler)
	// Run 启动服务
	Run()
}

// EventReporter 事件上报接口，用于避免循环导入
type EventReporter interface {
	// ReportEvent 上报事件
	ReportEvent(e interface{}) error
}

// Engine 编排调度引擎，API相关逻辑在这里执行
type Engine interface {
	// Destroy 销毁流程引擎
	Destroy() error
	// SyncGetResources 同步加载资源，可通过配置参数指定一次同时加载多个资源
	SyncGetResources(req CacheValueQuery) error
	// SyncGetOneInstance 同步获取负载均衡后的服务实例
	SyncGetOneInstance(req *model.GetOneInstanceRequest) (*model.OneInstanceResponse, error)
	// SyncGetInstances 同步获取批量服务实例
	SyncGetInstances(req *model.GetInstancesRequest) (*model.InstancesResponse, error)
	// SyncGetAllInstances 同步获取全量服务实例
	SyncGetAllInstances(req *model.GetAllInstancesRequest) (*model.InstancesResponse, error)
	// SyncRegister 同步进行服务注册
	SyncRegister(instance *model.InstanceRegisterRequest) (*model.InstanceRegisterResponse, error)
	// SyncDeregister 同步进行服务反注册
	SyncDeregister(instance *model.InstanceDeRegisterRequest) error
	// SyncHeartbeat 同步进行心跳上报
	SyncHeartbeat(instance *model.InstanceHeartbeatRequest) error
	// SyncUpdateServiceCallResult 上报调用结果信息
	SyncUpdateServiceCallResult(result *model.ServiceCallResult) error
	// SyncReportStat 上报实例统计信息
	SyncReportStat(typ model.MetricType, stat model.InstanceGauge) error
	// SyncGetServiceRule 同步获取服务规则
	SyncGetServiceRule(
		eventType model.EventType, req *model.GetServiceRuleRequest) (*model.ServiceRuleResponse, error)
	// SyncGetServices 同步获取批量服务
	SyncGetServices(
		eventType model.EventType, req *model.GetServicesRequest) (*model.ServicesResponse, error)
	// AsyncGetQuota 同步获取配额信息
	AsyncGetQuota(request *model.QuotaRequestImpl) (*model.QuotaFutureImpl, error)
	// ScheduleTask 启动定时任务
	ScheduleTask(task *model.PeriodicTask) (chan<- *model.PriorityTask, model.TaskValues)
	// WatchService 监听服务的change
	WatchService(request *model.WatchServiceRequest) (*model.WatchServiceResponse, error)
	// GetContext 获取上下文
	GetContext() ValueContext
	// InitCalleeService 所需的被调初始化
	InitCalleeService(req *model.InitCalleeServiceRequest) error
	// SyncGetConfigFile 同步获取配置文件
	SyncGetConfigFile(req *model.GetConfigFileRequest) (model.ConfigFile, error)
	// SyncGetConfigGroup 同步获取配置文件
	SyncGetConfigGroup(namespace, fileGroup string) (model.ConfigFileGroup, error)
	// SyncGetConfigGroupWithReq 同步获取配置文件
	SyncGetConfigGroupWithReq(req *model.GetConfigGroupRequest) (model.ConfigFileGroup, error)
	// SyncCreateConfigFile 同步创建配置文件
	SyncCreateConfigFile(namespace, fileGroup, fileName, content string) error
	// SyncUpdateConfigFile 同步更新配置文件
	SyncUpdateConfigFile(namespace, fileGroup, fileName, content string) error
	// SyncPublishConfigFile 同步发布配置文件
	SyncPublishConfigFile(namespace, fileGroup, fileName string) error
	// SyncUpsertAndPublishConfigFile 同步创建并发布配置文件
	SyncUpsertAndPublishConfigFile(namespace, fileGroup, fileName, content string) error
	// ProcessRouters 执行路由链过滤，返回经过路由后的实例列表
	ProcessRouters(req *model.ProcessRoutersRequest) (*model.InstancesResponse, error)
	// ProcessLoadBalance 执行负载均衡策略，返回负载均衡后的实例
	ProcessLoadBalance(req *model.ProcessLoadBalanceRequest) (*model.OneInstanceResponse, error)
	// WatchAllInstances 监听实例变更事件
	WatchAllInstances(request *model.WatchAllInstancesRequest) (*model.WatchAllInstancesResponse, error)
	// WatchAllServices 监听服务列表变更事件
	WatchAllServices(request *model.WatchAllServicesRequest) (*model.WatchAllServicesResponse, error)
	// Check
	Check(model.Resource) (*model.CheckResult, error)
	// Report
	Report(*model.ResourceStat) error
	// MakeFunctionDecorator
	MakeFunctionDecorator(model.CustomerFunction, *model.RequestContext) model.DecoratorFunction
	// MakeInvokeHandler
	MakeInvokeHandler(*model.RequestContext) model.InvokeHandler
	GetEventReportChain() interface{}
	GetAdmin() Admin
	GetRegisterState() RegisterState
}
