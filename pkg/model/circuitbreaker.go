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

// MethodResource 接口级（方法级）熔断资源
// 与 polaris-java 的 MethodResource 保持一致，承载 protocol/method/path 三元组
// Protocol 协议（http、grpc 等），空串规范化为 "*"
// Method   HTTP 方法（GET、POST 等），gRPC 场景填 "*"，空串规范化为 "*"
// Path     接口路径（HTTP 的 URL Path 或 gRPC 的 fullMethod）
// 兼容说明：旧构造 NewMethodResource(svc, caller, method) 把 method 视作 path，
// 此时 Protocol/Method 自动设为 "*"，避免破坏现有用户代码
type MethodResource struct {
	*abstractResource
	Protocol string
	Method   string
	Path     string
}

// String 输出全部字段，确保 counters bucket 的 map key 唯一
// 不同 protocol/method/path 的接口不会共用一个 counter
func (r *MethodResource) String() string {
	callerSvc := r.callerService
	if callerSvc == nil {
		callerSvc = EmptyServiceKey
	}
	return fmt.Sprintf("level=%s|protocol=%s|method=%s|path=%s|service=%s|caller=%s",
		r.level.String(), r.Protocol, r.Method, r.Path, r.service.String(), callerSvc.String())
}

// NewMethodResource 创建接口级熔断资源（兼容旧调用，method 参数被视为 path）
// svc     被调服务
// caller  主调服务
// method  接口路径，对应 polaris-java 的 path 参数；空串返回错误
// 返回的 Resource 中 Protocol 与 Method 字段被规范化为 "*"
func NewMethodResource(svc, caller *ServiceKey, method string) (*MethodResource, error) {
	return NewMethodResourceWithAPI(svc, caller, "", "", method)
}

// NewMethodResourceWithAPI 创建接口级熔断资源（完整四元组）
// svc        被调服务
// caller     主调服务
// protocol   协议名（http、grpc 等），空串自动转为 "*"
// httpMethod HTTP 方法名（GET、POST 等），空串自动转为 "*"
// path       接口路径，不能为空
// 返回值的 Level 固定为 fault_tolerance.Level_METHOD
func NewMethodResourceWithAPI(svc, caller *ServiceKey, protocol, httpMethod, path string) (*MethodResource, error) {
	if path == "" {
		return nil, errors.New("path can not be empty")
	}
	if protocol == "" {
		protocol = "*"
	}
	if httpMethod == "" {
		httpMethod = "*"
	}
	abstractRes, err := newAbstractResource(svc, caller)
	if err != nil {
		return nil, err
	}
	abstractRes.level = fault_tolerance.Level_METHOD
	res := &MethodResource{
		abstractResource: abstractRes,
		Protocol:         protocol,
		Method:           httpMethod,
		Path:             path,
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

// CircuitBreakerStatusWrapper 上方熔断管理器的包装，用于存入 atomic.Value
type CircuitBreakerStatusWrapper struct {
	Val CircuitBreakerStatus
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

// RequestContext 业务调用上下文，由 CircuitBreakerAPI.MakeFunctionDecorator/MakeInvokeHandler 使用
// Caller      主调服务
// Callee      被调服务
// Method      旧字段，保持向后兼容；新业务建议改用 Protocol/HTTPMethod/Path 三元组
// Protocol    协议名（http、grpc 等），可选；提供时优先使用四元组构造 MethodResource
// HTTPMethod  HTTP 方法名（GET、POST 等），可选
// Path        接口路径，可选；非空时启用接口级熔断
// CodeConvert 业务返回值转错误码的转换器
// 当 Path 为空但 Method 非空时，沿用旧逻辑把 Method 视作 path
type RequestContext struct {
	Caller      *ServiceKey
	Callee      *ServiceKey
	Method      string
	Protocol    string
	HTTPMethod  string
	Path        string
	CodeConvert ResultToErrorCode
}

type ResponseContext struct {
	Duration time.Duration
	Result   interface{}
	Err      error
	// Instance 业务回调通过 InvokeContext 回填的本次实际调用实例。
	// 装饰器 OnSuccess/OnError 据此可触发实例级 Resource 上报；为 nil 时跳过。
	// 仅装饰器内部使用，业务一般不需要手动设置。
	Instance Instance
}

// InvokeContext 装饰器内部传给业务回调的可变载体。
// 业务在 customer func 内拿到所选实例后调用 SetInstance，
// 装饰器在 OnSuccess/OnError 阶段会自动按该实例发起 InstanceResource 上报，
// 从而让"实例级熔断"用例和"服务级 / 接口级"用例共享同一套装饰器写法。
//
// 兼容性：业务不调 SetInstance 时（即 instance 仍为 nil），装饰器跳过实例级上报，
// 行为与历史完全一致；存量调用方零改动。
type InvokeContext struct {
	instance Instance
}

// SetInstance 由业务在 customer func 内回填本次实际调用的实例。
func (ic *InvokeContext) SetInstance(ins Instance) {
	if ic == nil {
		return
	}
	ic.instance = ins
}

// Instance 返回业务此前回填的实例；未回填时返回 nil。
func (ic *InvokeContext) Instance() Instance {
	if ic == nil {
		return nil
	}
	return ic.instance
}

// invokeCtxKey 是 ctx.WithValue 用的 key 类型，避免与外部 key 冲突。
type invokeCtxKey struct{}

// WithInvokeContext 在 ctx 上挂载 InvokeContext；装饰器内部调用，业务一般不直接使用。
func WithInvokeContext(ctx context.Context, ic *InvokeContext) context.Context {
	return context.WithValue(ctx, invokeCtxKey{}, ic)
}

// GetInvokeContext 取出装饰器在 ctx 上挂载的 InvokeContext。
// 业务在 customer func 内调用：
//
//	if ic := model.GetInvokeContext(ctx); ic != nil { ic.SetInstance(chosenInstance) }
//
// 仅在通过 MakeFunctionDecorator 启动的调用链路里返回非 nil。
func GetInvokeContext(ctx context.Context) *InvokeContext {
	if ctx == nil {
		return nil
	}
	v := ctx.Value(invokeCtxKey{})
	if v == nil {
		return nil
	}
	ic, _ := v.(*InvokeContext)
	return ic
}

// InvokeHandler .
type InvokeHandler interface {
	// AcquirePermission .
	AcquirePermission() (bool, *CallAborted, error)
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

// HalfOpenStatus 半开状态
// 状态机进入半开后会按 maxRequest 的额度精确放行调用：
//   - AcquirePermission：请求阶段调用，按发放配额扣减；超额返回 false 视作熔断拒绝
//   - Release：响应归集阶段调用，按结果累计 calledResult，触发条件满足时执行状态判定
//
// 同时保留 Report 入口供未走装饰器的旧路径（ResourceCounters.Report）继续使用，
// 二者互斥使用：装饰器路径用 AcquirePermission/Release；非装饰器路径仍走 Report。
type HalfOpenStatus struct {
	BaseCircuitBreakerStatus
	// maxRequest 半开后请求总数；从 Close 切回需归集的最少成功请求数
	maxRequest int
	// allocated 已发放配额，atomic 维护
	allocated int64
	// finished 已归集结果数，atomic 维护
	finished int64
	// scheduled 状态切换调度去重标记，atomic 维护
	scheduled int32
	// calledResult 归集到的调用结果序列，受 lock 保护
	calledResult []bool
	// triggered 状态判定触发标记，受 lock 保护
	triggered bool
	// lock 保护 calledResult 与 triggered，归集阶段必须串行
	lock sync.Mutex
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
	// 请求失败了 OR 已经探测够了
	needTrigger := !success || (len(c.calledResult) >= c.maxRequest)
	if needTrigger && !c.triggered {
		c.triggered = true
		// 需要执行状态转换
		return true
	}
	return false
}

// AcquirePermission 申请一次半开放行配额
// 热路径无锁，使用 atomic.AddInt64 自增 allocated；超过 maxRequest 立即回退并返回 false。
// 当 maxRequest <= 0 时表示规则未配置或恢复条件无效，按拒绝处理避免半开态下无限放行。
func (c *HalfOpenStatus) AcquirePermission() bool {
	if c.maxRequest <= 0 {
		return false
	}
	if atomic.AddInt64(&c.allocated, 1) > int64(c.maxRequest) {
		atomic.AddInt64(&c.allocated, -1)
		return false
	}
	return true
}

// Release 归集一次半开放行结果
// 由装饰器在 OnSuccess/OnError 阶段调用；返回值表示是否需要立即执行状态判定（与 Report 行为一致）。
// 任意一次失败 OR 累计结果数达到 maxRequest 时设置 triggered，调用方据此决定是否调度切换。
func (c *HalfOpenStatus) Release(success bool) bool {
	atomic.AddInt64(&c.finished, 1)

	c.lock.Lock()
	defer c.lock.Unlock()

	c.calledResult = append(c.calledResult, success)
	needTrigger := !success || len(c.calledResult) >= c.maxRequest
	if needTrigger && !c.triggered {
		c.triggered = true
		return true
	}
	return false
}

// AllocatedCount 仅用于测试观察发放配额数
func (c *HalfOpenStatus) AllocatedCount() int64 {
	return atomic.LoadInt64(&c.allocated)
}

// FinishedCount 仅用于测试观察归集结果数
func (c *HalfOpenStatus) FinishedCount() int64 {
	return atomic.LoadInt64(&c.finished)
}

// MaxRequest 仅用于测试与日志展示半开态可放行的最大配额数
func (c *HalfOpenStatus) MaxRequest() int {
	return c.maxRequest
}

func (c *HalfOpenStatus) Schedule() bool {
	return atomic.CompareAndSwapInt32(&c.scheduled, 0, 1)
}

func (c *HalfOpenStatus) CalNextStatus() Status {
	c.lock.Lock()
	defer c.lock.Unlock()

	if !c.triggered {
		// 不需要执行状态转换, 保持半开状态
		return HalfOpen
	}

	for _, ret := range c.calledResult {
		if !ret {
			// 任意一次失败, 熔断器打开
			return Open
		}
	}
	// 连续成功数达到最大请求数，熔断器关闭
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

var ErrorCallAborted = errors.New("call aborted")
