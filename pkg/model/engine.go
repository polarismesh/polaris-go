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

//通知开关，标识本次需要获取哪些资源
type NotifyTrigger struct {
	EnableDstInstances bool
	EnableDstRoute     bool
	EnableSrcRoute     bool
	EnableDstRateLimit bool
	EnableMeshConfig   bool
	EnableServices     bool
	EnableMesh         bool
}

//清理缓存信息
func (n *NotifyTrigger) Clear() {
	n.EnableDstInstances = false
	n.EnableDstRoute = false
	n.EnableSrcRoute = false
	n.EnableDstRateLimit = false
	n.EnableMeshConfig = false
	n.EnableServices = false
	n.EnableMesh = false
}

//单次查询的控制参数
type ControlParam struct {
	Timeout       time.Duration
	MaxRetry      int
	RetryInterval time.Duration
}

//缓存查询请求对象
type CacheValueQuery interface {
	//获取目标服务
	GetDstService() *ServiceKey
	//获取源服务
	GetSrcService() *ServiceKey
	//获取缓存查询触发器
	GetNotifierTrigger() *NotifyTrigger
	//设置目标服务实例
	SetDstInstances(instances ServiceInstances)
	//设置目标服务路由规则
	SetDstRoute(rule ServiceRule)
	//设置目标服务限流规则
	SetDstRateLimit(rule ServiceRule)
	//设置源服务路由规则
	SetSrcRoute(rule ServiceRule)
	//获取API调用控制参数
	GetControlParam() *ControlParam
	//获取API调用统计
	GetCallResult() *APICallResult
	//设置网格规则
	SetMeshConfig(mc MeshConfig)
}

/**
 * @brief 编排调度引擎，API相关逻辑在这里执行
 */
type Engine interface {
	/**
	 * @brief 销毁流程引擎
	 */
	Destroy() error

	/**
	 * 同步加载资源，可通过配置参数指定一次同时加载多个资源
	 */
	SyncGetResources(req CacheValueQuery) error

	/**
	 * @brief 同步获取负载均衡后的服务实例
	 */
	SyncGetOneInstance(req *GetOneInstanceRequest) (*OneInstanceResponse, error)

	/**
	 * @brief 同步获取批量服务实例
	 */
	SyncGetInstances(req *GetInstancesRequest) (*InstancesResponse, error)

	/**
	 * @brief 同步获取全量服务实例
	 */
	SyncGetAllInstances(req *GetAllInstancesRequest) (*InstancesResponse, error)

	/**
	 * @brief 同步进行服务注册
	 */
	SyncRegister(instance *InstanceRegisterRequest) (*InstanceRegisterResponse, error)

	/**
	 * @brief 同步进行服务反注册
	 */
	SyncDeregister(instance *InstanceDeRegisterRequest) error

	/**
	 * @brief 同步进行心跳上报
	 */
	SyncHeartbeat(instance *InstanceHeartbeatRequest) error

	/**
	 * @brief 上报调用结果信息
	 */
	SyncUpdateServiceCallResult(result *ServiceCallResult) error

	/**
	 * @brief 上报实例统计信息
	 */
	SyncReportStat(typ MetricType, stat InstanceGauge) error

	/**
	 * @brief 同步获取服务规则
	 */
	SyncGetServiceRule(
		eventType EventType, req *GetServiceRuleRequest) (*ServiceRuleResponse, error)

	/**
	 * @brief 同步获取网格规则
	 */
	SyncGetMeshConfig(
		eventType EventType, req *GetMeshConfigRequest) (*MeshConfigResponse, error)

	/**
	 * @brief 同步获取网格
	 */
	SyncGetMesh(
		eventType EventType, req *GetMeshRequest) (*MeshResponse, error)

	/**
	 * @brief 同步获取批量服务
	 */
	SyncGetServices(
		eventType EventType, req *GetServicesRequest) (*ServicesResponse, error)

	/**
	 * @brief 同步获取配额信息
	 */
	AsyncGetQuota(request *QuotaRequestImpl) (*QuotaFutureImpl, error)

	/**
	 * @brief 启动定时任务
	 */
	ScheduleTask(task *PeriodicTask) (chan<- *PriorityTask, TaskValues)

	/**
	 * @brief 监听服务的change
	 */
	WatchService(request *WatchServiceRequest) (*WatchServiceResponse, error)

	GetContext() ValueContext
	/**
	 * @brief 所需的被调初始化
	 */
	InitCalleeService(req *InitCalleeServiceRequest) error
}
