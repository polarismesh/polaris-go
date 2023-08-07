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
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/polarismesh/polaris-go/pkg/clock"
	"github.com/polarismesh/specification/source/go/api/v1/fault_tolerance"
)

type ResourceStat struct {
	Resource  Resource
	RetCode   string
	Delay     time.Duration
	RetStatus RetStatus
}

type Node struct {
	Host string
	Port uint32
}

// Resource
type Resource interface {
	// GetLevel
	GetLevel() fault_tolerance.Level
	// GetService
	GetService() *ServiceKey
	// GetCallerService
	GetCallerService() *ServiceKey
}

type ServiceResource struct {
	*abstractResource
}

func NewServiceResource(svc, caller *ServiceKey) (*ServiceResource, error) {
	abstractRes, err := newAbstractResource(svc, caller)
	if err != nil {
		return nil, err
	}
	abstractRes.level = fault_tolerance.Level_SERVICE
	res := &ServiceResource{
		abstractResource: abstractRes,
	}
	return res, nil
}

type MethodResource struct {
	*abstractResource
	Method string
}

func NewMethodResource(svc, caller *ServiceKey, method string) (*MethodResource, error) {
	if method == "" {
		return nil, errors.New("method can not be empty")
	}
	abstractRes, err := newAbstractResource(svc, caller)
	if err != nil {
		return nil, err
	}
	abstractRes.level = fault_tolerance.Level_METHOD
	res := &MethodResource{
		abstractResource: abstractRes,
		Method:           method,
	}
	return res, nil
}

type InstanceResource struct {
	*abstractResource
	Protocol string
	Node     Node
}

func NewInstanceResource(svc, caller *ServiceKey, protocol, host string, port uint32) (*InstanceResource, error) {
	if host == "" {
		return nil, errors.New("host can not be empty")
	}
	abstractRes, err := newAbstractResource(svc, caller)
	if err != nil {
		return nil, err
	}
	abstractRes.level = fault_tolerance.Level_INSTANCE
	res := &InstanceResource{
		abstractResource: abstractRes,
		Protocol:         protocol,
		Node:             Node{Host: host, Port: port},
	}
	return res, nil
}

type abstractResource struct {
	level         fault_tolerance.Level
	service       *ServiceKey
	callerService *ServiceKey
}

func newAbstractResource(service, caller *ServiceKey) (*abstractResource, error) {
	if service == nil {
		return nil, errors.New("service can not be empty")
	}
	if caller.Namespace == "" {
		return nil, errors.New("namespace can not be blank")
	}
	if caller.Service == "" {
		return nil, errors.New("service can not be blank")
	}
	return &abstractResource{
		service:       service,
		callerService: caller,
	}, nil
}

func (ar *abstractResource) GetLevel() fault_tolerance.Level {
	return ar.level
}

func (ar *abstractResource) GetService() *ServiceKey {
	return ar.service
}

func (ar *abstractResource) GetCallerService() *ServiceKey {
	return ar.callerService
}

// CircuitBreakerStatus  熔断器状态管理器
type CircuitBreakerStatus interface {
	// GetCircuitBreaker 标识被哪个熔断器熔断
	GetCircuitBreaker() string
	// GetStatus 熔断状态
	GetStatus() Status
	// GetStartTime 状态转换的时间
	GetStartTime() time.Time
	// IsAvailable 是否可以分配请求
	IsAvailable() bool
	// Allocate 执行请求分配
	Allocate() bool
	// GetRequestsAfterHalfOpen 获取进入半开状态之后分配的请求数
	GetRequestsAfterHalfOpen() int32
	// GetFailRequestsAfterHalfOpen 获取进入半开状态之后的失败请求数
	GetFailRequestsAfterHalfOpen() int32
	// AddRequestCountAfterHalfOpen 添加半开状态下面的请求数
	AddRequestCountAfterHalfOpen(n int32, success bool) int32
	// GetFinalAllocateTimeInt64 获取分配了最后配额的时间
	GetFinalAllocateTimeInt64() int64
	// AcquireStatusLock 获取状态转换锁，主要是避免状态重复发生转变，如多个协程上报调用失败时，每个stat方法都返回需要转化为熔断状态
	AcquireStatusLock() bool
	// AllocatedRequestsAfterHalfOpen 获取在半开之后，分配出去的请求数，即getOneInstance接口返回这个实例的次数
	AllocatedRequestsAfterHalfOpen() int32
}

// Status 断路器状态
type Status int

const (
	// Open 断路器已打开，代表节点已经被熔断
	Open Status = 1
	// HalfOpen 断路器半开，节点处于刚熔断恢复，只允许少量请求通过
	HalfOpen Status = 2
	// Close 断路器关闭，节点处于正常工作状态
	Close Status = 3
)

// String toString method
func (s Status) String() string {
	switch s {
	case Open:
		return "open"
	case HalfOpen:
		return "half-open"
	case Close:
		return "close"
	}
	return "unknown"
}

// HealthCheckStatus 健康探测状态
type HealthCheckStatus int

const (
	// Healthy 节点探测结果已经恢复健康, 代表可以放开一部分流量
	Healthy HealthCheckStatus = 1
	// Dead 节点仍然不可用
	Dead HealthCheckStatus = 2
)

// ActiveDetectStatus 健康探测管理器
type ActiveDetectStatus interface {
	// GetStatus 健康探测结果状态
	GetStatus() HealthCheckStatus
	// GetStartTime 状态转换的时间
	GetStartTime() time.Time
}

// CircuitBreakerStatusImpl 熔断状态模型，实现了CircuitBreakerStatus接口
type CircuitBreakerStatusImpl struct {
	CircuitBreaker string
	Status         Status
	StartTime      time.Time
	finalAllocTime int64

	// 半开后最多请求此数
	MaxHalfOpenAllowReqTimes int
	// 半开后配个分配 初始化为maxHalfOpenAllowReqTimes
	HalfOpenQuota int32
	// 半开后，分配的个数
	allocatedRequestsAfterHalfOpen int32

	// 半开后发生请求的次数
	halfOpenRequests int32
	// 半开后发生的失败请求数
	halfOpenFailRequests int32

	// 状态发生转变的锁
	statusLock int32
}

// AllocatedRequestsAfterHalfOpen 获取在半开之后，分配出去的请求数，即getOneInstance接口返回这个实例的次数
func (c *CircuitBreakerStatusImpl) AllocatedRequestsAfterHalfOpen() int32 {
	return atomic.LoadInt32(&c.allocatedRequestsAfterHalfOpen)
}

// AcquireStatusLock 获取状态转换锁
func (c *CircuitBreakerStatusImpl) AcquireStatusLock() bool {
	return atomic.CompareAndSwapInt32(&c.statusLock, 0, 1)
}

// GetRequestsAfterHalfOpen 获取进入半开状态之后分配的请求数
func (c *CircuitBreakerStatusImpl) GetRequestsAfterHalfOpen() int32 {
	return atomic.LoadInt32(&c.halfOpenRequests)
}

// GetFailRequestsAfterHalfOpen 获取进入半开状态之后的成功请求数
func (c *CircuitBreakerStatusImpl) GetFailRequestsAfterHalfOpen() int32 {
	return atomic.LoadInt32(&c.halfOpenFailRequests)
}

// AddRequestCountAfterHalfOpen 添加半开状态下面的请求数
func (c *CircuitBreakerStatusImpl) AddRequestCountAfterHalfOpen(n int32, success bool) int32 {
	if !success {
		atomic.AddInt32(&c.halfOpenFailRequests, n)
	}
	return atomic.AddInt32(&c.halfOpenRequests, n)
}

// GetCircuitBreaker 标识被哪个熔断器熔断
func (c *CircuitBreakerStatusImpl) GetCircuitBreaker() string {
	return c.CircuitBreaker
}

// GetStatus 熔断状态
func (c *CircuitBreakerStatusImpl) GetStatus() Status {
	return c.Status
}

// GetStartTime 状态转换的时间
func (c *CircuitBreakerStatusImpl) GetStartTime() time.Time {
	return c.StartTime
}

// String 打印熔断状态数据
func (c CircuitBreakerStatusImpl) String() string {
	return fmt.Sprintf("{circuitBreaker:%s, status:%v, startTime:%v}",
		c.CircuitBreaker, c.Status, c.StartTime)
}

// IsAvailable 是否可以分配请求
func (c *CircuitBreakerStatusImpl) IsAvailable() bool {
	if nil == c || Close == c.Status {
		return true
	}
	if Open == c.Status {
		return false
	}
	return atomic.LoadInt32(&c.HalfOpenQuota) > 0
}

// GetFinalAllocateTimeInt64 获取分配了最后配额的时间
func (c *CircuitBreakerStatusImpl) GetFinalAllocateTimeInt64() int64 {
	return atomic.LoadInt64(&c.finalAllocTime)
}

// Allocate 执行实例到请求的分配
func (c *CircuitBreakerStatusImpl) Allocate() bool {
	if nil == c || Close == c.Status {
		return true
	}
	if Open == c.Status {
		return false
	}
	left := atomic.AddInt32(&c.HalfOpenQuota, -1)
	// 如果刚好是0，即分配了最后一个半开配额，记录时间
	if left == 0 {
		atomic.StoreInt64(&c.finalAllocTime, clock.GetClock().Now().UnixNano())
	}
	// 如果进行了分配的话，那么只要left大于等于0即可
	allocated := left >= 0
	if allocated {
		atomic.AddInt32(&c.allocatedRequestsAfterHalfOpen, 1)
	}
	return allocated
}

type CustomerFunction func(args interface{}) (interface{}, error)

type FallbackInfo struct {
	Code    int
	Headers map[string]string
	Body    string
}

type ResultToErrorCode interface {
	// OnSuccess
	OnSuccess(val interface{}) string
	// OnError
	OnError(err error) string
}

type CheckResult struct {
	Pass         bool
	RuleName     string
	FallbackInfo *FallbackInfo
}

type RequestContext struct {
	Caller      *ServiceKey
	Callee      *ServiceKey
	Method      string
	CodeConvert ResultToErrorCode
}

type ResponseContext struct {
	Duration time.Duration
	Result   interface{}
	Err      error
}

type InvokeHandler interface {
	AcquirePermission() (*CallAborted, error)
	OnSuccess(*ResponseContext)
	OnError(*ResponseContext)
}

type CallAborted struct {
	Rule     string
	Fallback *FallbackInfo
}
