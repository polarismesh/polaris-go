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
	"github.com/polarismesh/polaris-go/pkg/clock"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/modern-go/reflect2"
	"sync"
	"sync/atomic"
	"time"
)

//回调函数，用于将统计数据入桶
type AddBucketFunc func(gauge model.InstanceGauge, bucket *Bucket) int64

//封装类型，封装统计数据，以及添加统计的操作回调
type GaugeOperation struct {
	Gauge     model.InstanceGauge
	Operation AddBucketFunc
}

//执行统计数据添加
func (g *GaugeOperation) addBucket(bucket *Bucket) {
	g.Operation(g.Gauge, bucket)
}

//滑窗
type SliceWindow struct {
	//滑窗所属的类型，一般为插件名
	Type string
	//滑桶的长度
	bucketInterval time.Duration
	//滑桶长度（毫秒结算）
	bucketIntervalMilli int64
	//滑窗总时间
	windowLength time.Duration
	//初始化桶的并发锁
	bucketMutex *sync.Mutex
	//滑桶个数
	bucketCount int
	//滑桶序列
	buckets atomic.Value
	//维度的长度
	metricSize int
	//滑窗开始的时间，以ms为单位，以绝对零时间为起始时间
	windowStartTimeMilli int64
	//最后更新时间，以ns为单位，单位纳秒
	lastUpdateTime int64
	//最后读取时间，以ns为单位
	lastReadTime int64

	Lock *sync.Mutex
	//周期开始时间
	PeriodStartTime int64
	//同一周期已经上报的个数
	//PeriodAcquired int64
}

//创建资源滑窗
func NewSliceWindow(typ string, bucketCount int, bucketInterval time.Duration, metricSize int,
	curTime int64) *SliceWindow {
	window := &SliceWindow{
		Type:                typ,
		bucketInterval:      bucketInterval,
		bucketIntervalMilli: bucketInterval.Nanoseconds() / milliDivide,
		windowLength:        bucketInterval * time.Duration(bucketCount),
		metricSize:          metricSize,
		bucketMutex:         &sync.Mutex{},
		bucketCount:         bucketCount,
		PeriodStartTime:     0,
	}
	window.lastUpdateTime = curTime
	window.lastReadTime = curTime - 1
	window.Lock = &sync.Mutex{}
	//curTime := GetCurrentMilliseconds(startTime)
	//window.windowStartTimeMilli = curTime
	//for i := 0; i < window.bucketCount; i++ {
	//	window.buckets[i].mutex = &sync.RWMutex{}
	//	window.buckets[i].window = window
	//	window.buckets[i].startTime = curTime
	//	window.buckets[i].metrics = NewResMetricArray(metricSize)
	//	curTime += window.bucketIntervalMilli
	//}
	return window
}

//初始化bucket信息
func (s *SliceWindow) initBucket() []Bucket {
	buckets := make([]Bucket, s.bucketCount)
	curTime := s.CalcStartTime(model.ParseMilliSeconds(time.Now().UnixNano()))
	for i := 0; i < s.bucketCount; i++ {
		idx := s.calcBucketIndex(curTime)
		buckets[idx].mutex = &sync.RWMutex{}
		buckets[idx].window = s
		buckets[idx].startTime = curTime
		buckets[idx].metrics = NewResMetricArray(s.metricSize)
		curTime += s.bucketIntervalMilli
	}
	s.buckets.Store(buckets)
	return buckets
}

//获取滑桶的长度
func (s *SliceWindow) GetBucketInterval() time.Duration {
	return s.bucketInterval
}

//计算桶下表
func (s *SliceWindow) calcBucketIndex(curMilliseconds int64) int {
	timeIdx := (curMilliseconds - s.windowStartTimeMilli) / s.bucketIntervalMilli
	return int(timeIdx) % s.bucketCount
}

const milliDivide = 1000 * 1000

//获取当前的相对毫秒值
func GetCurrentMilliseconds(startTime time.Time) int64 {
	return startTime.UnixNano() / milliDivide
}

//计算对应bucket起始时间戳
func (s *SliceWindow) CalcStartTime(curTime int64) int64 {
	curStartTimeInWindow := curTime - s.windowStartTimeMilli
	return s.windowStartTimeMilli + (curStartTimeInWindow - curStartTimeInWindow%s.bucketIntervalMilli)
}

//获取最近更新时间
func (s *SliceWindow) GetLastUpdateTime() int64 {
	return atomic.LoadInt64(&s.lastUpdateTime)
}

//设置最近读取时间
func (s *SliceWindow) SetLastReadTime() {
	atomic.StoreInt64(&s.lastReadTime, clock.GetClock().Now().UnixNano())
}

//判断是否在上次读取了数据后，又有数据更新
func (s *SliceWindow) IsMetricUpdate() bool {
	return s.GetLastUpdateTime() > atomic.LoadInt64(&s.lastReadTime)
}

//寻找bucket
func (s *SliceWindow) lookupBucket(now time.Time) *Bucket {
	curTime := GetCurrentMilliseconds(now)
	return s.lookupBucketByMillTime(curTime)
}

func (s *SliceWindow) lookupBucketByMillTime(curTime int64) *Bucket {
	bucketIndex := s.calcBucketIndex(curTime)
	startTime := s.CalcStartTime(curTime)
	bucket := s.getBucket(bucketIndex)
	if atomic.LoadInt64(&bucket.startTime) == startTime {
		return bucket
	}
	return nil
}

//获取bucket数组信息
func (s *SliceWindow) getBuckets() []Bucket {
	bucketsValue := s.buckets.Load()
	if !reflect2.IsNil(bucketsValue) {
		return bucketsValue.([]Bucket)
	}
	return nil
}

//获取bucket信息
func (s *SliceWindow) getBucket(bucketIndex int) *Bucket {
	var buckets []Bucket
	buckets = s.getBuckets()
	if nil != buckets {
		return &buckets[bucketIndex]
	}
	s.bucketMutex.Lock()
	defer s.bucketMutex.Unlock()
	buckets = s.getBuckets()
	if nil != buckets {
		return &buckets[bucketIndex]
	}
	buckets = s.initBucket()
	return &buckets[bucketIndex]
}

//寻找bucket，如果找不到，则进行创建
//入参：当前时间
//返回1个参数，首个参数为命中的桶
func (s *SliceWindow) lookupAndCreateBucket(now time.Time) *Bucket {
	curTime := GetCurrentMilliseconds(now)
	return s.lookupAndCreateBucketByMillTime(curTime)
}

func (s *SliceWindow) lookupAndCreateBucketByMillTime(curTime int64) *Bucket {
	bucketIndex := s.calcBucketIndex(curTime)
	startTime := s.CalcStartTime(curTime)
	bucket := s.getBucket(bucketIndex)
	if atomic.LoadInt64(&bucket.startTime) == startTime {
		return bucket
	}
	//时间已经过期，需要更新bucket
	bucket.mutex.Lock()
	defer bucket.mutex.Unlock()
	bucketStartTime := atomic.LoadInt64(&bucket.startTime)
	if bucketStartTime == startTime {
		return bucket
	}
	//重置bucket
	bucket.metrics = NewResMetricArray(s.metricSize)
	atomic.StoreInt64(&bucket.startTime, startTime)
	return bucket
}

//限流桶操作函数
type BucketOperation func(bucket *Bucket) int64

//添加历史数据
//返回函数运行结果，以及是否命中
func (s *SliceWindow) AddHistoryMetric(now time.Time, operation BucketOperation) (int64, bool) {
	bucket := s.lookupBucket(now)
	if nil == bucket {
		return 0, false
	}
	return operation(bucket), true
}

func (s *SliceWindow) AddHistoryMetricByMillTime(now int64, operation BucketOperation) (int64, bool) {
	bucket := s.lookupBucketByMillTime(now)
	if nil == bucket {
		return 0, false
	}
	return operation(bucket), true
}

//添加统计数据
func (s *SliceWindow) AddGauge(gauge model.InstanceGauge, operation AddBucketFunc) int64 {
	value, _ := s.AddGaugeAdvance(gauge, operation)
	return value
}

//添加统计数据，返回就近的数据以及bucket起始时间
func (s *SliceWindow) AddGaugeAdvance(gauge model.InstanceGauge, operation AddBucketFunc) (int64, int64) {
	curTime := clock.GetClock().Now()
	atomic.StoreInt64(&s.lastUpdateTime, curTime.UnixNano())
	bucket := s.lookupAndCreateBucket(curTime)
	return operation(gauge, bucket), bucket.startTime
}

//func (s *SliceWindow) GetBucketsTotalNumForReportForAcquire(nowTime int64) int64 {
//	var retNum int64 = 0
//	startTime := s.CalcStartTime(nowTime)
//	for _, v := range s.getBuckets() {
//		if atomic.LoadInt64(&v.startTime) == startTime {
//			num := v.metrics.GetMetric(0)
//			if num < 0 {
//				num = 0
//			}
//			retNum += num
//		}
//	}
//	if atomic.LoadInt64(&s.PeriodStartTime) == startTime {
//		retNum -= atomic.LoadInt64(&s.PeriodAcquired)
//		if retNum < 0 {
//			log.GetBaseLogger().Warnf("periodAcquired > metricTotal maybe something not right")
//			retNum = 0
//		}
//		atomic.AddInt64(&s.PeriodAcquired, retNum)
//		return retNum
//	} else {
//		s.Lock.Lock()
//		defer s.Lock.Unlock()
//		if atomic.LoadInt64(&s.PeriodStartTime) == startTime {
//			retNum -= atomic.LoadInt64(&s.PeriodAcquired)
//			atomic.AddInt64(&s.PeriodAcquired, retNum)
//			return retNum
//		}
//		atomic.StoreInt64(&s.PeriodAcquired, retNum)
//		atomic.StoreInt64(&s.PeriodStartTime, startTime)
//		return retNum
//	}
//}

//func (s *SliceWindow) GetBucketsTotalNumForReport(nowTime int64) int64 {
//	var retNum int64 = 0
//	startTime := s.CalcStartTime(nowTime)
//	for _, v := range s.getBuckets() {
//		if atomic.LoadInt64(&v.startTime) == startTime {
//			retNum += v.metrics.GetMetric(0)
//		}
//	}
//	if atomic.LoadInt64(&s.PeriodStartTime) == startTime {
//		retNum -= atomic.LoadInt64(&s.PeriodAcquired)
//		if retNum < 0 {
//			log.GetBaseLogger().Warnf("periodAcquired > metricTotal maybe something not right")
//			retNum = 0
//		}
//	}
//	return retNum
//}

//添加统计数据
func (s *SliceWindow) AddGaugeByValue(value int64, curTime time.Time) int64 {
	var bucket *Bucket
	s.lastUpdateTime = curTime.UnixNano()
	bucket = s.lookupAndCreateBucket(curTime)
	return bucket.AddMetric(0, value)
}

func (s *SliceWindow) AddGaugeByValueByMillTime(value int64, curTime int64) int64 {
	var bucket *Bucket
	bucket = s.lookupAndCreateBucketByMillTime(curTime)
	return bucket.AddMetric(0, value)
}

func (s *SliceWindow) SetPeriodStart(now int64) {
	s.PeriodStartTime = s.CalcStartTime(now)
}
