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
//@Time: 2021/10/19 14:06

package prometheus

import (
	"math"
	"sync"
	"sync/atomic"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	// SystemMetricName
	CalleeNamespace = "callee_namespace"
	CalleeService   = "callee_service"
	CalleeSubset    = "callee_subset"
	CalleeInstance  = "callee_instance"
	CalleeRetCode   = "callee_result_code"
	CalleeMethod    = "callee_method"
	CallerNamespace = "caller_namespace"
	CallerService   = "caller_service"
	CallerIP        = "caller_ip"
	CallerLabels    = "caller_labels"
	MetricNameLabel = "metric_name"

	// SystemMetricValue
	NilValue = "__NULL__"
)

var (
	InstanceGaugeLabelOrder []string = []string{
		CalleeNamespace,
		CalleeService,
		CalleeSubset,
		CalleeInstance,
		CalleeRetCode,
		CallerLabels,
		CallerNamespace,
		CallerService,
		CallerIP,
		MetricNameLabel,
	}

	RateLimitGaugeLabelOrder []string = []string{
		CalleeNamespace,
		CalleeService,
		CalleeMethod,
		CallerLabels,
		MetricNameLabel,
	}

	CircuitBreakerGaugeLabelOrder []string = []string{
		CalleeNamespace,
		CalleeService,
		CalleeMethod,
		CalleeSubset,
		CalleeInstance,
		CallerNamespace,
		CallerService,
		CallerIP,
		MetricNameLabel,
	}
)

// StatMetric
type StatMetric struct {
	MetricName string
	Labels     map[string]string
	Signature  int64
	Value      uint64
	prometheus.Gauge
}

// NewStatMetric
func NewStatMetric(metricName string, labels map[string]string) *StatMetric {
	return &StatMetric{
		MetricName: metricName,
		Labels:     labels,
		Signature:  LabelsToSignature(labels),
		Value:      0.0,
	}
}

// NewStatMetricWithSignature
func NewStatMetricWithSignature(metricName string, labels map[string]string, signature int64) *StatMetric {
	return &StatMetric{
		MetricName: metricName,
		Labels:     labels,
		Signature:  signature,
		Value:      0.0,
	}
}

func (metric *StatMetric) Get() float64 {
	return math.Float64frombits(atomic.LoadUint64(&metric.Value))
}

func (metric *StatMetric) Set(val float64) {
	atomic.StoreUint64(&metric.Value, math.Float64bits(val))
}

func (metric *StatMetric) Inc() {
	metric.Add(1.0)
}

func (metric *StatMetric) Dec() {
	metric.Add(-1.0)
}

func (metric *StatMetric) Add(val float64) {
	for {
		oldBits := atomic.LoadUint64(&metric.Value)
		newBits := math.Float64bits(math.Float64frombits(oldBits) + val)
		if atomic.CompareAndSwapUint64(&metric.Value, oldBits, newBits) {
			return
		}
	}
}

func (metric *StatMetric) CompareAndSwap(expect, update float64) bool {
	return atomic.CompareAndSwapUint64(&metric.Value, math.Float64bits(expect), math.Float64bits(update))
}

// StatRevisionMetric
type StatRevisionMetric struct {
	*StatMetric
	revision int64
}

// NewStatRevisionMetric
func NewStatRevisionMetric(metricName string, labels map[string]string, signature, revision int64) *StatRevisionMetric {
	return &StatRevisionMetric{
		StatMetric: NewStatMetricWithSignature(metricName, labels, signature),
		revision:   revision,
	}
}

// GetRevision
func (metric *StatRevisionMetric) GetRevision() int64 {
	return atomic.LoadInt64(&metric.revision)
}

// SetRevision
func (metric *StatRevisionMetric) SetRevision(revision int64) {
	atomic.StoreInt64(&metric.revision, revision)
}

// MetricValueAggregationStrategy
type MetricValueAggregationStrategy interface {
	StrategyDescription() string

	StrategyName() string

	UpdateMetricValue(targetValue *StatMetric, dataSource interface{})

	InitMetricValue(dataSource interface{}) float64
}

// StatInfoCollector
type StatInfoCollector interface {
	CollectStatInfo(info interface{}, metricLabels map[string]string, strategies []MetricValueAggregationStrategy)

	CollectedValues() []interface{}
}

func NewStatInfoRevisionCollector() *StatInfoRevisionCollector {
	return &StatInfoRevisionCollector{
		container:       model.NewConcurrentMap(),
		lock:            &sync.RWMutex{},
		currentRevision: 0,
	}
}

// StatInfoRevisionCollector
type StatInfoRevisionCollector struct {
	lock *sync.RWMutex
	// map[int64]*StatRevisionMetric
	container       *model.ConcurrentMap
	currentRevision int64
}

// CollectStatInfo
func (collector *StatInfoRevisionCollector) CollectStatInfo(info interface{}, metricLabels map[string]string, strategies []MetricValueAggregationStrategy) {
	if len(strategies) == 0 {
		return
	}

	for _, strategy := range strategies {
		metricName := strategy.StrategyName()
		signature := LabelsToSignature(metricLabels)

		exist, val := collector.container.ComputeIfAbsent(signature, func(key interface{}) interface{} {
			return NewStatRevisionMetric(metricName, CopyLabels(metricLabels), signature, atomic.LoadInt64(&collector.currentRevision))
		})
		if !exist {
			continue
		}

		metrics := val.(*StatRevisionMetric)

		if metrics.GetRevision() != atomic.LoadInt64(&collector.currentRevision) {
			metrics.Set(strategy.InitMetricValue(info))
			metrics.SetRevision(atomic.LoadInt64(&collector.currentRevision))
			continue
		}

		strategy.UpdateMetricValue(metrics.StatMetric, info)
	}
}

// CollectedValues
func (collector *StatInfoRevisionCollector) CollectedValues() []interface{} {
	return collector.container.Values()
}

// StatStatefulMetric
type StatStatefulMetric struct {
	*StatMetric
	lock            *sync.RWMutex
	markedContainer map[string]struct{}
}

// NewStatStatefulMetric
func NewStatStatefulMetric(metricName string, labels map[string]string, signature int64) *StatStatefulMetric {
	return &StatStatefulMetric{
		StatMetric:      NewStatMetricWithSignature(metricName, labels, signature),
		lock:            &sync.RWMutex{},
		markedContainer: make(map[string]struct{}),
	}
}

// AddMarkedName
func (metric *StatStatefulMetric) AddMarkedName(markedName string) bool {
	lock := metric.lock

	lock.Lock()
	defer lock.Unlock()

	metric.markedContainer[markedName] = struct{}{}
	return true
}

// RemoveMarkedName
func (metric *StatStatefulMetric) RemoveMarkedName(markedName string) bool {
	lock := metric.lock

	lock.Lock()
	defer lock.Unlock()

	delete(metric.markedContainer, markedName)
	return true
}

// ExistMarkedName
func (metric *StatStatefulMetric) ExistMarkedName(markedName string) bool {
	lock := metric.lock

	lock.RLock()
	defer lock.RUnlock()

	_, exist := metric.markedContainer[markedName]
	return exist
}

func NewStatInfoStatefulCollector() *StatInfoStatefulCollector {
	return &StatInfoStatefulCollector{
		container: model.NewConcurrentMap(),
	}
}

// StatInfoStatefulCollector
type StatInfoStatefulCollector struct {
	// map[int64]*StatStatefulMetric
	container *model.ConcurrentMap
}

// CollectStatInfo
func (collector *StatInfoStatefulCollector) CollectStatInfo(info interface{}, metricLabels map[string]string, strategies []MetricValueAggregationStrategy) {
	if len(strategies) == 0 {
		return
	}

	for _, strategy := range strategies {
		metricName := strategy.StrategyName()
		signature := LabelsToSignature(metricLabels)

		exist, val := collector.container.ComputeIfAbsent(signature, func(key interface{}) interface{} {
			return NewStatStatefulMetric(metricName, CopyLabels(metricLabels), signature)
		})
		if !exist {
			continue
		}

		metrics := val.(*StatStatefulMetric)
		strategy.UpdateMetricValue(metrics.StatMetric, info)
	}
}

// CollectedValues
func (collector *StatInfoStatefulCollector) CollectedValues() []interface{} {
	return collector.container.Values()
}

// StatInfoCollectorContainer
type StatInfoCollectorContainer struct {
	insCollector            *StatInfoRevisionCollector
	rateLimitCollector      *StatInfoStatefulCollector
	circuitBreakerCollector *StatInfoStatefulCollector
}

// NewStatInfoCollectorContainer
func NewStatInfoCollectorContainer() *StatInfoCollectorContainer {
	return &StatInfoCollectorContainer{
		insCollector:            NewStatInfoRevisionCollector(),
		rateLimitCollector:      NewStatInfoStatefulCollector(),
		circuitBreakerCollector: NewStatInfoStatefulCollector(),
	}
}

// GetInsCollector
func (container *StatInfoCollectorContainer) GetInsCollector() *StatInfoRevisionCollector {
	return container.insCollector
}

// GetRatelimitCollector
func (container *StatInfoCollectorContainer) GetRatelimitCollector() *StatInfoStatefulCollector {
	return container.rateLimitCollector
}

// GetCircuitBreakerCollector
func (container *StatInfoCollectorContainer) GetCircuitBreakerCollector() *StatInfoStatefulCollector {
	return container.circuitBreakerCollector
}

// GetCollectors
func (container *StatInfoCollectorContainer) GetCollectors() []StatInfoCollector {
	return []StatInfoCollector{
		container.insCollector,
		container.rateLimitCollector,
		container.circuitBreakerCollector,
	}
}
