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

package monitor

import (
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/hashicorp/go-multierror"
	"math"
	"time"
)

const (
	DefaultMetricsReportWindow = 1 * time.Minute
	DefaultMetricsNumBuckets   = 12
)

//统计类型
type StatType int

//插件配置
type Config struct {
	MetricsReportWindow *time.Duration `yaml:"metricsReportWindow"`
	MetricsNumBuckets   int            `yaml:"metricsNumBuckets"`
}

//校验配置
func (c *Config) Verify() error {
	var errs error
	if c.MetricsNumBuckets <= 0 {
		errs = multierror.Append(errs,
			fmt.Errorf("stat2Monitor.metricsNumBuckets must be greater than 0, current is: %v", c.MetricsNumBuckets))
	}
	if *c.MetricsReportWindow <= 0 {
		errs = multierror.Append(errs,
			fmt.Errorf("stat2Monitor.metricsReportWindow must be greater than 0, current is: %v", c.MetricsReportWindow))
	}
	return errs
}

//获取滑桶数量
func (c *Config) GetBucketInterval() time.Duration {
	bucketSize := math.Ceil(float64(*c.MetricsReportWindow) / float64(c.MetricsNumBuckets))
	return time.Duration(bucketSize)
}

//设置默认值
func (c *Config) SetDefault() {
	if c.MetricsNumBuckets == 0 {
		c.MetricsNumBuckets = DefaultMetricsNumBuckets
	}
	if nil == c.MetricsReportWindow {
		c.MetricsReportWindow = model.ToDurationPtr(DefaultMetricsReportWindow)
	}
}
