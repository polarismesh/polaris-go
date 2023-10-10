package flow

import (
	"context"
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

func newCircuitBreakerFlow(e *Engine) (*CircuitBreakerFlow, error) {
	return nil, nil
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
	)

	defer func() {
		delay := time.Since(start)
		if err != nil {
			rspCtx := &model.ResponseContext{
				Duration: delay,
				Err:      err,
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

	ret, err = df.customerFunc(ctx, args)
	if err != nil {
		return nil, nil, err
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
		return &model.CallAborted{
			Rule:     check.RuleName,
			Fallback: check.FallbackInfo,
		}, nil
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
	h.commonReport(h.reqCtx, delay, code, retStatus)
}

func (h *DefaultInvokeHandler) OnError(respCtx *model.ResponseContext) {

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
