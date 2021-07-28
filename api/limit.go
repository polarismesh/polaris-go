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
	"time"
)

//配额查询请求
type QuotaRequest interface {
	//设置命名空间
	SetNamespace(string)
	//设置服务名
	SetService(string)
	//设置集群名
	SetCluster(string)
	//设置业务标签信息
	SetLabels(map[string]string)
	//设置单次请求超时时间
	SetTimeout(timeout time.Duration)
	//设置最大重试次数
	SetRetryCount(retryCount int)
}

//创建配额查询请求
func NewQuotaRequest() QuotaRequest {
	return &model.QuotaRequestImpl{}
}

//实时/延时分配future
type QuotaFuture interface {
	//标识分配是否结束
	Done() <-chan struct{}
	//获取分配结果
	Get() *model.QuotaResponse
	//释放资源，仅用于并发数限流的场景
	Release()
}

const (
	QuotaResultOk      = model.QuotaResultOk
	QuotaResultLimited = model.QuotaResultLimited
)

// 限流相关的API相关接口
type LimitAPI interface {
	SDKOwner
	// 获取限流配额，一次接口只获取一个配额
	GetQuota(request QuotaRequest) (QuotaFuture, error)
	//销毁API，销毁后无法再进行调用
	Destroy()
}

var (
	//通过以默认域名为埋点server的默认配置创建LimitAPI
	NewLimitAPI = newLimitAPI
	//通过配置对象创建LimitAPI
	NewLimitAPIByConfig = newLimitAPIByConfig
	//通过sdkContext创建LimitAPI
	NewLimitAPIByContext = newLimitAPIByContext
	//通过配置文件创建SDK LimitAPI对象
	NewLimitAPIByFile = newLimitAPIByFile
)
