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
	"context"
	"errors"
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/circuitbreaker"
)

// Check
func (e *Engine) Check(resource model.Resource) (*model.CheckResult, error) {
	return e.circuitBreakerFlow.Check(resource)
}

// Report
func (e *Engine) Report(reportStat *model.ResourceStat) error {
	return e.circuitBreakerFlow.Report(reportStat)
}

// MakeFunctionDecorator
func (e *Engine) MakeFunctionDecorator(f model.CustomerFunction, reqCtx *model.RequestContext) model.DecoratorFunction {
	return e.circuitBreakerFlow.MakeFunctionDecorator(f, reqCtx)
}

// MakeInvokeHandler
func (e *Engine) MakeInvokeHandler(reqCtx *model.RequestContext) model.InvokeHandler {
	return e.circuitBreakerFlow.MakeInvokeHandler(reqCtx)
}

type CircuitBreakerFlow struct {
	engine          *Engine
	resourceBreaker circuitbreaker.CircuitBreaker
}

func newCircuitBreakerFlow(e *Engine, breaker circuitbreaker.CircuitBreaker) *CircuitBreakerFlow {
	return &CircuitBreakerFlow{
		engine:          e,
		resourceBreaker: breaker,
	}
}

func (e *CircuitBreakerFlow) Check(resource model.Resource) (*model.CheckResult, error) {
	if e.resourceBreaker == nil {
		return nil, model.NewSDKError(model.ErrCodeInternalError, nil, "circuitbreaker not found")
	}

	status := e.resourceBreaker.CheckResource(resource)
	if status != nil {
		return circuitBreakerStatusToResult(status), nil
	}

	return &model.CheckResult{
		Pass:         true,
		RuleName:     "",
		FallbackInfo: nil,
	}, nil
}

// circuitBreakerStatusToResult 将熔断状态映射为 CheckResult
// Open      → Pass=false，调用方据此构造 CallAborted 拒绝放行；
// HalfOpen  → 调 HalfOpenStatus.AcquirePermission 精确发放配额；
//
//	成功获取配额时 Pass=true，配额耗尽时 Pass=false（等价 Open 处理）；
//
// Close     → Pass=true，正常放行。
func circuitBreakerStatusToResult(breakerStatus model.CircuitBreakerStatus) *model.CheckResult {
	status := breakerStatus.GetStatus()
	if status == model.Open {
		return &model.CheckResult{
			Pass:         false,
			RuleName:     breakerStatus.GetCircuitBreaker(),
			FallbackInfo: breakerStatus.GetFallbackInfo(),
		}
	}
	if status == model.HalfOpen {
		halfOpen, ok := breakerStatus.(*model.HalfOpenStatus)
		if ok && !halfOpen.AcquirePermission() {
			return &model.CheckResult{
				Pass:         false,
				RuleName:     breakerStatus.GetCircuitBreaker(),
				FallbackInfo: breakerStatus.GetFallbackInfo(),
			}
		}
	}
	return &model.CheckResult{
		Pass:         true,
		RuleName:     breakerStatus.GetCircuitBreaker(),
		FallbackInfo: breakerStatus.GetFallbackInfo(),
	}
}

func (e *CircuitBreakerFlow) Report(reportStat *model.ResourceStat) error {
	if e.resourceBreaker == nil {
		return model.NewSDKError(model.ErrCodeInternalError, nil, "circuitbreaker not found")
	}
	return e.resourceBreaker.Report(reportStat)
}

func (e *CircuitBreakerFlow) MakeFunctionDecorator(f model.CustomerFunction, reqCtx *model.RequestContext) model.DecoratorFunction {
	decorator := &DefaultFunctionalDecorator{
		invoke: &DefaultInvokeHandler{
			flow:   e,
			reqCtx: reqCtx,
		},
		customerFunc: f,
	}
	return decorator.Decorator
}

func (e *CircuitBreakerFlow) MakeInvokeHandler(reqCtx *model.RequestContext) model.InvokeHandler {
	return &DefaultInvokeHandler{
		flow:   e,
		reqCtx: reqCtx,
	}
}

type DefaultFunctionalDecorator struct {
	invoke       model.InvokeHandler
	customerFunc model.CustomerFunction
}

func (df *DefaultFunctionalDecorator) Decorator(ctx context.Context, args interface{}) (interface{}, *model.CallAborted, error) {
	invoke := df.invoke
	pass, aborted, err := invoke.AcquirePermission()
	if err != nil {
		return nil, nil, err
	}
	var (
		ret   interface{}
		start = time.Now()
		cerr  error
	)
	if !pass {
		cerr = aborted.GetError()
		return nil, aborted, cerr
	}

	// 把可变的 InvokeContext 挂到 ctx 上：
	// 业务回调在 customer func 内拿到所选实例后调 SetInstance，
	// 装饰器结束后会从这里取出 instance 触发实例级上报。
	// 业务不调用时 ic.instance == nil，行为退化为只上报服务级 + 接口级（向后兼容）。
	ic := &model.InvokeContext{}
	ctx = model.WithInvokeContext(ctx, ic)

	defer func() {
		delay := time.Since(start)
		// Instance 字段统一在装饰器层填充，下游 InvokeHandler 可在 commonReport
		// 里根据它决定是否需要实例级 Resource 上报。
		if cerr != nil {
			rspCtx := &model.ResponseContext{
				Duration: delay,
				Err:      cerr,
				Instance: ic.Instance(),
			}
			invoke.OnError(rspCtx)
		} else {
			rspCtx := &model.ResponseContext{
				Duration: delay,
				Result:   ret,
				Instance: ic.Instance(),
			}
			invoke.OnSuccess(rspCtx)
		}
	}()

	ret, cerr = df.customerFunc(ctx, args)
	if cerr != nil {
		return nil, nil, cerr
	}
	return ret, nil, nil
}

type DefaultInvokeHandler struct {
	flow   *CircuitBreakerFlow
	reqCtx *model.RequestContext
}

func (h *DefaultInvokeHandler) AcquirePermission() (bool, *model.CallAborted, error) {
	check, err := h.commonCheck(h.reqCtx)
	if err != nil {
		return true, nil, err
	}
	if check != nil {
		return false, model.NewCallAborted(model.ErrorCallAborted, check.RuleName, check.FallbackInfo), nil
	}
	return true, nil, nil
}

func (h *DefaultInvokeHandler) OnSuccess(respCtx *model.ResponseContext) {
	delay := respCtx.Duration
	code := "0"
	retStatus := model.RetUnknown
	if h.reqCtx.CodeConvert != nil {
		code = h.reqCtx.CodeConvert.OnSuccess(respCtx.Result)
	}
	if err := h.commonReport(h.reqCtx, delay, code, retStatus, respCtx.Instance); err != nil {
		h.flow.engine.logCtx.GetBaseLogger().Errorf("DefaultInvokeHandler.commonReport in OnSuccess: %v", err)
	}
}

func (h *DefaultInvokeHandler) OnError(respCtx *model.ResponseContext) {
	delay := respCtx.Duration
	code := "-1"
	retStatus := model.RetUnknown
	if h.reqCtx.CodeConvert != nil {
		code = h.reqCtx.CodeConvert.OnError(respCtx.Err)
	}
	if errors.Is(respCtx.Err, model.ErrorCallAborted) {
		retStatus = model.RetReject
	}
	if err := h.commonReport(h.reqCtx, delay, code, retStatus, respCtx.Instance); err != nil {
		h.flow.engine.logCtx.GetBaseLogger().Errorf("DefaultInvokeHandler.commonReport in OnError: %v", err)
	}
}

// commonCheck 业务调用前的统一熔断检查
// 按服务级 → 接口级顺序短路检查，任一级拒绝即放行 CheckResult；
// 服务级先检查是为了让"全服务不可用"信号优先于"单接口不可用"。
// 接口级 Resource 的构造规则：
//  1. 若 reqCtx.Path 非空，使用完整四元组（protocol/httpMethod/path）；
//  2. 否则若 reqCtx.Method 非空，沿用旧逻辑把 Method 视作 path（向后兼容）；
//  3. 二者都为空时跳过接口级检查。
//
// 返回值：拦截到熔断时返回对应 CheckResult，否则返回 (nil, nil) 表示放行。
func (h *DefaultInvokeHandler) commonCheck(reqCtx *model.RequestContext) (*model.CheckResult, error) {
	svcRes, err := model.NewServiceResource(reqCtx.Callee, reqCtx.Caller)
	if err != nil {
		return nil, err
	}
	result, err := h.flow.Check(svcRes)
	if err != nil {
		return nil, err
	}
	if !result.Pass {
		return result, nil
	}
	methodSvc, err := buildMethodResource(reqCtx)
	if err != nil {
		return nil, err
	}
	if methodSvc == nil {
		return nil, nil
	}
	result, err = h.flow.Check(methodSvc)
	if err != nil {
		return nil, err
	}
	if !result.Pass {
		return result, nil
	}
	return nil, nil
}

// buildMethodResource 根据 RequestContext 构造接口级熔断资源
// 优先使用四元组（Protocol/HTTPMethod/Path），缺失 Path 时退化到旧 Method 字段（视作 path），
// 三者均空时返回 (nil, nil) 表示不参与接口级熔断。
func buildMethodResource(reqCtx *model.RequestContext) (*model.MethodResource, error) {
	if reqCtx.Path != "" {
		return model.NewMethodResourceWithAPI(
			reqCtx.Callee, reqCtx.Caller, reqCtx.Protocol, reqCtx.HTTPMethod, reqCtx.Path)
	}
	if reqCtx.Method != "" {
		return model.NewMethodResource(reqCtx.Callee, reqCtx.Caller, reqCtx.Method)
	}
	return nil, nil
}

// commonReport 业务调用结束的统一熔断上报
// 与 commonCheck 对称：先按服务级 Resource 上报，再按接口级 Resource 上报；
// 当业务回调通过 InvokeContext.SetInstance 回填了具体实例时，再追加一次实例级上报，
// 让"实例级熔断"用例和"服务级 / 接口级"用例共用同一套装饰器写法。
//
// 接口级 Resource 的构造规则同 buildMethodResource。
// 任一级上报失败立即返回错误，不阻塞已成功上报的级别。
//
// 参数 instance 为 nil 时跳过实例级上报，对存量调用方完全向后兼容。
func (h *DefaultInvokeHandler) commonReport(reqCtx *model.RequestContext, delay time.Duration, code string,
	retStatus model.RetStatus, instance model.Instance) error {
	svcRes, err := model.NewServiceResource(reqCtx.Callee, reqCtx.Caller)
	if err != nil {
		return err
	}
	resourceStat := &model.ResourceStat{
		Resource:  svcRes,
		RetCode:   code,
		Delay:     delay,
		RetStatus: retStatus,
	}
	if err := h.flow.Report(resourceStat); err != nil {
		return err
	}
	methodSvc, err := buildMethodResource(reqCtx)
	if err != nil {
		return err
	}
	if methodSvc != nil {
		resourceStat = &model.ResourceStat{
			Resource:  methodSvc,
			RetCode:   code,
			Delay:     delay,
			RetStatus: retStatus,
		}
		if err := h.flow.Report(resourceStat); err != nil {
			return err
		}
	}
	if instance == nil {
		return nil
	}
	// 实例级上报：所选协议优先取实例自身的 protocol，缺失时退化为 reqCtx.Protocol，
	// 再缺失时使用 "http" —— 与 InstanceResource.NewInstanceResource 的常用调用形态对齐。
	protocol := instance.GetProtocol()
	if protocol == "" {
		protocol = reqCtx.Protocol
	}
	if protocol == "" {
		protocol = "http"
	}
	insRes, err := model.NewInstanceResource(reqCtx.Callee, reqCtx.Caller,
		protocol, instance.GetHost(), instance.GetPort())
	if err != nil {
		return err
	}
	resourceStat = &model.ResourceStat{
		Resource:  insRes,
		RetCode:   code,
		Delay:     delay,
		RetStatus: retStatus,
	}
	return h.flow.Report(resourceStat)
}
