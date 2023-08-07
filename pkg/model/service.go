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
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/hashicorp/go-multierror"
	"github.com/modern-go/reflect2"
)

// RunMode SDK的运行模式，可以指定为agent或者no-agent模式
type RunMode int

const (
	// ModeNoAgent 以no agent模式运行
	ModeNoAgent = iota
	// ModeWithAgent 带agent模式运行
	ModeWithAgent
)

// ServiceMetadata 服务元数据信息
type ServiceMetadata interface {
	// GetService 获取服务名
	GetService() string
	// GetNamespace 获取命名空间
	GetNamespace() string
	// GetMetadata 获取元数据信息
	GetMetadata() map[string]string
}

// ToStringService 服务元数据的ToString操作
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

// ServiceInstances 服务实例列表
type ServiceInstances interface {
	ServiceMetadata
	RegistryValue
	// GetInstances 获取服务实例列表
	GetInstances() []Instance
	// GetTotalWeight 获取全部实例总权重
	GetTotalWeight() int
	// GetServiceClusters 获取集群索引
	GetServiceClusters() ServiceClusters
	// ReloadServiceClusters 重建缓存索引
	ReloadServiceClusters()
	// GetInstance 获取单个服务实例
	GetInstance(string) Instance
	// IsCacheLoaded 数据是否来自于缓存文件
	IsCacheLoaded() bool
}

// Instance 服务实例信息
type Instance interface {
	// GetInstanceKey 获取实例四元组标识
	GetInstanceKey() InstanceKey
	// GetNamespace 实例所在命名空间
	GetNamespace() string
	// GetService 实例所在服务名
	GetService() string
	// GetId 服务实例唯一标识
	GetId() string
	// GetHost 实例的域名/IP信息
	GetHost() string
	// GetPort 实例的监听端口
	GetPort() uint32
	// GetVpcId 实例的vpcId
	GetVpcId() string
	// GetProtocol 服务实例的协议
	GetProtocol() string
	// GetVersion 实例版本号
	GetVersion() string
	// GetWeight 实例静态权重值
	GetWeight() int
	// GetPriority 实例优先级信息
	GetPriority() uint32
	// GetMetadata 实例元数据信息
	GetMetadata() map[string]string
	// GetLogicSet 实例逻辑分区
	GetLogicSet() string
	// GetCircuitBreakerStatus 实例的断路器状态，包括：
	// 打开（被熔断）、半开（探测恢复）、关闭（正常运行）
	GetCircuitBreakerStatus() CircuitBreakerStatus
	// IsHealthy 实例是否健康，基于服务端返回的健康数据
	IsHealthy() bool
	// IsIsolated 实例是否已经被手动隔离
	IsIsolated() bool
	// IsEnableHealthCheck 实例是否启动了健康检查
	IsEnableHealthCheck() bool
	// GetRegion 实例所属的大区信息
	GetRegion() string
	// GetZone 实例所属的地方信息
	GetZone() string
	// GetIDC .
	// Deprecated，建议使用GetCampus方法
	GetIDC() string
	// GetCampus 实例所属的园区信息
	GetCampus() string
	// GetRevision .获取实例的修订版本信息
	// 与上一次比较，用于确认服务实例是否发生变更
	GetRevision() string
	// GetTtl 获取实例设置的 TTL
	GetTtl() int64
	// SetHealthy
	SetHealthy(status bool)
	// DeepClone deep clone Instance
	DeepClone() Instance
}

// InstanceWeight 节点权重
type InstanceWeight struct {
	// 实例ID
	InstanceID string
	// 实例动态权重值
	DynamicWeight uint32
}

// FailOverHandler 元数据路由兜底策略
type FailOverHandler int

const (
	// GetOneHealth 通配所有可用ip实例，等于关闭meta路由
	GetOneHealth FailOverHandler = 1
	// NotContainMetaKey 匹配不带 metaData key路由
	NotContainMetaKey FailOverHandler = 2
	// CustomMeta 匹配自定义meta
	CustomMeta FailOverHandler = 3
)

// FailOverDefaultMetaConfig .
type FailOverDefaultMetaConfig struct {
	// 元数据路由兜底策略类型
	Type FailOverHandler
	// 仅type==CustomMeta时需要填写
	Meta map[string]string
}

// GetOneInstanceRequest 单个服务实例查询请求
type GetOneInstanceRequest struct {
	// 可选，流水号，用于跟踪用户的请求，默认0
	FlowID uint64
	// 必选，服务名
	Service string
	// 必选，命名空间
	Namespace string
	// 可选，元数据信息，仅用于dstMetadata路由插件的过滤
	Metadata map[string]string
	// 是否开启元数据匹配不到时启用自定义匹配规则，仅用于dstMetadata路由插件
	EnableFailOverDefaultMeta bool
	// 自定义匹配规则，仅当EnableFailOverDefaultMeta为true时生效
	FailOverDefaultMeta FailOverDefaultMetaConfig
	// 用户计算hash值的key
	HashKey []byte
	// 已经计算好的hash值，用于一致性hash的负载均衡选择
	// Deprecated: 已弃用，请直接使用HashKey参数传入key来计算hash
	HashValue uint64
	// 主调方服务信息
	SourceService *ServiceInfo
	// 路由标签参数
	Arguments []Argument
	// 可选，单次查询超时时间，默认直接获取全局的超时配置
	// 用户总最大超时时间为(1+RetryCount) * Timeout
	Timeout *time.Duration
	// 可选，重试次数，默认直接获取全局的超时配置
	RetryCount *int
	// 可选，备份节点数
	// 对于一致性hash等有状态的负载均衡方式
	ReplicateCount int
	// 应答，无需用户填充，由主流程进行填充
	response InstancesResponse
	// 可选，负载均衡算法
	LbPolicy string
	// 金丝雀
	Canary string
}

// SetTimeout 设置超时时间
func (g *GetOneInstanceRequest) SetTimeout(duration time.Duration) {
	g.Timeout = ToDurationPtr(duration)
}

// SetRetryCount 设置重试次数
func (g *GetOneInstanceRequest) SetRetryCount(retryCount int) {
	g.RetryCount = &retryCount
}

// GetService 获取服务名
func (g *GetOneInstanceRequest) GetService() string {
	return g.Service
}

// GetNamespace 获取命名空间
func (g *GetOneInstanceRequest) GetNamespace() string {
	return g.Namespace
}

// GetMetadata 获取命名空间
func (g *GetOneInstanceRequest) GetMetadata() map[string]string {
	return g.Metadata
}

// GetResponse 获取应答指针
func (g *GetOneInstanceRequest) GetResponse() *InstancesResponse {
	return &g.response
}

// GetTimeoutPtr 获取超时值指针
func (g *GetOneInstanceRequest) GetTimeoutPtr() *time.Duration {
	return g.Timeout
}

// GetRetryCountPtr 获取重试次数指针
func (g *GetOneInstanceRequest) GetRetryCountPtr() *int {
	return g.RetryCount
}

// GetCanary .
func (g *GetOneInstanceRequest) GetCanary() string {
	return g.Canary
}

// SetCanary .
func (g *GetOneInstanceRequest) SetCanary(canary string) {
	g.Canary = canary
}

// AddArguments .
func (g *GetOneInstanceRequest) AddArguments(argumet ...Argument) {
	if len(g.Arguments) == 0 {
		g.Arguments = make([]Argument, 0, 4)
	}
	g.Arguments = append(g.Arguments, argumet...)
}

// Validate 校验获取单个服务实例请求对象
func (g *GetOneInstanceRequest) Validate() error {
	if nil == g {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "GetOneInstanceRequest can not be nil")
	}
	if err := validateServiceMetadata("GetOneInstanceRequest", g); err != nil {
		return NewSDKError(ErrCodeAPIInvalidArgument, err,
			"fail to validate GetInstancesRequest")
	}
	return nil
}

// GetAllInstancesRequest 获取所有实例的请求
type GetAllInstancesRequest struct {
	// 可选，流水号，用于跟踪用户的请求，默认0
	FlowID uint64
	// 必选，服务名
	Service string
	// 必选，命名空间
	Namespace string
	// 可选，单次查询超时时间，默认直接获取全局的超时配置
	// 用户总最大超时时间为(1+RetryCount) * Timeout
	Timeout *time.Duration
	// 可选，重试次数，默认直接获取全局的超时配置
	RetryCount *int
	// 应答，无需用户填充，由主流程进行填充
	response InstancesResponse
}

// SetTimeout 设置超时时间
func (g *GetAllInstancesRequest) SetTimeout(duration time.Duration) {
	g.Timeout = ToDurationPtr(duration)
}

// SetRetryCount 设置重试次数
func (g *GetAllInstancesRequest) SetRetryCount(retryCount int) {
	g.RetryCount = &retryCount
}

// GetService 获取服务名
func (g *GetAllInstancesRequest) GetService() string {
	return g.Service
}

// GetNamespace 获取命名空间
func (g *GetAllInstancesRequest) GetNamespace() string {
	return g.Namespace
}

// GetMetadata 获取命名空间
func (g *GetAllInstancesRequest) GetMetadata() map[string]string {
	return nil
}

// GetResponse 获取应答指针
func (g *GetAllInstancesRequest) GetResponse() *InstancesResponse {
	return &g.response
}

// GetTimeoutPtr 获取超时值指针
func (g *GetAllInstancesRequest) GetTimeoutPtr() *time.Duration {
	return g.Timeout
}

// GetRetryCountPtr 获取重试次数指针
func (g *GetAllInstancesRequest) GetRetryCountPtr() *int {
	return g.RetryCount
}

// Validate 校验获取全部服务实例请求对象
func (g *GetAllInstancesRequest) Validate() error {
	if nil == g {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "GetAllInstancesRequest can not be nil")
	}
	if err := validateServiceMetadata("GetAllInstancesRequest", g); err != nil {
		return NewSDKError(ErrCodeAPIInvalidArgument, err,
			"fail to validate GetAllInstancesRequest")
	}
	return nil
}

// GetInstancesRequest 批量服务实例查询请求
type GetInstancesRequest struct {
	// 可选，流水号，用于跟踪用户的请求，默认0
	FlowID uint64
	// 必选，服务名
	Service string
	// 必选，命名空间
	Namespace string
	// 可选，元数据信息，仅用于dstMetadata路由插件的过滤
	Metadata map[string]string
	// 主调方服务信息，只用于路由规则匹配
	SourceService *ServiceInfo
	// 路由标签参数
	Arguments []Argument
	// 可选，是否包含被熔断的服务实例，默认false
	// Deprecated: 已弃用，1.0版本后会正式去掉，需要返回全量IP直接设置SkipRouteFilter=true
	IncludeCircuitBreakInstances bool
	// 可选，是否包含不健康的服务实例，默认false
	// Deprecated: 已弃用，1.0版本后会正式去掉，需要返回全量IP直接设置SkipRouteFilter=true
	IncludeUnhealthyInstances bool
	// 可选，是否跳过服务路由筛选，默认false
	SkipRouteFilter bool
	// 可选，单次查询超时时间，默认直接获取全局的超时配置
	// 用户总最大超时时间为(1+RetryCount) * Timeout
	Timeout *time.Duration
	// 可选，重试次数，默认直接获取全局的超时配置
	RetryCount *int
	// 应答，无需用户填充，由主流程进行填充
	response InstancesResponse
	// 金丝雀
	Canary string
}

// SetTimeout 设置超时时间
func (g *GetInstancesRequest) SetTimeout(duration time.Duration) {
	g.Timeout = ToDurationPtr(duration)
}

// SetRetryCount 设置重试次数
func (g *GetInstancesRequest) SetRetryCount(retryCount int) {
	g.RetryCount = &retryCount
}

// GetService 获取服务名
func (g *GetInstancesRequest) GetService() string {
	return g.Service
}

// GetNamespace 获取命名空间
func (g *GetInstancesRequest) GetNamespace() string {
	return g.Namespace
}

// GetMetadata 获取命名空间
func (g *GetInstancesRequest) GetMetadata() map[string]string {
	return g.Metadata
}

// GetResponse 获取应答指针
func (g *GetInstancesRequest) GetResponse() *InstancesResponse {
	return &g.response
}

// GetTimeoutPtr 获取超时值指针
func (g *GetInstancesRequest) GetTimeoutPtr() *time.Duration {
	return g.Timeout
}

// GetRetryCountPtr 获取重试次数指针
func (g *GetInstancesRequest) GetRetryCountPtr() *int {
	return g.RetryCount
}

// GetCanary .
func (g *GetInstancesRequest) GetCanary() string {
	return g.Canary
}

// SetCanary .
func (g *GetInstancesRequest) SetCanary(canary string) {
	g.Canary = canary
}

// AddArguments .
func (g *GetInstancesRequest) AddArguments(argumet ...Argument) {
	if len(g.Arguments) == 0 {
		g.Arguments = make([]Argument, 0, 4)
	}
	g.Arguments = append(g.Arguments, argumet...)
}

// Validate 校验获取全部服务实例请求对象
func (g *GetInstancesRequest) Validate() error {
	if nil == g {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "GetInstancesRequest can not be nil")
	}
	if err := validateServiceMetadata("GetInstancesRequest", g); err != nil {
		return NewSDKError(ErrCodeAPIInvalidArgument, err,
			"fail to validate GetInstancesRequest")
	}
	return nil
}

// GetServicesRequest 获取服务请求
type GetServicesRequest struct {
	// 可选，流水号，用于跟踪用户的请求，默认0
	FlowID uint64
	// 可选，业务名
	Business string
	// 可选，命名空间
	Namespace string
	// 可选，元数据信息，可用于过滤
	Metadata map[string]string
	// 可选，单次查询超时时间，默认直接获取全局的超时配置
	// 用户总最大超时时间为(1+RetryCount) * Timeout
	Timeout *time.Duration
	// 可选，重试次数，默认直接获取全局的超时配置
	RetryCount *int
}

// SetTimeout 设置超时时间
func (g *GetServicesRequest) SetTimeout(duration time.Duration) {
	g.Timeout = ToDurationPtr(duration)
}

// SetRetryCount 设置重试次数
func (g *GetServicesRequest) SetRetryCount(retryCount int) {
	g.RetryCount = &retryCount
}

// GetTimeoutPtr 获取超时值指针
func (g *GetServicesRequest) GetTimeoutPtr() *time.Duration {
	return g.Timeout
}

// GetRetryCountPtr 获取重试次数指针
func (g *GetServicesRequest) GetRetryCountPtr() *int {
	return g.RetryCount
}

// Validate 验证请求参数
func (g *GetServicesRequest) Validate() error {
	return nil
}

// InitCalleeServiceRequest .
type InitCalleeServiceRequest struct {
	Namespace string
	Service   string
	Timeout   *time.Duration
}

// Validate .验证请求参数
func (g *InitCalleeServiceRequest) Validate() error {
	var errs error
	if g.Service == "" || g.Namespace == "" {
		errs = multierror.Append(errs, fmt.Errorf("namespace or service is empty"))
		return NewSDKError(ErrCodeAPIInvalidArgument, errs, "input not correct")
	}
	return nil
}

// Services 批量服务
type Services interface {
	RegistryValue
	GetValue() []*ServiceKey
	GetNamespace() string
}

// ServicesResponse 批量服务应答
type ServicesResponse struct {
	// 规则类型
	Type EventType
	// 规则对象，不同EventType对应不同类型实例
	Value []*ServiceKey
	// 规则版本信息
	Revision string
	// 规则的hash值
	HashValue uint64
	// 规则缓存
	// RuleCache RuleCache
	// 规则校验异常
	ValidateError error
}

// GetType 获取类型
func (s *ServicesResponse) GetType() EventType {
	return s.Type
}

// GetValue 获取值
// PB场景下，路由规则类型为*Routing
func (s *ServicesResponse) GetValue() []*ServiceKey {
	return s.Value
}

// IsInitialized 配置规则是否已经加载
func (s *ServicesResponse) IsInitialized() bool {
	return true
}

// GetRevision 获取配置规则的修订版本信息
func (s *ServicesResponse) GetRevision() string {
	return s.Revision
}

// GetHashValue 获取数据的hash值
func (s *ServicesResponse) GetHashValue() uint64 {
	return s.HashValue
}

// IsNotExists 资源是否存在
func (s *ServicesResponse) IsNotExists() bool {
	return false
}

// GetValidateError 获取规则校验异常
func (s *ServicesResponse) GetValidateError() error {
	return s.ValidateError
}

// ServiceInfo 服务信息
type ServiceInfo struct {
	// 必选，服务名
	Service string
	// 必选，命名空间
	Namespace string
	// 可选，服务元数据信息
	Metadata map[string]string
}

// AddArgument 添加本次流量标签参数
func (i *ServiceInfo) AddArgument(arg Argument) {
	arg.ToLabels(i.Metadata)
}

// GetService 获取服务名
func (i *ServiceInfo) GetService() string {
	return i.Service
}

// GetNamespace 获取命名空间
func (i *ServiceInfo) GetNamespace() string {
	return i.Namespace
}

// GetMetadata 获取元数据信息
func (i *ServiceInfo) GetMetadata() map[string]string {
	return i.Metadata
}

// String 格式化输出内容
func (i ServiceInfo) String() string {
	return ToStringService(&i, true)
}

// IsEmpty 服务数据内容是否为空
func (i *ServiceInfo) IsEmpty() bool {
	if i == nil {
		return true
	}
	return len(i.Namespace) == 0 && len(i.Service) == 0 && len(i.Metadata) == 0
}

// HasService 是否存在服务名
func (i *ServiceInfo) HasService() bool {
	if i == nil {
		return false
	}
	return len(i.Namespace) > 0 && len(i.Service) > 0
}

// OneInstanceResponse 单个服务实例
type OneInstanceResponse struct {
	InstancesResponse
}

// GetInstance get the only instance
func (o *OneInstanceResponse) GetInstance() Instance {
	if len(o.InstancesResponse.Instances) > 0 {
		return o.InstancesResponse.Instances[0]
	}
	return nil
}

// InstancesResponse 服务实例查询应答
type InstancesResponse struct {
	ServiceInfo
	// 可选，流水号，用于跟踪用户的请求，默认0
	FlowID uint64
	// 服务权重类型
	TotalWeight int
	// 获取实例的修订版本信息
	// 与上一次比较，用于确认服务实例是否发生变更
	Revision string
	// 获取服务实例hash值
	HashValue uint64
	// 服务实例列表
	Instances []Instance
	// 当前查询结果所属的集群，如果是获取GetOneInstance返回的结果，则为nil
	Cluster *Cluster
	// 服务是否存在
	NotExists bool
}

// GetType 获取配置类型
func (i *InstancesResponse) GetType() EventType {
	return EventInstances
}

// GetInstances 获取服务实例列表
func (i *InstancesResponse) GetInstances() []Instance {
	return i.Instances
}

// GetInstance 获取单个服务实例
func (i *InstancesResponse) GetInstance(instanceID string) Instance {
	for _, v := range i.Instances {
		if v.GetId() == instanceID {
			return v
		}
	}
	return nil
}

// IsInitialized 服务实例列表是否已经加载
func (i *InstancesResponse) IsInitialized() bool {
	return true
}

// IsCacheLoaded 数据是否来自于缓存文件
func (i *InstancesResponse) IsCacheLoaded() bool {
	return false
}

// GetRevision 获取服务的修订版本信息
func (i *InstancesResponse) GetRevision() string {
	return i.Revision
}

// GetHashValue 获取数据的hash值
func (i *InstancesResponse) GetHashValue() uint64 {
	return i.HashValue
}

// GetTotalWeight 获取全部实例总权重
func (i *InstancesResponse) GetTotalWeight() int {
	return i.TotalWeight
}

// GetServiceClusters 获取集群缓存
func (i *InstancesResponse) GetServiceClusters() ServiceClusters {
	if nil == i.Cluster {
		return nil
	}
	return i.Cluster.GetClusters()
}

// ReloadServiceClusters 重建集群缓存
func (i *InstancesResponse) ReloadServiceClusters() {
	if nil == i.Cluster {
		return
	}
	i.Cluster.clusters.GetServiceInstances().ReloadServiceClusters()
}

// GetCluster InstancesResponse get the cluster
func (i *InstancesResponse) GetCluster() *Cluster {
	return i.Cluster
}

// IsNotExists 服务是否存在
func (i *InstancesResponse) IsNotExists() bool {
	return i.NotExists
}

// RetStatus 调用结果状态
type RetStatus string

const (
	// RetSuccess 调用成功
	RetSuccess RetStatus = "success"
	// RetFail 调用失败
	RetFail RetStatus = "fail"
	// RetTimeout 调用超时
	RetTimeout RetStatus = "timeout"
	// RetFlowControl 限流
	RetFlowControl RetStatus = "flow_control"
	// RetReject 被熔断
	RetReject RetStatus = "reject"
	// RetUnknown
	RetUnknown RetStatus = "unknown"
)

// ServiceCallResult 服务调用结果
type ServiceCallResult struct {
	EmptyInstanceGauge
	// 上报的服务实例
	CalledInstance Instance
	// 调用接口方法
	Method string
	// 必选，本地服务调用的状态，正常or异常
	RetStatus RetStatus
	// 必选，本地服务调用的返回码
	RetCode *int32
	// 必选，被调服务实例获取接口的最大时延
	Delay *time.Duration
	// 可选，主调实例的IP信息
	CalledIP string
	// 可选，生效的规则名称
	RuleName string
	// 可选，主调服务实例的服务信息
	SourceService *ServiceInfo
}

// RateLimitGauge Rate Limit Gauge
type RateLimitGauge struct {
	EmptyInstanceGauge
	Namespace string
	Service   string
	Method    string
	Arguments []Argument
	Result    QuotaResultCode
	RuleName  string
}

// CircuitBreakGauge Circuit Break Gauge
type CircuitBreakGauge struct {
	EmptyInstanceGauge
	ChangeInstance Instance
	Method         string
	CBStatus       CircuitBreakerStatus
}

// GetCircuitBreakerStatus 获取当前实例熔断状态
func (cbg *CircuitBreakGauge) GetCircuitBreakerStatus() CircuitBreakerStatus {
	return cbg.CBStatus
}

// GetCalledInstance 获取状态发生改变的实例
func (cbg *CircuitBreakGauge) GetCalledInstance() Instance {
	return cbg.ChangeInstance
}

// 检测指标是否合法
func (cbg *CircuitBreakGauge) Validate() error {
	if !reflect2.IsNil(cbg.ChangeInstance) {
		return nil
	}
	return NewSDKError(ErrCodeAPIInvalidArgument, nil, "empty change instance")
}

// APICallKey API调用的唯一标识
type APICallKey struct {
	// 调用的API接口名字
	APIName ApiOperation
	// 必选，本地服务调用的错误码
	RetCode ErrCode
	// 延迟的范围
	DelayRange ApiDelayRange
}

// Validate 校验InstanceDeRegisterRequest
func (s *ServiceCallResult) Validate() error {
	if nil == s {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "ServiceCallResult can not be nil")
	}
	var errs error
	if nil == s.CalledInstance || reflect2.IsNil(s.CalledInstance) {
		errs = multierror.Append(errs, fmt.Errorf("ServiceCallResult: The instance called can not be empty"))
	}
	if nil == s.GetRetCode() {
		errs = multierror.Append(errs, fmt.Errorf("ServiceCallResult: retCode should not be empty"))
	}
	if nil == s.GetDelay() {
		errs = multierror.Append(errs, fmt.Errorf("ServiceCallResult: delay should not be empty"))
	}
	if errs != nil {
		return NewSDKError(ErrCodeAPIInvalidArgument, errs, "fail to validate ServiceCallResult: ")
	}
	return nil
}

// SetRetStatus 设置返回状态
func (s *ServiceCallResult) SetRetStatus(retStatus RetStatus) *ServiceCallResult {
	s.RetStatus = retStatus
	return s
}

// SetCalledInstance 设置实例
func (s *ServiceCallResult) SetCalledInstance(inst Instance) *ServiceCallResult {
	s.CalledInstance = inst
	return s
}

// SetMethod 调用时延
func (s *ServiceCallResult) SetMethod(method string) {
	s.Method = method
}

// GetService 实例所属服务名
func (s *ServiceCallResult) GetService() string {
	return s.CalledInstance.GetService()
}

// GetNamespace 实例所属命名空间
func (s *ServiceCallResult) GetNamespace() string {
	return s.CalledInstance.GetNamespace()
}

// GetID 实例ID
func (s *ServiceCallResult) GetID() string {
	return s.CalledInstance.GetId()
}

// GetCalledInstance 获取被调服务实例
func (s *ServiceCallResult) GetCalledInstance() Instance {
	return s.CalledInstance
}

// GetMethod 调用时延
func (s *ServiceCallResult) GetMethod() string {
	return s.Method
}

// GetHost 实例的节点信息
func (s *ServiceCallResult) GetHost() string {
	return s.CalledInstance.GetHost()
}

// GetPort 实例的端口信息
func (s *ServiceCallResult) GetPort() int {
	return int(s.CalledInstance.GetPort())
}

// GetRetCode 实例的返回码
func (s *ServiceCallResult) GetRetCode() *int32 {
	return s.RetCode
}

// GetRetCodeValue 实例的返回码
func (s *ServiceCallResult) GetRetCodeValue() int32 {
	if nil == s.RetCode {
		return 0
	}
	return *s.RetCode
}

// SetRetCode 设置实例返回码
func (s *ServiceCallResult) SetRetCode(value int32) *ServiceCallResult {
	s.RetCode = &value
	return s
}

// GetDelay 调用时延
func (s *ServiceCallResult) GetDelay() *time.Duration {
	return s.Delay
}

// SetDelay 设置时延值
func (s *ServiceCallResult) SetDelay(duration time.Duration) *ServiceCallResult {
	s.Delay = ToDurationPtr(duration)
	return s
}

// GetRetStatus 获取本地调用状态
func (s *ServiceCallResult) GetRetStatus() RetStatus {
	return s.RetStatus
}

// GetCallerService 获取主调服务实例的服务信息
func (s *ServiceCallResult) GetCallerService() string {
	if s.SourceService != nil {
		return s.SourceService.Service
	}
	return ""
}

// GetCallerNamespace 获取主调服务实例的命名空间
func (s *ServiceCallResult) GetCallerNamespace() string {
	if s.SourceService != nil {
		return s.SourceService.Namespace
	}
	return ""
}

// APICallResult sdk api调用结果
type APICallResult struct {
	EmptyInstanceGauge
	APICallKey
	// 必选，本地服务调用的状态，正常or异常
	RetStatus RetStatus
	// 必选，调用延时
	delay time.Duration
}

// SetSuccess 设置成功的调用结果
func (a *APICallResult) SetSuccess(delay time.Duration) {
	a.RetStatus = RetSuccess
	a.RetCode = ErrCodeSuccess
	a.SetDelay(delay)
}

// SetFail 设置失败的调用结果
func (a *APICallResult) SetFail(retCode ErrCode, delay time.Duration) {
	a.RetStatus = RetFail
	a.RetCode = retCode
	a.SetDelay(delay)
}

// GetAPI 获取调用api
func (a *APICallResult) GetAPI() ApiOperation {
	return a.APICallKey.APIName
}

// GetRetStatus 实例的调用返回状态
func (a *APICallResult) GetRetStatus() RetStatus {
	return a.RetStatus
}

// GetRetCode 实例的返回码
func (a *APICallResult) GetRetCode() *int32 {
	r := int32(a.RetCode)
	return &r
}

// GetRetCodeValue 实例的返回码
func (a *APICallResult) GetRetCodeValue() int32 {
	return int32(a.RetCode)
}

// GetDelay 调用时延
func (a *APICallResult) GetDelay() *time.Duration {
	return &a.delay
}

// SetDelay 设置调用时延
func (a *APICallResult) SetDelay(delay time.Duration) {
	a.delay = delay
	a.DelayRange = GetApiDelayRange(a.delay)
}

// GetDelayRange 返回延迟范围
func (a *APICallResult) GetDelayRange() ApiDelayRange {
	return a.DelayRange
}

// InstanceHeartbeatRequest 心跳上报请求
type InstanceHeartbeatRequest struct {
	// 必选，服务名
	Service string
	// 必选，服务访问Token
	ServiceToken string
	// 必选，命名空间
	Namespace string
	// 必选，服务实例ID
	InstanceID string
	// 必选，服务实例ip
	Host string
	// 必选，服务实例端口
	Port int
	// 可选，单次查询超时时间，默认直接获取全局的超时配置
	// 用户总最大超时时间为(1+RetryCount) * Timeout
	Timeout *time.Duration
	// 可选，重试次数，默认直接获取全局的超时配置
	RetryCount *int
}

// String 打印消息内容
func (g InstanceHeartbeatRequest) String() string {
	return fmt.Sprintf("{service=%s, namespace=%s, host=%s, port=%d, instanceID=%s}",
		g.Service, g.Namespace, g.Host, g.Port, g.InstanceID)
}

// SetTimeout 设置超时时间
func (g *InstanceHeartbeatRequest) SetTimeout(duration time.Duration) {
	g.Timeout = ToDurationPtr(duration)
}

// SetRetryCount 设置重试次数
func (g *InstanceHeartbeatRequest) SetRetryCount(retryCount int) {
	g.RetryCount = &retryCount
}

// GetTimeoutPtr 获取超时值指针
func (g *InstanceHeartbeatRequest) GetTimeoutPtr() *time.Duration {
	return g.Timeout
}

// GetRetryCountPtr 获取重试次数指针
func (g *InstanceHeartbeatRequest) GetRetryCountPtr() *int {
	return g.RetryCount
}

// Validate 校验InstanceDeRegisterRequest
func (g *InstanceHeartbeatRequest) Validate() error {
	if nil == g {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "InstanceHeartbeatRequest can not be nil")
	}
	var errs error
	if len(g.InstanceID) > 0 {
		return errs
	}
	if len(g.Service) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("InstanceHeartbeatRequest:"+
			" serviceName should not be empty when instanceId is empty"))
	}
	if len(g.Namespace) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("InstanceHeartbeatRequest:"+
			" namespace should not be empty when instanceId is empty"))
	}
	if len(g.Host) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("InstanceHeartbeatRequest:"+
			" host should not be empty when instanceId is empty"))
	}
	if g.Port <= 0 || g.Port >= 65536 {
		errs = multierror.Append(errs, fmt.Errorf("InstanceRegisterRequest: port should be in range (0, 65536)"))
	}
	if errs != nil {
		return NewSDKError(ErrCodeAPIInvalidArgument, errs, "fail to validate InstanceHeartbeatRequest: ")
	}
	return nil
}

// InstanceDeRegisterRequest 反注册服务请求
type InstanceDeRegisterRequest struct {
	// 服务名
	Service string
	// 服务访问Token
	ServiceToken string
	// 命名空间
	Namespace string
	// 服务实例ID
	InstanceID string
	// 服务实例ip
	Host string
	// 服务实例端口
	Port int
	// 可选，单次查询超时时间，默认直接获取全局的超时配置
	// 用户总最大超时时间为(1+RetryCount) * Timeout
	Timeout *time.Duration
	// 可选，重试次数，默认直接获取全局的超时配置
	RetryCount *int
}

// String 打印消息内容
func (g InstanceDeRegisterRequest) String() string {
	return fmt.Sprintf("{service=%s, namespace=%s, host=%s, port=%d, instanceID=%s}",
		g.Service, g.Namespace, g.Host, g.Port, g.InstanceID)
}

// SetTimeout 设置超时时间
func (g *InstanceDeRegisterRequest) SetTimeout(duration time.Duration) {
	g.Timeout = ToDurationPtr(duration)
}

// SetRetryCount 设置重试次数
func (g *InstanceDeRegisterRequest) SetRetryCount(retryCount int) {
	g.RetryCount = &retryCount
}

// GetTimeoutPtr 获取超时值指针
func (g *InstanceDeRegisterRequest) GetTimeoutPtr() *time.Duration {
	return g.Timeout
}

// GetRetryCountPtr 获取重试次数指针
func (g *InstanceDeRegisterRequest) GetRetryCountPtr() *int {
	return g.RetryCount
}

// Validate 校验InstanceDeRegisterRequest
func (g *InstanceDeRegisterRequest) Validate() error {
	if nil == g {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "InstanceDeRegisterRequest can not be nil")
	}
	var errs error
	if len(g.InstanceID) > 0 {
		return errs
	}
	if len(g.Service) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("InstanceHeartbeatRequest:"+
			" serviceName should not be empty when instanceId is empty"))
	}
	if len(g.Namespace) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("InstanceHeartbeatRequest:"+
			" namespace should not be empty when instanceId is empty"))
	}
	if len(g.Host) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("InstanceHeartbeatRequest:"+
			" host should not be empty when instanceId is empty"))
	}
	if g.Port <= 0 || g.Port >= 65536 {
		errs = multierror.Append(errs, fmt.Errorf("InstanceRegisterRequest: port should be in range (0, 65536)"))
	}
	if errs != nil {
		return NewSDKError(ErrCodeAPIInvalidArgument, errs, "fail to validate InstanceDeRegisterRequest: ")
	}
	return nil
}

const (
	// MinWeight 最小权重值
	MinWeight int = 0
	// MaxWeight 最大权重值
	MaxWeight int = 10000
	// MinPriority 最小优先级
	MinPriority = 0
	// MaxPriority 最大优先级
	MaxPriority = 9
)

const (
	// HealthCheckTypeHeartBeat 健康检查类型：心跳
	HealthCheckTypeHeartBeat int = 0
	// DefaultHeartbeatTtl
	DefaultHeartbeatTtl int = 5
)

// InstanceRegisterRequest 注册服务请求
type InstanceRegisterRequest struct {
	// 必选，服务名
	Service string
	// 必选，命名空间
	Namespace string
	// 必选，服务监听host，支持IPv6地址
	Host string
	// 必选，服务实例监听port
	Port int

	// 以下字段可选，默认nil表示客户端不配置，使用服务端配置
	// 可选，服务访问Token
	ServiceToken string
	// 服务协议
	Protocol *string
	// 服务权重，默认100，范围0-10000
	Weight *int
	// 实例优先级，默认为0，数值越小，优先级越高
	Priority *int
	// 实例提供服务版本号
	Version *string
	// 用户自定义metadata信息
	Metadata map[string]string
	// 该服务实例是否健康，默认健康
	Healthy *bool
	// 该服务实例是否隔离，默认不隔离
	Isolate *bool
	// ttl超时时间，如果节点要调用heartbeat上报，则必须填写，否则会400141错误码，单位：秒
	TTL *int

	Location *Location

	// 可选，单次查询超时时间，默认直接获取全局的超时配置
	// 用户总最大超时时间为(1+RetryCount) * Timeout
	Timeout *time.Duration
	// 可选，重试次数，默认直接获取全局的超时配置
	RetryCount *int
	// 可选，指定实例id
	InstanceId string
}

// String 打印消息内容
func (g InstanceRegisterRequest) String() string {
	return fmt.Sprintf("{service=%s, namespace=%s, host=%s, port=%d}", g.Service, g.Namespace, g.Host, g.Port)
}

// SetHealthy 设置实例是否健康
func (g *InstanceRegisterRequest) SetHealthy(healthy bool) {
	g.Healthy = &healthy
}

// SetIsolate 设置实例是否隔离
func (g *InstanceRegisterRequest) SetIsolate(isolate bool) {
	g.Isolate = &isolate
}

// SetTimeout 设置超时时间
func (g *InstanceRegisterRequest) SetTimeout(duration time.Duration) {
	g.Timeout = ToDurationPtr(duration)
}

// SetRetryCount 设置重试次数
func (g *InstanceRegisterRequest) SetRetryCount(retryCount int) {
	g.RetryCount = &retryCount
}

// SetTTL 设置服务实例TTL
func (g *InstanceRegisterRequest) SetTTL(ttl int) {
	g.TTL = &ttl
}

// SetLocation 设置服务实例的地理信息
func (g *InstanceRegisterRequest) SetLocation(loc *Location) {
	g.Location = loc
}

// GetTimeoutPtr 获取超时值指针
func (g *InstanceRegisterRequest) GetTimeoutPtr() *time.Duration {
	return g.Timeout
}

// GetRetryCountPtr 获取重试次数指针
func (g *InstanceRegisterRequest) GetRetryCountPtr() *int {
	return g.RetryCount
}

// GetLocation 获取实例的地址信息
func (g *InstanceRegisterRequest) GetLocation() *Location {
	return g.Location
}

// SetDefaultTTL set default ttl
func (g *InstanceRegisterRequest) SetDefaultTTL() {
	if g.TTL == nil {
		g.SetTTL(DefaultHeartbeatTtl)
	}
}

// validateMetadata 校验元数据的key是否为空
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

// Validate 校验InstanceRegisterRequest
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
	if g.TTL != nil && *g.TTL <= 0 {
		errs = multierror.Append(errs, fmt.Errorf("InstanceRegisterRequest: heartbeat ttl should be greater than zero"))
	}
	var err error
	if err = validateMetadata("InstanceRegisterRequest", g.Metadata); err != nil {
		errs = multierror.Append(errs, err)
	}
	if errs != nil {
		return NewSDKError(ErrCodeAPIInvalidArgument, errs, "fail to validate InstanceRegisterRequest: ")
	}
	return nil
}

// InstanceRegisterResponse 注册服务应答
type InstanceRegisterResponse struct {
	// 实例ID
	InstanceID string
	// 实例是否已存在
	Existed bool
}

// StatInfo 监控插件元数据信息
type StatInfo struct {
	Target   string
	Port     uint32
	Path     string
	Protocol string
}

func (s StatInfo) Empty() bool {
	return s.Target == ""
}

// ReportClientRequest 客户端上报请求信息
type ReportClientRequest struct {
	// 客户端进程唯一标识
	ID string
	// 客户端IP地址
	Host string
	// 客户端类型
	Type string
	// 客户端版本信息
	Version string
	// 可选，单次查询超时时间，默认直接获取全局的超时配置
	// 用户总最大超时时间为(1+RetryCount) * Timeout
	Timeout time.Duration
	// 地理位置信息
	Location *Location
	// 监控插件的上报信息
	StatInfos []StatInfo
	// 持久化回调
	PersistHandler func(message proto.Message) error
}

// Validate 校验ReportClientRequest
func (r *ReportClientRequest) Validate() error {
	if nil == r {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "ReportClientRequest can not be nil")
	}
	var errs error
	if len(r.Version) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("ReportClientRequest: version should not be empty"))
	}
	if errs != nil {
		return NewSDKError(ErrCodeAPIInvalidArgument, errs, "fail to validate ReportClientRequest: ")
	}
	return nil
}

// ReportClientResponse 客户端上报应答信息
type ReportClientResponse struct {
	Mode    RunMode
	Version string
	Region  string
	Zone    string
	Campus  string
}

// routeFilterCounter 服务路由实例过滤计算
type routeFilterCounter struct {
	value int32
}

// addValue 增加偏移值
func (r *routeFilterCounter) addValue(diff int32) {
	atomic.AddInt32(&r.value, diff)
}

// getValue 获取过滤值
func (r *routeFilterCounter) getValue() int32 {
	return atomic.LoadInt32(&r.value)
}

// FilteredInstanceCounter 本地缓存中过滤的实例数量
type FilteredInstanceCounter struct {
	isolatedInstances  int32
	unhealthyInstances int32
	// 服务路由插件过滤的节点计数器, key pluginName, value
	routeFilterCounters sync.Map
}

// AddIsolatedInstances 增加过滤的被隔离实例数量
func (f *FilteredInstanceCounter) AddIsolatedInstances(n int32) {
	atomic.AddInt32(&f.isolatedInstances, n)
}

// GetIsolatedInstances 被隔离实例数量
func (f *FilteredInstanceCounter) GetIsolatedInstances() int32 {
	return atomic.LoadInt32(&f.isolatedInstances)
}

// AddUnhealthyInstances 增加过滤的不健康实例数量
func (f *FilteredInstanceCounter) AddUnhealthyInstances(n int32) {
	atomic.AddInt32(&f.unhealthyInstances, n)
}

// GetUnhealthyInstances 不健康实例数量
func (f *FilteredInstanceCounter) GetUnhealthyInstances() int32 {
	return atomic.LoadInt32(&f.unhealthyInstances)
}

// AddRouteFilteredInstances 增加被就近接入路由过滤的实例数量
func (f *FilteredInstanceCounter) AddRouteFilteredInstances(routerName string, n int32) {
	value, loaded := f.routeFilterCounters.LoadOrStore(routerName, &routeFilterCounter{value: n})
	if loaded {
		value.(*routeFilterCounter).addValue(n)
	}
}

// getRouteFilteredInstances 就近接入路由过滤的实例数量
func (f *FilteredInstanceCounter) getRouteFilteredInstances(routerName string) int32 {
	value, loaded := f.routeFilterCounters.Load(routerName)
	if loaded {
		return value.(*routeFilterCounter).getValue()
	}
	return 0
}

// validateServiceMetadata 校验服务元数据信息
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
