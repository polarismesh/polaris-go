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

// ProcessRoutersRequest the input request parameters for RouterAPI.ProcessRouters
type ProcessRoutersRequest struct {
	// Routers indicate the router plugins, optional.
	// If empty, it will use the default routers config by console or sdk.
	Routers []string
	// SourceService indicate the source service to match the route rule, optional.
	SourceService ServiceInfo
	// Arguments traffic labels
	Arguments []Argument
	// DstInstances indicate the destination instances resolved from discovery, required.
	// Two implementations to ServiceInstances:
	// 1. InstancesResponse, returned from ConsumerAPI.GetAllInstances.
	// 2. DefaultServiceInstances, for user to construct manually.
	DstInstances ServiceInstances
	// Method indicate the invoke method to match the route rule, optional.
	Method string
	// Timeout indicate the max timeout for single remote request, optional.
	// Total request timeout is (1+RetryCount) * Timeout, default is 1s
	Timeout *time.Duration
	// RetryCount indicate the request retry count, default is 0
	RetryCount *int
	// response, internal data, not for user to set.
	response InstancesResponse
}

// GetResponse get the response object
func (p *ProcessRoutersRequest) GetResponse() *InstancesResponse {
	return &p.response
}

// AddArgument add one traffic label
func (p *ProcessRoutersRequest) AddArguments(arg ...Argument) {
	if len(p.Arguments) == 0 {
		p.Arguments = make([]Argument, 0, 4)
	}
	p.Arguments = append(p.Arguments, arg...)
}

// Validate validate the request object
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

// GetTimeoutPtr get the timeout pointer
func (p *ProcessRoutersRequest) GetTimeoutPtr() *time.Duration {
	return p.Timeout
}

// SetTimeout set the timeout value to request
func (p *ProcessRoutersRequest) SetTimeout(duration time.Duration) {
	p.Timeout = &duration
}

// GetRetryCountPtr get the retry count pointer
func (p *ProcessRoutersRequest) GetRetryCountPtr() *int {
	return p.RetryCount
}

// SetRetryCount set the retry count to request
func (p *ProcessRoutersRequest) SetRetryCount(v int) {
	p.RetryCount = &v
}

// ProcessLoadBalanceRequest the input request parameters for RouterAPI.ProcessLoadBalance
type ProcessLoadBalanceRequest struct {
	// DstInstances indicate the destination instances resolved from discovery, required.
	// Two implementations to ServiceInstances:
	// 1. InstancesResponse, returned from ConsumerAPI.GetAllInstances.
	// 2. DefaultServiceInstances, for user to construct manually.
	DstInstances ServiceInstances
	// LbPolicy indicate the load balancer plugin, optional.
	// If empty, it will use the default load balancer config by console or sdk.
	LbPolicy string
	// HashKey indicate the hash key to do load balance, optional.
	HashKey []byte
	// ReplicateCount indicate the sibling count in consist hash ring, optional.
	ReplicateCount int
	// response, internal data, not for user to set.
	response InstancesResponse
}

// Validate validate the request object
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

// GetResponse get the response object
func (p *ProcessLoadBalanceRequest) GetResponse() *InstancesResponse {
	return &p.response
}
