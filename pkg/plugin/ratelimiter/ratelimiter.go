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
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"time"
)

//配额查询相关的信息
type InitCriteria struct {
	DstRule    *namingpb.Rule
}

//单个配额
type AmountDuration struct {
	AmountUsed    uint32
	ValidDuration time.Duration
}

//配额池
type QuotaBucket interface {
	//在令牌桶/漏桶中进行单个配额的划扣，并返回本次分配的结果
	GetQuota() (*QuotaResult, error)
	//释放配额（仅对于并发数限流有用）
	Release()
}

//配额分配的结果
type QuotaResult struct {
	//分配的结果码
	Code model.QuotaResultCode
	//分配的提示信息
	Info string
	//排队时间，标识多长时间后可以有新配额供应
	QueueTime time.Duration
}

// 服务限流处理插件接口
type ServiceRateLimiter interface {
	plugin.Plugin
	//初始化并创建令牌桶/漏桶
	//主流程会在首次调用，以及规则对象变更的时候，调用该方法
	InitQuota(criteria *InitCriteria) (QuotaBucket, error)
}

//初始化
func init() {
	plugin.RegisterPluginInterface(common.TypeRateLimiter, new(ServiceRateLimiter))
}
