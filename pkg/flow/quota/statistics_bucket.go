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

package quota

import (
	"sync"
	"sync/atomic"
	"time"
)

// 用于metric report 统计
type StatisticsBucket struct {
	periodStartTimeSecond int64
	//bucketStartTimeSecond int64

	totalCount int64
	limitCount int64

	mutex *sync.Mutex
}

// add
func (b *StatisticsBucket) add(isLimit bool) {
	atomic.AddInt64(&b.totalCount, 1)
	if isLimit {
		atomic.AddInt64(&b.limitCount, 1)
	}
	return
}

// AddCount，外面使用该接口
func (b *StatisticsBucket) AddCount(isLimit bool, now time.Time) {
	unixTime := now.Unix()
	b.AddCountByUnixTime(isLimit, unixTime)
}

func (b *StatisticsBucket) AddCountByUnixTime(isLimit bool, now int64) {
	perStart := atomic.LoadInt64(&b.periodStartTimeSecond)
	if perStart == now {
		b.add(isLimit)
		return
	} else {
		b.mutex.Lock()
		defer b.mutex.Unlock()
		if atomic.LoadInt64(&b.periodStartTimeSecond) == now {
			b.add(isLimit)
			return
		}
		atomic.StoreInt64(&b.periodStartTimeSecond, now)
		atomic.SwapInt64(&b.totalCount, 1)
		if isLimit {
			atomic.SwapInt64(&b.limitCount, 1)
		} else {
			atomic.StoreInt64(&b.limitCount, 0)
		}
	}
}

// GetReportData
func (b *StatisticsBucket) GetReportData(periodTime int64) *ReportElements {
	data := new(ReportElements)
	data.TotalCount = 0
	data.LimitCount = 0
	if atomic.LoadInt64(&b.periodStartTimeSecond) == periodTime {
		data.TotalCount = atomic.SwapInt64(&b.totalCount, 0)
		data.LimitCount = atomic.SwapInt64(&b.limitCount, 0)
	}
	return data
}

// create StatisticsBucket
func NewStatisticsBucket() *StatisticsBucket {
	bucket := new(StatisticsBucket)
	bucket.totalCount = 0
	bucket.limitCount = 0
	bucket.periodStartTimeSecond = 0
	bucket.mutex = &sync.Mutex{}
	return bucket
}

// 返回记录
type ReportElements struct {
	TotalCount int64
	LimitCount int64
}
