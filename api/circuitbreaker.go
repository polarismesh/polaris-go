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

package api

import (
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

type CircuitBreakerAPI interface {
	SDKOwner
	Check(model.Resource) (*model.CheckResult, error)
	Report(*model.ResourceStat) error
	MakeFunctionDecorator(*RequestContext) model.CustomerFunction
	MakeInvokeHandler(*RequestContext) model.InvokeHandler
}

type ResultToErrorCode interface {
	model.ResultToErrorCode
}

type RequestContext struct {
	model.RequestContext
}

type ResponseContext struct {
	model.ResponseContext
}

var (
	// NewConsumerAPI 通过以默认域名为埋点server的默认配置创建 CircuitBreakerAPI
	NewCircuitBreakerAPI = newCircuitBreakerAPI
	// NewCircuitBreakerByFile 通过配置文件创建SDK CircuitBreakerAPI 对象
	NewCircuitBreakerByFile = newCircuitBreakerAPIByFile
	// NewCircuitBreakerByConfig 通过配置对象创建SDK CircuitBreakerAPI 对象
	NewCircuitBreakerByConfig = newCircuitBreakerAPIByConfig
	// NewCircuitBreakerByContext 通过上下文创建SDK CircuitBreakerAPI 对象
	NewCircuitBreakerByContext = newCircuitBreakerAPIByContext
)

// 通过以默认域名为埋点server的默认配置创建ConsumerAPI
func newCircuitBreakerAPI() (CircuitBreakerAPI, error) {
	return newCircuitBreakerAPIByConfig(config.NewDefaultConfigurationWithDomain())
}

// newCircuitBreakerAPIByFile 通过配置文件创建SDK ConsumerAPI对象
func newCircuitBreakerAPIByFile(path string) (CircuitBreakerAPI, error) {
	context, err := InitContextByFile(path)
	if err != nil {
		return nil, err
	}
	return &circuitBreakerAPI{context}, nil
}

// newCircuitBreakerAPIByConfig 通过配置对象创建SDK ConsumerAPI对象
func newCircuitBreakerAPIByConfig(cfg config.Configuration) (CircuitBreakerAPI, error) {
	context, err := InitContextByConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &circuitBreakerAPI{context}, nil
}

// newCircuitBreakerAPIByContext 通过上下文创建SDK ConsumerAPI对象
func newCircuitBreakerAPIByContext(context SDKContext) CircuitBreakerAPI {
	return &circuitBreakerAPI{context}
}
