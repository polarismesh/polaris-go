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

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"
	"runtime/pprof"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/go-multierror"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/benchmark/flags"
	"github.com/polarismesh/polaris-go/pkg/model"
)

const (
	operationSyncGetOneInstance       = "syncGetOneInstance"
	operationSyncGetInstances         = "syncGetInstances"
	operationSyncGetOneInstanceUpdate = "syncGetOneInstanceUpdate"
)

const (
	opCodeSyncGetOneInstance = iota
	opCodeSyncGetInstances
	opCodeSyncGetOneInstanceUpdate
)

var (
	defaultMaxConcurrentCalls = []int{1, 4, 8}
	allOperations             = []string{operationSyncGetOneInstance, operationSyncGetInstances,
		operationSyncGetOneInstanceUpdate}
	// 时间到
	timeExceed      int32
	operationToCode = map[string]int{
		operationSyncGetOneInstance:       opCodeSyncGetOneInstance,
		operationSyncGetInstances:         opCodeSyncGetInstances,
		operationSyncGetOneInstanceUpdate: opCodeSyncGetOneInstanceUpdate,
	}
)

var (
	workload = flag.String("workload", operationSyncGetOneInstance,
		fmt.Sprintf("Workloads to execute - One of: %v", strings.Join(allOperations, ", ")))
	maxConcurrentCalls = flags.IntSlice(
		"maxConcurrentCalls", defaultMaxConcurrentCalls, "Number of concurrent RPCs during benchmarks")
	maxQps = flag.Int(
		"maxQps", 0, "Number of QPS during benchmarks")
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
	namespace = flag.String("namespace", "", "The namespace of the testing service.")
	service   = flag.String("service", "", "The service name of the testing service.")
)

// 耗时序列
type Durations []time.Duration

// 总长度
func (d Durations) Len() int {
	return len(d)
}

// 降序
func (d Durations) Less(i, j int) bool {
	return d[i] > d[j]
}

// 交换
func (d Durations) Swap(i, j int) {
	d[i], d[j] = d[j], d[i]
}

// 校验参数
func validate() error {
	var errs error
	if nil == workload || len(*workload) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("workloads can not be empty"))
	}
	if nil == maxConcurrentCalls || len(*maxConcurrentCalls) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("maxConcurrentCalls can not be empty"))
	}
	if nil == benchTime || *benchTime < time.Second {
		errs = multierror.Append(errs, fmt.Errorf("benchtime should greater than 1s"))
	}
	if nil == namespace || len(*namespace) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("namespace can not be empty"))
	}
	if nil == service || len(*service) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("service can not be empty"))
	}
	return errs
}

// makeClient returns a gRPC client for the grpc.testing.BenchmarkService
// service. The client is configured using the different options in the passed
// 'bf'. Also returns a cleanup function to close the client and release
// resources.
func makeClient() (api.ConsumerAPI, func()) {
	var err error
	var consumerAPI api.ConsumerAPI
	if consumerAPI, err = api.NewConsumerAPI(); err != nil {
		log.Fatalf("fail to init consumer api, error is %v", err)
	}
	return consumerAPI, func() {
		consumerAPI.Destroy()
	}
}

// 拆分测试任务
func splitFeatures(client api.ConsumerAPI) (fes []*feature) {
	sdkContext := client.(api.SDKOwner).SDKContext()
	for _, maxConcurrentCall := range *maxConcurrentCalls {
		getOneRequest := &api.GetOneInstanceRequest{}
		getOneRequest.Namespace = *namespace
		getOneRequest.Service = *service
		result := &api.ServiceCallResult{}
		result.SetRetCode(200)
		result.SetDelay(200 * time.Millisecond)
		getMultiRequest := &api.GetInstancesRequest{}
		getMultiRequest.Namespace = *namespace
		getMultiRequest.Service = *service
		fe := &feature{
			sdkContext:        sdkContext,
			opCode:            operationToCode[*workload],
			maxConcurrentCall: maxConcurrentCall,
			maxQps:            *maxQps,
			benchTime:         *benchTime,
			namespace:         *namespace,
			service:           *service,
			consumerClient:    client,
			getOneRequest:     getOneRequest,
			getMultiRequest:   getMultiRequest,
			result:            result,
			maxDelays:         make(Durations, maxConcurrentCall),
		}
		if fe.maxQps > 0 {
			fe.qpsLimit = int32(fe.maxQps)
		}
		fes = append(fes, fe)
	}
	return fes
}

// 单次测试的任务
type feature struct {
	opCode            int
	maxQps            int
	maxConcurrentCall int
	benchTime         time.Duration
	namespace         string
	service           string
	consumerClient    api.ConsumerAPI
	sdkContext        api.SDKContext
	totalIteration    int64
	getOneRequest     *api.GetOneInstanceRequest
	getMultiRequest   *api.GetInstancesRequest
	result            *api.ServiceCallResult
	maxDelays         Durations
	qpsLimit          int32
	allConsume        int64
}

// 任务的字面值输出
func (f feature) String() string {
	return fmt.Sprintf("benchTime_%v-maxConcurrentCalls_%v-namespace_%s-service_%s",
		f.benchTime, f.maxConcurrentCall, f.namespace, f.service)
}

// 运行测试任务
func runFeature(fe *feature) {
	wg := sync.WaitGroup{}
	wg.Add(fe.maxConcurrentCall)
	for i := 0; i < fe.maxConcurrentCall; i++ {
		go func(idx int) {
			defer wg.Done()
			var maxDelay time.Duration
			for {
				if atomic.LoadInt32(&timeExceed) != 0 {
					break
				}
				startTime := fe.sdkContext.GetValueContext().Now()
				switch fe.opCode {
				case opCodeSyncGetOneInstance:
					doSyncGetOneInstance(fe)
					atomic.AddInt64(&fe.totalIteration, 1)
				case opCodeSyncGetInstances:
					doSyncGetMultiInstances(fe)
					atomic.AddInt64(&fe.totalIteration, 1)
				case opCodeSyncGetOneInstanceUpdate:
					doSyncGetOneInstanceUpload(fe)
					atomic.AddInt64(&fe.totalIteration, 1)
				}
				consumeDuration := fe.sdkContext.GetValueContext().Since(startTime)
				if consumeDuration > maxDelay {
					maxDelay = consumeDuration
				}
			}
			fe.maxDelays[idx] = maxDelay
		}(i)
	}
	wg.Wait()
}

// 运行测试任务
func runQpsLimitFeature(fe *feature) {
	wg := sync.WaitGroup{}
	wg.Add(fe.maxConcurrentCall)
	for i := 0; i < fe.maxConcurrentCall; i++ {
		go func(idx int) {
			defer wg.Done()
			var maxDelay time.Duration
			for {
				if atomic.LoadInt32(&timeExceed) != 0 {
					break
				}
				if atomic.LoadInt32(&fe.qpsLimit) <= 0 || atomic.AddInt32(&fe.qpsLimit, -1) <= 0 {
					time.Sleep(200 * time.Millisecond)
					continue
				}
				startTime := time.Now()
				switch fe.opCode {
				case opCodeSyncGetOneInstance:
					doSyncGetOneInstance(fe)
					atomic.AddInt64(&fe.totalIteration, 1)
				case opCodeSyncGetInstances:
					doSyncGetMultiInstances(fe)
					atomic.AddInt64(&fe.totalIteration, 1)
				case opCodeSyncGetOneInstanceUpdate:
					doSyncGetOneInstanceUpload(fe)
					atomic.AddInt64(&fe.totalIteration, 1)
				}
				consumeDuration := time.Since(startTime)
				if consumeDuration > maxDelay {
					maxDelay = consumeDuration
				}
				atomic.AddInt64(&fe.allConsume, int64(consumeDuration))
			}
			fe.maxDelays[idx] = maxDelay
		}(i)
	}
	wg.Wait()
}

// 参数
type benchOpts struct {
	memProfileRate   int
	memProfile       string
	mutexProfile     string
	mutexProfileRate int
	cpuProfile       string
}

// 运行获取单个服务实例并上报
func doSyncGetOneInstanceUpload(fe *feature) {
	instance, err := fe.consumerClient.GetOneInstance(fe.getOneRequest)
	if err != nil {
		log.Fatalf("fail to invoke GetOneInstance for service %s, err is %v", fe.getOneRequest.Service, err)
	}
	fe.result.RetStatus = model.RetSuccess
	fe.result.CalledInstance = instance.Instances[0]
	err = fe.consumerClient.UpdateServiceCallResult(fe.result)
	if err != nil {
		log.Fatalf("fail to invoke UpdateServiceCallResult for service %s, err is %v", fe.getOneRequest.Service, err)
	}
}

// 运行获取多个服务实例
func doSyncGetOneInstance(fe *feature) {
	_, err := fe.consumerClient.GetOneInstance(fe.getOneRequest)
	if err != nil {
		log.Fatalf("fail to invoke GetOneInstance for service %s, err is %v", fe.getOneRequest.Service, err)
	}
}

// 运行获取单个服务实例
func doSyncGetMultiInstances(fe *feature) {
	_, err := fe.consumerClient.GetInstances(fe.getMultiRequest)
	if err != nil {
		log.Fatalf("fail to invoke GetInstances for service %s, err is %v", fe.getOneRequest.Service, err)
	}
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

// after testing function
func after(opts *benchOpts) {
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

// 启动定时器
func startTimer(startTime time.Time, duration time.Duration) {
	atomic.StoreInt32(&timeExceed, 0)
	go func() {
		ctx, cancel := context.WithDeadline(context.Background(), startTime.Add(duration))
		<-ctx.Done()
		atomic.StoreInt32(&timeExceed, 1)
		cancel()
	}()
}

// 测试入口
func main() {
	flag.Parse()
	if flag.NArg() != 0 {
		log.Fatal("Error: unparsed arguments: ", flag.Args())
	}
	var err error
	if err = validate(); err != nil {
		log.Fatalf("fail to validate parameters, error is %v", err)
	}
	ops := &benchOpts{
		memProfileRate:   *memProfileRate,
		memProfile:       *memProfile,
		mutexProfile:     *mutexProfile,
		mutexProfileRate: *mutexProfileRate,
		cpuProfile:       *cpuProfile,
	}
	before(ops)
	client, cancel := makeClient()
	defer cancel()
	features := splitFeatures(client)
	useQpsLimit := features[0].maxQps > 0
	if useQpsLimit {
		go func() {
			startTime := time.Now()
			for {
				if time.Since(startTime) >= time.Second {
					for _, feature := range features {
						atomic.StoreInt32(&feature.qpsLimit, int32(feature.maxQps))
					}
				}
				time.Sleep(200 * time.Second)
			}
		}()
	}
	type runner func(fe *feature)
	var featureRunner runner = runFeature
	if useQpsLimit {
		featureRunner = runQpsLimitFeature
	}
	for _, feature := range features {
		log.Printf("start to run %s", feature.String())
		startTime := time.Now()
		startTimer(startTime, feature.benchTime)
		featureRunner(feature)
		consumeTimeNano := time.Since(startTime).Nanoseconds()
		qps := feature.totalIteration / int64(feature.benchTime.Seconds())
		var delay int64
		if useQpsLimit {
			delay = feature.allConsume / feature.totalIteration
		} else {
			delay = (consumeTimeNano * int64(feature.maxConcurrentCall)) / feature.totalIteration
		}

		delayMicro := float64(delay) / float64(1000)
		sort.Sort(feature.maxDelays)
		log.Printf("QPS: %d, delay %.2f micro, max delay is %v", qps, delayMicro, feature.maxDelays[0])
	}
	after(ops)
}
