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
)

type limitAPI struct {
	rawAPI api.LimitAPI
}

// SDKContext 获取SDK上下文
func (c *limitAPI) SDKContext() api.SDKContext {
	return c.rawAPI.SDKContext()
}

// GetQuota 获取限流配额，一次接口只获取一个配额
func (c *limitAPI) GetQuota(request QuotaRequest) (QuotaFuture, error) {
	return c.rawAPI.GetQuota(request)
}

// Destroy 销毁API，销毁后无法再进行调用
func (c *limitAPI) Destroy() {
	c.rawAPI.Destroy()
}

// NewLimitAPI 通过以默认域名为埋点server的默认配置创建LimitAPI
func NewLimitAPI() (LimitAPI, error) {
	l, err := api.NewLimitAPI()
	if err != nil {
		return nil, err
	}
	return &limitAPI{rawAPI: l}, nil
}

// NewLimitAPIByConfig 通过配置对象创建SDK LimitAPI对象
func NewLimitAPIByConfig(cfg config.Configuration) (LimitAPI, error) {
	l, err := api.NewLimitAPIByConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &limitAPI{rawAPI: l}, nil
}

// NewLimitAPIByContext 通过上下文创建SDK LimitAPI对象
func NewLimitAPIByContext(context api.SDKContext) LimitAPI {
	l := api.NewLimitAPIByContext(context)
	return &limitAPI{rawAPI: l}
}

// NewLimitAPIByFile 通过配置文件创建SDK LimitAPI对象
func NewLimitAPIByFile(path string) (LimitAPI, error) {
	l, err := api.NewLimitAPIByFile(path)
	if err != nil {
		return nil, err
	}
	return &limitAPI{rawAPI: l}, nil
}

// NewLimitAPIByAddress 通过地址创建SDK LimitAPI对象
func NewLimitAPIByAddress(address ...string) (LimitAPI, error) {
	l, err := api.NewLimitAPIByAddress(address...)
	if err != nil {
		return nil, err
	}
	return &limitAPI{rawAPI: l}, nil
}
