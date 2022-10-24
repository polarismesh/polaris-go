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

package errorrate

import (
	"fmt"
	"math"
	"time"

	"github.com/hashicorp/go-multierror"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// 定义错误率熔断配置的默认值
const (
	// DefaultRequestVolumeThreshold 只有请求数达到某个阈值才执行熔断计算，默认10
	DefaultRequestVolumeThreshold = 10
	// DefaultErrorRateThreshold 触发熔断的错误率阈值，默认0.5
	DefaultErrorRateThreshold float64 = 0.5
	// DefaultErrorRatePercent 默认错误率百分比
	DefaultErrorRatePercent int = 50
	// MaxErrorRatePercent 最大错误率百分比
	MaxErrorRatePercent int = 100
	// DefaultMetricStatTimeWindow 错误率统计时间窗口，默认1分钟
	DefaultMetricStatTimeWindow = 60 * time.Second
	// MinMetricStatTimeWindow 最小错误率统计时间窗口，1s
	MinMetricStatTimeWindow = 1 * time.Second
	// DefaultMetricNumBuckets 统计窗口细分的桶数量，默认10
	DefaultMetricNumBuckets = 5
	// MinMetricStatBucketSize 最小的滑窗时间片，1ms
	MinMetricStatBucketSize = 1 * time.Millisecond
)

// Config 基于错误率熔断器的配置结构
type Config struct {
	RequestVolumeThreshold int `yaml:"requestVolumeThreshold" json:"requestVolumeThreshold"`
	ErrorRatePercent       int `yaml:"errorRatePercent" json:"errorRatePercent"`
	// Deprecated: 请使用ErrorRatePercent
	ErrorRateThreshold   float64        `yaml:"errorRateThreshold" json:"errorRateThreshold"`
	MetricStatTimeWindow *time.Duration `yaml:"metricStatTimeWindow" json:"metricStatTimeWindow"`
	MetricNumBuckets     int            `yaml:"metricNumBuckets" json:"metricNumBuckets"`
}

// Verify 检验错误率熔断配置
func (r *Config) Verify() error {
	var errs error
	if r.RequestVolumeThreshold <= 0 {
		errs = multierror.Append(errs, fmt.Errorf("errRate.requestVolumeThreshold must be greater than 0"))
	}
	if r.ErrorRatePercent <= 0 || r.ErrorRatePercent > MaxErrorRatePercent {
		errs = multierror.Append(errs, fmt.Errorf(
			"errRate.errorRatePercent must be greater than 0 and lower than %d", MaxErrorRatePercent))
	}
	if nil != r.MetricStatTimeWindow && *r.MetricStatTimeWindow < MinMetricStatTimeWindow {
		errs = multierror.Append(errs,
			fmt.Errorf("errRate.metricStatTimeWindow must be greater than %v", MinMetricStatTimeWindow))
	}
	if r.MetricNumBuckets <= 0 {
		errs = multierror.Append(errs, fmt.Errorf("errRate.metricNumBuckets must be greater than 0"))
	}
	if r.GetBucketInterval() < MinMetricStatBucketSize {
		errs = multierror.Append(errs,
			fmt.Errorf("bucketSize(metricStatTimeWindow/metricNumBuckets) must be greater than %v",
				MinMetricStatBucketSize))
	}
	return errs
}

// GetBucketInterval 获取滑桶时间间隔
func (r *Config) GetBucketInterval() time.Duration {
	bucketSize := math.Ceil(float64(*r.MetricStatTimeWindow) / float64(r.MetricNumBuckets))
	return time.Duration(bucketSize)
}

// SetDefault 设置错误率熔断配置的默认值
func (r *Config) SetDefault() {
	if r.RequestVolumeThreshold == 0 {
		r.RequestVolumeThreshold = DefaultRequestVolumeThreshold
	}
	// 兼容原有配置项
	if r.ErrorRateThreshold > 0 && r.ErrorRatePercent == 0 {
		r.ErrorRatePercent = int(r.ErrorRateThreshold * 100)
	}
	if r.ErrorRatePercent == 0 {
		r.ErrorRatePercent = DefaultErrorRatePercent
	}
	if nil == r.MetricStatTimeWindow {
		r.MetricStatTimeWindow = model.ToDurationPtr(DefaultMetricStatTimeWindow)
	}
	if r.MetricNumBuckets == 0 {
		r.MetricNumBuckets = DefaultMetricNumBuckets
	}
}

// GetRequestVolumeThreshold 触发错误率熔断的请求量阈值
func (r *Config) GetRequestVolumeThreshold() int {
	return r.RequestVolumeThreshold
}

// SetRequestVolumeThreshold 设置触发错误率熔断的请求量阈值
func (r *Config) SetRequestVolumeThreshold(value int) {
	r.RequestVolumeThreshold = value
}

// GetErrorRatePercent 触发熔断的错误率阈值，取值范围(0, 100]
func (r *Config) GetErrorRatePercent() int {
	return r.ErrorRatePercent
}

// SetErrorRatePercent 设置错误率阈值
func (r *Config) SetErrorRatePercent(value int) {
	r.ErrorRatePercent = value
}

// GetMetricStatTimeWindow 错误率统计时间窗口
func (r *Config) GetMetricStatTimeWindow() time.Duration {
	return *r.MetricStatTimeWindow
}

// SetMetricStatTimeWindow 设置错误率统计时间窗口
func (r *Config) SetMetricStatTimeWindow(value time.Duration) {
	r.MetricStatTimeWindow = &value
}

// GetMetricNumBuckets 统计窗口细分的桶数量
func (r *Config) GetMetricNumBuckets() int {
	return r.MetricNumBuckets
}

// SetMetricNumBuckets 设置统计窗口细分的桶数量
func (r *Config) SetMetricNumBuckets(value int) {
	r.MetricNumBuckets = value
}
