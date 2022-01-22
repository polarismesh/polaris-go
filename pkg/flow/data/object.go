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

package data

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/loadbalancer"
	"github.com/polarismesh/polaris-go/pkg/plugin/ratelimiter"
	"github.com/polarismesh/polaris-go/pkg/plugin/servicerouter"
)

var (
	// 缓存查询请求的对象池
	instanceRequestPool = &sync.Pool{}
	// 缓存规则查询请求的对象池
	ruleRequestPool = &sync.Pool{}
	// 限流请求对象池
	rateLimitRequestPool = &sync.Pool{}
	// 网格规则请求对象池
	meshConfigRequestPool = &sync.Pool{}
	// 网格请求对象池
	meshRequestPool = &sync.Pool{}
	// 批量服务请求对象池
	servicesRequestPool = &sync.Pool{}
	// 调用结果上报请求对象池
	serviceCallResultRequestPool = &sync.Pool{}
)

// PoolGetCommonInstancesRequest 通过池子获取请求对象
func PoolGetCommonInstancesRequest(plugins plugin.Supplier) *CommonInstancesRequest {
	value := instanceRequestPool.Get()
	if nil == value {
		req := &CommonInstancesRequest{}
		req.RouteInfo.Init(plugins)
		return req
	}
	return value.(*CommonInstancesRequest)
}

// PoolPutCommonInstancesRequest 归还到请求对象到池子
func PoolPutCommonInstancesRequest(request *CommonInstancesRequest) {
	instanceRequestPool.Put(request)
}

func PoolGetCommonServiceCallResultRequest(plugins plugin.Supplier) *CommonServiceCallResultRequest {
	value := serviceCallResultRequestPool.Get()
	if nil == value {
		req := &CommonServiceCallResultRequest{}
		return req
	}
	return value.(*CommonServiceCallResultRequest)
}

func PoolPutCommonServiceCallResultRequest(request *CommonServiceCallResultRequest) {
	serviceCallResultRequestPool.Put(request)
}

// PoolGetCommonRuleRequest 通过池子获取请求对象
func PoolGetCommonRuleRequest() *CommonRuleRequest {
	value := ruleRequestPool.Get()
	if nil == value {
		return &CommonRuleRequest{}
	}
	return value.(*CommonRuleRequest)
}

// PoolPutCommonRuleRequest 归还到请求对象到池子
func PoolPutCommonRuleRequest(request *CommonRuleRequest) {
	ruleRequestPool.Put(request)
}

// PoolGetCommonRateLimitRequest 通过池子获取请求对象
func PoolGetCommonRateLimitRequest() *CommonRateLimitRequest {
	value := rateLimitRequestPool.Get()
	if nil == value {
		return &CommonRateLimitRequest{}
	}
	return value.(*CommonRateLimitRequest)
}

// PoolPutCommonRateLimitRequest 归还到请求对象到池子
func PoolPutCommonRateLimitRequest(request *CommonRateLimitRequest) {
	rateLimitRequestPool.Put(request)
}

// BaseRequest 通用的请求对象基类，实现了基本的方法，
// 具体请求可继承此类，根据情况实现具体方法
type BaseRequest struct {
	FlowID       uint64
	DstService   model.ServiceKey
	SrcService   model.ServiceKey
	Trigger      model.NotifyTrigger
	ControlParam model.ControlParam
	CallResult   model.APICallResult
}

func (br *BaseRequest) clearValues() {
	br.FlowID = 0
	br.Trigger.Clear()
}

// GetDstService 获取DstService
func (br *BaseRequest) GetDstService() *model.ServiceKey {
	return &br.DstService

}

// GetSrcService 获取SrcService
func (br *BaseRequest) GetSrcService() *model.ServiceKey {
	return &br.SrcService
}

// GetNotifierTrigger 获取Trigger
func (br *BaseRequest) GetNotifierTrigger() *model.NotifyTrigger {
	return &br.Trigger
}

// SetDstRoute 设置路由规则
func (br *BaseRequest) SetDstRoute(rule model.ServiceRule) {
	// do nothing
}

// SetDstRateLimit 设置ratelimit
func (br *BaseRequest) SetDstRateLimit(rule model.ServiceRule) {
	// do nothing
}

// SetSrcRoute 设置route
func (br *BaseRequest) SetSrcRoute(rule model.ServiceRule) {
	// do nothing
}

// GetControlParam 获取ControlParam
func (br *BaseRequest) GetControlParam() *model.ControlParam {
	return &br.ControlParam
}

// GetCallResult 获取结果
func (br *BaseRequest) GetCallResult() *model.APICallResult {
	return &br.CallResult
}

// SetMeshConfig 设置网格规则
func (br *BaseRequest) SetMeshConfig(mc model.MeshConfig) {
	// do nothing
}

// SetDstInstances 设置实例
func (br *BaseRequest) SetDstInstances(instances model.ServiceInstances) {
	// do nothing
}

// CommonInstancesRequest 通用请求对象，主要用于在消息过程减少GC
type CommonInstancesRequest struct {
	FlowID          uint64
	DstService      model.ServiceKey
	SrcService      model.ServiceKey
	Trigger         model.NotifyTrigger
	HasSrcService   bool
	DoLoadBalance   bool
	RouteInfo       servicerouter.RouteInfo
	DstInstances    model.ServiceInstances
	Revision        string
	Criteria        loadbalancer.Criteria
	FetchAll        bool
	SkipRouteFilter bool
	ControlParam    model.ControlParam
	CallResult      model.APICallResult
	response        *model.InstancesResponse
	// 负载均衡算法
	LbPolicy string
}

// clearValues 清理请求体
func (c *CommonInstancesRequest) clearValues(cfg config.Configuration) {
	c.FlowID = 0
	c.RouteInfo.ClearValue()
	c.DstInstances = nil
	c.Criteria.HashValue = 0
	c.Criteria.HashKey = nil
	c.Criteria.Cluster = nil
	c.Trigger.Clear()
	c.Criteria.ReplicateInfo.Count = 0
	c.Criteria.ReplicateInfo.Nodes = nil
	c.DoLoadBalance = false
	c.HasSrcService = false
	c.SkipRouteFilter = false
	c.FetchAll = false
	c.response = nil
	c.LbPolicy = ""
}

// InitByGetOneRequest 通过获取单个请求初始化通用请求对象
func (c *CommonInstancesRequest) InitByGetOneRequest(request *model.GetOneInstanceRequest, cfg config.Configuration) {
	c.clearValues(cfg)
	c.FlowID = request.FlowID
	c.DstService.Service = request.Service
	c.DstService.Namespace = request.Namespace
	c.RouteInfo.DestService = request
	c.RouteInfo.EnableFailOverDefaultMeta = request.EnableFailOverDefaultMeta
	c.RouteInfo.FailOverDefaultMeta = request.FailOverDefaultMeta
	c.RouteInfo.Canary = request.Canary
	c.response = request.GetResponse()
	c.DoLoadBalance = true
	srcService := request.SourceService
	c.Trigger.EnableDstInstances = true
	c.Trigger.EnableDstRoute = true
	if nil != srcService {
		c.HasSrcService = true
		c.SrcService.Namespace = srcService.Namespace
		c.SrcService.Service = srcService.Service
		c.RouteInfo.SourceService = srcService
		if len(srcService.Namespace) > 0 && len(srcService.Service) > 0 {
			c.Trigger.EnableSrcRoute = true
		}
	}
	c.Criteria.HashKey = request.HashKey
	c.Criteria.HashValue = request.HashValue
	c.Criteria.ReplicateInfo.Count = request.ReplicateCount
	c.CallResult.APIName = model.ApiGetOneInstance
	c.CallResult.RetStatus = model.RetSuccess
	c.CallResult.RetCode = model.ErrCodeSuccess
	c.LbPolicy = request.LbPolicy
	BuildControlParam(request, cfg, &c.ControlParam)
}

// InitByGetMultiRequest 通过获取多个请求初始化通用请求对象
func (c *CommonInstancesRequest) InitByGetMultiRequest(request *model.GetInstancesRequest, cfg config.Configuration) {
	c.clearValues(cfg)
	c.FlowID = request.FlowID
	c.DstService.Service = request.Service
	c.DstService.Namespace = request.Namespace
	c.RouteInfo.DestService = request
	c.RouteInfo.Canary = request.Canary
	c.response = request.GetResponse()
	c.SkipRouteFilter = request.SkipRouteFilter
	srcService := request.SourceService
	c.Trigger.EnableDstInstances = true
	c.Trigger.EnableDstRoute = true
	if nil != srcService {
		c.HasSrcService = true
		c.SrcService.Namespace = srcService.Namespace
		c.SrcService.Service = srcService.Service
		c.RouteInfo.SourceService = srcService
		if len(srcService.Namespace) > 0 && len(srcService.Service) > 0 {
			c.Trigger.EnableSrcRoute = true
		}
	}
	c.CallResult.APIName = model.ApiGetInstances
	c.CallResult.RetStatus = model.RetSuccess
	c.CallResult.RetCode = model.ErrCodeSuccess
	BuildControlParam(request, cfg, &c.ControlParam)
}

// InitByGetAllRequest 通过获取全部请求初始化通用请求对象
func (c *CommonInstancesRequest) InitByGetAllRequest(request *model.GetAllInstancesRequest, cfg config.Configuration) {
	c.clearValues(cfg)
	c.FlowID = request.FlowID
	c.DstService.Service = request.Service
	c.DstService.Namespace = request.Namespace
	c.RouteInfo.DestService = request
	c.response = request.GetResponse()
	c.FetchAll = true
	c.Trigger.EnableDstInstances = true
	c.CallResult.APIName = model.ApiGetAllInstances
	c.CallResult.RetStatus = model.RetSuccess
	c.CallResult.RetCode = model.ErrCodeSuccess
	BuildControlParam(request, cfg, &c.ControlParam)
}

// RefreshByRedirect 通过重定向服务来进行刷新
func (c *CommonInstancesRequest) RefreshByRedirect(redirectedService *model.ServiceInfo) {
	c.DstService.Namespace = redirectedService.Namespace
	c.DstService.Service = redirectedService.Service
	c.Trigger.EnableDstInstances = true
	c.Trigger.EnableDstRoute = true
	c.RouteInfo.DestRouteRule = nil
	c.DstInstances = nil
}

// BuildInstancesResponse 构建查询实例的应答
func (c *CommonInstancesRequest) BuildInstancesResponse(flowID uint64, dstService model.ServiceKey,
	cluster *model.Cluster, instances []model.Instance, totalWeight int, revision string,
	serviceMetaData map[string]string) *model.InstancesResponse {
	return buildInstancesResponse(c.response, flowID, dstService, cluster, instances, totalWeight, revision,
		serviceMetaData)
}

// GetDstService 获取目标服务
func (c *CommonInstancesRequest) GetDstService() *model.ServiceKey {
	return &c.DstService
}

// GetSrcService 获取源服务
func (c *CommonInstancesRequest) GetSrcService() *model.ServiceKey {
	return &c.SrcService
}

// GetNotifierTrigger 获取缓存查询触发器
func (c *CommonInstancesRequest) GetNotifierTrigger() *model.NotifyTrigger {
	return &c.Trigger
}

// SetDstInstances 设置目标服务实例
func (c *CommonInstancesRequest) SetDstInstances(instances model.ServiceInstances) {
	c.DstInstances = instances
	c.Revision = instances.GetRevision()
}

// SetDstRoute 设置目标服务路由规则
func (c *CommonInstancesRequest) SetDstRoute(rule model.ServiceRule) {
	c.RouteInfo.DestRouteRule = rule
}

// SetDstRateLimit 设置目标服务限流规则
func (c *CommonInstancesRequest) SetDstRateLimit(rule model.ServiceRule) {
	// do nothing
}

// SetMeshConfig 设置网格规则
func (c *CommonInstancesRequest) SetMeshConfig(mc model.MeshConfig) {
	// do nothing
}

// SetSrcRoute 设置源服务路由规则
func (c *CommonInstancesRequest) SetSrcRoute(rule model.ServiceRule) {
	c.RouteInfo.SourceRouteRule = rule
}

// GetCallResult 获取接口调用统计结果
func (c *CommonInstancesRequest) GetCallResult() *model.APICallResult {
	return &c.CallResult
}

// GetControlParam 获取API调用控制参数
func (c *CommonInstancesRequest) GetControlParam() *model.ControlParam {
	return &c.ControlParam
}

// SingleInstancesOwner 获取单个实例数组的持有者
type SingleInstancesOwner interface {
	// 获取单个实例数组引用
	SingleInstances() []model.Instance
}

// buildInstancesResponse 构建查询实例的应答
func buildInstancesResponse(response *model.InstancesResponse, flowID uint64, dstService model.ServiceKey,
	cluster *model.Cluster, instances []model.Instance, totalWeight int, revision string,
	serviceMetaData map[string]string) *model.InstancesResponse {
	response.FlowID = flowID
	response.ServiceInfo.Service = dstService.Service
	response.ServiceInfo.Namespace = dstService.Namespace
	response.ServiceInfo.Metadata = serviceMetaData
	if nil != cluster {
		// 对外返回的cluster，无需池化，因为可能会被别人引用
		cluster.SetReuse(false)
	}
	response.Cluster = cluster
	response.TotalWeight = totalWeight
	response.Instances = instances
	response.Revision = revision
	return response
}

// PoolGetServicesRequest 获取对象池中请求
func PoolGetServicesRequest() *ServicesRequest {
	value := servicesRequestPool.Get()
	if nil == value {
		return &ServicesRequest{}
	}
	return value.(*ServicesRequest)
}

// PoolPutServicesRequest 归还到请求对象到池子
func PoolPutServicesRequest(request *ServicesRequest) {
	servicesRequestPool.Put(request)
}

type ServicesRequest struct {
	BaseRequest
	Services model.Services
}

// GetServices 获取services
func (mc *ServicesRequest) GetServices() model.Services {
	return mc.Services
}

// SetMeshConfig 设置网格规则
func (cr *ServicesRequest) SetMeshConfig(mc model.MeshConfig) {
	// 此处复用网格接口
	cr.Services = mc
}

func (cr *ServicesRequest) InitByGetServicesRequest(
	eventType model.EventType, request *model.GetServicesRequest, cfg config.Configuration) {
	cr.clearValues()
	cr.FlowID = request.FlowID
	cr.CallResult.APIName = model.ApiMeshConfig
	cr.CallResult.RetStatus = model.RetSuccess
	cr.CallResult.RetCode = model.ErrCodeSuccess
	cr.DstService.Namespace = request.Namespace
	cr.DstService.Service = request.Business
	cr.Trigger.EnableServices = true
	BuildControlParam(request, cfg, &cr.ControlParam)
}

// BuildServicesResponse 构建答复
func (mc *ServicesRequest) BuildServicesResponse(mesh model.Services) *model.ServicesResponse {
	resp := model.ServicesResponse{
		Type:     mesh.GetType(),
		Value:    mesh.GetValue(),
		Service:  mc.DstService,
		Revision: mesh.GetRevision(),
	}
	return &resp
}

// PoolGetMeshConfigRequest 获取对象池中请求
func PoolGetMeshConfigRequest() *MeshConfigRequest {
	value := meshConfigRequestPool.Get()
	if nil == value {
		return &MeshConfigRequest{}
	}
	return value.(*MeshConfigRequest)
}

// PoolPutMeshConfigRequest 归还到请求对象到池子
func PoolPutMeshConfigRequest(request *MeshConfigRequest) {
	meshConfigRequestPool.Put(request)
}

// PoolGetMeshRequest 获取对象池中请求
func PoolGetMeshRequest() *MeshRequest {
	value := meshRequestPool.Get()
	if nil == value {
		return &MeshRequest{}
	}
	return value.(*MeshRequest)
}

// PoolPutMeshRequest 归还到请求对象到池子
func PoolPutMeshRequest(request *MeshRequest) {
	meshRequestPool.Put(request)
}

// MeshRequest 网格请求
type MeshRequest struct {
	BaseRequest
	Mesh model.Mesh
}

// GetMesh 获取services
func (mc *MeshRequest) GetMesh() model.Mesh {
	return mc.Mesh
}

// SetMeshConfig 设置网格规则
func (cr *MeshRequest) SetMeshConfig(mc model.MeshConfig) {
	// 此处复用网格接口
	cr.Mesh = mc
}

// InitByGetMeshRequest 初始化请求
func (cr *MeshRequest) InitByGetMeshRequest(
	eventType model.EventType, request *model.GetMeshRequest, cfg config.Configuration) {
	cr.clearValues()
	cr.FlowID = request.FlowID
	cr.CallResult.APIName = model.ApiMesh
	cr.CallResult.RetStatus = model.RetSuccess
	cr.CallResult.RetCode = model.ErrCodeSuccess
	cr.DstService.Namespace = request.Namespace
	cr.DstService.Service = request.MeshId
	cr.Trigger.EnableMesh = true
	BuildControlParam(request, cfg, &cr.ControlParam)
}

// BuildMeshResponse 构建答复
func (mc *MeshRequest) BuildMeshResponse(mesh model.Mesh) *model.MeshResponse {
	resp := model.MeshResponse{
		Type:     mesh.GetType(),
		Value:    mesh.GetValue(),
		Service:  mc.DstService,
		Revision: mesh.GetRevision(),
	}
	return &resp
}

// MeshConfigRequest 网格规则请求
type MeshConfigRequest struct {
	FlowID       uint64
	DstService   model.ServiceKey
	SrcService   model.ServiceKey
	Trigger      model.NotifyTrigger
	ControlParam model.ControlParam
	CallResult   model.APICallResult
	MeshConfig   model.MeshConfig
	MeshType     string
}

// SetMeshConfig 设置网格规则
func (cr *MeshConfigRequest) SetMeshConfig(mc model.MeshConfig) {
	cr.MeshConfig = mc
}

// GetMeshConfig 获取网格规则
func (cr *MeshConfigRequest) GetMeshConfig() model.MeshConfig {
	return cr.MeshConfig
}

func (cr *MeshConfigRequest) clearValues() {
	cr.FlowID = 0
	// cr.DstService = nil
	cr.Trigger.EnableMeshConfig = false
	cr.Trigger.Clear()
}

// InitByGetRuleRequest 初始化
func (cr *MeshConfigRequest) InitByGetRuleRequest(
	eventType model.EventType, request *model.GetMeshConfigRequest, cfg config.Configuration) {
	cr.clearValues()
	cr.FlowID = request.FlowID
	cr.CallResult.APIName = model.ApiMeshConfig
	cr.CallResult.RetStatus = model.RetSuccess
	cr.CallResult.RetCode = model.ErrCodeSuccess
	cr.DstService.Namespace = request.Namespace
	cr.DstService.Service = model.MeshPrefix + model.MeshKeySpliter +
		request.MeshId + model.MeshKeySpliter + request.MeshType
	cr.MeshType = request.MeshType
	cr.Trigger.EnableMeshConfig = true
	BuildControlParam(request, cfg, &cr.ControlParam)
}

// BuildMeshConfigResponse 构建答复
func (mc *MeshConfigRequest) BuildMeshConfigResponse(mesh model.MeshConfig) *model.MeshConfigResponse {
	resp := model.MeshConfigResponse{
		Type:     mesh.GetType(),
		Value:    mesh.GetValue(),
		Service:  mc.DstService,
		Revision: mesh.GetRevision(),
	}
	return &resp
}

// GetDstService 获取DstService
func (mc *MeshConfigRequest) GetDstService() *model.ServiceKey {
	return &mc.DstService

}

// 获取SrcService
func (mc *MeshConfigRequest) GetSrcService() *model.ServiceKey {
	return &mc.SrcService
}

// GetNotifierTrigger 获取Trigger
func (mc *MeshConfigRequest) GetNotifierTrigger() *model.NotifyTrigger {
	return &mc.Trigger
}

// SetDstInstances 设置实例
func (mc *MeshConfigRequest) SetDstInstances(instances model.ServiceInstances) {
	// do nothing
}

// SetDstRoute 设置路由规则
func (mc *MeshConfigRequest) SetDstRoute(rule model.ServiceRule) {
	// do nothing
}

// SetDstRateLimit 设置ratelimit
func (mc *MeshConfigRequest) SetDstRateLimit(rule model.ServiceRule) {
	// do nothing
}

// SetSrcRoute 设置route
func (mc *MeshConfigRequest) SetSrcRoute(rule model.ServiceRule) {
	// do nothing
}

// GetControlParam 获取ControlParam
func (mc *MeshConfigRequest) GetControlParam() *model.ControlParam {
	return &mc.ControlParam
}

// GetCallResult 获取结果
func (mc *MeshConfigRequest) GetCallResult() *model.APICallResult {
	return &mc.CallResult
}

// CommonRuleRequest 通用规则查询请求
type CommonRuleRequest struct {
	FlowID       uint64
	DstService   model.ServiceEventKey
	ControlParam model.ControlParam
	CallResult   model.APICallResult
	response     *model.ServiceRuleResponse
}

// clearValues 清理请求体
func (cr *CommonRuleRequest) clearValues(cfg config.Configuration) {
	cr.FlowID = 0
	cr.response = nil
}

// InitByGetRuleRequest 通过获取路由规则请求初始化通用请求对象
func (cr *CommonRuleRequest) InitByGetRuleRequest(
	eventType model.EventType, request *model.GetServiceRuleRequest, cfg config.Configuration) {
	cr.clearValues(cfg)
	cr.FlowID = request.FlowID
	cr.CallResult.APIName = model.ApiGetRouteRule
	cr.CallResult.RetStatus = model.RetSuccess
	cr.CallResult.RetCode = model.ErrCodeSuccess
	cr.DstService.Namespace = request.Namespace
	cr.DstService.Service = request.Service
	cr.DstService.Type = eventType
	cr.response = request.GetResponse()
	BuildControlParam(request, cfg, &cr.ControlParam)
}

// BuildServiceRuleResponse 构建规则查询应答
func (cr *CommonRuleRequest) BuildServiceRuleResponse(rule model.ServiceRule) *model.ServiceRuleResponse {
	resp := cr.response
	resp.Type = rule.GetType()
	resp.Value = rule.GetValue()
	resp.Revision = rule.GetRevision()
	resp.RuleCache = rule.GetRuleCache()
	resp.Service.Service = cr.DstService.Service
	resp.Service.Namespace = cr.DstService.Namespace
	resp.ValidateError = rule.GetValidateError()
	return resp
}

// GetCallResult 获取接口调用统计结果
func (cr *CommonRuleRequest) GetCallResult() *model.APICallResult {
	return &cr.CallResult
}

// GetControlParam 获取API调用控制参数
func (cr *CommonRuleRequest) GetControlParam() *model.ControlParam {
	return &cr.ControlParam
}

// CommonRateLimitRequest 通用限流接口的请求体
type CommonRateLimitRequest struct {
	DstService    model.ServiceKey
	Cluster       string
	Labels        map[string]string
	RateLimitRule model.ServiceRule
	Criteria      ratelimiter.InitCriteria
	Trigger       model.NotifyTrigger
	ControlParam  model.ControlParam
	CallResult    model.APICallResult
}

// clearValues 清理请求体
func (cl *CommonRateLimitRequest) clearValues() {
	cl.Criteria.DstRule = nil
	cl.Trigger.Clear()
	cl.Cluster = ""
	cl.Labels = nil
}

// InitByGetQuotaRequest 初始化配额获取请求
func (cl *CommonRateLimitRequest) InitByGetQuotaRequest(request *model.QuotaRequestImpl, cfg config.Configuration) {
	cl.clearValues()
	cl.DstService.Namespace = request.GetNamespace()
	cl.DstService.Service = request.GetService()
	cl.Cluster = request.GetCluster()
	cl.Labels = request.GetLabels()
	cl.Trigger.EnableDstRateLimit = true
	cl.CallResult.APIName = model.ApiGetQuota
	cl.CallResult.RetStatus = model.RetSuccess
	cl.CallResult.RetCode = model.ErrCodeSuccess
	BuildControlParam(request, cfg, &cl.ControlParam)

	// 限流相关同步请求，减少重试此数和重试间隔
	if cl.ControlParam.MaxRetry > 2 {
		cl.ControlParam.MaxRetry = 2
	}
	if cl.ControlParam.RetryInterval > time.Millisecond*500 {
		cl.ControlParam.RetryInterval = time.Millisecond * 500
	}
	if cl.ControlParam.Timeout > time.Millisecond*500 {
		cl.ControlParam.Timeout = time.Millisecond * 500
	}
}

// GetDstService 获取目标服务
func (cl *CommonRateLimitRequest) GetDstService() *model.ServiceKey {
	return &cl.DstService
}

// GetSrcService 获取源服务
func (cl *CommonRateLimitRequest) GetSrcService() *model.ServiceKey {
	return nil
}

// GetNotifierTrigger 获取缓存查询触发器
func (cl *CommonRateLimitRequest) GetNotifierTrigger() *model.NotifyTrigger {
	return &cl.Trigger
}

// SetDstInstances 设置目标服务实例
func (cl *CommonRateLimitRequest) SetDstInstances(instances model.ServiceInstances) {
	// do nothing
}

// SetDstRoute 设置目标服务路由规则
func (cl *CommonRateLimitRequest) SetDstRoute(rule model.ServiceRule) {
	// do nothing
}

// SetDstRateLimit 设置目标服务限流规则
func (cl *CommonRateLimitRequest) SetDstRateLimit(rule model.ServiceRule) {
	cl.RateLimitRule = rule
}

// SetMeshConfig 设置网格规则
func (cl *CommonRateLimitRequest) SetMeshConfig(mc model.MeshConfig) {
	// do nothing
}

// SetSrcRoute 设置源服务路由规则
func (cl *CommonRateLimitRequest) SetSrcRoute(rule model.ServiceRule) {
	// do nothing
}

// GetCallResult 获取接口调用统计结果
func (cl *CommonRateLimitRequest) GetCallResult() *model.APICallResult {
	return &cl.CallResult
}

// GetControlParam 获取API调用控制参数
func (cl *CommonRateLimitRequest) GetControlParam() *model.ControlParam {
	return &cl.ControlParam
}

// FormatLabelToStr 格式化字符串
func (cl *CommonRateLimitRequest) FormatLabelToStr(rule *namingpb.Rule) string {
	if len(cl.Labels) == 0 {
		return ""
	}
	var tmpList []string
	ruleLabels := rule.GetLabels()
	regexCombine := rule.GetRegexCombine().GetValue()
	for ruleKey, ruleValue := range ruleLabels {
		if ruleValue.GetType() == namingpb.MatchString_REGEX && regexCombine {
			tmpList = append(tmpList, ruleKey+config.DefaultMapKeyValueSeparator+ruleValue.GetValue().GetValue())
		} else {
			tmpList = append(tmpList, ruleKey+config.DefaultMapKeyValueSeparator+cl.Labels[ruleKey])
		}
	}
	sort.Strings(tmpList)
	s := strings.Join(tmpList, config.DefaultMapKVTupleSeparator)
	return s
}

type CommonServiceCallResultRequest struct {
	CallResult model.APICallResult
}

func (c *CommonServiceCallResultRequest) InitByServiceCallResult(request *model.ServiceCallResult,
	cfg config.Configuration) {
	c.CallResult.APIName = model.ApiUpdateServiceCallResult
	c.CallResult.RetStatus = model.RetSuccess
	c.CallResult.RetCode = model.ErrCodeSuccess
}

type ConsumerInitCallServiceResultRequest struct {
	CallResult model.APICallResult
}

func (c *ConsumerInitCallServiceResultRequest) InitByServiceCallResult(req *model.InitCalleeServiceRequest,
	cfg config.Configuration) {
	if req.Timeout == nil {
		req.Timeout = model.ToDurationPtr(cfg.GetGlobal().GetAPI().GetTimeout())
	}
	c.CallResult.APIName = model.ApiInitCalleeServices
	c.CallResult.RetStatus = model.RetSuccess
	c.CallResult.RetCode = model.ErrCodeSuccess
}
