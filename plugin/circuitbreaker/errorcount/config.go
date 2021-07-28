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

package errorcount

import (
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/hashicorp/go-multierror"
	"math"
	"time"
)

//定义连续错误熔断配置的默认值
const (
	//默认可触发熔断的连续错误数，默认10
	DefaultContinuousErrorThreshold = 10
	//连续错误数统计时间窗口，默认1分钟
	DefaultMetricStatTimeWindow = 1 * time.Minute
	//最小连续错误数统计时间窗口
	MinMetricStatTimeWindow = 1 * time.Second
	//连续错误数统计滑桶数量
	DefaultErrCountMetricBucketCount = 10
)

//连续错误熔断的配置对象
type Config struct {
	//连续错误数阈值
	ContinuousErrorThreshold int `yaml:"continuousErrorThreshold" json:"continuousErrorThreshold"`
	//连续错误数统计时间窗口
	MetricStatTimeWindow *time.Duration `yaml:"metricStatTimeWindow" json:"metricStatTimeWindow"`
	//连续错误数统计滑桶数量
	MetricNumBuckets int `yaml:"metricNumBuckets" json:"metricNumBuckets"`
}

//检验连续错误熔断配置
func (r *Config) Verify() error {
	var errs error
	if r.ContinuousErrorThreshold <= 0 {
		errs = multierror.Append(errs, fmt.Errorf("errorCount.continuousErrorThreshold must be greater than 0"))
	}
	if nil != r.MetricStatTimeWindow && *r.MetricStatTimeWindow < MinMetricStatTimeWindow {
		errs = multierror.Append(errs,
			fmt.Errorf("errorCount.metricStatTimeWindow must be greater than %v", MinMetricStatTimeWindow))
	}
	return errs
}

//设置连续错误熔断配置默认值
func (r *Config) SetDefault() {
	if r.ContinuousErrorThreshold == 0 {
		r.ContinuousErrorThreshold = DefaultContinuousErrorThreshold
	}
	if nil == r.MetricStatTimeWindow {
		r.MetricStatTimeWindow = model.ToDurationPtr(DefaultMetricStatTimeWindow)
	}
	if r.MetricNumBuckets == 0 {
		r.MetricNumBuckets = DefaultErrCountMetricBucketCount
	}
}

//获取滑桶时间间隔
func (r *Config) GetBucketInterval() time.Duration {
	bucketSize := math.Ceil(float64(*r.MetricStatTimeWindow) / float64(r.MetricNumBuckets))
	return time.Duration(bucketSize)
}

//连续错误数阈值
func (r *Config) GetContinuousErrorThreshold() int {
	return r.ContinuousErrorThreshold
}

//设置连续错误数阈值
func (r *Config) SetContinuousErrorThreshold(value int) {
	r.ContinuousErrorThreshold = value
}

//连续错误数统计时间窗口
func (r *Config) GetMetricStatTimeWindow() time.Duration {
	return *r.MetricStatTimeWindow
}

//设置连续错误数统计时间窗口
func (r *Config) SetMetricStatTimeWindow(value time.Duration) {
	r.MetricStatTimeWindow = &value
}

//连续错误数统计滑桶数量
func (r *Config) GetMetricNumBuckets() int {
	return r.MetricNumBuckets
}

//设置连续错误数统计滑桶数量
func (r *Config) SetMetricNumBuckets(value int) {
	r.MetricNumBuckets = value
}
