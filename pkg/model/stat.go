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

//InstanceGauge 针对单个实例的单次评估指标
type InstanceGauge interface {
	//获取服务的命名空间
	GetNamespace() string
	//获取服务名
	GetService() string
	//获取调用api
	GetAPI() ApiOperation
	//实例的节点信息
	GetHost() string
	//实例的端口信息
	GetPort() int
	//实例的调用返回状态
	GetRetStatus() RetStatus
	//实例的熔断状态
	GetCircuitBreakerStatus() CircuitBreakerStatus
	//实例的返回码
	GetRetCodeValue() int32
	//调用时延
	GetDelay() *time.Duration
	//调用时延
	GetDelayRange() ApiDelayRange
	//获取被调节点
	GetCalledInstance() Instance
	//检测指标是否合法
	Validate() error
}

//统计类型
type MetricType int

const (
	SDKAPIStat MetricType = iota
	ServiceStat
	InstanceStat
	SDKCfgStat
	CircuitBreakStat
	PluginAPIStat
	LoadBalanceStat
	RateLimitStat
	RouteStat
)

var metricTypes = HashSet{}

//检测是不是合法的统计类型
func ValidMetircType(t MetricType) bool {
	return metricTypes.Contains(t)
}

//instangeGauge的空实现
type EmptyInstanceGauge struct {
}

//namesapce
func (e EmptyInstanceGauge) GetNamespace() string {
	return ""
}

//service
func (e EmptyInstanceGauge) GetService() string {
	return ""
}

//host
func (e EmptyInstanceGauge) GetHost() string {
	return ""
}

//port
func (e EmptyInstanceGauge) GetPort() int {
	return -1
}

//retstatus
func (e EmptyInstanceGauge) GetRetStatus() RetStatus {
	return RetFail
}

//CircuitBreakerStatus
func (e EmptyInstanceGauge) GetCircuitBreakerStatus() CircuitBreakerStatus {
	return nil
}

//retcode
func (e EmptyInstanceGauge) GetRetCodeValue() int32 {
	return 0
}

//delay
func (e EmptyInstanceGauge) GetDelay() *time.Duration {
	return nil
}

//api
func (e EmptyInstanceGauge) GetAPI() ApiOperation {
	return ApiOperationMax
}

//校验
func (e EmptyInstanceGauge) Validate() error {
	return nil
}

//获取被调节点
func (e EmptyInstanceGauge) GetCalledInstance() Instance {
	return nil
}

//调用时延
func (e EmptyInstanceGauge) GetDelayRange() ApiDelayRange {
	return ApiDelayMax
}

//命名类型，标识具体的API类型
type ApiOperation int

//ToString方法
func (a ApiOperation) String() string {
	return apiOperationPresents[a]
}

//API标识
const (
	ApiGetOneInstance ApiOperation = iota
	ApiGetInstances
	ApiGetRouteRule
	ApiRegister
	ApiDeregister
	ApiHeartbeat
	ApiGetQuota
	ApiGetAllInstances
	ApiUpdateServiceCallResult
	ApiMeshConfig
	ApiInitCalleeServices
	ApiMesh
	//ApiOperationMax这个必须在最下面
	ApiOperationMax
)

//API标识到别名
var (
	apiOperationPresents = map[ApiOperation]string{
		ApiGetOneInstance:          "Consumer::GetOneInstance",
		ApiGetInstances:            "Consumer::GetInstances",
		ApiGetRouteRule:            "Consumer::GetRouteRule",
		ApiGetAllInstances:         "Consumer::GetAllInstances",
		ApiRegister:                "Provider::Register",
		ApiDeregister:              "Provider::Deregister",
		ApiHeartbeat:               "Provider::Heartbeat",
		ApiGetQuota:                "Limit::GetQuota",
		ApiUpdateServiceCallResult: "Consumer::UpdateServiceCallResult",
		ApiMeshConfig:              "Consumer::GetMeshConfig",
		ApiInitCalleeServices:      "Consumer::InitCalleeServices",
		ApiMesh:                    "Consumer::GetMesh",
	}
)

//API延时范围
type ApiDelayRange int

//API延时范围常量
const (
	ApiDelayBelow50 ApiDelayRange = iota
	ApiDelayBelow100
	ApiDelayBelow150
	ApiDelayBelow200
	ApiDelayOver200
	ApiDelayMax
)

var (
	apiDelayPresents = map[ApiDelayRange]string{
		ApiDelayBelow50:  "[0ms,50ms)",
		ApiDelayBelow100: "[50ms,100ms)",
		ApiDelayBelow150: "[100ms,150ms)",
		ApiDelayBelow200: "[150ms,200ms)",
		ApiDelayOver200:  "[200ms,)",
	}
)

//ToString方法
func (a ApiDelayRange) String() string {
	return apiDelayPresents[a]
}

const (
	timeRange    = 50 * time.Millisecond
	maxTimeRange = 200 * time.Millisecond
)

//获取api时延范围
func GetApiDelayRange(delay time.Duration) ApiDelayRange {
	if delay > maxTimeRange {
		delay = maxTimeRange
	}
	diff := delay.Nanoseconds() / timeRange.Nanoseconds()
	return ApiDelayRange(diff)
}

//初始化
func init() {
	metricTypes.Add(SDKAPIStat)
	metricTypes.Add(ServiceStat)
	metricTypes.Add(SDKCfgStat)
	metricTypes.Add(InstanceStat)
	metricTypes.Add(CircuitBreakStat)
	metricTypes.Add(PluginAPIStat)
	metricTypes.Add(LoadBalanceStat)
	metricTypes.Add(RateLimitStat)
	metricTypes.Add(RouteStat)
}
