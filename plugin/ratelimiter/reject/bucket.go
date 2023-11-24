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

package reject

import (
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/ratelimiter"
)

type QuotaBucketReject struct {
	bucket *RemoteAwareQpsBucket
}

// GetQuota 在令牌桶/漏桶中进行单个配额的划扣，并返回本次分配的结果
func (q *QuotaBucketReject) GetQuota(curTimeMs int64, token uint32) *model.QuotaResponse {
	return q.bucket.Allocate(curTimeMs, token)
}

// Release 释放配额（仅对于并发数限流有用）
func (q *QuotaBucketReject) Release() {
	q.bucket.Release()
}

// OnRemoteUpdate 远程配额更新
func (q *QuotaBucketReject) OnRemoteUpdate(remoteQuota ratelimiter.RemoteQuotaResult) {
	q.bucket.SetRemoteQuota(remoteQuota)
}

// GetQuotaUsed 拉取本地使用配额情况以供上报
func (q *QuotaBucketReject) GetQuotaUsed(curTimeMilli int64) ratelimiter.UsageInfo {
	return q.bucket.GetQuotaUsed(curTimeMilli)
}

// GetAmountInfos 获取规则的限流阈值信息
func (q *QuotaBucketReject) GetAmountInfos() []ratelimiter.AmountInfo {
	tokenBuckets := q.bucket.GetTokenBuckets()
	var amounts []ratelimiter.AmountInfo
	if len(tokenBuckets) == 0 {
		return amounts
	}
	for _, tokenBucket := range tokenBuckets {
		amounts = append(amounts, ratelimiter.AmountInfo{
			ValidDuration: tokenBucket.validDurationSecond,
			MaxAmount:     tokenBucket.ruleTokenAmount,
		})
	}
	return amounts
}
