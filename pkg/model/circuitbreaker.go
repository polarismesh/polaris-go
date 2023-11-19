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
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

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

func (n Node) String() string {
	return n.Host + ":" + strconv.FormatUint(uint64(n.Port), 10)
}

// Resource
type Resource interface {
	fmt.Stringer
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

func (r *ServiceResource) String() string {
	callerSvc := r.callerService
	if callerSvc == nil {
		callerSvc = EmptyServiceKey
	}
	return fmt.Sprintf("level=%s|service=%s|caller=%s", r.level.String(), r.service.String(),
		callerSvc.String())
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

func (r *MethodResource) String() string {
	callerSvc := r.callerService
	if callerSvc == nil {
		callerSvc = EmptyServiceKey
	}
	return fmt.Sprintf("level=%s|method=%s|service=%s|caller=%s", r.level.String(), r.Method,
		r.service.String(), callerSvc.String())
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
	protocol string
	node     Node
}

func (r *InstanceResource) GetProtocol() string {
	return r.protocol
}

func (r *InstanceResource) GetNode() Node {
	return r.node
}

func (r *InstanceResource) String() string {
	callerSvc := r.callerService
	if callerSvc == nil {
		callerSvc = EmptyServiceKey
	}
	return fmt.Sprintf("level=%s|instance=%s|service=%s|caller=%s", r.level.String(), r.node.String(),
		r.service.String(), callerSvc.String())
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
		protocol:         protocol,
		node:             Node{Host: host, Port: port},
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
	if service.Namespace == "" {
		return nil, errors.New("namespace can not be blank")
	}
	if service.Service == "" {
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
	// GetFallbackInfo 获取熔断器的降级信息
	GetFallbackInfo() *FallbackInfo
	// SetFallbackInfo 获取熔断器的降级信息
	SetFallbackInfo(*FallbackInfo)
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
	fallbackInfo   *FallbackInfo
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

type CustomerFunction func(ctx context.Context, args interface{}) (interface{}, error)

type DecoratorFunction func(ctx context.Context, args interface{}) (interface{}, *CallAborted, error)

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

// InvokeHandler .
type InvokeHandler interface {
	// AcquirePermission .
	AcquirePermission() (*CallAborted, error)
	// OnSuccess .
	OnSuccess(*ResponseContext)
	// OnError .
	OnError(*ResponseContext)
}

func NewCallAborted(err error,
	rule string,
	fallback *FallbackInfo) *CallAborted {
	return &CallAborted{
		err:      err,
		rule:     rule,
		fallback: fallback,
	}
}

type CallAborted struct {
	err      error
	rule     string
	fallback *FallbackInfo
}

func (c *CallAborted) HasFallback() bool {
	return c.fallback != nil
}

func (c *CallAborted) GetFallbackCode() int {
	if !c.HasFallback() {
		return 0
	}
	return c.fallback.Code
}

func (c *CallAborted) GetFallbackBody() string {
	if !c.HasFallback() {
		return ""
	}
	return c.fallback.Body
}

func (c *CallAborted) GetFallbackHeaders() map[string]string {
	if !c.HasFallback() {
		return map[string]string{}
	}
	return c.fallback.Headers
}

func (c *CallAborted) GetError() error {
	return c.err
}

type InitCircuitBreakerStatus func(CircuitBreakerStatus)

func NewCircuitBreakerStatus(name string, status Status, startTime time.Time,
	options ...InitCircuitBreakerStatus) CircuitBreakerStatus {
	impl := &BaseCircuitBreakerStatus{
		name:      name,
		status:    status,
		startTime: startTime,
	}
	for i := range options {
		options[i](impl)
	}
	return impl
}

type BaseCircuitBreakerStatus struct {
	name         string
	status       Status
	startTime    time.Time
	fallbackInfo *FallbackInfo
}

// GetCircuitBreaker 标识被哪个熔断器熔断
func (c *BaseCircuitBreakerStatus) GetCircuitBreaker() string {
	return c.name
}

// GetStatus 熔断状态
func (c *BaseCircuitBreakerStatus) GetStatus() Status {
	return c.status
}

// GetStartTime 状态转换的时间
func (c *BaseCircuitBreakerStatus) GetStartTime() time.Time {
	return c.startTime
}

func (c *BaseCircuitBreakerStatus) GetFallbackInfo() *FallbackInfo {
	return c.fallbackInfo
}

func (c *BaseCircuitBreakerStatus) SetFallbackInfo(info *FallbackInfo) {
	c.fallbackInfo = info
}

func (c *BaseCircuitBreakerStatus) IsAvailable() bool {
	if c.status == Close {
		return true
	}
	if c.status == Open {
		return false
	}
	return true
}

type HalfOpenStatus struct {
	BaseCircuitBreakerStatus
	maxRequest   int
	scheduled    int32
	calledResult []bool
	triggered    bool
	lock         sync.Mutex
}

func NewHalfOpenStatus(name string, start time.Time, maxRequest int) CircuitBreakerStatus {
	return &HalfOpenStatus{
		BaseCircuitBreakerStatus: BaseCircuitBreakerStatus{
			name:      name,
			status:    HalfOpen,
			startTime: start,
		},
		maxRequest: maxRequest,
	}
}

func (c *HalfOpenStatus) Report(success bool) bool {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.calledResult = append(c.calledResult, success)
	needTrigger := !success || (len(c.calledResult) >= c.maxRequest)
	if needTrigger && !c.triggered {
		c.triggered = true
		return true
	}
	return false
}

func (c *HalfOpenStatus) Schedule() bool {
	return atomic.CompareAndSwapInt32(&c.scheduled, 0, 1)
}

func (c *HalfOpenStatus) CalNextStatus() Status {
	c.lock.Lock()
	defer c.lock.Unlock()

	if !c.triggered {
		return HalfOpen
	}

	for _, ret := range c.calledResult {
		if !ret {
			return Open
		}
	}
	return Close
}

func (c *HalfOpenStatus) IsAvailable() bool {
	if c.status == Close {
		return true
	}
	if c.status == Open {
		return false
	}
	return true
}

var CallAbortedError = errors.New("call aborted")
