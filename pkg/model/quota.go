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
	"sync"
	"time"

	"github.com/hashicorp/go-multierror"
)

// QuotaRequestImpl 配额获取的请求.
type QuotaRequestImpl struct {
	// 必选，命名空间
	namespace string
	// 必选，服务名
	service string
	// 可选，集群metadata信息
	cluster string
	// 可选，业务标签信息
	labels map[string]string
	// 可选，单次查询超时时间，默认直接获取全局的超时配置
	// 用户总最大超时时间为(1+RetryCount) * Timeout
	Timeout *time.Duration
	// 可选，重试次数，默认直接获取全局的超时配置
	RetryCount *int
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

// GetCluster 获取集群.
func (q *QuotaRequestImpl) GetCluster() string {
	return q.cluster
}

// SetCluster 设置集群.
func (q *QuotaRequestImpl) SetCluster(cluster string) {
	q.cluster = cluster
}

// SetLabels 设置业务标签.
func (q *QuotaRequestImpl) SetLabels(labels map[string]string) {
	q.labels = labels
}

// GetLabels 获取业务标签.
func (q *QuotaRequestImpl) GetLabels() map[string]string {
	return q.labels
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
}

// QuotaAllocator 配额分配器，执行配额分配及回收.
type QuotaAllocator interface {
	// Allocate 执行配额分配操作
	Allocate() *QuotaResponse
	// Release 执行配额回收操作
	Release()
}

type quotaFutureOption func(f *QuotaFutureImpl)

// WithQuotaFutureReq .
func WithQuotaFutureReq(req *QuotaRequestImpl) quotaFutureOption {
	return func(f *QuotaFutureImpl) {
		f.req = req
	}
}

// WithQuotaFutureResp .Response.
func WithQuotaFutureResp(resp *QuotaResponse) quotaFutureOption {
	return func(f *QuotaFutureImpl) {
		f.resp = resp
	}
}

// WithQuotaFutureDeadline .deadline.
func WithQuotaFutureDeadline(deadline time.Time) quotaFutureOption {
	return func(f *QuotaFutureImpl) {
		var cancel context.CancelFunc
		f.deadlineCtx, cancel = context.WithDeadline(context.Background(), deadline)
		f.cancel = cancel
	}
}

// WithQuotaFutureQuotaAllocator .quotaAllocator.
func WithQuotaFutureQuotaAllocator(allocator QuotaAllocator) quotaFutureOption {
	return func(f *QuotaFutureImpl) {
		f.allocator = allocator
	}
}

// WithQuotaFutureHooks .hooks.
func WithQuotaFutureHooks(hooks ...finishHook) quotaFutureOption {
	return func(f *QuotaFutureImpl) {
		f.hooks = hooks
	}
}

// NewQuotaFuture 创建分配future 可以直接传入.
func NewQuotaFuture(options ...quotaFutureOption) *QuotaFutureImpl {
	future := &QuotaFutureImpl{}

	for i := range options {
		options[i](future)
	}

	if nil != future.resp {
		// 已经有结果，则直接结束context
		future.cancel()
		future.cancel = nil
	}
	if nil == future.allocator {
		future.released = true
	}
	return future
}

type finishHook func(req *QuotaRequestImpl, res *QuotaResponse)

// QuotaFutureImpl 异步获取配额的future.
type QuotaFutureImpl struct {
	mutex       sync.Mutex
	req         *QuotaRequestImpl
	resp        *QuotaResponse
	released    bool
	deadlineCtx context.Context
	allocator   QuotaAllocator
	cancel      context.CancelFunc
	hooks       []finishHook
}

// Done 分配是否结束.
func (q *QuotaFutureImpl) Done() <-chan struct{} {
	return q.deadlineCtx.Done()
}

// Get 获取分配结果.
func (q *QuotaFutureImpl) Get() *QuotaResponse {
	if nil == q {
		return nil
	}
	if nil != q.resp {
		return q.resp
	}
	q.mutex.Lock()
	defer q.mutex.Unlock()
	if nil != q.resp {
		return q.resp
	}
	<-q.deadlineCtx.Done()
	q.resp = q.allocator.Allocate()
	if q.cancel != nil {
		q.cancel()
	}

	if q.hooks != nil {
		for i := range q.hooks {
			q.hooks[i](q.req, q.resp)
		}
	}

	return q.resp
}

// Release 释放资源，仅用于并发数限流的场景.
func (q *QuotaFutureImpl) Release() {
	if q.released {
		return
	}
	q.mutex.Lock()
	defer q.mutex.Unlock()
	if q.released {
		return
	}
	q.allocator.Release()
	q.released = true
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
