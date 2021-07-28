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

package metric

import (
	"github.com/polarismesh/polaris-go/pkg/log"
	"sync"
	"sync/atomic"
	"time"
)

//资源统计序列
type ResMetricArray struct {
	//统计序列
	metrics []int64
}

//创建资源统计序列
func NewResMetricArray(metricSize int) *ResMetricArray {
	return &ResMetricArray{
		metrics: make([]int64, metricSize),
	}
}

//获取统计值
func (r *ResMetricArray) GetMetric(dimension int) int64 {
	return atomic.LoadInt64(&r.metrics[dimension])
}

func (r *ResMetricArray) SwapMetric(dimension int, newValue int64) int64 {
	return atomic.SwapInt64(&r.metrics[dimension], newValue)
}

//累加统计值
func (r *ResMetricArray) AddMetric(dimension int, value int64) int64 {
	return atomic.AddInt64(&r.metrics[dimension], value)
}

//重置统计值
func (r *ResMetricArray) SetMetric(dimension int, value int64) {
	atomic.StoreInt64(&r.metrics[dimension], value)
}

//timeRange的类型
type IntervalType int

const (
	IncludeStart IntervalType = iota
	IncludeEnd
	IncludeBoth
)

//将IntervalType转化为string
func (i IntervalType) String() string {
	switch i {
	case IncludeStart:
		return "IncludeStart"
	case IncludeEnd:
		return "IncludeEnd"
	case IncludeBoth:
		return "IncludeBoth"
	}
	return "InvalidIntervalType"
}

//时间段
type TimeRange struct {
	//起始时间
	Start time.Time
	//结束时间
	End time.Time
	//时间段类型
	Type IntervalType
}

const (
	Before int = iota
	Inside
	After
)

//判断时间点是否在范围中
func (t *TimeRange) IsTimeInBucket(value time.Time) int {
	if value.Before(t.Start) {
		return Before
	}
	if value.Equal(t.Start) || (value.After(t.Start) && value.Before(t.End)) {
		return Inside
	}
	return After
}

//统计单元
type Bucket struct {
	//滑桶的起始时间，单位毫秒
	startTime int64
	//同步锁，控制滑桶的变更
	mutex *sync.RWMutex
	//所归属的滑窗
	window *SliceWindow
	//统计序列
	metrics *ResMetricArray
}

//获取统计值
func (b *Bucket) GetMetric(dimension int) int64 {
	return b.metrics.GetMetric(dimension)
}

//累加统计值
func (b *Bucket) AddMetric(dimension int, value int64) int64 {
	return b.metrics.AddMetric(dimension, value)
}

//设置统计值
func (b *Bucket) SetMetric(dimension int, value int64) {
	b.metrics.SetMetric(dimension, value)
}

//计算时间范围内的桶的统计值，如果这个桶包括在时间范围内，那么计算，否则不计入
func (b *Bucket) CalcBucketMetrics(dimensions []int, startTime int64, endTime int64, rangeType IntervalType) []int64 {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	bucketStartTime := atomic.LoadInt64(&b.startTime)
	bucketEndTime := bucketStartTime + b.window.bucketIntervalMilli
	if !b.inRange(startTime, endTime, bucketStartTime, rangeType) {
		if log.GetBaseLogger().IsLevelEnabled(log.TraceLog) {
			log.GetBaseLogger().Tracef(
				"bucket %s start not matched, startTime %v, endTime %v, rangeType %s, bucketStartTime %v, bucketEndTime %v",
				b.window.Type, startTime, endTime, rangeType, atomic.LoadInt64(&b.startTime), bucketEndTime)
		}
		return nil
	}
	values := make([]int64, 0, len(dimensions))
	for _, dimension := range dimensions {
		values = append(values, b.GetMetric(dimension))
	}
	return values
}

//一个bucket是否在对应区间内
//根据区间起止时间和区间类型区分
func (b *Bucket) inRange(startTime int64, endTime int64, bucketStartTime int64, rangeType IntervalType) bool {
	res := false
	bucketEndTime := bucketStartTime + b.window.bucketIntervalMilli
	switch rangeType {
	case IncludeStart:
		res = bucketEndTime > startTime && bucketEndTime <= endTime
	case IncludeEnd:
		res = bucketStartTime >= startTime && bucketStartTime < endTime
	case IncludeBoth:
		res = bucketEndTime > startTime && bucketStartTime < endTime
	}
	return res
}

//计算维度下所有的时间段的值之和
func (s *SliceWindow) CalcMetrics(dimension int, timeRange *TimeRange) int64 {
	startNanoseconds := GetCurrentMilliseconds(timeRange.Start)
	endNanoseconds := GetCurrentMilliseconds(timeRange.End)
	dimensions := []int{dimension}
	var total int64
	buckets := s.getBuckets()
	if nil == buckets {
		return total
	}
	for _, bucket := range buckets {
		values := bucket.CalcBucketMetrics(dimensions, startNanoseconds, endNanoseconds, timeRange.Type)
		if len(values) == 0 {
			continue
		}
		total += values[0]
	}
	return total
}

//计算多个维度下的统计值
func (s *SliceWindow) CalcMetricsInMultiDimensions(dimensions []int, timeRange *TimeRange) []int64 {
	startNanoseconds := GetCurrentMilliseconds(timeRange.Start)
	endNanoseconds := GetCurrentMilliseconds(timeRange.End)
	dimensionCount := len(dimensions)
	var totalValues = make([]int64, dimensionCount)
	buckets := s.getBuckets()
	if nil == buckets {
		return totalValues
	}
	for _, bucket := range buckets {
		values := bucket.CalcBucketMetrics(dimensions, startNanoseconds, endNanoseconds, timeRange.Type)
		if len(values) == 0 {
			continue
		}
		for i := 0; i < dimensionCount; i++ {
			totalValues[i] += values[i]
		}
	}
	return totalValues
}
