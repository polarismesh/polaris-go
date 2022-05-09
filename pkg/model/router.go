/**
 * Tencent is pleased to support the open source community by making Polaris available.
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

import "time"

type ProcessRoutersRequest struct {
	// router plugins
	Routers []string
	// source service
	SourceService ServiceInfo
	// destination instances
	DstInstances ServiceInstances
	// invoke method
	Method string
	// 可选，单次查询超时时间，默认直接获取全局的超时配置
	// 用户总最大超时时间为(1+RetryCount) * Timeout
	Timeout *time.Duration
	// 可选，重试次数，默认直接获取全局的超时配置
	RetryCount *int
	// 应答，无需用户填充，由主流程进行填充
	response InstancesResponse
}

// GetResponse 获取应答对象
func (p *ProcessRoutersRequest) GetResponse() *InstancesResponse {
	return &p.response
}

// Validate 校验获取单个服务实例请求对象
func (p *ProcessRoutersRequest) Validate() error {
	if nil == p {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "ProcessRoutersRequest can not be nil")
	}
	if nil == p.DstInstances {
		return NewSDKError(
			ErrCodeAPIInvalidArgument, nil, "ProcessRoutersRequest.DstInstances can not be nil")
	}
	return nil
}

// 获取超时值指针
func (p *ProcessRoutersRequest) GetTimeoutPtr() *time.Duration {
	return p.Timeout
}

// 设置超时时间间隔
func (p *ProcessRoutersRequest) SetTimeout(duration time.Duration) {
	p.Timeout = &duration
}

// 获取重试次数指针
func (p *ProcessRoutersRequest) GetRetryCountPtr() *int {
	return p.RetryCount
}

// 设置重试次数
func (p *ProcessRoutersRequest) SetRetryCount(v int) {
	p.RetryCount = &v
}

type ProcessLoadBalanceRequest struct {
	// destination instances
	DstInstances ServiceInstances
	// load balance method
	LbPolicy string
	// hash key
	HashKey []byte
	// 可选，备份节点数
	// 对于一致性hash等有状态的负载均衡方式
	ReplicateCount int
	// 应答，无需用户填充，由主流程进行填充
	response InstancesResponse
}

// Validate 校验获取单个服务实例请求对象
func (p *ProcessLoadBalanceRequest) Validate() error {
	if nil == p {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "ProcessLoadBalanceRequest can not be nil")
	}
	if nil == p.DstInstances {
		return NewSDKError(
			ErrCodeAPIInvalidArgument, nil, "ProcessLoadBalanceRequest.DstInstances can not be nil")
	}
	return nil
}

// GetResponse 获取应答对象
func (p *ProcessLoadBalanceRequest) GetResponse() *InstancesResponse {
	return &p.response
}
