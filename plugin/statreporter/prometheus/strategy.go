// Tencent is pleased to support the open source community by making polaris-go available.
//
// Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
//
// Licensed under the BSD 3-Clause License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// https://opensource.org/licenses/BSD-3-Clause
//
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissionsr and limitations under the License.
//
//@Author: springliao
//@Description:
//@Time: 2021/10/19 17:29

package prometheus

import (
	"strings"

	"github.com/polarismesh/polaris-go/pkg/model"
)

var ()

// UpstreamRequestTotalStrategy Total number of service invocation requests
type UpstreamRequestTotalStrategy struct {
}

func (strategy *UpstreamRequestTotalStrategy) StrategyDescription() string {
	return "total of request per period"
}

func (strategy *UpstreamRequestTotalStrategy) StrategyName() string {
	return "upstream_rq_total"
}

func (strategy *UpstreamRequestTotalStrategy) UpdateMetricValue(targetValue *StatMetric, dataSource interface{}) {
	targetValue.Inc()
}

func (strategy *UpstreamRequestTotalStrategy) InitMetricValue(dataSource interface{}) float64 {
	return 1.0
}

// UpstreamRequestSuccessStrategy Total number of successful service invocations
type UpstreamRequestSuccessStrategy struct {
}

func (strategy *UpstreamRequestSuccessStrategy) StrategyDescription() string {
	return "total of success request per period"
}

func (strategy *UpstreamRequestSuccessStrategy) StrategyName() string {
	return "upstream_rq_success"
}

func (strategy *UpstreamRequestSuccessStrategy) UpdateMetricValue(targetValue *StatMetric, dataSource interface{}) {
	gauge := dataSource.(model.InstanceGauge)
	if model.RetSuccess == gauge.GetRetStatus() {
		targetValue.Inc()
	}
}

func (strategy *UpstreamRequestSuccessStrategy) InitMetricValue(dataSource interface{}) float64 {
	gauge := dataSource.(model.InstanceGauge)
	if model.RetSuccess == gauge.GetRetStatus() {
		return 1
	}
	return 0
}

// UpstreamRequestTimeoutStrategy Total delay of service invocation
type UpstreamRequestTimeoutStrategy struct {
}

func (strategy *UpstreamRequestTimeoutStrategy) StrategyDescription() string {
	return "total of request delay per period"
}

func (strategy *UpstreamRequestTimeoutStrategy) StrategyName() string {
	return "upstream_rq_timeout"
}

func (strategy *UpstreamRequestTimeoutStrategy) UpdateMetricValue(targetValue *StatMetric, dataSource interface{}) {
	gauge := dataSource.(model.InstanceGauge)
	delay := gauge.GetDelay()

	if delay == nil {
		return
	}

	targetValue.Add(float64(*delay))
}

func (strategy *UpstreamRequestTimeoutStrategy) InitMetricValue(dataSource interface{}) float64 {
	gauge := dataSource.(model.InstanceGauge)
	delay := gauge.GetDelay()

	if delay == nil {
		return 0
	}

	return float64(*delay)
}

// UpstreamRequestTimeoutStrategy Total delay of service invocation
type UpstreamRequestMaxTimeoutStrategy struct {
}

func (strategy *UpstreamRequestMaxTimeoutStrategy) StrategyDescription() string {
	return "maximum request delay per period"
}

func (strategy *UpstreamRequestMaxTimeoutStrategy) StrategyName() string {
	return "upstream_rq_max_timeout"
}

func (strategy *UpstreamRequestMaxTimeoutStrategy) UpdateMetricValue(targetValue *StatMetric, dataSource interface{}) {
	gauge := dataSource.(model.InstanceGauge)
	delay := gauge.GetDelay()

	if delay == nil {
		return
	}

	for {
		if float64(*gauge.GetDelay()) > targetValue.Get() {
			if targetValue.CompareAndSwap(targetValue.Get(), float64(*gauge.GetDelay())) {
				return
			}
		} else {
			return
		}
	}
}

func (strategy *UpstreamRequestMaxTimeoutStrategy) InitMetricValue(dataSource interface{}) float64 {
	gauge := dataSource.(model.InstanceGauge)
	delay := gauge.GetDelay()

	if delay == nil {
		return 0
	}

	return float64(*delay)
}

// RateLimitRequestTotalStrategy Total number of requests for flow limiting calls
type RateLimitRequestTotalStrategy struct {
}

func (strategy *RateLimitRequestTotalStrategy) StrategyDescription() string {
	return "total of rate limit per period"
}

func (strategy *RateLimitRequestTotalStrategy) StrategyName() string {
	return "ratelimit_rq_total"
}

func (strategy *RateLimitRequestTotalStrategy) UpdateMetricValue(targetValue *StatMetric, dataSource interface{}) {
	targetValue.Inc()
}

func (strategy *RateLimitRequestTotalStrategy) InitMetricValue(dataSource interface{}) float64 {
	return 1.0
	gauge := dataSource.(model.RateLimitGauge)
	if strings.Compare(model.RateLimitResultForPass, gauge.GetResult()) == 0 {

	}
	return 0
}

// RateLimitRequestPassStrategy Total number of requests for flow limiting calls
type RateLimitRequestPassStrategy struct {
}

func (strategy *RateLimitRequestPassStrategy) StrategyDescription() string {
	return "total of rate limit per period"
}

func (strategy *RateLimitRequestPassStrategy) StrategyName() string {
	return "ratelimit_rq_total"
}

func (strategy *RateLimitRequestPassStrategy) UpdateMetricValue(targetValue *StatMetric, dataSource interface{}) {
	gauge := dataSource.(model.RateLimitGauge)
	if strings.Compare(model.RateLimitResultForPass, gauge.GetResult()) == 0 {
		targetValue.Inc()
	}
}

func (strategy *RateLimitRequestPassStrategy) InitMetricValue(dataSource interface{}) float64 {
	gauge := dataSource.(model.RateLimitGauge)
	if strings.Compare(model.RateLimitResultForPass, gauge.GetResult()) == 0 {
		return 1.0
	}
	return 0.0
}
