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
	"github.com/polarismesh/polaris-go/pkg/model"
	_ "github.com/polarismesh/polaris-go/pkg/plugin/register"
)

type circuitBreakerAPI struct {
	context SDKContext
}

// SDKContext 获取SDK上下文
func (c *circuitBreakerAPI) SDKContext() SDKContext {
	return c.context
}

func (c *circuitBreakerAPI) Check(resource model.Resource)  (*model.CheckResult, error) {
	return c.context.GetEngine().Check(resource)
}

func (c *circuitBreakerAPI) Report(reportStat *model.ResourceStat)  error {
	return c.context.GetEngine().Report(reportStat)
}

func (c *circuitBreakerAPI) MakeFunctionDecorator(f model.CustomerFunction, reqCtx *RequestContext) model.DecoratorFunction {
	return c.context.GetEngine().MakeFunctionDecorator(f, &reqCtx.RequestContext)
}

func (c *circuitBreakerAPI) MakeInvokeHandler(reqCtx *RequestContext) model.InvokeHandler {
	return c.context.GetEngine().MakeInvokeHandler(&reqCtx.RequestContext)
}
