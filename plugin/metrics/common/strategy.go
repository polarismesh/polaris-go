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

package common

import (
	"github.com/polarismesh/polaris-go/pkg/model"
)

var (
	ServiceCallStrategy = []MetricValueAggregationStrategy{
		&UpstreamRequestTotalStrategy{},
		&UpstreamRequestSuccessStrategy{},
		&UpstreamRequestTimeoutStrategy{},
		&UpstreamRequestMaxTimeoutStrategy{},
	}
	ServiceCallLabelOrder = []string{
		CalleeNamespace,
		CalleeService,
		CalleeSubset,
		CalleeMethod,
		CalleeInstance,
		CalleeRetCode,
		CalleeResult,
		CallerLabels,
		CallerNamespace,
		CallerService,
		CallerIP,
		MetricNameLabel,
		RuleName,
	}

	RateLimitStrategy = []MetricValueAggregationStrategy{
		&RateLimitRequestTotalStrategy{},
		&RateLimitRequestPassStrategy{},
		&RateLimitRequestLimitStrategy{},
	}
	RateLimitLabelOrder = []string{
		CalleeNamespace,
		CalleeService,
		CalleeMethod,
		CallerLabels,
		RuleName,
		MetricNameLabel,
	}

	CircuitBreakerStrategy = []MetricValueAggregationStrategy{
		&CircuitBreakerHalfOpenStrategy{},
		&CircuitBreakerOpenStrategy{},
	}
	CircuitBreakerLabelOrder = []string{
		CalleeNamespace,
		CalleeService,
		CalleeMethod,
		CalleeSubset,
		CalleeInstance,
		CallerNamespace,
		CallerService,
		RuleName,
		MetricNameLabel,
	}
)

type MetricValueAggregationStrategy interface {
	// 返回策略的描述信息
	GetStrategyDescription() string
	// 返回策略名称，通常该名称用作metricName
	GetStrategyName() string
	// 根据数据源的内容获取第一次创建metric的时候的初始值
	InitMetricValue(dataSource interface{}) float64
	// 根据metric自身的value值和聚合数据源T的值来更新metric的value
	UpdateMetricValue(targetValue StatMetric, dataSource interface{})
}

type AvgMetricValueAggregationStrategy interface {
	MetricValueAggregationStrategy
	NeedAvg() bool
}

type UpstreamRequestTotalStrategy struct {
}

// 返回策略的描述信息
func (us *UpstreamRequestTotalStrategy) GetStrategyDescription() string {
	return "total of request per period"
}

// 返回策略名称，通常该名称用作metricName
func (us *UpstreamRequestTotalStrategy) GetStrategyName() string {
	return MetricsNameUpstreamRequestTotal
}

// 根据数据源的内容获取第一次创建metric的时候的初始值
func (us *UpstreamRequestTotalStrategy) InitMetricValue(dataSource interface{}) float64 {
	return 1.0
}

// 根据metric自身的value值和聚合数据源T的值来更新metric的value
func (us *UpstreamRequestTotalStrategy) UpdateMetricValue(targetValue StatMetric, dataSource interface{}) {
	targetValue.Inc()
}

type UpstreamRequestSuccessStrategy struct {
}

// 返回策略的描述信息
func (us *UpstreamRequestSuccessStrategy) GetStrategyDescription() string {
	return "total of success request per period"
}

// 返回策略名称，通常该名称用作metricName
func (us *UpstreamRequestSuccessStrategy) GetStrategyName() string {
	return MetricsNameUpstreamRequestSuccess
}

// 根据数据源的内容获取第一次创建metric的时候的初始值
func (us *UpstreamRequestSuccessStrategy) InitMetricValue(dataSource interface{}) float64 {
	guage, ok := dataSource.(*model.ServiceCallResult)
	if !ok {
		return 0
	}
	if guage.RetStatus == model.RetSuccess {
		return 1
	}
	return 0
}

// 根据metric自身的value值和聚合数据源T的值来更新metric的value
func (us *UpstreamRequestSuccessStrategy) UpdateMetricValue(targetValue StatMetric, dataSource interface{}) {
	if guage, ok := dataSource.(*model.ServiceCallResult); ok {
		if guage.RetStatus == model.RetSuccess {
			targetValue.Inc()
		}
	}
}

type UpstreamRequestTimeoutStrategy struct {
}

// 返回策略的描述信息
func (us *UpstreamRequestTimeoutStrategy) GetStrategyDescription() string {
	return "total of request delay per period"
}

// 返回策略名称，通常该名称用作metricName
func (us *UpstreamRequestTimeoutStrategy) GetStrategyName() string {
	return MetricsNameUpstreamRequestTimeout
}

// 根据数据源的内容获取第一次创建metric的时候的初始值
func (us *UpstreamRequestTimeoutStrategy) InitMetricValue(dataSource interface{}) float64 {
	guage, ok := dataSource.(*model.ServiceCallResult)
	if !ok {
		return 0
	}
	delay := guage.GetDelay()
	if delay == nil {
		return 0
	}
	delayMs := (*delay).Milliseconds()
	return float64(delayMs)
}

// 根据metric自身的value值和聚合数据源T的值来更新metric的value
func (us *UpstreamRequestTimeoutStrategy) UpdateMetricValue(targetValue StatMetric, dataSource interface{}) {
	guage, ok := dataSource.(*model.ServiceCallResult)
	if !ok {
		return
	}
	delay := guage.GetDelay()
	if delay == nil {
		return
	}
	targetValue.Add((*delay).Milliseconds())
}

func (us *UpstreamRequestTimeoutStrategy) NeedAvg() bool {
	return true
}

type UpstreamRequestMaxTimeoutStrategy struct {
}

// 返回策略的描述信息
func (us *UpstreamRequestMaxTimeoutStrategy) GetStrategyDescription() string {
	return "maximum request delay per period"
}

// 返回策略名称，通常该名称用作metricName
func (us *UpstreamRequestMaxTimeoutStrategy) GetStrategyName() string {
	return MetricsNameUpstreamRequestMaxTimeout
}

// 根据数据源的内容获取第一次创建metric的时候的初始值
func (us *UpstreamRequestMaxTimeoutStrategy) InitMetricValue(dataSource interface{}) float64 {
	guage, ok := dataSource.(*model.ServiceCallResult)
	if !ok {
		return 0
	}
	delay := guage.GetDelay()
	if delay == nil {
		return 0
	}
	return float64((*delay).Milliseconds())
}

// 根据metric自身的value值和聚合数据源T的值来更新metric的value
func (us *UpstreamRequestMaxTimeoutStrategy) UpdateMetricValue(targetValue StatMetric, dataSource interface{}) {
	guage, ok := dataSource.(*model.ServiceCallResult)
	if !ok {
		return
	}
	delay := guage.GetDelay()
	if delay == nil {
		return
	}
	delayMs := float64((*delay).Milliseconds())
	for {
		oldValue := targetValue.GetValue()
		if delayMs > float64(oldValue) {
			if targetValue.CompareAndSwap(int64(oldValue), int64(delayMs)) {
				return
			}
		} else {
			return
		}
	}
}

type CircuitBreakerOpenStrategy struct {
}

// 返回策略的描述信息
func (us *CircuitBreakerOpenStrategy) GetStrategyDescription() string {
	return "total of opened circuit breaker"
}

// 返回策略名称，通常该名称用作metricName
func (us *CircuitBreakerOpenStrategy) GetStrategyName() string {
	return MetricsNameCircuitBreakerOpen
}

// 根据数据源的内容获取第一次创建metric的时候的初始值
func (us *CircuitBreakerOpenStrategy) InitMetricValue(dataSource interface{}) float64 {
	gauge, ok := dataSource.(model.CircuitBreakGauge)
	if !ok {
		return 0
	}
	status := gauge.CBStatus
	if status == nil {
		return 0
	}
	if status.GetStatus() == model.Open {
		return 1
	}
	return 0
}

// 根据metric自身的value值和聚合数据源T的值来更新metric的value
func (us *CircuitBreakerOpenStrategy) UpdateMetricValue(targetValue StatMetric, dataSource interface{}) {
	gauge, ok := dataSource.(model.CircuitBreakGauge)
	if !ok {
		return
	}
	status := gauge.CBStatus
	if status == nil {
		return
	}
	if status.GetStatus() == model.Open {
		targetValue.Inc()
	}
	if status.GetStatus() == model.HalfOpen {
		targetValue.Dec()
	}
}

type CircuitBreakerHalfOpenStrategy struct {
}

// 返回策略的描述信息
func (us *CircuitBreakerHalfOpenStrategy) GetStrategyDescription() string {
	return "total of half-open circuit breaker"
}

// 返回策略名称，通常该名称用作metricName
func (us *CircuitBreakerHalfOpenStrategy) GetStrategyName() string {
	return MetricsNameCircuitBreakerHalfOpen
}

// 根据数据源的内容获取第一次创建metric的时候的初始值
func (us *CircuitBreakerHalfOpenStrategy) InitMetricValue(dataSource interface{}) float64 {
	gauge, ok := dataSource.(model.CircuitBreakGauge)
	if !ok {
		return 0
	}
	status := gauge.CBStatus
	if status == nil {
		return 0
	}
	if status.GetStatus() == model.HalfOpen {
		return 1
	}
	return 0
}

// 根据metric自身的value值和聚合数据源T的值来更新metric的value
func (us *CircuitBreakerHalfOpenStrategy) UpdateMetricValue(targetValue StatMetric, dataSource interface{}) {
	gauge, ok := dataSource.(model.CircuitBreakGauge)
	if !ok {
		return
	}
	status := gauge.CBStatus
	if status == nil {
		return
	}
	stateful, ok := targetValue.(*StatStatefulMetric)
	if !ok {
		return
	}
	switch status.GetStatus() {
	case model.Open:
		if stateful.MarkedViewContainer.existMarkedName(status.GetCircuitBreaker()) {
			stateful.Dec()
		}
	case model.HalfOpen:
		stateful.MarkedViewContainer.addMarkedName(status.GetCircuitBreaker())
		stateful.Inc()
	case model.Close:
		stateful.MarkedViewContainer.removeMarkedName(status.GetCircuitBreaker())
		stateful.Dec()
	}
}

type RateLimitRequestTotalStrategy struct {
}

// 返回策略的描述信息
func (us *RateLimitRequestTotalStrategy) GetStrategyDescription() string {
	return "total of rate limit per period"
}

// 返回策略名称，通常该名称用作metricName
func (us *RateLimitRequestTotalStrategy) GetStrategyName() string {
	return MetricsNameRateLimitRequestTotal
}

// 根据数据源的内容获取第一次创建metric的时候的初始值
func (us *RateLimitRequestTotalStrategy) InitMetricValue(dataSource interface{}) float64 {
	return 1.0
}

// 根据metric自身的value值和聚合数据源T的值来更新metric的value
func (us *RateLimitRequestTotalStrategy) UpdateMetricValue(targetValue StatMetric, dataSource interface{}) {
	targetValue.Inc()
}

type RateLimitRequestPassStrategy struct {
}

// 返回策略的描述信息
func (us *RateLimitRequestPassStrategy) GetStrategyDescription() string {
	return "total of passed request per period"
}

// 返回策略名称，通常该名称用作metricName
func (us *RateLimitRequestPassStrategy) GetStrategyName() string {
	return MetricsNameRateLimitRequestPass
}

// 根据数据源的内容获取第一次创建metric的时候的初始值
func (us *RateLimitRequestPassStrategy) InitMetricValue(dataSource interface{}) float64 {
	guage, ok := dataSource.(*model.RateLimitGauge)
	if !ok {
		return 0
	}
	if guage.Result == model.QuotaResultOk {
		return 1.0
	}
	return 0
}

// 根据metric自身的value值和聚合数据源T的值来更新metric的value
func (us *RateLimitRequestPassStrategy) UpdateMetricValue(targetValue StatMetric, dataSource interface{}) {
	guage, ok := dataSource.(*model.RateLimitGauge)
	if !ok {
		return
	}
	if guage.Result == model.QuotaResultOk {
		targetValue.Inc()
	}
}

type RateLimitRequestLimitStrategy struct {
}

// 返回策略的描述信息
func (us *RateLimitRequestLimitStrategy) GetStrategyDescription() string {
	return "total of limited request per period"
}

// 返回策略名称，通常该名称用作metricName
func (us *RateLimitRequestLimitStrategy) GetStrategyName() string {
	return MetricsNameRateLimitRequestLimit
}

// 根据数据源的内容获取第一次创建metric的时候的初始值
func (us *RateLimitRequestLimitStrategy) InitMetricValue(dataSource interface{}) float64 {
	guage, ok := dataSource.(*model.RateLimitGauge)
	if !ok {
		return 0
	}
	if guage.Result == model.QuotaResultLimited {
		return 1.0
	}
	return 0
}

// 根据metric自身的value值和聚合数据源T的值来更新metric的value
func (us *RateLimitRequestLimitStrategy) UpdateMetricValue(targetValue StatMetric, dataSource interface{}) {
	guage, ok := dataSource.(*model.RateLimitGauge)
	if !ok {
		return
	}
	if guage.Result == model.QuotaResultLimited {
		targetValue.Inc()
	}
}
