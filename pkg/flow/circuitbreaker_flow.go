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

	"github.com/polarismesh/polaris-go/pkg/log"
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

func circuitBreakerStatusToResult(breakerStatus model.CircuitBreakerStatus) *model.CheckResult {
	status := breakerStatus.GetStatus()
	if status == model.Open {
		return &model.CheckResult{
			Pass:         false,
			RuleName:     breakerStatus.GetCircuitBreaker(),
			FallbackInfo: breakerStatus.GetFallbackInfo(),
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
	aborted, err := invoke.AcquirePermission()
	if err != nil {
		return nil, nil, err
	}
	if aborted != nil {
		return nil, aborted, nil
	}
	var (
		ret   interface{}
		start = time.Now()
		cerr  error
	)

	defer func() {
		delay := time.Since(start)
		if cerr != nil {
			rspCtx := &model.ResponseContext{
				Duration: delay,
				Err:      cerr,
			}
			invoke.OnError(rspCtx)
		} else {
			rspCtx := &model.ResponseContext{
				Duration: delay,
				Result:   ret,
			}
			invoke.OnError(rspCtx)
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

func (h *DefaultInvokeHandler) AcquirePermission() (*model.CallAborted, error) {
	check, err := h.commonCheck(h.reqCtx)
	if err != nil {
		return nil, err
	}
	if check != nil {
		return model.NewCallAborted(model.ErrorCallAborted, check.RuleName, check.FallbackInfo), nil
	}
	return nil, nil
}

func (h *DefaultInvokeHandler) OnSuccess(respCtx *model.ResponseContext) {
	delay := respCtx.Duration
	code := "0"
	retStatus := model.RetUnknown
	if h.reqCtx.CodeConvert != nil {
		code = h.reqCtx.CodeConvert.OnSuccess(respCtx.Result)
	}
	if err := h.commonReport(h.reqCtx, delay, code, retStatus); err != nil {
		log.GetBaseLogger().Errorf("DefaultInvokeHandler.commonReport in OnSuccess: %v", err)
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
	if err := h.commonReport(h.reqCtx, delay, code, retStatus); err != nil {
		log.GetBaseLogger().Errorf("DefaultInvokeHandler.commonReport in OnError: %v", err)
	}
}

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
	if reqCtx.Method != "" {
		methodSvc, err := model.NewMethodResource(reqCtx.Callee, reqCtx.Caller, reqCtx.Method)
		if err != nil {
			return nil, err
		}
		result, err := h.flow.Check(methodSvc)
		if err != nil {
			return nil, err
		}
		if !result.Pass {
			return result, nil
		}
	}
	return nil, nil
}

func (h *DefaultInvokeHandler) commonReport(reqCtx *model.RequestContext, delay time.Duration, code string,
	retStatus model.RetStatus) error {
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
	if reqCtx.Method != "" {
		methodSvc, err := model.NewMethodResource(reqCtx.Callee, reqCtx.Caller, reqCtx.Method)
		if err != nil {
			return err
		}
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
	return nil
}
