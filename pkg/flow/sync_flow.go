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

package flow

import (
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/flow/cbcheck"
	"github.com/polarismesh/polaris-go/pkg/flow/data"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/loadbalancer"
	"github.com/polarismesh/polaris-go/pkg/plugin/servicerouter"
	"time"
)

//结果上报及归还请求实例请求对象
func (e *Engine) syncInstancesReportAndFinalize(commonRequest *data.CommonInstancesRequest) {
	//调用api的结果上报
	e.reportAPIStat(&commonRequest.CallResult)
	data.PoolPutCommonInstancesRequest(commonRequest)
}

//结果上报及归还限流请求对象
func (e *Engine) syncRateLimitReportAndFinalize(commonRequest *data.CommonRateLimitRequest) {
	//调用api的结果上报
	e.reportAPIStat(&commonRequest.CallResult)
	data.PoolPutCommonRateLimitRequest(commonRequest)
}

//结果上报及归还请求实例规则对象
func (e *Engine) syncRuleReportAndFinalize(commonRequest *data.CommonRuleRequest) {
	//调用api的结果上报
	e.reportAPIStat(&commonRequest.CallResult)
	data.PoolPutCommonRuleRequest(commonRequest)
}

func (e *Engine) syncMeshConfigAndFinalize(commonRequest *data.MeshConfigRequest) {
	//调用api的结果上报
	e.reportAPIStat(&commonRequest.CallResult)
	data.PoolPutMeshConfigRequest(commonRequest)
}

func (e *Engine) syncMeshAndFinalize(commonRequest *data.MeshRequest) {
	//调用api的结果上报
	e.reportAPIStat(&commonRequest.CallResult)
	data.PoolPutMeshRequest(commonRequest)
}

func (e *Engine) syncServicesAndFinalize(commonRequest *data.ServicesRequest) {
	//调用api的结果上报
	e.reportAPIStat(&commonRequest.CallResult)
	data.PoolPutServicesRequest(commonRequest)
}

func (e *Engine) syncServiceCallResultReportAndFinalize(commonRequest *data.CommonServiceCallResultRequest) {
	e.reportAPIStat(&commonRequest.CallResult)
	data.PoolPutCommonServiceCallResultRequest(commonRequest)
}

func (e *Engine) syncConsumerInitCallServiceAndFinalize(commonRequest *data.ConsumerInitCallServiceResultRequest) {
	e.reportAPIStat(&commonRequest.CallResult)
}

//SyncGetOneInstance 同步获取服务实例
func (e *Engine) SyncGetOneInstance(req *model.GetOneInstanceRequest) (*model.OneInstanceResponse, error) {
	//方法开始时间
	commonRequest := data.PoolGetCommonInstancesRequest(e.plugins)
	commonRequest.InitByGetOneRequest(req, e.configuration)
	resp, err := e.doSyncGetOneInstance(commonRequest)
	e.syncInstancesReportAndFinalize(commonRequest)
	return resp, err
}

//操作主要业务逻辑
func (e *Engine) doSyncGetOneInstance(commonRequest *data.CommonInstancesRequest) (*model.OneInstanceResponse, error) {
	startTime := e.globalCtx.Now()
	err := e.syncGetWrapInstances(commonRequest)
	consumeTime := e.globalCtx.Since(startTime)
	if nil != err {
		(&commonRequest.CallResult).SetFail(model.GetErrorCodeFromError(err), consumeTime)
		return nil, err
	}
	balancer, err := e.getLoadBalancer(commonRequest.DstInstances, commonRequest.LbPolicy)
	if err != nil {
		return nil, err
	}
	inst, err := loadbalancer.ChooseInstance(e.globalCtx, balancer, &commonRequest.Criteria, commonRequest.DstInstances)
	consumeTime = e.globalCtx.Since(startTime)
	if nil != err {
		(&commonRequest.CallResult).SetFail(model.GetErrorCodeFromError(err), consumeTime)
		return nil, err
	}
	(&commonRequest.CallResult).SetSuccess(consumeTime)
	var instances []model.Instance
	replicateInstances := commonRequest.Criteria.ReplicateInfo.Nodes
	if len(replicateInstances) > 0 {
		instances = make([]model.Instance, 0, len(replicateInstances)+1)
		instances = append(instances, inst)
		instances = append(instances, replicateInstances...)
	} else {
		instances = inst.(data.SingleInstancesOwner).SingleInstances()
	}
	instancesResp := commonRequest.BuildInstancesResponse(commonRequest.FlowID, commonRequest.DstService,
		nil, instances, 0, commonRequest.Revision, commonRequest.DstInstances.GetMetadata())
	return &model.OneInstanceResponse{InstancesResponse: *instancesResp}, nil
}

//同步加载资源
func (e *Engine) SyncGetResources(req model.CacheValueQuery) error {
	var err error
	var retryTimes = -1
	var combineContext *CombineNotifyContext
	dstService := req.GetDstService()
	param := req.GetControlParam()
	var totalConsumedTime, totalSleepTime time.Duration
outLoop:
	for retryTimes < param.MaxRetry {
		startTime := e.globalCtx.Now()
		//尝试获取本地缓存的值
		combineContext, err = getAndLoadCacheValues(e.registry, req, retryTimes < param.MaxRetry)
		if nil != err {
			break outLoop
		}
		//本地缓存已经加载完成，退出
		if nil == combineContext {
			return nil
		}
		//发起并等待远程的结果
		retryTimes++
		syncCtx := combineContext
		exceedTimeout := syncCtx.Wait(param.Timeout)
		//计算请求耗时
		consumedTime := e.globalCtx.Since(startTime)
		totalConsumedTime += consumedTime
		sdkErrs := syncCtx.Errs()
		if len(sdkErrs) > 0 {
			e.reportCombinedErrs(req.GetCallResult(), consumedTime, sdkErrs)
			rawErr := combineSDKErrors(sdkErrs)
			log.GetBaseLogger().Errorf("error occur while processing GetInstances request,"+
				" serviceKey: %s, time consume is %v, error is %s", *dstService, consumedTime, rawErr)
			return model.NewSDKError(model.ErrCodeServerUserError, rawErr,
				fmt.Sprintf("multierrs received for GetInstances request, serviceKey: %s", *dstService))
		}
		if exceedTimeout {
			//只有网络错误才可以重试
			time.Sleep(param.RetryInterval)
			totalSleepTime += param.RetryInterval
			//log.GetBaseLogger().Warnf("retry GetInstances for timeout, consume time %v,"+
			//	" serviceKey: %s, retry times: %d",
			//	consumedTime, *dstService, retryTimes)
			continue
		}
		//没有发生远程错误，直接走下一轮获取本地缓存
		log.GetBaseLogger().Debugf("requests for instances and rules finished,"+
			" serviceKey: %s, time consume is %v, retryTimes: %v", *dstService, consumedTime, retryTimes)
		continue
	}
	//超时过后，并且没有其他错误，那么尝试使用从缓存中获取的信息
	var success bool
	if nil == err {
		success, err = tryGetServiceValuesFromCache(e.registry, req)
		if success {
			log.GetBaseLogger().Warnf("retryTimes %d equals maxRetryTimes %d, get %s from cache",
				retryTimes, param.MaxRetry, *dstService)
			return nil
		}
	}
	log.GetBaseLogger().Errorf("fail to get resource of %s for timeout, retryTimes: %d, total consumed time: %v,"+
		" total sleep time: %v", *dstService, retryTimes, totalConsumedTime, totalSleepTime)
	errMsg := fmt.Sprintf("retry times exceed %d in SyncGetResources, serviceKey: %s, timeout is %v",
		retryTimes, *dstService, param.Timeout)
	log.GetBaseLogger().Errorf(errMsg)
	return model.NewSDKError(model.ErrCodeAPITimeoutError, err, errMsg)
}

//上报在获取实例信息时可能发生的多个错误
func (e *Engine) reportCombinedErrs(apiRes *model.APICallResult, consumedTime time.Duration,
	errs map[ContextKey]model.SDKError) {
	origDelay := *apiRes.GetDelay()
	origStatus := apiRes.RetStatus
	origRetCode := apiRes.RetCode
	apiRes.SetDelay(consumedTime)
	apiRes.RetStatus = model.RetFail
	for _, v := range errs {
		apiRes.RetCode = v.ErrorCode()
		e.reportAPIStat(apiRes)
	}
	apiRes.SetDelay(origDelay)
	apiRes.RetCode = origRetCode
	apiRes.RetStatus = origStatus
}

//getServiceRoutedInstances 过滤经过规则路由后的服务实例
func (e *Engine) getServiceRoutedInstances(
	req *data.CommonInstancesRequest) (routeResult *servicerouter.RouteResult, err model.SDKError) {
	var routerChain = e.getRouterChain(req.DstInstances)
	return servicerouter.GetFilterCluster(e.globalCtx, routerChain.Chain, &req.RouteInfo,
		req.DstInstances.GetServiceClusters())
}

//同步获取封装的服务实例应答
func (e *Engine) syncGetWrapInstances(req *data.CommonInstancesRequest) error {
	var redirectedTimes = 0
	var cluster *model.Cluster
	var redirectedService *model.ServiceInfo
	for redirectedTimes <= config.MaxRedirectTimes {
		err := e.SyncGetResources(req)
		if nil != err {
			return err
		}
		if req.FetchAll {
			//获取全量服务实例
			cluster = model.NewCluster(req.DstInstances.GetServiceClusters(), nil)
		} else {
			//走就近路由
			cluster, redirectedService, err = e.afterLazyGetInstances(req)
			if nil != err {
				return err
			}
			if nil != redirectedService {
				redirectedTimes++
				req.RefreshByRedirect(redirectedService)
				continue
			}
		}
		req.Criteria.Cluster = verifyCluster(req.DstInstances, cluster)
		return nil
	}
	return model.NewSDKError(model.ErrCodeInvalidRule, nil,
		"redirect times exceed %d in route rule, service %s, namespace %s",
		config.MaxRedirectTimes, req.DstService.Service, req.DstService.Namespace)
}

//缓存对账，确保cluster的根与当前查询出来的服务实例一致
func verifyCluster(svcInstances model.ServiceInstances, cluster *model.Cluster) *model.Cluster {
	clsServices := cluster.GetClusters().GetServiceInstances()
	if clsServices.GetRevision() == svcInstances.GetRevision() {
		return cluster
	}
	//对账失败，需要重建cluster
	log.GetBaseLogger().Warnf("cluster invalid, namespace: %s, service:%s cluster revision %s,   "+
		"namespace: %s, service:%s services revision %s, rebuild cluster",
		clsServices.GetService(), clsServices.GetNamespace(), clsServices.GetRevision(),
		svcInstances.GetNamespace(), svcInstances.GetService(), svcInstances.GetRevision())
	newCls := model.NewCluster(svcInstances.GetServiceClusters(), cluster)
	cluster.PoolPut()
	return newCls
}

//SyncGetInstances 同步获取服务实例
func (e *Engine) SyncGetInstances(req *model.GetInstancesRequest) (*model.InstancesResponse, error) {
	commonRequest := data.PoolGetCommonInstancesRequest(e.plugins)
	commonRequest.InitByGetMultiRequest(req, e.configuration)
	resp, err := e.doSyncGetInstances(commonRequest)
	e.syncInstancesReportAndFinalize(commonRequest)
	return resp, err
}

//SyncGetInstances 同步获取服务实例
func (e *Engine) SyncGetAllInstances(req *model.GetAllInstancesRequest) (*model.InstancesResponse, error) {
	commonRequest := data.PoolGetCommonInstancesRequest(e.plugins)
	commonRequest.InitByGetAllRequest(req, e.configuration)
	resp, err := e.doSyncGetAllInstances(commonRequest)
	e.syncInstancesReportAndFinalize(commonRequest)
	return resp, err
}

//doSyncGetAllInstances 同步获取全量服务实例
func (e *Engine) doSyncGetAllInstances(commonRequest *data.CommonInstancesRequest) (*model.InstancesResponse, error) {
	startTime := e.globalCtx.Now()
	err := e.syncGetWrapInstances(commonRequest)
	consumeTime := e.globalCtx.Since(startTime)
	if nil != err {
		(&commonRequest.CallResult).SetFail(model.GetErrorCodeFromError(err), consumeTime)
		return nil, err
	}
	(&commonRequest.CallResult).SetSuccess(consumeTime)
	dstInstances := commonRequest.DstInstances
	return commonRequest.BuildInstancesResponse(
		commonRequest.FlowID, commonRequest.DstService, commonRequest.Criteria.Cluster,
		dstInstances.GetInstances(), dstInstances.GetTotalWeight(), dstInstances.GetRevision(),
		dstInstances.GetMetadata()), nil
}

//doSyncGetInstances 同步获取服务实例
func (e *Engine) doSyncGetInstances(commonRequest *data.CommonInstancesRequest) (*model.InstancesResponse, error) {
	startTime := e.globalCtx.Now()
	err := e.syncGetWrapInstances(commonRequest)
	consumeTime := e.globalCtx.Since(startTime)
	if nil != err {
		(&commonRequest.CallResult).SetFail(model.GetErrorCodeFromError(err), consumeTime)
		return nil, err
	}
	(&commonRequest.CallResult).SetSuccess(consumeTime)
	targetCls := commonRequest.Criteria.Cluster
	var instances []model.Instance
	var totalWeight int
	if commonRequest.SkipRouteFilter {
		instances, totalWeight = targetCls.GetInstancesWhenSkipRouteFilter()
	} else {
		instances, totalWeight = targetCls.GetInstances()
	}
	return commonRequest.BuildInstancesResponse(commonRequest.FlowID, commonRequest.DstService,
		targetCls, instances, totalWeight, commonRequest.Revision, commonRequest.DstInstances.GetMetadata()), nil
}

/**
 * @brief 同步进行服务注册
 */
func (e *Engine) SyncRegister(instance *model.InstanceRegisterRequest) (*model.InstanceRegisterResponse, error) {
	//调用api的结果上报
	apiCallResult := &model.APICallResult{
		APICallKey: model.APICallKey{
			APIName: model.ApiRegister,
			RetCode: model.ErrCodeSuccess,
		},
		RetStatus: model.RetSuccess,
	}
	defer e.reportAPIStat(apiCallResult)
	param := &model.ControlParam{}
	data.BuildControlParam(instance, e.configuration, param)
	//方法开始时间
	startTime := e.globalCtx.Now()
	svcKey := model.ServiceKey{Namespace: instance.Namespace, Service: instance.Service}
	resp, err := data.RetrySyncCall("register", &svcKey, instance, func(request interface{}) (interface{}, error) {
		return e.connector.RegisterInstance(request.(*model.InstanceRegisterRequest))
	}, param)
	consumeTime := e.globalCtx.Since(startTime)
	if nil != err {
		apiCallResult.SetFail(model.GetErrorCodeFromError(err), consumeTime)
		return nil, err
	}
	apiCallResult.SetSuccess(consumeTime)
	return resp.(*model.InstanceRegisterResponse), nil
}

/**
 * @brief 同步进行服务反注册
 */
func (e *Engine) SyncDeregister(instance *model.InstanceDeRegisterRequest) error {
	//调用api的结果上报
	apiCallResult := &model.APICallResult{
		APICallKey: model.APICallKey{
			APIName: model.ApiDeregister,
			RetCode: model.ErrCodeSuccess,
		},
		RetStatus: model.RetSuccess,
	}
	defer e.reportAPIStat(apiCallResult)
	param := &model.ControlParam{}
	data.BuildControlParam(instance, e.configuration, param)
	//方法开始时间
	startTime := e.globalCtx.Now()
	svcKey := model.ServiceKey{Namespace: instance.Namespace, Service: instance.Service}
	_, err := data.RetrySyncCall("deregister", &svcKey, instance, func(request interface{}) (interface{}, error) {
		return nil, e.connector.DeregisterInstance(request.(*model.InstanceDeRegisterRequest))
	}, param)
	consumeTime := e.globalCtx.Since(startTime)
	if nil != err {
		apiCallResult.SetFail(model.GetErrorCodeFromError(err), consumeTime)
	} else {
		apiCallResult.SetSuccess(consumeTime)
	}
	return err
}

/**
 * @brief 同步进行心跳上报
 */
func (e *Engine) SyncHeartbeat(instance *model.InstanceHeartbeatRequest) error {
	//调用api的结果上报
	apiCallResult := &model.APICallResult{
		APICallKey: model.APICallKey{
			APIName: model.ApiHeartbeat,
			RetCode: model.ErrCodeSuccess,
		},
		RetStatus: model.RetSuccess,
	}
	defer e.reportAPIStat(apiCallResult)
	param := &model.ControlParam{}
	data.BuildControlParam(instance, e.configuration, param)
	//方法开始时间
	startTime := e.globalCtx.Now()
	svcKey := model.ServiceKey{Namespace: instance.Namespace, Service: instance.Service}
	_, err := data.RetrySyncCall("heartbeat", &svcKey, instance, func(request interface{}) (interface{}, error) {
		return nil, e.connector.Heartbeat(request.(*model.InstanceHeartbeatRequest))
	}, param)
	consumeTime := e.globalCtx.Since(startTime)
	if nil != err {
		apiCallResult.SetFail(model.GetErrorCodeFromError(err), consumeTime)
	} else {
		apiCallResult.SetSuccess(consumeTime)
	}
	return err
}

/**
 * @brief 同步上报调用结果信息
 */
func (e *Engine) SyncUpdateServiceCallResult(result *model.ServiceCallResult) error {
	commonRequest := data.PoolGetCommonServiceCallResultRequest(e.plugins)
	commonRequest.InitByServiceCallResult(result, e.configuration)
	startTime := e.globalCtx.Now()
	err := e.realSyncUpdateServiceCallResult(result)
	consumeTime := e.globalCtx.Since(startTime)
	if nil != err {
		(&commonRequest.CallResult).SetFail(model.GetErrorCodeFromError(err), consumeTime)
	} else {
		(&commonRequest.CallResult).SetSuccess(consumeTime)
	}
	e.syncServiceCallResultReportAndFinalize(commonRequest)
	return err
}

/**
 * @brief 同步上报调用结果信息 实际处理函数
 */
func (e *Engine) realSyncUpdateServiceCallResult(result *model.ServiceCallResult) error {
	//当前处理熔断和服务调用统计上报
	if err := e.reportSvcStat(result); nil != err {
		return err
	}
	if nil == e.rtCircuitBreakChan || len(e.circuitBreakerChain) == 0 {
		return nil
	}
	var rtTask *cbcheck.RealTimeLimitTask
	for _, cbreaker := range e.circuitBreakerChain {
		cbName := cbreaker.Name()
		rtLimit, err := cbreaker.Stat(result)
		if nil != err {
			return model.NewSDKError(model.ErrCodeCircuitBreakerError, err,
				"fail to do real time circuitBreak in %s", cbName)
		}
		if rtLimit && nil == rtTask {
			rtTask = &cbcheck.RealTimeLimitTask{
				SvcKey: model.ServiceKey{
					Namespace: result.GetNamespace(),
					Service:   result.GetService()},
				InstID: result.GetID(),
				Host:   result.GetHost(),
				Port:   result.GetPort(),
				CbName: cbName}
		}
	}
	if nil == rtTask {
		return nil
	}
	rtCircuitBreakTask := &model.PriorityTask{
		Name:     fmt.Sprintf("real-time-cb-%s", result.GetID()),
		CallBack: cbcheck.NewCircuitBreakRealTimeCallBack(e.circuitBreakTask, rtTask),
	}
	log.GetDetectLogger().Debugf("realTime circuit break task %s for %s generated", rtCircuitBreakTask.Name, rtTask.SvcKey)
	e.rtCircuitBreakChan <- rtCircuitBreakTask
	return nil
}

//SyncGetServices
func (e *Engine) SyncGetServices(eventType model.EventType,
	req *model.GetServicesRequest) (*model.ServicesResponse, error) {
	commonRequest := data.PoolGetServicesRequest()
	commonRequest.InitByGetServicesRequest(eventType, req, e.configuration)
	resp, err := e.doSyncGetServices(commonRequest)
	e.syncServicesAndFinalize(commonRequest)
	return resp, err
}

func (e *Engine) doSyncGetServices(commonRequest *data.ServicesRequest) (*model.ServicesResponse, error) {
	log.GetBaseLogger().Debugf("doSyncGetServices----->")
	err := e.SyncGetResources(commonRequest)
	if nil != err {
		return nil, err
	}
	return commonRequest.BuildServicesResponse(commonRequest.GetServices()), nil
}

//SyncGetMeshConfig
func (e *Engine) SyncGetMeshConfig(eventType model.EventType,
	req *model.GetMeshConfigRequest) (*model.MeshConfigResponse, error) {
	//方法开始时间
	commonRequest := data.PoolGetMeshConfigRequest()
	commonRequest.InitByGetRuleRequest(eventType, req, e.configuration)
	resp, err := e.doSyncGetMeshConfig(commonRequest)
	e.syncMeshConfigAndFinalize(commonRequest)
	return resp, err
}

func (e *Engine) doSyncGetMeshConfig(commonRequest *data.MeshConfigRequest) (
	*model.MeshConfigResponse, error) {
	err := e.SyncGetResources(commonRequest)
	if nil != err {
		return nil, err
	}
	return commonRequest.BuildMeshConfigResponse(commonRequest.GetMeshConfig()), nil
}

//同步获取网格
func (e *Engine) SyncGetMesh(eventType model.EventType,
	req *model.GetMeshRequest) (*model.MeshResponse, error) {
	//方法开始时间
	commonRequest := data.PoolGetMeshRequest()
	commonRequest.InitByGetMeshRequest(eventType, req, e.configuration)
	resp, err := e.doSyncGetMesh(commonRequest)
	e.syncMeshAndFinalize(commonRequest)
	return resp, err
}

func (e *Engine) doSyncGetMesh(commonRequest *data.MeshRequest) (
	*model.MeshResponse, error) {
	err := e.SyncGetResources(commonRequest)
	if nil != err {
		return nil, err
	}
	return commonRequest.BuildMeshResponse(commonRequest.GetMesh()), nil
}

//同步获取服务规则
func (e *Engine) SyncGetServiceRule(
	eventType model.EventType, req *model.GetServiceRuleRequest) (*model.ServiceRuleResponse, error) {
	commonRequest := data.PoolGetCommonRuleRequest()
	commonRequest.InitByGetRuleRequest(eventType, req, e.configuration)
	resp, err := e.doSyncGetServiceRule(commonRequest)
	e.syncRuleReportAndFinalize(commonRequest)
	return resp, err
}

//同步获取服务规则
func (e *Engine) doSyncGetServiceRule(commonRequest *data.CommonRuleRequest) (*model.ServiceRuleResponse, error) {
	maxRetryTimes := commonRequest.ControlParam.MaxRetry
	//构建规则过滤器
	var retryTimes = -1
	var err error
	svcRuleKey := &ContextKey{
		ServiceKey: &commonRequest.DstService.ServiceKey,
		Operation:  keyDstRoute}
	apiStartTime := e.globalCtx.Now()
	for retryTimes < maxRetryTimes {
		startTime := e.globalCtx.Now()
		svcRule := e.registry.GetServiceRouteRule(&commonRequest.DstService.ServiceKey, false)
		if svcRule.IsInitialized() {
			commonRequest.CallResult.SetSuccess(e.globalCtx.Since(startTime))
			return commonRequest.BuildServiceRuleResponse(svcRule), nil
		}
		var notifier *common.Notifier
		if notifier, err = e.registry.LoadServiceRouteRule(&commonRequest.DstService.ServiceKey); nil != err {
			(&commonRequest.CallResult).SetFail(
				model.GetErrorCodeFromError(err), e.globalCtx.Since(apiStartTime))
			return nil, err
		}
		singleCtx := NewSingleNotifyContext(svcRuleKey, notifier)
		retryTimes++
		exceedTimeout := singleCtx.Wait(commonRequest.ControlParam.Timeout)
		//计算请求耗时
		consumedTime := e.globalCtx.Since(startTime)
		if exceedTimeout {
			//只有网络错误才可以重试
			time.Sleep(commonRequest.ControlParam.RetryInterval)
			log.GetBaseLogger().Warnf("retry GetRoutes for timeout, consume time %v,"+
				" Namespace: %s, Service: %s, retry times: %d",
				consumedTime, commonRequest.DstService.Namespace, commonRequest.DstService.Service, retryTimes)
			continue
		}
		sdkErr := singleCtx.Err()
		if nil != sdkErr {
			log.GetBaseLogger().Errorf("error occur while processing %s request,"+
				" Namespace: %s, Service: %s, time consume is %v, error is %s",
				svcRuleKey.Operation, commonRequest.DstService.Namespace, commonRequest.DstService.Service,
				consumedTime, sdkErr)
			(&commonRequest.CallResult).SetFail(
				model.GetErrorCodeFromError(sdkErr), consumedTime)
			return nil, sdkErr
		}
	}
	log.GetBaseLogger().Warnf("retry GetRoutes from cache loaded from cache files because of timeout, "+
		" Namespace: %s, Service: %s",
		commonRequest.DstService.Namespace, commonRequest.DstService.Service)
	//上面的尝试超时之后，向尝试获取从缓存文件加载的信息
	svcRule := e.registry.GetServiceRouteRule(&commonRequest.DstService.ServiceKey, true)
	if svcRule.IsInitialized() {
		commonRequest.CallResult.SetSuccess(e.globalCtx.Since(apiStartTime))
		return commonRequest.BuildServiceRuleResponse(svcRule), nil
	}
	(&commonRequest.CallResult).SetFail(
		model.ErrCodeAPITimeoutError, e.globalCtx.Since(apiStartTime))
	return nil, model.NewSDKError(model.ErrCodeAPITimeoutError, nil,
		"retry times exceed %d in SyncGetServiceRule, service %s, namespace %s",
		maxRetryTimes, commonRequest.DstService.Service, commonRequest.DstService.Namespace)
}

//初始化服务运行中需要的被调服务
func (e *Engine) InitCalleeService(req *model.InitCalleeServiceRequest) error {
	commonRequest := &data.ConsumerInitCallServiceResultRequest{}
	commonRequest.InitByServiceCallResult(req, e.configuration)
	err := e.realInitCalleeService(req, commonRequest)
	e.syncConsumerInitCallServiceAndFinalize(commonRequest)
	return err
}

//初始化服务运行中需要的被调服务
func (e *Engine) realInitCalleeService(req *model.InitCalleeServiceRequest,
	reportReq *data.ConsumerInitCallServiceResultRequest) error {
	getAllReq := model.GetAllInstancesRequest{
		FlowID:    0,
		Service:   req.Service,
		Namespace: req.Namespace,
		Timeout:   req.Timeout,
	}
	startTime := e.globalCtx.Now()
	commonRequest := data.PoolGetCommonInstancesRequest(e.plugins)
	defer data.PoolPutCommonInstancesRequest(commonRequest)
	commonRequest.InitByGetAllRequest(&getAllReq, e.configuration)
	_, err := e.doSyncGetAllInstances(commonRequest)
	costTime := e.globalCtx.Since(startTime)
	if err != nil {
		reportReq.CallResult.SetFail(model.ErrCodeConsumerInitCalleeError, costTime)
		sdkErr := model.NewSDKError(model.ErrCodeConsumerInitCalleeError, err, err.Error())
		return sdkErr
	}
	reportReq.CallResult.SetSuccess(costTime)
	return nil
}
