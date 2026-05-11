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
	"time"
)

// AuthCode 鉴权结果码（API 层暴露）
type AuthCode int

const (
	// AuthResultOk 鉴权通过
	AuthResultOk AuthCode = iota
	// AuthResultForbidden 鉴权拒绝
	AuthResultForbidden
)

// AuthenticateRequest 鉴权请求
type AuthenticateRequest struct {
	// FlowID 可选，流水号，用于跟踪用户的请求，默认 0
	FlowID uint64
	// Namespace 必选，被调命名空间
	Namespace string
	// Service 必选，被调服务名
	Service string
	// Method 可选，调用方法（HTTP method 或 gRPC method）
	Method string
	// Path 可选，调用路径（HTTP path）
	Path string
	// Protocol 可选，调用协议（如 HTTP / gRPC）
	Protocol string
	// SourceService 可选，主调服务信息（含 Metadata）
	SourceService *ServiceInfo
	// Arguments 可选，流量标签（与 ProcessRouters 等一致的 Argument 体系）
	Arguments []Argument
	// Timeout 可选，本次鉴权调用的最大超时
	Timeout *time.Duration
	// RetryCount 可选，重试次数
	RetryCount *int
}

// GetService 获取被调服务名
func (r *AuthenticateRequest) GetService() string {
	return r.Service
}

// GetNamespace 获取被调命名空间
func (r *AuthenticateRequest) GetNamespace() string {
	return r.Namespace
}

// GetMetadata 获取元数据信息（鉴权请求本身不携带元数据，按接口约定返回 nil）
func (r *AuthenticateRequest) GetMetadata() map[string]string {
	return nil
}

// SetTimeout 设置超时时间
func (r *AuthenticateRequest) SetTimeout(duration time.Duration) {
	r.Timeout = ToDurationPtr(duration)
}

// SetRetryCount 设置重试次数
func (r *AuthenticateRequest) SetRetryCount(retryCount int) {
	r.RetryCount = &retryCount
}

// AddArgument 添加流量标签参数
func (r *AuthenticateRequest) AddArgument(arg Argument) {
	r.Arguments = append(r.Arguments, arg)
}

// Validate 校验鉴权请求参数
func (r *AuthenticateRequest) Validate() error {
	if nil == r {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "AuthenticateRequest can not be nil")
	}
	if err := validateServiceMetadata("AuthenticateRequest", r); err != nil {
		return NewSDKError(ErrCodeAPIInvalidArgument, err, "fail to validate AuthenticateRequest")
	}
	return nil
}

// AuthenticateResponse 鉴权应答
type AuthenticateResponse struct {
	// Code 鉴权结果码
	Code AuthCode
	// Info 鉴权信息（拒绝原因等）
	Info string
}

// GetCode 获取鉴权结果码
func (r *AuthenticateResponse) GetCode() AuthCode {
	return r.Code
}

// GetInfo 获取鉴权信息
func (r *AuthenticateResponse) GetInfo() string {
	return r.Info
}

// IsAllowed 是否鉴权通过
func (r *AuthenticateResponse) IsAllowed() bool {
	return r != nil && r.Code == AuthResultOk
}
