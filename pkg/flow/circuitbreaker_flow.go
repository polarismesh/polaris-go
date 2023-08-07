package flow

import (
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
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
func (e *Engine) MakeFunctionDecorator(reqCtx *model.RequestContext) model.CustomerFunction {
	return e.circuitBreakerFlow.MakeFunctionDecorator(reqCtx)
}

// MakeInvokeHandler
func (e *Engine) MakeInvokeHandler(reqCtx *model.RequestContext) model.InvokeHandler {
	return e.circuitBreakerFlow.MakeInvokeHandler(reqCtx)
}

type CircuitBreakerFlow struct {
	engine *Engine
}

func (e *CircuitBreakerFlow) Check(resource model.Resource) (*model.CheckResult, error) {

}

func (e *CircuitBreakerFlow) Report(reportStat *model.ResourceStat) error {

}

func (e *CircuitBreakerFlow) MakeFunctionDecorator(reqCtx *model.RequestContext) model.CustomerFunction {

}

func (e *CircuitBreakerFlow) MakeInvokeHandler(reqCtx *model.RequestContext) model.InvokeHandler {

}

type DefaultFunctionalDecorator struct {
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
