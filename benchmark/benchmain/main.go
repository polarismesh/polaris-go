/**
 * Tencent is pleased to support the open source community by making CL5 available.
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

package main

import (
	"encoding/gob"
	"flag"
	"fmt"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/benchmark/flags"
	"github.com/polarismesh/polaris-go/benchmark/stats"
	"github.com/polarismesh/polaris-go/pkg/model"
	"log"
	"math/rand"
	"os"
	"reflect"
	"runtime"
	"runtime/pprof"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	numStatsBuckets = 10
	warmupCallCount = 1

	operationRpcDirect = "rpcDirect"
	operationRpcNaming = "rpcNaming"
)

var (
	defaultMaxConcurrentCalls = []int{1, 8, 64, 512}
	allOperations             = []string{operationRpcDirect, operationRpcNaming}
)

var (
	workloads = flags.StringWithAllowedValues("workloads", operationRpcDirect,
		fmt.Sprintf("Workloads to execute - One of: %v", strings.Join(allOperations, ", ")), allOperations)
	maxConcurrentCalls = flags.IntSlice(
		"maxConcurrentCalls", defaultMaxConcurrentCalls, "Number of concurrent RPCs during benchmarks")
	benchTime = flag.Duration(
		"benchtime", time.Second, "Configures the amount of time to run each benchmark")
	memProfile = flag.String(
		"memProfile", "", "Enables memory profiling output to the filename provided.")
	memProfileRate = flag.Int("memProfileRate", 512*1024, "Configures the memory profiling rate. \n"+
		"memProfile should be set before setting profile rate. To include every allocated block in the profile, "+
		"set MemProfileRate to 1. To turn off profiling entirely, set MemProfileRate to 0. 512 * 1024 by default.")
	mutexProfile = flag.String(
		"mutexProfile", "", "Enables mutex profiling output to the filename provided.")
	mutexProfileRate = flag.Int("mutexProfileRate", 1, "Configures the mutex profiling rate. \n")
	cpuProfile       = flag.String(
		"cpuProfile", "", "Enables CPU profiling output to the filename provided")

	benchmarkResultFile = flag.String(
		"resultFile", "", "Save the benchmark result into a binary file")
	namespace = flag.String("namespace", "", "The namespace of the testing service.")
	service   = flag.String("service", "", "The service name of the testing service.")
	arraySize = flag.Int("arraySize", 25, "Config the size for client send message")
)

// runModes indicates the workloads to run. This is initialized with a call to
// `runModesFromWorkloads`, passing the workloads flag set by the user.
type runModes struct {
	rpcDirect, rpcNaming bool
}

// runModesFromWorkloads determines the runModes based on the value of
// workloads flag set by the user.
func runModesFromWorkloads(workload string) runModes {
	r := runModes{}
	switch workload {
	case operationRpcDirect:
		r.rpcDirect = true
	case operationRpcNaming:
		r.rpcNaming = true
	default:
		log.Fatalf("Unknown workloads setting: %v (want one of: %v)",
			workloads, strings.Join(allOperations, ", "))
	}
	return r
}

type startFunc func(mode string, bf stats.Features)
type stopFunc func(count uint64)
type rpcCallFunc func(pos int, ctx *runContext)
type rpcCleanupFunc func()

// benchOpts represents all configurable options available while running this
// benchmark. This is built from the values passed as flags.
type benchOpts struct {
	rModes              runModes
	benchTime           time.Duration
	memProfileRate      int
	memProfile          string
	mutexProfile        string
	mutexProfileRate    int
	cpuProfile          string
	benchmarkResultFile string
	features            *featureOpts
}

// featureOpts represents options which can have multiple values. The user
// usually provides a comma-separated list of options for each of these
// features through command line flags. We generate all possible combinations
// for the provided values and run the benchmarks for each combination.
type featureOpts struct {
	namespace          string
	service            string
	maxConcurrentCalls []int
}

// processFlags reads the command line flags and builds benchOpts. Specifying
// invalid values for certain flags will cause flag.Parse() to fail, and the
// program to terminate.
// This *SHOULD* be the only place where the flags are accessed. All other
// parts of the benchmark code should rely on the returned benchOpts.
func processFlags() *benchOpts {
	flag.Parse()
	if flag.NArg() != 0 {
		log.Fatal("Error: unparsed arguments: ", flag.Args())
	}

	opts := &benchOpts{
		rModes:              runModesFromWorkloads(*workloads),
		benchTime:           *benchTime,
		memProfileRate:      *memProfileRate,
		memProfile:          *memProfile,
		cpuProfile:          *cpuProfile,
		mutexProfile:        *mutexProfile,
		mutexProfileRate:    *mutexProfileRate,
		benchmarkResultFile: *benchmarkResultFile,
		features: &featureOpts{
			namespace:          *namespace,
			service:            *service,
			maxConcurrentCalls: append([]int(nil), *maxConcurrentCalls...),
		},
	}
	return opts
}

// before testing function
func before(opts *benchOpts) {
	if opts.memProfile != "" {
		runtime.MemProfileRate = opts.memProfileRate
	}
	if opts.mutexProfile != "" {
		runtime.SetMutexProfileFraction(opts.mutexProfileRate)
	}
	if opts.cpuProfile != "" {
		f, err := os.Create(opts.cpuProfile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "testing: %s\n", err)
			return
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			fmt.Fprintf(os.Stderr, "testing: can't start cpu profile: %s\n", err)
			f.Close()
			return
		}
	}
}

// write the prof result into file
func writeProfTo(name, fn string) {
	p := pprof.Lookup(name)
	if p == nil {
		fmt.Fprintf(os.Stderr, "%s prof not found", name)
		return
	}
	f, err := os.Create(fn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v", err.Error())
		return
	}
	defer f.Close()
	err = p.WriteTo(f, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v", err.Error())
		return
	}
}

// generateFeatures generates all combinations of the provided feature options.
// While all the feature options are stored in the benchOpts struct, the input
// parameter 'featuresNum' is a slice indexed by 'featureIndex' enum containing
// the number of values for each feature.
// For example, let's say the user sets -workloads=all and
// -maxConcurrentCalls=1,100, this would end up with the following
// combinations:
// [workloads: unary, maxConcurrentCalls=1]
// [workloads: unary, maxConcurrentCalls=1]
// [workloads: streaming, maxConcurrentCalls=100]
// [workloads: streaming, maxConcurrentCalls=100]
// [workloads: unconstrained, maxConcurrentCalls=1]
// [workloads: unconstrained, maxConcurrentCalls=100]
func (b *benchOpts) generateFeatures(featuresNum []int) []stats.Features {
	// curPos and initialPos are two slices where each value acts as an index
	// into the appropriate feature slice maintained in benchOpts.features. This
	// loop generates all possible combinations of features by changing one value
	// at a time, and once curPos becomes equal to initialPos, we have explored
	// all options.
	var result []stats.Features
	var curPos []int
	initialPos := make([]int, stats.MaxFeatureIndex)
	for !reflect.DeepEqual(initialPos, curPos) {
		if curPos == nil {
			curPos = make([]int, stats.MaxFeatureIndex)
		}
		feature := stats.Features{
			// These features stay the same for each iteration.
			BenchTime: b.benchTime,
			// These features can potentially change for each iteration.
			MaxConcurrentCalls: b.features.maxConcurrentCalls[curPos[stats.MaxConcurrentCallsIndex]],
			Namespace:          b.features.namespace,
			Service:            b.features.service,
			ArraySize:          *arraySize,
		}
		result = append(result, feature)
		addOne(curPos, featuresNum)
	}
	return result
}

// addOne mutates the input slice 'features' by changing one feature, thus
// arriving at the next combination of feature values. 'featuresMaxPosition'
// provides the numbers of allowed values for each feature, indexed by
// 'featureIndex' enum.
func addOne(features []int, featuresMaxPosition []int) {
	for i := len(features) - 1; i >= 0; i-- {
		features[i] = (features[i] + 1)
		if features[i]/featuresMaxPosition[i] == 0 {
			break
		}
		features[i] = features[i] % featuresMaxPosition[i]
	}
}

// after testing function
func after(opts *benchOpts, data []stats.BenchResults) {
	if opts.cpuProfile != "" {
		pprof.StopCPUProfile() // flushes profile to disk
	}
	if opts.memProfile != "" {
		f, err := os.Create(opts.memProfile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "testing: %s\n", err)
			os.Exit(2)
		}
		runtime.GC() // materialize all statistics
		if err = pprof.WriteHeapProfile(f); err != nil {
			fmt.Fprintf(os.Stderr, "testing: can't write heap profile %s: %s\n", opts.memProfile, err)
			os.Exit(2)
		}
		f.Close()
	}
	if opts.mutexProfile != "" {
		writeProfTo("mutex", opts.mutexProfile)
	}
	if opts.benchmarkResultFile != "" {
		f, err := os.Create(opts.benchmarkResultFile)
		if err != nil {
			log.Fatalf("testing: can't write benchmark result %s: %s\n", opts.benchmarkResultFile, err)
		}
		dataEncoder := gob.NewEncoder(f)
		dataEncoder.Encode(data)
		f.Close()
	}
}

// makeFeaturesNum returns a slice of ints of size 'maxFeatureIndex' where each
// element of the slice (indexed by 'featuresIndex' enum) contains the number
// of features to be exercised by the benchmark code.
// For example: Index 0 of the returned slice contains the number of values for
// enableTrace feature, while index 1 contains the number of value of
// readLatencies feature and so on.
func makeFeaturesNum(b *benchOpts) []int {
	featuresNum := make([]int, stats.MaxFeatureIndex)
	for i := 0; i < len(featuresNum); i++ {
		switch stats.FeatureIndex(i) {
		case stats.MaxConcurrentCallsIndex:
			featuresNum[i] = len(b.features.maxConcurrentCalls)
		default:
			log.Fatalf("Unknown feature index %v in generateFeatures. maxFeatureIndex is %v", i, stats.MaxFeatureIndex)
		}
	}
	return featuresNum
}

// benchmark for the rpc with naming
func rpcNamingBenchmark(start startFunc, stop stopFunc, bf stats.Features, s *stats.Stats) {
	caller, cleanup := makeFuncSync(bf)
	defer cleanup()
	runBenchmark(caller, start, stop, bf, s, operationRpcDirect)
}

// benchmark for the rpc direct
func rpcDirectBenchmark(start startFunc, stop stopFunc, bf stats.Features, s *stats.Stats) {
	rand.Seed(time.Now().UnixNano())
	array := make([]int, 0, bf.ArraySize)
	for i := 0; i < bf.ArraySize; i++ {
		array = append(array, rand.Intn(bf.ArraySize*2))
	}
	caller := func(pos int, ctx *runContext) {
		rpcSortArray(array)
	}
	runBenchmark(caller, start, stop, bf, s, operationRpcDirect)
}

//单协程运行上下文
type runContext struct {
	data []interface{}
}

// single call sync discovery
func makeFuncSync(bf stats.Features) (rpcCallFunc, rpcCleanupFunc) {
	tc, cleanup := makeClient()
	rand.Seed(time.Now().UnixNano())
	array := make([]int, 0, bf.ArraySize)
	for i := 0; i < bf.ArraySize; i++ {
		array = append(array, rand.Intn(1000))
	}
	return func(index int, ctx *runContext) {
		if len(ctx.data) == 0 {
			ctx.data = make([]interface{}, 0, 3)
			var req = &api.GetOneInstanceRequest{}
			req.Namespace = bf.Namespace
			req.Service = bf.Service
			ctx.data = append(ctx.data, req)
			var result = &api.ServiceCallResult{}
			result.SetDelay(200 * time.Millisecond)
			result.SetRetCode(0)
			ctx.data = append(ctx.data, result)
		}
		req := ctx.data[0].(*api.GetOneInstanceRequest)
		instanceRsp, err := tc.GetOneInstance(req)
		if nil != err {
			log.Fatalf("fail to invoke %d call for service %s, err is %v", index, req.Service, err)
		}
		instance := instanceRsp.Instances[0]
		rpcSortArray(array)
		result := ctx.data[1].(*api.ServiceCallResult)
		result.CalledInstance = instance
		result.RetStatus = model.RetSuccess
		tc.UpdateServiceCallResult(result)
	}, cleanup
}

// makeClient returns a gRPC client for the grpc.testing.BenchmarkService
// service. The client is configured using the different options in the passed
// 'bf'. Also returns a cleanup function to close the client and release
// resources.
func makeClient() (api.ConsumerAPI, func()) {
	var err error
	var consumerAPI api.ConsumerAPI
	if consumerAPI, err = api.NewConsumerAPI(); nil != err {
		log.Fatalf("fail to init consumer api, error is %v", err)
	}
	return consumerAPI, func() {
		consumerAPI.Destroy()
	}
}

// main runner for benchmark
func runBenchmark(caller rpcCallFunc, start startFunc, stop stopFunc, bf stats.Features, s *stats.Stats, mode string) {
	// Run benchmark.
	start(mode, bf)
	var wg sync.WaitGroup
	wg.Add(bf.MaxConcurrentCalls)
	bmEnd := time.Now().Add(bf.BenchTime)
	var count uint64
	for i := 0; i < bf.MaxConcurrentCalls; i++ {
		go func(pos int) {
			ctx := &runContext{}
			defer wg.Done()
			for j := 0; j < warmupCallCount; j++ {
				caller(-1, ctx)
			}
			for {
				t := time.Now()
				if t.After(bmEnd) {
					return
				}
				start := time.Now()
				caller(pos, ctx)
				elapse := time.Since(start)
				atomic.AddUint64(&count, 1)
				s.AddDuration(elapse)
			}
		}(i)
	}
	wg.Wait()
	stop(count)
}

//进行排序运算
func rpcSortArray(array []int) {
	sort.Ints(array)
}

// main test entry
func main() {
	opts := processFlags()
	before(opts)

	s := stats.NewStats(numStatsBuckets)
	featuresNum := makeFeaturesNum(opts)

	var (
		start = func(operation string, bf stats.Features) { s.StartRun(operation, bf) }
		stop  = func(count uint64) { s.EndRun(count) }
	)
	for _, bf := range opts.generateFeatures(featuresNum) {
		if opts.rModes.rpcNaming {
			rpcNamingBenchmark(start, stop, bf, s)
		} else if opts.rModes.rpcDirect {
			rpcDirectBenchmark(start, stop, bf, s)
		}
	}
	after(opts, s.GetResults())
}
