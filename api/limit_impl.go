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

// 限流API对象
type limitAPI struct {
	context SDKContext
}

//获取SDK上下文
func (c *limitAPI) SDKContext() SDKContext {
	return c.context
}

//获取限流配额
func (c *limitAPI) GetQuota(request QuotaRequest) (QuotaFuture, error) {
	if err := checkAvailable(c); nil != err {
		return nil, err
	}
	mRequest := request.(*model.QuotaRequestImpl)
	if err := mRequest.Validate(); nil != err {
		return nil, err
	}
	return c.context.GetEngine().AsyncGetQuota(mRequest)
}

//销毁API
func (c *limitAPI) Destroy() {
	if nil != c.context {
		c.context.Destroy()
	}
}

//通过以默认域名为埋点server的默认配置创建LimitAPI
func newLimitAPI() (LimitAPI, error) {
	return newLimitAPIByConfig(config.NewDefaultConfigurationWithDomain())
}

//newLimitAPIByConfig 通过配置对象创建SDK LimitAPI对象
func newLimitAPIByConfig(cfg config.Configuration) (LimitAPI, error) {
	context, err := InitContextByConfig(cfg)
	if nil != err {
		return nil, err
	}
	return &limitAPI{context}, nil
}

//newLimitAPIByContext 通过上下文创建SDK LimitAPI对象
func newLimitAPIByContext(context SDKContext) LimitAPI {
	return &limitAPI{context}
}

//newLimitAPIByFile 通过配置文件创建SDK LimitAPI对象
func newLimitAPIByFile(path string) (LimitAPI, error) {
	context, err := InitContextByFile(path)
	if nil != err {
		return nil, err
	}
	return &limitAPI{context: context}, nil
}
