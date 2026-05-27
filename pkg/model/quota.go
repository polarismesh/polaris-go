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
	"fmt"
	"time"

	"github.com/hashicorp/go-multierror"
	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"
)

// QuotaRequestImpl 配额获取的请求.
type QuotaRequestImpl struct {
	// 必选，命名空间
	namespace string
	// 必选，服务名
	service string
	// 可选，方法
	method string
	// 可选，业务标签信息
	arguments []Argument
	// 可选，单次查询超时时间，默认直接获取全局的超时配置
	// 用户总最大超时时间为(1+RetryCount) * Timeout
	Timeout *time.Duration
	// 可选，重试次数，默认直接获取全局的超时配置
	RetryCount *int
	// 可选，获取的配额数
	Token uint32
}

// GetService 获取服务名.
func (q *QuotaRequestImpl) GetService() string {
	return q.service
}

// SetService 设置服务名称.
func (q *QuotaRequestImpl) SetService(svc string) {
	q.service = svc
}

// GetNamespace 获取命名空间.
func (q *QuotaRequestImpl) GetNamespace() string {
	return q.namespace
}

// SetNamespace 设置命名空间.
func (q *QuotaRequestImpl) SetNamespace(namespace string) {
	q.namespace = namespace
}

// SetMethod set method
func (q *QuotaRequestImpl) SetMethod(method string) {
	q.method = method
}

// SetToken set token
func (q *QuotaRequestImpl) SetToken(token uint32) {
	q.Token = token
}

// GetToken get token
func (q *QuotaRequestImpl) GetToken() uint32 {
	return q.Token
}

// SetLabels 设置业务标签.
func (q *QuotaRequestImpl) SetLabels(labels map[string]string) {
	if len(labels) == 0 {
		return
	}
	for labelKey, labelValue := range labels {
		q.arguments = append(q.arguments, BuildArgumentFromLabel(labelKey, labelValue))
	}
}

// GetLabels 获取业务标签.
func (q *QuotaRequestImpl) GetLabels() map[string]string {
	labels := make(map[string]string, len(q.arguments))
	for _, argument := range q.arguments {
		argument.ToLabels(labels)
	}
	return labels
}

func (q *QuotaRequestImpl) GetMethod() string {
	return q.method
}

// AddArgument add the match argument
func (q *QuotaRequestImpl) AddArgument(argument Argument) {
	q.arguments = append(q.arguments, argument)
}

func (q *QuotaRequestImpl) Arguments() []Argument {
	return q.arguments
}

// SetTimeout 设置单次查询超时时间.
func (q *QuotaRequestImpl) SetTimeout(timeout time.Duration) {
	q.Timeout = &timeout
}

// SetRetryCount 设置重试次数.
func (q *QuotaRequestImpl) SetRetryCount(retryCount int) {
	q.RetryCount = &retryCount
}

// GetTimeoutPtr 获取超时值指针.
func (q *QuotaRequestImpl) GetTimeoutPtr() *time.Duration {
	return q.Timeout
}

// GetRetryCountPtr 获取重试次数指针.
func (q *QuotaRequestImpl) GetRetryCountPtr() *int {
	return q.RetryCount
}

// Validate 校验.
func (q *QuotaRequestImpl) Validate() error {
	if nil == q {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "QuotaRequestImpl can not be nil")
	}
	var errs error
	if len(q.GetService()) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("QuotaRequestImpl: service is empty"))
	}
	if len(q.GetNamespace()) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("QuotaRequestImpl: namespace is empty"))
	}
	return errs
}

// QuotaResultCode 应答码.
type QuotaResultCode int

const (
	// QuotaResultOk 应答码：成功.
	QuotaResultOk QuotaResultCode = 0
	// QuotaResultLimited 应答码：限制.
	QuotaResultLimited QuotaResultCode = -1
)

// QuotaResponse 配额查询应答.
type QuotaResponse struct {
	// 配额分配的返回码
	Code QuotaResultCode
	// 配额分配的结果提示信息
	Info string
	// 需要等待的时间段
	WaitMs int64
	// ActiveRule 命中的限流规则；仅在 Code == QuotaResultLimited 时填充。
	// 通过 GetActiveRule().GetCustomResponse().GetBody() 可以读取规则中配置的自定义返回内容；
	// 也可通过 GetActiveRuleName() / GetActiveRuleId() 获取规则元信息，便于业务侧自定义返回。
	ActiveRule *apitraffic.Rule
	// releaseFunc release回调链，仅用于并发数限流场景，由 Bucket 在 GetQuota 通过时注入
	releaseFunc []func()
}

// GetActiveRule 获取本次限流命中的规则；仅在被限流（Code == QuotaResultLimited）时返回非 nil。
// receiver 为 nil 时安全返回 nil，便于业务侧链式调用 GetActiveRule().GetCustomResponse().GetBody().
func (q *QuotaResponse) GetActiveRule() *apitraffic.Rule {
	if q == nil {
		return nil
	}
	return q.ActiveRule
}

// GetActiveRuleName 获取本次限流命中规则的名称；非限流场景或规则未命名时返回空串。
// receiver 或 ActiveRule 为 nil 时安全返回空串。
func (q *QuotaResponse) GetActiveRuleName() string {
	if q == nil || q.ActiveRule == nil {
		return ""
	}
	return q.ActiveRule.GetName().GetValue()
}

// GetActiveRuleId 获取本次限流命中规则的 ID；非限流场景或规则无 ID 时返回空串。
// receiver 或 ActiveRule 为 nil 时安全返回空串。
func (q *QuotaResponse) GetActiveRuleId() string {
	if q == nil || q.ActiveRule == nil {
		return ""
	}
	return q.ActiveRule.GetId().GetValue()
}

// AddRelease 注册释放回调，并发数限流场景下由 Bucket 注入，请求完成后由 QuotaFutureImpl.Release 触发执行.
func (q *QuotaResponse) AddRelease(fn func()) {
	if fn == nil {
		return
	}
	q.releaseFunc = append(q.releaseFunc, fn)
}

// GetReleaseFuncs 获取已注册的 release 回调列表，仅供框架内部合并多 window 的回调使用.
func (q *QuotaResponse) GetReleaseFuncs() []func() {
	return q.releaseFunc
}

// QuotaFutureImpl 异步获取配额的future.
type QuotaFutureImpl struct {
	resp        *QuotaResponse
	deadlineCtx context.Context
	cancel      context.CancelFunc
}

func QuotaFutureWithResponse(resp *QuotaResponse) *QuotaFutureImpl {
	var deadlineCtx context.Context
	var cancel context.CancelFunc
	if resp.WaitMs > 0 {
		deadlineCtx, cancel = context.WithTimeout(context.Background(), time.Duration(resp.WaitMs)*time.Millisecond)
	}
	return &QuotaFutureImpl{
		resp: resp, deadlineCtx: deadlineCtx, cancel: cancel}
}

// Done 分配是否结束.
func (q *QuotaFutureImpl) Done() <-chan struct{} {
	if nil != q.deadlineCtx {
		return nil
	}
	return q.deadlineCtx.Done()
}

func (q *QuotaFutureImpl) GetImmediately() *QuotaResponse {
	return q.resp
}

// Get 获取分配结果.
//
// 当 resp.WaitMs > 0（unirate 排队场景）时，本方法会先阻塞等待 deadlineCtx 超时（等价于 sleep waitMs），
// 然后返回原始 resp——业务侧无需再次 sleep，直接处理后续逻辑即可.
//
// 返回的 resp.WaitMs 保留实际等待过的毫秒数（仅用于观测 / 上报指标），不再被清零；
// 业务侧不要把它当作"还需等待"的剩余时间再次 sleep——SDK 已经替你等过了.
func (q *QuotaFutureImpl) Get() *QuotaResponse {
	if nil != q.deadlineCtx {
		<-q.deadlineCtx.Done()
	}
	return q.resp
}

// Release 释放资源，仅用于并发数限流的场景.
// 调用方在请求完成后必须调用此方法归还并发配额；对 QPS 限流场景为空操作.
// 重复调用安全：首次执行后清空回调链，后续调用不会引起计数错乱.
func (q *QuotaFutureImpl) Release() {
	if q.resp == nil || len(q.resp.releaseFunc) == 0 {
		return
	}
	for _, fn := range q.resp.releaseFunc {
		fn()
	}
	q.resp.releaseFunc = nil
}

const (
	// RateLimitLocal 在本地限流.
	RateLimitLocal = "local"
	// RateLimitGlobal 在全局限流.
	RateLimitGlobal = "global"
)

// ConfigMode 配置模式.
type ConfigMode int

const (
	// ConfigQuotaLocalMode 在本地配置.
	ConfigQuotaLocalMode ConfigMode = 0
	// ConfigQuotaGlobalMode 在全局配置.
	ConfigQuotaGlobalMode ConfigMode = 1
)
