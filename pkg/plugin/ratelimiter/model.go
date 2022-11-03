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
	"time"

	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
)

// InitCriteria 配额查询相关的信息
type InitCriteria struct {
	DstRule   *namingpb.Rule
	WindowKey string
}

// AmountDuration 单个配额
type AmountDuration struct {
	AmountUsed    uint32
	ValidDuration time.Duration
}

// RemoteQuotaResult 远程下发配额
type RemoteQuotaResult struct {
	Left            int64
	ClientCount     uint32
	ServerTimeMilli int64
	DurationMill    int64
	ClientTimeMilli int64
}

// UsageInfo 配额使用信息
type UsageInfo struct {
	// 配额使用时间
	CurTimeMilli int64
	// 配额使用详情
	Passed map[int64]uint32
	// 限流情况
	Limited map[int64]uint32
}

type AmountInfo struct {
	// 限流时间段，单位秒
	ValidDuration uint32
	// 最大配额数
	MaxAmount uint32
}
