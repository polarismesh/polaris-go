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
	"fmt"
	"github.com/golang/protobuf/proto"
	"github.com/modern-go/reflect2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/go-multierror"
)

//RunMode SDK的运行模式，可以指定为agent或者no-agent模式
type RunMode int

const (
	//ModeNoAgent 以no agent模式运行
	ModeNoAgent = iota
	//ModeWithAgent 带agent模式运行
	ModeWithAgent
)

//ServiceMetadata 服务元数据信息
type ServiceMetadata interface {
	//获取服务名
	GetService() string
	//获取命名空间
	GetNamespace() string
	//获取元数据信息
	GetMetadata() map[string]string
}

//服务元数据的ToString操作
func ToStringService(svc ServiceMetadata, printMeta bool) string {
	if reflect2.IsNil(svc) {
		return "nil"
	}
	if printMeta {
		return fmt.Sprintf("{service: %s, namespace: %s, metadata: %s}",
			svc.GetService(), svc.GetNamespace(), svc.GetMetadata())
	}
	return fmt.Sprintf("{service: %s, namespace: %s}", svc.GetService(), svc.GetNamespace())
}

//ServiceInstances 服务实例列表
type ServiceInstances interface {
	ServiceMetadata
	RegistryValue
	//获取服务实例列表
	GetInstances() []Instance
	//获取全部实例总权重
	GetTotalWeight() int
	//获取集群索引
	GetServiceClusters() ServiceClusters
	//重建缓存索引
	ReloadServiceClusters()
	//获取单个服务实例
	GetInstance(string) Instance
	//数据是否来自于缓存文件
	IsCacheLoaded() bool
}

//断路器状态
type Status int

const (
	//断路器已打开，代表节点已经被熔断
	Open Status = 1
	//断路器半开，节点处于刚熔断恢复，只允许少量请求通过
	HalfOpen Status = 2
	//断路器关闭，节点处于正常工作状态
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

//熔断器状态管理器
type CircuitBreakerStatus interface {
	//标识被哪个熔断器熔断
	GetCircuitBreaker() string
	//熔断状态
	GetStatus() Status
	//状态转换的时间
	GetStartTime() time.Time
	//是否可以分配请求
	IsAvailable() bool
	//执行请求分配
	Allocate() bool
	//获取进入半开状态之后分配的请求数
	GetRequestsAfterHalfOpen() int32
	//获取进入半开状态之后的失败请求数
	GetFailRequestsAfterHalfOpen() int32
	//添加半开状态下面的请求数
	AddRequestCountAfterHalfOpen(n int32, success bool) int32
	//获取分配了最后配额的时间
	GetFinalAllocateTimeInt64() int64
	//获取状态转换锁，主要是避免状态重复发生转变，如多个协程上报调用失败时，每个stat方法都返回需要转化为熔断状态
	AcquireStatusLock() bool
	//获取在半开之后，分配出去的请求数，即getOneInstance接口返回这个实例的次数
	AllocatedRequestsAfterHalfOpen() int32
}

// ActiveDetectStatus 健康探测管理器
type ActiveDetectStatus interface {
	// GetStatus 健康探测结果状态
	GetStatus() HealthCheckStatus
	// GetStartTime 状态转换的时间
	GetStartTime() time.Time
}

//服务实例信息
type Instance interface {
	//获取实例四元组标识
	GetInstanceKey() InstanceKey
	//实例所在命名空间
	GetNamespace() string
	//实例所在服务名
	GetService() string
	//服务实例唯一标识
	GetId() string
	//实例的域名/IP信息
	GetHost() string
	//实例的监听端口
	GetPort() uint32
	//实例的vpcId
	GetVpcId() string
	//服务实例的协议
	GetProtocol() string
	//实例版本号
	GetVersion() string
	//实例静态权重值
	GetWeight() int
	//实例优先级信息
	GetPriority() uint32
	//实例元数据信息
	GetMetadata() map[string]string
	//实例逻辑分区
	GetLogicSet() string
	//实例的断路器状态，包括：
	//打开（被熔断）、半开（探测恢复）、关闭（正常运行）
	GetCircuitBreakerStatus() CircuitBreakerStatus
	//实例是否健康，基于服务端返回的健康数据
	IsHealthy() bool
	//实例是否已经被手动隔离
	IsIsolated() bool
	//实例是否启动了健康检查
	IsEnableHealthCheck() bool
	//实例所属的大区信息
	GetRegion() string
	//实例所属的地方信息
	GetZone() string
	//Deprecated，建议使用GetCampus方法
	GetIDC() string
	//实例所属的园区信息
	GetCampus() string
	//获取实例的修订版本信息
	//与上一次比较，用于确认服务实例是否发生变更
	GetRevision() string
}

//InstanceWeight 节点权重
type InstanceWeight struct {
	//实例ID
	InstanceID string
	//实例动态权重值
	DynamicWeight uint32
}

//元数据路由兜底策略
type FailOverHandler int

const (
	//通配所有可用ip实例，等于关闭meta路由
	GetOneHealth FailOverHandler = 1
	//匹配不带 metaData key路由
	NotContainMetaKey FailOverHandler = 2
	//匹配自定义meta
	CustomMeta FailOverHandler = 3
)

type FailOverDefaultMetaConfig struct {
	//元数据路由兜底策略类型
	Type FailOverHandler
	//仅type==CustomMeta时需要填写
	Meta map[string]string
}

//单个服务实例查询请求
type GetOneInstanceRequest struct {
	//可选，流水号，用于跟踪用户的请求，默认0
	FlowID uint64
	//必选，服务名
	Service string
	//必选，命名空间
	Namespace string
	//可选，元数据信息，仅用于dstMetadata路由插件的过滤
	Metadata map[string]string
	//是否开启元数据匹配不到时启用自定义匹配规则，仅用于dstMetadata路由插件
	EnableFailOverDefaultMeta bool
	//自定义匹配规则，仅当EnableFailOverDefaultMeta为true时生效
	FailOverDefaultMeta FailOverDefaultMetaConfig
	//用户计算hash值的key
	HashKey []byte
	//已经计算好的hash值，用于一致性hash的负载均衡选择
	// Deprecated: 已弃用，请直接使用HashKey参数传入key来计算hash
	HashValue uint64
	//主调方服务信息
	SourceService *ServiceInfo
	//可选，单次查询超时时间，默认直接获取全局的超时配置
	//用户总最大超时时间为(1+RetryCount) * Timeout
	Timeout *time.Duration
	//可选，重试次数，默认直接获取全局的超时配置
	RetryCount *int
	//可选，备份节点数
	//对于一致性hash等有状态的负载均衡方式
	ReplicateCount int
	//应答，无需用户填充，由主流程进行填充
	response InstancesResponse
	//可选，负载均衡算法
	LbPolicy string
	//金丝雀
	Canary string
}

//设置超时时间
func (g *GetOneInstanceRequest) SetTimeout(duration time.Duration) {
	g.Timeout = ToDurationPtr(duration)
}

//设置重试次数
func (g *GetOneInstanceRequest) SetRetryCount(retryCount int) {
	g.RetryCount = &retryCount
}

//获取服务名
func (g *GetOneInstanceRequest) GetService() string {
	return g.Service
}

//获取命名空间
func (g *GetOneInstanceRequest) GetNamespace() string {
	return g.Namespace
}

//获取命名空间
func (g *GetOneInstanceRequest) GetMetadata() map[string]string {
	return g.Metadata
}

//获取应答指针
func (g *GetOneInstanceRequest) GetResponse() *InstancesResponse {
	return &g.response
}

//获取超时值指针
func (g *GetOneInstanceRequest) GetTimeoutPtr() *time.Duration {
	return g.Timeout
}

//获取重试次数指针
func (g *GetOneInstanceRequest) GetRetryCountPtr() *int {
	return g.RetryCount
}

func (g *GetOneInstanceRequest) GetCanary() string {
	return g.Canary
}

func (g *GetOneInstanceRequest) SetCanary(canary string) {
	g.Canary = canary
}

//校验获取单个服务实例请求对象
func (g *GetOneInstanceRequest) Validate() error {
	if nil == g {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "GetOneInstanceRequest can not be nil")
	}
	if err := validateServiceMetadata("GetOneInstanceRequest", g); nil != err {
		return NewSDKError(ErrCodeAPIInvalidArgument, err,
			"fail to validate GetInstancesRequest")
	}
	return nil
}

//获取所有实例的请求
type GetAllInstancesRequest struct {
	//可选，流水号，用于跟踪用户的请求，默认0
	FlowID uint64
	//必选，服务名
	Service string
	//必选，命名空间
	Namespace string
	//可选，单次查询超时时间，默认直接获取全局的超时配置
	//用户总最大超时时间为(1+RetryCount) * Timeout
	Timeout *time.Duration
	//可选，重试次数，默认直接获取全局的超时配置
	RetryCount *int
	//应答，无需用户填充，由主流程进行填充
	response InstancesResponse
}

//设置超时时间
func (g *GetAllInstancesRequest) SetTimeout(duration time.Duration) {
	g.Timeout = ToDurationPtr(duration)
}

//设置重试次数
func (g *GetAllInstancesRequest) SetRetryCount(retryCount int) {
	g.RetryCount = &retryCount
}

//获取服务名
func (g *GetAllInstancesRequest) GetService() string {
	return g.Service
}

//获取命名空间
func (g *GetAllInstancesRequest) GetNamespace() string {
	return g.Namespace
}

//获取命名空间
func (g *GetAllInstancesRequest) GetMetadata() map[string]string {
	return nil
}

//获取应答指针
func (g *GetAllInstancesRequest) GetResponse() *InstancesResponse {
	return &g.response
}

//获取超时值指针
func (g *GetAllInstancesRequest) GetTimeoutPtr() *time.Duration {
	return g.Timeout
}

//获取重试次数指针
func (g *GetAllInstancesRequest) GetRetryCountPtr() *int {
	return g.RetryCount
}

//校验获取全部服务实例请求对象
func (g *GetAllInstancesRequest) Validate() error {
	if nil == g {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "GetAllInstancesRequest can not be nil")
	}
	if err := validateServiceMetadata("GetAllInstancesRequest", g); nil != err {
		return NewSDKError(ErrCodeAPIInvalidArgument, err,
			"fail to validate GetAllInstancesRequest")
	}
	return nil
}

//批量服务实例查询请求
type GetInstancesRequest struct {
	//可选，流水号，用于跟踪用户的请求，默认0
	FlowID uint64
	//必选，服务名
	Service string
	//必选，命名空间
	Namespace string
	//可选，元数据信息，仅用于dstMetadata路由插件的过滤
	Metadata map[string]string
	//主调方服务信息，只用于路由规则匹配
	SourceService *ServiceInfo
	//可选，是否包含被熔断的服务实例，默认false
	// Deprecated: 已弃用，1.0版本后会正式去掉，需要返回全量IP直接设置SkipRouteFilter=true
	IncludeCircuitBreakInstances bool
	//可选，是否包含不健康的服务实例，默认false
	// Deprecated: 已弃用，1.0版本后会正式去掉，需要返回全量IP直接设置SkipRouteFilter=true
	IncludeUnhealthyInstances bool
	//可选，是否跳过服务路由筛选，默认false
	SkipRouteFilter bool
	//可选，单次查询超时时间，默认直接获取全局的超时配置
	//用户总最大超时时间为(1+RetryCount) * Timeout
	Timeout *time.Duration
	//可选，重试次数，默认直接获取全局的超时配置
	RetryCount *int
	//应答，无需用户填充，由主流程进行填充
	response InstancesResponse
	//金丝雀
	Canary string
}

//设置超时时间
func (g *GetInstancesRequest) SetTimeout(duration time.Duration) {
	g.Timeout = ToDurationPtr(duration)
}

//设置重试次数
func (g *GetInstancesRequest) SetRetryCount(retryCount int) {
	g.RetryCount = &retryCount
}

//获取服务名
func (g *GetInstancesRequest) GetService() string {
	return g.Service
}

//获取命名空间
func (g *GetInstancesRequest) GetNamespace() string {
	return g.Namespace
}

//获取命名空间
func (g *GetInstancesRequest) GetMetadata() map[string]string {
	return g.Metadata
}

//获取应答指针
func (g *GetInstancesRequest) GetResponse() *InstancesResponse {
	return &g.response
}

//获取超时值指针
func (g *GetInstancesRequest) GetTimeoutPtr() *time.Duration {
	return g.Timeout
}

//获取重试次数指针
func (g *GetInstancesRequest) GetRetryCountPtr() *int {
	return g.RetryCount
}

func (g *GetInstancesRequest) GetCanary() string {
	return g.Canary
}

func (g *GetInstancesRequest) SetCanary(canary string) {
	g.Canary = canary
}

//校验获取全部服务实例请求对象
func (g *GetInstancesRequest) Validate() error {
	if nil == g {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "GetInstancesRequest can not be nil")
	}
	if err := validateServiceMetadata("GetInstancesRequest", g); nil != err {
		return NewSDKError(ErrCodeAPIInvalidArgument, err,
			"fail to validate GetInstancesRequest")
	}
	return nil
}

type GetServicesRequest struct {
	//可选，流水号，用于跟踪用户的请求，默认0
	FlowID uint64
	//可选，是否使用业务过滤
	EnableBusiness bool
	//必选，业务名
	Business string
	//必选，命名空间
	Namespace string
	//可选，元数据信息，可用于过滤
	Metadata map[string]string
	//可选，单次查询超时时间，默认直接获取全局的超时配置
	//用户总最大超时时间为(1+RetryCount) * Timeout
	Timeout *time.Duration
	//可选，重试次数，默认直接获取全局的超时配置
	RetryCount *int
}

//设置超时时间
func (g *GetServicesRequest) SetTimeout(duration time.Duration) {
	g.Timeout = ToDurationPtr(duration)
}

//设置重试次数
func (g *GetServicesRequest) SetRetryCount(retryCount int) {
	g.RetryCount = &retryCount
}

//获取超时值指针
func (g *GetServicesRequest) GetTimeoutPtr() *time.Duration {
	return g.Timeout
}

//获取重试次数指针
func (g *GetServicesRequest) GetRetryCountPtr() *int {
	return g.RetryCount
}

//验证请求参数
func (g *GetServicesRequest) Validate() error {
	var errs error
	if g.EnableBusiness && len(g.Business) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("enablebusiness but none!"))
		return NewSDKError(ErrCodeAPIInvalidArgument, errs, "input not correct")
	}
	if !g.EnableBusiness && len(g.Metadata) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("metadata empty!"))
		return NewSDKError(ErrCodeAPIInvalidArgument, errs, "input not correct")
	}
	return nil
}

type InitCalleeServiceRequest struct {
	Namespace string
	Service   string
	Timeout   *time.Duration
}

func (g *InitCalleeServiceRequest) Validate() error {
	var errs error
	if g.Service == "" || g.Namespace == "" {
		errs = multierror.Append(errs, fmt.Errorf("namespace or service is empty"))
		return NewSDKError(ErrCodeAPIInvalidArgument, errs, "input not correct")
	}
	return nil
}

//批量服务
type Services interface {
	RegistryValue
	GetNamespace() string
	GetService() string
	GetValue() interface{}
}

//批量服务应答
type ServicesResponse struct {
	//规则类型
	Type EventType
	//所属服务
	Service ServiceKey
	//规则对象，不同EventType对应不同类型实例
	Value interface{}
	//规则版本信息
	Revision string
	//规则缓存
	//RuleCache RuleCache
	//规则校验异常
	ValidateError error
}

//获取类型
func (s *ServicesResponse) GetType() EventType {
	return s.Type
}

//获取值
//PB场景下，路由规则类型为*Routing
func (s *ServicesResponse) GetValue() interface{} {
	return s.Value
}

//配置规则是否已经加载
func (s *ServicesResponse) IsInitialized() bool {
	return true
}

//获取配置规则的修订版本信息
func (s *ServicesResponse) GetRevision() string {
	return s.Revision
}

//获取命名空间
func (s *ServicesResponse) GetNamespace() string {
	return s.Service.Namespace
}

//获取服务名
func (s *ServicesResponse) GetService() string {
	return s.Service.Service
}

//获取规则校验异常
func (s *ServicesResponse) GetValidateError() error {
	return s.ValidateError
}

//服务信息
type ServiceInfo struct {
	//必选，服务名
	Service string
	//必选，命名空间
	Namespace string
	//可选，服务元数据信息
	Metadata map[string]string
}

//获取服务名
func (i *ServiceInfo) GetService() string {
	return i.Service
}

//获取命名空间
func (i *ServiceInfo) GetNamespace() string {
	return i.Namespace
}

//获取元数据信息
func (i *ServiceInfo) GetMetadata() map[string]string {
	return i.Metadata
}

//格式化输出内容
func (i ServiceInfo) String() string {
	return ToStringService(&i, true)
}

type OneInstanceResponse struct {
	InstancesResponse
}

//GetInstance get the only instance
func (o *OneInstanceResponse) GetInstance() Instance {
	if len(o.InstancesResponse.Instances) > 0 {
		return o.InstancesResponse.Instances[0]
	}
	return nil
}

//服务实例查询应答
type InstancesResponse struct {
	ServiceInfo
	//可选，流水号，用于跟踪用户的请求，默认0
	FlowID uint64
	//服务权重类型
	TotalWeight int
	//获取实例的修订版本信息
	//与上一次比较，用于确认服务实例是否发生变更
	Revision string
	//服务实例列表
	Instances []Instance
	//当前查询结果所属的集群，如果是获取GetOneInstance返回的结果，则为nil
	Cluster *Cluster
}

//获取配置类型
func (i *InstancesResponse) GetType() EventType {
	return EventInstances
}

//获取服务实例列表
func (i *InstancesResponse) GetInstances() []Instance {
	return i.Instances
}

//获取单个服务实例
func (i *InstancesResponse) GetInstance(instanceId string) Instance {
	for _, v := range i.Instances {
		if v.GetId() == instanceId {
			return v
		}
	}
	return nil
}

//服务实例列表是否已经加载
func (i *InstancesResponse) IsInitialized() bool {
	return true
}

//数据是否来自于缓存文件
func (i *InstancesResponse) IsCacheLoaded() bool {
	return false
}

//获取服务的修订版本信息
func (i *InstancesResponse) GetRevision() string {
	return i.Revision
}

//获取全部实例总权重
func (i *InstancesResponse) GetTotalWeight() int {
	return i.TotalWeight
}

//获取集群缓存
func (i *InstancesResponse) GetServiceClusters() ServiceClusters {
	if nil == i.Cluster {
		return nil
	}
	return i.Cluster.GetClusters()
}

//重建集群缓存
func (i *InstancesResponse) ReloadServiceClusters() {
	if nil == i.Cluster {
		return
	}
	i.Cluster.clusters.GetServiceInstances().ReloadServiceClusters()
}

//调用结果状态
type RetStatus int

const (
	//调用成功
	RetSuccess RetStatus = 1
	//调用失败
	RetFail RetStatus = 2
)

//ServiceCallResult 服务调用结果
type ServiceCallResult struct {
	EmptyInstanceGauge
	//上报的服务实例
	CalledInstance Instance
	//必选，本地服务调用的状态，正常or异常
	RetStatus RetStatus
	//必选，本地服务调用的返回码
	RetCode *int32
	//必选，被调服务实例获取接口的最大时延
	Delay *time.Duration
}

//API调用的唯一标识
type APICallKey struct {
	//调用的API接口名字
	APIName ApiOperation
	//必选，本地服务调用的错误码
	RetCode ErrCode
	//延迟的范围
	DelayRange ApiDelayRange
}

//校验InstanceDeRegisterRequest
func (s *ServiceCallResult) Validate() error {
	if nil == s {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "ServiceCallResult can not be nil")
	}
	var errs error
	if nil == s.CalledInstance || reflect2.IsNil(s.CalledInstance) {
		errs = multierror.Append(errs, fmt.Errorf("ServiceCallResult: The instance called can not be empty"))
	}
	if s.RetStatus != RetSuccess && s.RetStatus != RetFail {
		errs = multierror.Append(errs,
			fmt.Errorf("ServiceCallResult: retStatus should be const RetSuccess or RetFail"))
	}
	if nil == s.GetRetCode() {
		errs = multierror.Append(errs, fmt.Errorf("ServiceCallResult: retCode should not be empty"))
	}
	if nil == s.GetDelay() {
		errs = multierror.Append(errs, fmt.Errorf("ServiceCallResult: delay should not be empty"))
	}
	if nil != errs {
		return NewSDKError(ErrCodeAPIInvalidArgument, errs, "fail to validate ServiceCallResult: ")
	}
	return nil
}

//设置返回状态
func (s *ServiceCallResult) SetRetStatus(retStatus RetStatus) *ServiceCallResult {
	s.RetStatus = retStatus
	return s
}

//设置实例
func (s *ServiceCallResult) SetCalledInstance(inst Instance) *ServiceCallResult {
	s.CalledInstance = inst
	return s
}

//实例所属服务名
func (s *ServiceCallResult) GetService() string {
	return s.CalledInstance.GetService()
}

//实例所属命名空间
func (s *ServiceCallResult) GetNamespace() string {
	return s.CalledInstance.GetNamespace()
}

//实例ID
func (s *ServiceCallResult) GetID() string {
	return s.CalledInstance.GetId()
}

//获取被调服务实例
func (s *ServiceCallResult) GetCalledInstance() Instance {
	return s.CalledInstance
}

//实例的节点信息
func (s *ServiceCallResult) GetHost() string {
	return s.CalledInstance.GetHost()
}

//实例的端口信息
func (s *ServiceCallResult) GetPort() int {
	return int(s.CalledInstance.GetPort())
}

//实例的返回码
func (s *ServiceCallResult) GetRetCode() *int32 {
	return s.RetCode
}

//实例的返回码
func (s *ServiceCallResult) GetRetCodeValue() int32 {
	if nil == s.RetCode {
		return 0
	}
	return *s.RetCode
}

//设置实例返回码
func (s *ServiceCallResult) SetRetCode(value int32) *ServiceCallResult {
	s.RetCode = &value
	return s
}

//调用时延
func (s *ServiceCallResult) GetDelay() *time.Duration {
	return s.Delay
}

//设置时延值
func (s *ServiceCallResult) SetDelay(duration time.Duration) *ServiceCallResult {
	s.Delay = ToDurationPtr(duration)
	return s
}

//获取本地调用状态
func (s *ServiceCallResult) GetRetStatus() RetStatus {
	return s.RetStatus
}

//sdk api调用结果
type APICallResult struct {
	EmptyInstanceGauge
	APICallKey
	//必选，本地服务调用的状态，正常or异常
	RetStatus RetStatus
	//必选，调用延时
	delay time.Duration
}

//设置成功的调用结果
func (a *APICallResult) SetSuccess(delay time.Duration) {
	a.RetStatus = RetSuccess
	a.RetCode = ErrCodeSuccess
	a.SetDelay(delay)
}

//设置失败的调用结果
func (a *APICallResult) SetFail(retCode ErrCode, delay time.Duration) {
	a.RetStatus = RetFail
	a.RetCode = retCode
	a.SetDelay(delay)
}

//获取调用api
func (a *APICallResult) GetAPI() ApiOperation {
	return a.APICallKey.APIName
}

//实例的调用返回状态
func (a *APICallResult) GetRetStatus() RetStatus {
	return a.RetStatus
}

//实例的返回码
func (a *APICallResult) GetRetCode() *int32 {
	r := int32(a.RetCode)
	return &r
}

//实例的返回码
func (a *APICallResult) GetRetCodeValue() int32 {
	return int32(a.RetCode)
}

//调用时延
func (a *APICallResult) GetDelay() *time.Duration {
	return &a.delay
}

//设置调用时延
func (a *APICallResult) SetDelay(delay time.Duration) {
	a.delay = delay
	a.DelayRange = GetApiDelayRange(a.delay)
}

//返回延迟范围
func (a *APICallResult) GetDelayRange() ApiDelayRange {
	return a.DelayRange
}

//InstanceHeartbeatRequest 心跳上报请求
type InstanceHeartbeatRequest struct {
	//必选，服务名
	Service string
	//必选，服务访问Token
	ServiceToken string
	//必选，命名空间
	Namespace string
	//必选，服务实例ID
	InstanceID string
	//必选，服务实例ip
	Host string
	//必选，服务实例端口
	Port int
	//可选，单次查询超时时间，默认直接获取全局的超时配置
	//用户总最大超时时间为(1+RetryCount) * Timeout
	Timeout *time.Duration
	//可选，重试次数，默认直接获取全局的超时配置
	RetryCount *int
}

//打印消息内容
func (g InstanceHeartbeatRequest) String() string {
	return fmt.Sprintf("{service=%s, namespace=%s, host=%s, port=%d, instanceID=%s}",
		g.Service, g.Namespace, g.Host, g.Port, g.InstanceID)
}

//设置超时时间
func (g *InstanceHeartbeatRequest) SetTimeout(duration time.Duration) {
	g.Timeout = ToDurationPtr(duration)
}

//设置重试次数
func (g *InstanceHeartbeatRequest) SetRetryCount(retryCount int) {
	g.RetryCount = &retryCount
}

//获取超时值指针
func (g *InstanceHeartbeatRequest) GetTimeoutPtr() *time.Duration {
	return g.Timeout
}

//获取重试次数指针
func (g *InstanceHeartbeatRequest) GetRetryCountPtr() *int {
	return g.RetryCount
}

//校验InstanceDeRegisterRequest
func (i *InstanceHeartbeatRequest) Validate() error {
	if nil == i {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "InstanceHeartbeatRequest can not be nil")
	}
	var errs error
	if len(i.InstanceID) > 0 {
		return errs
	}
	if len(i.Service) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("InstanceHeartbeatRequest:"+
			" serviceName should not be empty when instanceId is empty"))
	}
	if len(i.Namespace) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("InstanceHeartbeatRequest:"+
			" namespace should not be empty when instanceId is empty"))
	}
	if len(i.Host) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("InstanceHeartbeatRequest:"+
			" host should not be empty when instanceId is empty"))
	}
	if i.Port <= 0 || i.Port >= 65536 {
		errs = multierror.Append(errs, fmt.Errorf("InstanceRegisterRequest: port should be in range (0, 65536)"))
	}
	if nil != errs {
		return NewSDKError(ErrCodeAPIInvalidArgument, errs, "fail to validate InstanceHeartbeatRequest: ")
	}
	return nil
}

//InstanceDeRegisterRequest 反注册服务请求
type InstanceDeRegisterRequest struct {
	//服务名
	Service string
	//服务访问Token
	ServiceToken string
	//命名空间
	Namespace string
	//服务实例ID
	InstanceID string
	//服务实例ip
	Host string
	//服务实例端口
	Port int
	//可选，单次查询超时时间，默认直接获取全局的超时配置
	//用户总最大超时时间为(1+RetryCount) * Timeout
	Timeout *time.Duration
	//可选，重试次数，默认直接获取全局的超时配置
	RetryCount *int
}

//打印消息内容
func (g InstanceDeRegisterRequest) String() string {
	return fmt.Sprintf("{service=%s, namespace=%s, host=%s, port=%d, instanceID=%s}",
		g.Service, g.Namespace, g.Host, g.Port, g.InstanceID)
}

//设置超时时间
func (g *InstanceDeRegisterRequest) SetTimeout(duration time.Duration) {
	g.Timeout = ToDurationPtr(duration)
}

//设置重试次数
func (g *InstanceDeRegisterRequest) SetRetryCount(retryCount int) {
	g.RetryCount = &retryCount
}

//获取超时值指针
func (g *InstanceDeRegisterRequest) GetTimeoutPtr() *time.Duration {
	return g.Timeout
}

//获取重试次数指针
func (g *InstanceDeRegisterRequest) GetRetryCountPtr() *int {
	return g.RetryCount
}

//校验InstanceDeRegisterRequest
func (i *InstanceDeRegisterRequest) Validate() error {
	if nil == i {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "InstanceDeRegisterRequest can not be nil")
	}
	var errs error
	if len(i.InstanceID) > 0 {
		return errs
	}
	if len(i.Service) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("InstanceHeartbeatRequest:"+
			" serviceName should not be empty when instanceId is empty"))
	}
	if len(i.Namespace) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("InstanceHeartbeatRequest:"+
			" namespace should not be empty when instanceId is empty"))
	}
	if len(i.Host) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("InstanceHeartbeatRequest:"+
			" host should not be empty when instanceId is empty"))
	}
	if i.Port <= 0 || i.Port >= 65536 {
		errs = multierror.Append(errs, fmt.Errorf("InstanceRegisterRequest: port should be in range (0, 65536)"))
	}
	if nil != errs {
		return NewSDKError(ErrCodeAPIInvalidArgument, errs, "fail to validate InstanceDeRegisterRequest: ")
	}
	return nil
}

const (
	//最小权重值
	MinWeight int = 0
	//最大权重值
	MaxWeight int = 10000
	//最小优先级
	MinPriority = 0
	//最大优先级
	MaxPriority = 9
)

const (
	//健康检查类型：心跳
	HealthCheckTypeHeartBeat int = 0
)

//InstanceRegisterRequest 注册服务请求
type InstanceRegisterRequest struct {
	//必选，服务名
	Service string
	//必选，服务访问Token
	ServiceToken string
	//必选，命名空间
	Namespace string
	//必选，服务监听host，支持IPv6地址
	Host string
	//必选，服务实例监听port
	Port int

	//以下字段可选，默认nil表示客户端不配置，使用服务端配置
	//服务协议
	Protocol *string
	//服务权重，默认100，范围0-10000
	Weight *int
	//实例优先级，默认为0，数值越小，优先级越高
	Priority *int
	//实例提供服务版本号
	Version *string
	//用户自定义metadata信息
	Metadata map[string]string
	//该服务实例是否健康，默认健康
	Healthy *bool
	//该服务实例是否隔离，默认不隔离
	Isolate *bool
	//ttl超时时间，如果节点要调用heartbeat上报，则必须填写，否则会400141错误码，单位：秒
	TTL *int

	//可选，单次查询超时时间，默认直接获取全局的超时配置
	//用户总最大超时时间为(1+RetryCount) * Timeout
	Timeout *time.Duration
	//可选，重试次数，默认直接获取全局的超时配置
	RetryCount *int
}

//打印消息内容
func (g InstanceRegisterRequest) String() string {
	return fmt.Sprintf("{service=%s, namespace=%s, host=%s, port=%d}", g.Service, g.Namespace, g.Host, g.Port)
}

//设置实例是否健康
func (g *InstanceRegisterRequest) SetHealthy(healthy bool) {
	g.Healthy = &healthy
}

//设置实例是否隔离
func (g *InstanceRegisterRequest) SetIsolate(isolate bool) {
	g.Isolate = &isolate
}

//设置超时时间
func (g *InstanceRegisterRequest) SetTimeout(duration time.Duration) {
	g.Timeout = ToDurationPtr(duration)
}

//设置重试次数
func (g *InstanceRegisterRequest) SetRetryCount(retryCount int) {
	g.RetryCount = &retryCount
}

//设置服务实例TTL
func (g *InstanceRegisterRequest) SetTTL(ttl int) {
	g.TTL = &ttl
}

//获取超时值指针
func (g *InstanceRegisterRequest) GetTimeoutPtr() *time.Duration {
	return g.Timeout
}

//获取重试次数指针
func (g *InstanceRegisterRequest) GetRetryCountPtr() *int {
	return g.RetryCount
}

//校验元数据的key是否为空
func validateMetadata(prefix string, metadata map[string]string) error {
	if len(metadata) > 0 {
		for key := range metadata {
			if len(key) == 0 {
				return fmt.Errorf("%s: metadata has empty key", prefix)
			}
		}
	}
	return nil
}

//校验InstanceRegisterRequest
func (g *InstanceRegisterRequest) Validate() error {
	if nil == g {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "InstanceRegisterRequest can not be nil")
	}
	var errs error
	if len(g.Service) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("InstanceRegisterRequest: serviceName should not be empty"))
	}
	if len(g.Namespace) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("InstanceRegisterRequest: namespace should not be empty"))
	}
	if len(g.Host) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("InstanceRegisterRequest: host should not be empty"))
	}
	if g.Port <= 0 || g.Port >= 65536 {
		errs = multierror.Append(errs, fmt.Errorf("InstanceRegisterRequest: port should be in range (0, 65536)"))
	}
	if nil != g.Weight && (*g.Weight < MinWeight || *g.Weight > MaxWeight) {
		errs = multierror.Append(errs,
			fmt.Errorf("InstanceRegisterRequest: weight should be in range [%d, %d]", MinWeight, MaxWeight))
	}
	if nil != g.Priority && (*g.Priority < MinPriority || *g.Priority > MaxPriority) {
		errs = multierror.Append(errs,
			fmt.Errorf("InstanceRegisterRequest: priority should be in range [%d, %d]", MinPriority, MaxPriority))
	}
	var err error
	if err = validateMetadata("InstanceRegisterRequest", g.Metadata); nil != err {
		errs = multierror.Append(errs, err)
	}
	if nil != errs {
		return NewSDKError(ErrCodeAPIInvalidArgument, errs, "fail to validate InstanceRegisterRequest: ")
	}
	return nil
}

//InstanceRegisterResponse 注册服务应答
type InstanceRegisterResponse struct {
	//实例ID
	InstanceID string
	//实例是否已存在
	Existed bool
}

//客户端上报请求信息
type ReportClientRequest struct {
	//客户端IP地址
	Host string
	//客户端版本信息
	Version string
	//可选，单次查询超时时间，默认直接获取全局的超时配置
	//用户总最大超时时间为(1+RetryCount) * Timeout
	Timeout time.Duration
	//持久化回调
	PersistHandler func(message proto.Message) error
}

//校验ReportClientRequest
func (r *ReportClientRequest) Validate() error {
	if nil == r {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "ReportClientRequest can not be nil")
	}
	var errs error
	if len(r.Version) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("ReportClientRequest: version should not be empty"))
	}
	if nil != errs {
		return NewSDKError(ErrCodeAPIInvalidArgument, errs, "fail to validate ReportClientRequest: ")
	}
	return nil
}

//客户端上报应答信息
type ReportClientResponse struct {
	Mode    RunMode
	Version string
	Region  string
	Zone    string
	Campus  string
}

//服务路由实例过滤计算
type routeFilterCounter struct {
	value int32
}

//增加偏移值
func (r *routeFilterCounter) addValue(diff int32) {
	atomic.AddInt32(&r.value, diff)
}

//获取过滤值
func (r *routeFilterCounter) getValue() int32 {
	return atomic.LoadInt32(&r.value)
}

//本地缓存中过滤的实例数量
type FilteredInstanceCounter struct {
	isolatedInstances  int32
	unhealthyInstances int32
	//服务路由插件过滤的节点计数器, key pluginName, value
	routeFilterCounters sync.Map
}

//增加过滤的被隔离实例数量
func (f *FilteredInstanceCounter) AddIsolatedInstances(n int32) {
	atomic.AddInt32(&f.isolatedInstances, n)
}

//被隔离实例数量
func (f *FilteredInstanceCounter) GetIsolatedInstances() int32 {
	return atomic.LoadInt32(&f.isolatedInstances)
}

//增加过滤的不健康实例数量
func (f *FilteredInstanceCounter) AddUnhealthyInstances(n int32) {
	atomic.AddInt32(&f.unhealthyInstances, n)
}

//不健康实例数量
func (f *FilteredInstanceCounter) GetUnhealthyInstances() int32 {
	return atomic.LoadInt32(&f.unhealthyInstances)
}

//增加被就近接入路由过滤的实例数量
func (f *FilteredInstanceCounter) AddRouteFilteredInstances(routerName string, n int32) {
	value, loaded := f.routeFilterCounters.LoadOrStore(routerName, &routeFilterCounter{value: n})
	if loaded {
		value.(*routeFilterCounter).addValue(n)
	}
}

//就近接入路由过滤的实例数量
func (f *FilteredInstanceCounter) getRouteFilteredInstances(routerName string) int32 {
	value, loaded := f.routeFilterCounters.Load(routerName)
	if loaded {
		return value.(*routeFilterCounter).getValue()
	}
	return 0
}

//校验服务元数据信息
func validateServiceMetadata(prefix string, svcMeta ServiceMetadata) error {
	var errs error
	if len(svcMeta.GetService()) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("%s: service is empty", prefix))
	}
	if len(svcMeta.GetNamespace()) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("%s: namespace is empty", prefix))
	}
	if len(svcMeta.GetMetadata()) > 0 {
		for k := range svcMeta.GetMetadata() {
			if len(k) == 0 {
				errs = multierror.Append(errs, fmt.Errorf("%s: metadata has empty key", prefix))
				break
			}
		}
	}
	return errs
}
