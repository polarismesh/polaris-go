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
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// QuotaRequest 配额查询请求
type QuotaRequest interface {
	// SetNamespace 设置命名空间
	SetNamespace(string)
	// SetService 设置服务名
	SetService(string)
	// SetCluster 设置集群名
	SetCluster(string)
	// SetLabels 设置业务标签信息
	SetLabels(map[string]string)
	// SetTimeout 设置单次请求超时时间
	SetTimeout(timeout time.Duration)
	// SetRetryCount 设置最大重试次数
	SetRetryCount(retryCount int)
}

// NewQuotaRequest 创建配额查询请求
func NewQuotaRequest() QuotaRequest {
	return &model.QuotaRequestImpl{}
}

// QuotaFuture 实时/延时分配future
type QuotaFuture interface {
	// Done 标识分配是否结束
	Done() <-chan struct{}
	// Get 获取分配结果
	Get() *model.QuotaResponse
	// Release 释放资源，仅用于并发数限流的场景
	Release()
}

const (
	// QuotaResultOk 限流状态值
	QuotaResultOk = model.QuotaResultOk
	// QuotaResultLimited 限流结果
	QuotaResultLimited = model.QuotaResultLimited
)

// LimitAPI 限流相关的API相关接口
type LimitAPI interface {
	SDKOwner
	// GetQuota 获取限流配额，一次接口只获取一个配额
	GetQuota(request QuotaRequest) (QuotaFuture, error)
	// Destroy 销毁API，销毁后无法再进行调用
	Destroy()
}

var (
	// NewLimitAPI 通过以默认域名为埋点server的默认配置创建LimitAPI
	NewLimitAPI = newLimitAPI
	// NewLimitAPIByConfig 通过配置对象创建LimitAPI
	NewLimitAPIByConfig = newLimitAPIByConfig
	// NewLimitAPIByContext 通过sdkContext创建LimitAPI
	NewLimitAPIByContext = newLimitAPIByContext
	// NewLimitAPIByFile 通过配置文件创建SDK LimitAPI对象
	NewLimitAPIByFile = newLimitAPIByFile
)
