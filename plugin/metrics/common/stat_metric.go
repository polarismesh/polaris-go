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
	"sync/atomic"
)

type StatMetric interface {
	MetricName() string
	Inc()
	GetValue() float64
	Add(v int64)
	Dec()
	Set(v int64)
	CompareAndSwap(oldV, newV int64) bool
	GetLabels() map[string]string
	GetSignature() int64
}

func NewAvgStatMetricWithSignature(metricName string, labels map[string]string, signature int64) *avgStatMetric {
	return &avgStatMetric{
		metricsName: metricName,
		Labels:      labels,
		Signature:   signature,
		value:       0,
	}
}

func NewStatMetricWithSignature(metricName string, labels map[string]string, signature int64) *statMetric {
	return &statMetric{
		metricsName: metricName,
		Labels:      labels,
		Signature:   signature,
		value:       0,
	}
}

type statMetric struct {
	metricsName string
	Labels      map[string]string
	Signature   int64
	value       int64
}

func (s *statMetric) GetLabels() map[string]string {
	return s.Labels
}

func (s *statMetric) MetricName() string {
	return s.metricsName
}

func (s *statMetric) GetValue() float64 {
	return float64(atomic.LoadInt64(&s.value))
}

func (s *statMetric) Add(v int64) {
	atomic.AddInt64(&s.value, v)
}

func (s *statMetric) Inc() {
	atomic.AddInt64(&s.value, 1)
}

func (s *statMetric) Dec() {
	atomic.AddInt64(&s.value, -1)
}

func (s *statMetric) Set(v int64) {
	atomic.AddInt64(&s.value, v)
}

func (s *statMetric) CompareAndSwap(oldV, newV int64) bool {
	return atomic.CompareAndSwapInt64(&s.value, oldV, newV)
}

func (s *statMetric) GetSignature() int64 {
	return s.Signature
}

type avgStatMetric struct {
	callTimes   int64
	metricsName string
	Labels      map[string]string
	Signature   int64
	value       int64
}

func (s *avgStatMetric) GetLabels() map[string]string {
	return s.Labels
}

func (s *avgStatMetric) MetricName() string {
	return s.metricsName
}

func (s *avgStatMetric) GetValue() float64 {
	return float64(atomic.LoadInt64(&s.value)) / float64(atomic.LoadInt64(&s.callTimes))
}

func (s *avgStatMetric) Add(v int64) {
	atomic.AddInt64(&s.callTimes, 1)
	atomic.AddInt64(&s.value, v)
}

func (s *avgStatMetric) Inc() {
	atomic.AddInt64(&s.callTimes, 1)
	atomic.AddInt64(&s.value, 1)
}

func (s *avgStatMetric) Dec() {
	atomic.AddInt64(&s.callTimes, 1)
	atomic.AddInt64(&s.value, -1)
}

func (s *avgStatMetric) Set(v int64) {
	atomic.AddInt64(&s.callTimes, 1)
	atomic.AddInt64(&s.value, v)
}

func (s *avgStatMetric) CompareAndSwap(oldV, newV int64) bool {
	return true
}

func (s *avgStatMetric) GetSignature() int64 {
	return s.Signature
}

func NewAvgStatRevisionMetric(metricName string, labels map[string]string,
	signature, revision int64) *StatRevisionMetric {
	return &StatRevisionMetric{
		StatMetric: NewAvgStatMetricWithSignature(metricName, labels, signature),
		Revision:   revision,
	}
}

func NewStatRevisionMetric(metricName string, labels map[string]string,
	signature, revision int64) *StatRevisionMetric {
	return &StatRevisionMetric{
		StatMetric: NewStatMetricWithSignature(metricName, labels, signature),
		Revision:   revision,
	}
}

type StatRevisionMetric struct {
	StatMetric
	Revision int64
}

func (sm *StatRevisionMetric) GetRevision() int64 {
	return atomic.LoadInt64(&sm.Revision)
}

func (sm *StatRevisionMetric) UpdateRevision(r int64) {
	atomic.StoreInt64(&sm.Revision, r)
}

func NewStatStatefulMetric(metricName string, labels map[string]string, signature int64) *StatStatefulMetric {
	return &StatStatefulMetric{
		statMetric:          NewStatMetricWithSignature(metricName, labels, signature),
		MarkedViewContainer: NewMarkedViewContainer(),
	}
}

func NewStatStatefulMetricWithMarkedContainer(metricName string, labels map[string]string, markedContainer *MarkedViewContainer,
	signature int64) *StatStatefulMetric {
	return &StatStatefulMetric{
		statMetric:          NewStatMetricWithSignature(metricName, labels, signature),
		MarkedViewContainer: markedContainer,
	}
}

type StatStatefulMetric struct {
	*statMetric
	MarkedViewContainer *MarkedViewContainer
}
