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

package trigger

import (
	"math"
	"sync/atomic"
	"time"

	"github.com/polarismesh/polaris-go/pkg/clock"
	"github.com/polarismesh/polaris-go/pkg/metric"
	"github.com/polarismesh/polaris-go/pkg/model"
)

const (
	bucketCount = 10
)

const (
	// 错误率统计窗口下标
	metricIdxErrRate = iota
	// 半开统计窗口下标
	metricIdxHalfOpen
	// 最大窗口下标
	metricIdxMax
)

// 统计维度
const (
	// 总请求数
	keyRequestCount = iota
	// 错误数
	keyFailCount
	// 总统计维度
	maxDimension
)

type ErrRateCounter struct {
	*baseCounter
	sliceWindow    *metric.SliceWindow
	metricWindow   time.Duration
	minimumRequest int32
	errorPercent   int
	scheduled      int32
}

func NewErrRateCounter(name string, opt *Options) *ErrRateCounter {
	c := &ErrRateCounter{
		baseCounter: newBaseCounter(name, opt),
	}
	c.init()
	return c
}

func (c *ErrRateCounter) init() {
	c.log.Infof("[CircuitBreaker][ErrRateCounter] initialized, ruleName:%s, resource(%s)", c.ruleName, c.res.String())
	c.metricWindow = time.Duration(c.triggerCondition.Interval) * time.Second
	c.errorPercent = int(c.triggerCondition.ErrorPercent)
	c.minimumRequest = int32(c.triggerCondition.MinimumRequest)
	c.sliceWindow = metric.NewSliceWindow(c.res.String(), bucketCount, getBucketInterval(c.metricWindow), maxDimension,
		clock.GetClock().Now().UnixNano())
}

func (c *ErrRateCounter) Report(success bool) {
	if c.isSuspend() {
		c.log.Debugf("[CircuitBreaker][ErrRateCounter] suspended, skip report, ruleName:%s, resource(%s)",
			c.ruleName, c.res.String())
		return
	}
	c.log.Debugf("[CircuitBreaker][ErrRateCounter] report request, ruleName:%s, success(%v), resource(%s)",
		c.ruleName, success, c.res.String())

	retStatus := model.RetSuccess
	if !success {
		retStatus = model.RetFail
	}

	c.sliceWindow.AddGauge(&model.ServiceCallResult{
		RetStatus: retStatus,
	}, func(gauge model.InstanceGauge, bucket *metric.Bucket) int64 {
		ret := gauge.GetRetStatus()
		if ret == model.RetFail {
			bucket.AddMetric(keyFailCount, 1)
		}
		bucket.AddMetric(keyRequestCount, 1)
		return 0
	})
	if !success && atomic.CompareAndSwapInt32(&c.scheduled, 0, 1) {
		c.log.Infof("[CircuitBreaker][ErrRateCounter] scheduled error rate check, ruleName:%s, metricWindow(%v), "+
			"resource(%s)", c.ruleName, c.metricWindow, c.res.String())
		c.delayExecutor(c.metricWindow, func() {
			currentTime := time.Now()
			timeRange := &metric.TimeRange{
				Start: currentTime.Add(-1 * c.metricWindow),
				End:   currentTime,
			}
			reqCount := c.sliceWindow.CalcMetrics(keyRequestCount, timeRange)
			reqFailCount := c.sliceWindow.CalcMetrics(keyFailCount, timeRange)
			failRatio := (float64(reqFailCount) / float64(reqCount)) * 100
			if reqCount < int64(c.minimumRequest) || failRatio < float64(c.errorPercent) {
				atomic.StoreInt32(&c.scheduled, 0)
				c.log.Debugf("[CircuitBreaker][ErrRateCounter] threshold not reached, skip trigger, ruleName:%s, "+
					"reqCount(%d), minimumRequest(%d), failRatio(%.2f%%), errorPercent(%d%%), failCount(%d), "+
					"resource(%s)", c.ruleName, reqCount, c.minimumRequest, failRatio, c.errorPercent, reqFailCount,
					c.res.String())
				return
			}
			c.suspend()
			c.log.Infof("[CircuitBreaker][ErrRateCounter] triggered CloseToOpen, ruleName:%s, reqCount(%d), "+
				"failCount(%d), failRatio(%.2f%%), minimumRequest(%d), errorPercent(%d%%), resource(%s)",
				c.ruleName, reqCount, reqFailCount, failRatio, c.minimumRequest, c.errorPercent, c.res.String())
			c.handler.CloseToOpen(c.ruleName)
			atomic.StoreInt32(&c.scheduled, 0)
		})
	}
}

func (c *ErrRateCounter) Resume() {
	if c.isSuspend() {
		c.resume()
		c.log.Infof("[CircuitBreaker][ErrRateCounter] resumed counter, ruleName:%s, resource(%s)",
			c.ruleName, c.res.String())
	}
}

func getBucketInterval(interval time.Duration) time.Duration {
	bucketSize := math.Ceil(float64(interval) / float64(bucketCount))
	return time.Duration(bucketSize)
}

// ToErrorRateThreshold 转换成熔断错误率阈值
func ToErrorRateThreshold(errorRatePercent int) float64 {
	return float64(errorRatePercent) / 100
}
