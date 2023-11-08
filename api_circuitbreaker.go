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

package polaris

import (
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

// NewCircuitBreakerAPI 获取 CircuitBreakerAPI
func NewCircuitBreakerAPI() (CircuitBreakerAPI, error) {
	rawAPI, err := api.NewCircuitBreakerAPI()
	if err != nil {
		return nil, err
	}
	return &circuitBreakerAPI{
		rawAPI: rawAPI,
	}, nil
}

// NewCircuitBreakerAPIByConfig 通过配置对象获取 CircuitBreakerAPI
func NewCircuitBreakerAPIByConfig(cfg config.Configuration) (CircuitBreakerAPI, error) {
	rawAPI, err := api.NewCircuitBreakerByConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &circuitBreakerAPI{
		rawAPI: rawAPI,
	}, nil
}

// NewCircuitBreakerAPIByFile 通过配置文件获取 CircuitBreakerAPI
func NewCircuitBreakerAPIByFile(path string) (CircuitBreakerAPI, error) {
	rawAPI, err := api.NewCircuitBreakerByFile(path)
	if err != nil {
		return nil, err
	}
	return &circuitBreakerAPI{
		rawAPI: rawAPI,
	}, nil
}

// NewCircuitBreakerAPIByContext 通过上下文对象获取 CircuitBreakerAPI
func NewCircuitBreakerAPIByContext(context api.SDKContext) CircuitBreakerAPI {
	rawAPI := api.NewCircuitBreakerByContext(context)
	return &circuitBreakerAPI{
		rawAPI: rawAPI,
	}
}

type circuitBreakerAPI struct {
	rawAPI api.CircuitBreakerAPI
}

// Check
func (c *circuitBreakerAPI) Check(res model.Resource) (*model.CheckResult, error) {
	return c.rawAPI.Check(res)
}

// Report
func (c *circuitBreakerAPI) Report(stat *model.ResourceStat) error {
	return c.rawAPI.Report(stat)
}

// MakeFunctionDecorator
func (c *circuitBreakerAPI) MakeFunctionDecorator(f model.CustomerFunction, reqCtx *api.RequestContext) model.DecoratorFunction {
	return c.rawAPI.MakeFunctionDecorator(f, reqCtx)
}

// MakeInvokeHandler
func (c *circuitBreakerAPI) MakeInvokeHandler(reqCtx *api.RequestContext) model.InvokeHandler {
	return c.rawAPI.MakeInvokeHandler(reqCtx)
}

func (c *circuitBreakerAPI) SDKContext() api.SDKContext {
	return c.rawAPI.SDKContext()
}

// Destroy the api is destroyed and cannot be called again
func (c *circuitBreakerAPI) Destroy() {
	c.rawAPI.SDKContext().Destroy()
}
