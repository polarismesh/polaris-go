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

// Package stats tracks the statistics associated with benchmark runs.
package stats

import (
	"bytes"
	"fmt"
	"log"
	"math"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"time"
)

// FeatureIndex is an enum for features that usually differ across individual
// benchmark runs in a single execution. These are usually configured by the
// user through command line flags.
type FeatureIndex int

// FeatureIndex enum values corresponding to individually settable features.
const (
	MaxConcurrentCallsIndex FeatureIndex = iota
	// MaxFeatureIndex is a place holder to indicate the total number of feature
	// indices we have. Any new feature indices should be added above this.
	MaxFeatureIndex
)

// Features represent configured options for a specific benchmark run. This is
// usually constructed from command line arguments passed by the caller. See
// benchmark/benchmain/main.go for defined command line flags. This is also
// part of the BenchResults struct which is serialized and written to a file.
type Features struct {
	// BenchTime indicates the duration of the benchmark run.
	BenchTime time.Duration
	// MaxConcurrentCalls is the number of concurrent RPCs made during this
	// benchmark run.
	MaxConcurrentCalls int
	// Instance count in one service
	Namespace string
	// Metadata count in each instance
	Service string
	// size for client send message
	ArraySize int
}

// String returns all the feature values as a string.
func (f Features) String() string {
	return fmt.Sprintf("benchTime_%v-maxConcurrentCalls_%v-namespace_%s-service_%s",
		f.BenchTime, f.MaxConcurrentCalls, f.Namespace, f.Service)
}

// PrintableName returns a one line name which includes the features specified
// by 'wantFeatures' which is a bitmask of wanted features, indexed by
// FeaturesIndex.
func (f Features) PrintableName(wantFeatures []bool) string {
	var b bytes.Buffer
	f.partialString(&b, wantFeatures, "_", "-")
	return b.String()
}

// partialString writes features specified by 'wantFeatures' to the provided
// bytes.Buffer.
func (f Features) partialString(buf *bytes.Buffer, wantFeatures []bool, sep, delim string) {
	for i, sf := range wantFeatures {
		if sf {
			switch FeatureIndex(i) {
			case MaxConcurrentCallsIndex:
				buf.WriteString(fmt.Sprintf("Callers%v%v%v", sep, f.MaxConcurrentCalls, delim))
			default:
				log.Fatalf("Unknown feature index %v. maxFeatureIndex is %v", i, MaxFeatureIndex)
			}
		}
	}
}

// BenchResults records features and results of a benchmark run. A collection
// of these structs is usually serialized and written to a file after a
// benchmark execution, and could later be read for pretty-printing or
// comparison with other benchmark results.
type BenchResults struct {
	// TestOperation is the workload mode for this benchmark run. This could be unary,
	// stream or unconstrained.
	TestOperation string
	// Features represents the configured feature options for this run.
	Features Features
	// Data contains the statistical data of interest from the benchmark run.
	Data RunData
}

// RunData contains statistical data of interest from a benchmark run.
type RunData struct {
	// TotalOps is the number of operations executed during this benchmark run.
	// Only makes sense for unary and streaming workloads.
	TotalOps uint64
	// SendOps is the number of send operations executed during this benchmark
	// run. Only makes sense for unconstrained workloads.
	SendOps uint64
	// RecvOps is the number of receive operations executed during this benchmark
	// run. Only makes sense for unconstrained workloads.
	RecvOps uint64
	// AllocedBytes is the average memory allocation in bytes per operation.
	AllocedBytes float64
	// Allocs is the average number of memory allocations per operation.
	Allocs float64
	// Fiftieth is the 50th percentile latency.
	Fiftieth time.Duration
	// Ninetieth is the 90th percentile latency.
	Ninetieth time.Duration
	// Ninetyninth is the 99th percentile latency.
	NinetyNinth time.Duration
	// Average is the average latency.
	Average time.Duration
}

type durationSlice []time.Duration

// Len .
func (a durationSlice) Len() int { return len(a) }

// Swap .
func (a durationSlice) Swap(i, j int) { a[i], a[j] = a[j], a[i] }

// Less .
func (a durationSlice) Less(i, j int) bool { return a[i] < a[j] }

// Stats is a helper for gathering statistics about individual benchmark runs.
type Stats struct {
	mu         sync.Mutex
	numBuckets int
	hw         *histWrapper
	results    []BenchResults
	startMS    runtime.MemStats
	stopMS     runtime.MemStats
}

// histWrapper
type histWrapper struct {
	unit      time.Duration
	histogram *Histogram
	durations durationSlice
}

// NewStats creates a new Stats instance. If numBuckets is not positive, the
// default value (16) will be used.
func NewStats(numBuckets int) *Stats {
	if numBuckets <= 0 {
		numBuckets = 16
	}
	// Use one more bucket for the last unbounded bucket.
	s := &Stats{numBuckets: numBuckets + 1}
	s.hw = &histWrapper{}
	return s
}

// StartRun is to be invoked to indicate the start of a new benchmark run.
func (s *Stats) StartRun(operation string, f Features) {
	s.mu.Lock()
	defer s.mu.Unlock()

	runtime.ReadMemStats(&s.startMS)
	s.results = append(s.results, BenchResults{TestOperation: operation, Features: f})
}

// EndRun is to be invoked to indicate the end of the ongoing benchmark run. It
// computes a bunch of stats and dumps them to stdout.
func (s *Stats) EndRun(count uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	runtime.ReadMemStats(&s.stopMS)
	r := &s.results[len(s.results)-1]
	r.Data = RunData{
		TotalOps:     count,
		AllocedBytes: float64(s.stopMS.TotalAlloc-s.startMS.TotalAlloc) / float64(count),
		Allocs:       float64(s.stopMS.Mallocs-s.startMS.Mallocs) / float64(count),
	}
	s.computeLatencies(r)
	s.dump(r)
	s.hw = &histWrapper{}
}

// EndUnconstrainedRun is similar to EndRun, but is to be used for
// unconstrained workloads.
func (s *Stats) EndUnconstrainedRun(req uint64, resp uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	runtime.ReadMemStats(&s.stopMS)
	r := &s.results[len(s.results)-1]
	r.Data = RunData{
		SendOps:      req,
		RecvOps:      resp,
		AllocedBytes: float64(s.stopMS.TotalAlloc-s.startMS.TotalAlloc) / float64((req+resp)/2),
		Allocs:       float64(s.stopMS.Mallocs-s.startMS.Mallocs) / float64((req+resp)/2),
	}
	s.computeLatencies(r)
	s.dump(r)
	s.hw = &histWrapper{}
}

// AddDuration adds an elapsed duration per operation to the stats. This is
// used by unary and stream modes where request and response stats are equal.
func (s *Stats) AddDuration(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.hw.durations = append(s.hw.durations, d)
}

// GetResults returns the results from all benchmark runs.
func (s *Stats) GetResults() []BenchResults {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.results
}

// computeLatencies computes percentile latencies based on durations stored in
// the stats object and updates the corresponding fields in the result object.
func (s *Stats) computeLatencies(result *BenchResults) {
	if len(s.hw.durations) == 0 {
		return
	}
	sort.Sort(s.hw.durations)
	minDuration := int64(s.hw.durations[0])
	maxDuration := int64(s.hw.durations[len(s.hw.durations)-1])

	// Use the largest unit that can represent the minimum time duration.
	s.hw.unit = time.Nanosecond
	for _, u := range []time.Duration{time.Microsecond, time.Millisecond, time.Second} {
		if minDuration <= int64(u) {
			break
		}
		s.hw.unit = u
	}

	numBuckets := s.numBuckets
	if n := int(maxDuration - minDuration + 1); n < numBuckets {
		numBuckets = n
	}
	s.hw.histogram = NewHistogram(HistogramOptions{
		NumBuckets: numBuckets,
		// max-min(lower bound of last bucket) = (1 + growthFactor)^(numBuckets-2) * baseBucketSize.
		GrowthFactor:   math.Pow(float64(maxDuration-minDuration), 1/float64(numBuckets-2)) - 1,
		BaseBucketSize: 1.0,
		MinValue:       minDuration,
	})
	for _, d := range s.hw.durations {
		_ = s.hw.histogram.Add(int64(d))
	}
	result.Data.Fiftieth = s.hw.durations[max(s.hw.histogram.Count*int64(50)/100-1, 0)]
	result.Data.Ninetieth = s.hw.durations[max(s.hw.histogram.Count*int64(90)/100-1, 0)]
	result.Data.NinetyNinth = s.hw.durations[max(s.hw.histogram.Count*int64(99)/100-1, 0)]
	result.Data.Average = time.Duration(float64(s.hw.histogram.Sum) / float64(s.hw.histogram.Count))
}

// dump returns a printable version.
func (s *Stats) dump(result *BenchResults) {
	var buf bytes.Buffer
	// This prints the run mode and all features of the bench on a line.
	buf.WriteString(fmt.Sprintf("%s-%s:\n", result.TestOperation, result.Features.String()))
	unit := s.hw.unit
	tUnit := fmt.Sprintf("%v", unit)[1:] // stores one of s, ms, μs, ns

	if l := result.Data.Fiftieth; l != 0 {
		buf.WriteString(fmt.Sprintf("50_Latency: %s%s\t", strconv.FormatFloat(float64(l)/float64(unit), 'f', 4, 64), tUnit))
	}
	if l := result.Data.Ninetieth; l != 0 {
		buf.WriteString(fmt.Sprintf("90_Latency: %s%s\t", strconv.FormatFloat(float64(l)/float64(unit), 'f', 4, 64), tUnit))
	}
	if l := result.Data.NinetyNinth; l != 0 {
		buf.WriteString(fmt.Sprintf("99_Latency: %s%s\t", strconv.FormatFloat(float64(l)/float64(unit), 'f', 4, 64), tUnit))
	}
	if l := result.Data.Average; l != 0 {
		buf.WriteString(fmt.Sprintf("Avg_Latency: %s%s\t", strconv.FormatFloat(float64(l)/float64(unit), 'f', 4, 64), tUnit))
	}
	buf.WriteString(fmt.Sprintf("Bytes/op: %v\t", result.Data.AllocedBytes))
	buf.WriteString(fmt.Sprintf("Allocs/op: %v\t\n", result.Data.Allocs))

	// This prints the histogram stats for the latency.
	if s.hw.histogram == nil {
		buf.WriteString("Histogram (empty)\n")
	} else {
		buf.WriteString(fmt.Sprintf("Histogram (unit: %s)\n", tUnit))
		s.hw.histogram.PrintWithUnit(&buf, float64(unit))
	}

	// Print throughput data.
	req := result.Data.SendOps
	if req == 0 {
		req = result.Data.TotalOps
	}
	resp := result.Data.RecvOps
	if resp == 0 {
		resp = result.Data.TotalOps
	}
	seconds := result.Features.BenchTime.Seconds()
	buf.WriteString(fmt.Sprintf("Number of requests per second:  %v\tRequest\n", req/uint64(seconds)))
	buf.WriteString(fmt.Sprintf("Number of responses per second: %v\tResponse\n", resp/uint64(seconds)))
	fmt.Println(buf.String())
}

// max
func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
