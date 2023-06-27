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
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
)

type StatCollector interface {
	// CollectStatInfo
	CollectStatInfo(info interface{}, metricLabels map[string]string,
		strategies []MetricValueAggregationStrategy, order []string)
	// CollectValues
	CollectValues() []StatMetric
	// RemoveStatMetric
	RemoveStatMetric(signature int64)
}

func NewStatInfoCollector() *StatInfoCollector {
	return &StatInfoCollector{
		metricContainer: &MarkedContainer{
			data: map[int64]StatMetric{},
		},
	}
}

type StatInfoCollector struct {
	metricContainer *MarkedContainer
}

func (sc *StatInfoCollector) GetSignature(metricsName string, labels map[string]string, order []string) int64 {
	labels[MetricNameLabel] = metricsName
	return labelsToSignature(labels, order)
}

// CollectValues
func (sc *StatInfoCollector) CollectValues() []StatMetric {
	return sc.metricContainer.GetValues()
}

// RemoveStatMetric
func (sc *StatInfoCollector) RemoveStatMetric(signature int64) {
	sc.metricContainer.DelValue(signature)
}

func NewStatInfoStatefulCollector() *StatInfoStatefulCollector {
	return &StatInfoStatefulCollector{
		StatInfoCollector: NewStatInfoCollector(),
	}
}

type StatInfoStatefulCollector struct {
	*StatInfoCollector
	mutex sync.RWMutex
}

func (src *StatInfoStatefulCollector) CollectStatInfo(info interface{}, metricLabels map[string]string,
	strategies []MetricValueAggregationStrategy, order []string) {
	if len(strategies) == 0 {
		return
	}
	src.mutex.Lock()
	defer src.mutex.Unlock()

	for i := range strategies {
		strategy := strategies[i]
		metricsName := strategy.GetStrategyName()
		labels := copyLabels(metricLabels)
		signature := src.GetSignature(metricsName, labels, order)

		signatureValue := src.metricContainer.GetValue(signature)
		if signatureValue == nil {
			stateMetric := NewStatStatefulMetric(metricsName, labels, signature)
			stateMetric.Set(int64(strategy.InitMetricValue(info)))
			src.metricContainer.PutValue(signature, stateMetric)
			continue
		}

		revisionMetrics, ok := signatureValue.(*StatStatefulMetric)
		if !ok {
			continue
		}
		strategy.UpdateMetricValue(revisionMetrics, info)
	}
}

func NewStatInfoRevisionCollector() *StatInfoRevisionCollector {
	return &StatInfoRevisionCollector{
		StatInfoCollector: NewStatInfoCollector(),
		currentRevision:   0,
	}
}

type StatInfoRevisionCollector struct {
	*StatInfoCollector
	mutex           sync.RWMutex
	currentRevision int64
}

func (src *StatInfoRevisionCollector) GetCurrentRevision() int64 {
	return atomic.LoadInt64(&src.currentRevision)
}

func (src *StatInfoRevisionCollector) CollectStatInfo(info interface{}, metricLabels map[string]string,
	strategies []MetricValueAggregationStrategy, order []string) {
	if len(strategies) == 0 {
		return
	}
	src.mutex.Lock()
	defer src.mutex.Unlock()

	for i := range strategies {
		strategy := strategies[i]
		metricsName := strategy.GetStrategyName()
		labels := copyLabels(metricLabels)
		signature := src.GetSignature(metricsName, labels, order)
		_, useAvg := strategy.(AvgMetricValueAggregationStrategy)

		signatureValue := src.metricContainer.GetValue(signature)
		if signatureValue == nil {
			stateMetric := NewStatRevisionMetric(metricsName, labels, signature, src.GetCurrentRevision())
			if useAvg {
				stateMetric = NewAvgStatRevisionMetric(metricsName, labels, signature, src.GetCurrentRevision())
			}
			initVal := int64(strategy.InitMetricValue(info))
			stateMetric.Set(initVal)
			src.metricContainer.PutValue(signature, stateMetric)
			continue
		}

		revisionMetrics, ok := signatureValue.(*StatRevisionMetric)
		if !ok {
			continue
		}
		if revisionMetrics.GetRevision() != src.GetCurrentRevision() {
			revisionMetrics.Set(int64(strategy.InitMetricValue(info)))
			revisionMetrics.UpdateRevision(src.GetCurrentRevision())
			continue
		}
		strategy.UpdateMetricValue(revisionMetrics, info)
	}
}

func copyLabels(labels map[string]string) map[string]string {
	ret := map[string]string{}
	for k, v := range labels {
		ret[k] = v
	}
	return ret
}

const (
	RevisionMaxScope = 2
)

func PutDataFromContainerInOrder(metricVecCaches map[string]*prometheus.GaugeVec, collector StatCollector,
	currentRevision int64) {
	values := collector.CollectValues()
	for i := range values {
		metricValue := values[i]
		gauge, ok := metricVecCaches[metricValue.MetricName()]
		if !ok {
			continue
		}
		switch rs := metricValue.(type) {
		case *StatRevisionMetric:
			if rs.GetRevision() < currentRevision-RevisionMaxScope {
				// 如果连续两个版本还没有数据，就清除该数据
				gauge.Delete(rs.GetLabels())
				collector.RemoveStatMetric(rs.GetSignature())
				continue
			}
			if rs.GetRevision() < currentRevision {
				// 如果版本为老版本，则清零数据
				gauge.Delete(rs.GetLabels())
				gauge.With(rs.GetLabels()).Set(0)
				continue
			}
		}
		gauge.With(metricValue.GetLabels()).Set(float64(metricValue.GetValue()))
	}
}
