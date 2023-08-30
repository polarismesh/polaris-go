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

package ratelimiter

import (
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

// ServiceRateLimiter 服务限流处理插件接口
type ServiceRateLimiter interface {
	plugin.Plugin
	// InitQuota 初始化并创建令牌桶/漏桶
	// 主流程会在首次调用，以及规则对象变更的时候，调用该方法
	InitQuota(criteria *InitCriteria) QuotaBucket
}

// QuotaBucket 配额池
type QuotaBucket interface {
	// GetQuota 在令牌桶/漏桶中进行单个配额的划扣，并返回本次分配的结果
	GetQuota(curTimeMs int64, token uint32) *model.QuotaResponse
	// GetQuotaWithRelease 判断限流结果，并返回配额释放函数（对并发数限流、CPU自适应限流有用）
	GetQuotaWithRelease(curTimeMs int64, token uint32) (*model.QuotaResponse, func())
	// Release 释放配额（仅对于并发数限流有用）
	Release()
	// OnRemoteUpdate 远程配额更新
	OnRemoteUpdate(RemoteQuotaResult)
	// GetQuotaUsed 拉取本地使用配额情况以供上报
	GetQuotaUsed(curTimeMilli int64) UsageInfo
	// GetAmountInfos 获取规则的限流阈值信息
	GetAmountInfos() []AmountInfo
}

// init 初始化
func init() {
	plugin.RegisterPluginInterface(common.TypeRateLimiter, new(ServiceRateLimiter))
}
