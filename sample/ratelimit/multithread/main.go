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
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/sample/ratelimit/util"
	"log"
	"math/rand"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

//通过的请求数量
var passReq int64

//全量的请求数
var allReq int64

//限流请求
var quotaReq api.QuotaRequest

//limitApi
var limitAPI api.LimitAPI

//每个线程进行限流请求的间隔
var sleepWindow []time.Duration

//限流模拟程序
func main() {
	argsWithOutProd := os.Args[1:]
	//Threads: 并发线程数量
	//Nampspace, Service: 进行限流的服务的命名空间和服务名
	//AmountInterval：统计通过的限流请求的周期， IntervalNum：进行统计的周期数量
	//<labelk1:labelv1,labelk2:labelv2,....>：进行限流请求的标签
	if len(argsWithOutProd) < 6 {
		log.Fatalf("using %s <Threads> <Namespace> <Service> <AmountInterval> <IntervalNum> <SleepInterval>"+
			" <labelk1:labelv1,labelk2:labelv2,....>", os.Args[0])
	}
	threadNum, err := strconv.Atoi(argsWithOutProd[0])
	if err != nil {
		log.Fatalf("fail to convert <Threads> %s to num, err: %v", argsWithOutProd[0], err)
	}
	namespace := argsWithOutProd[1]
	service := argsWithOutProd[2]
	var interval time.Duration
	interval, err = time.ParseDuration(argsWithOutProd[3])
	if nil != err {
		log.Fatalf("fail to convert <AmountInterval> %s to duration, err: %v", argsWithOutProd[3], err)
	}
	var intervalNum int
	intervalNum, err = strconv.Atoi(argsWithOutProd[4])
	if err != nil {
		log.Fatalf("fail to convert <IntervalNum> %s to int, err: %v", argsWithOutProd[4], err)
	}
	var sleepInterval int
	sleepInterval, err = strconv.Atoi(argsWithOutProd[5])
	if err != nil {
		log.Fatalf("fail to convert <SleepInterval> %s to int, err: %v", argsWithOutProd[5], err)
	}
	labels := argsWithOutProd[6]
	labelsMap, err := util.ParseLabels(labels)
	if err != nil {
		log.Fatalf("fail to parse labels %s, err: %v", labels, err)
	}
	log.Printf("labels: %v", labelsMap)
	quotaReq = api.NewQuotaRequest()
	quotaReq.SetLabels(labelsMap)
	quotaReq.SetNamespace(namespace)
	quotaReq.SetService(service)
	configuration := api.NewConfiguration()
	api.SetLoggersLevel(api.DebugLog)
	limitAPI, err = api.NewLimitAPIByConfig(configuration)
	if nil != err {
		log.Fatalf("fail to create limitAPI, err: %v", err)
	}
	rand.Seed(time.Now().UnixNano())
	sleepWindow = make([]time.Duration, threadNum)
	for i := 0; i < threadNum; i++ {
		factor := rand.Intn(3) + 1
		sleepWindow[i] = time.Duration(factor)*time.Millisecond + time.Duration(sleepInterval)*time.Millisecond
	}
	wg := &sync.WaitGroup{}
	wg.Add(threadNum)
	stop := make(chan struct{})
	timePoints := make([]<-chan time.Time, intervalNum)
	for i := 0; i < intervalNum; i++ {
		timePoints[i] = time.After(time.Duration(i+1) * interval)
	}
	for i := 0; i < threadNum; i++ {
		go getQuota(wg, stop, i)
	}
	var previousIntervalPass int64
	var previousAllReq int64
	for i := 0; i < intervalNum; i++ {
		<-timePoints[i]
		currentIntervalPass := atomic.LoadInt64(&passReq)
		currentAllReq := atomic.LoadInt64(&allReq)
		log.Printf("%d interval pass: %d, all %d",
			i, currentIntervalPass-previousIntervalPass, currentAllReq-previousAllReq)
		previousIntervalPass = currentIntervalPass
		previousAllReq = currentAllReq
	}
	close(stop)
	log.Printf("stop closed")
	wg.Wait()
	log.Printf("total Pass %d, all %d", passReq, allReq)
	limitAPI.Destroy()
}

//进行限流请求的线程执行的函数
func getQuota(wg *sync.WaitGroup, stop <-chan struct{}, id int) {
	log.Printf("thread %d starts, sleep %v", id, sleepWindow[id])
	defer wg.Done()
	for {
		select {
		case <-stop:
			log.Printf("thread %d stops", id)
			return
		default:
			result, err := limitAPI.GetQuota(quotaReq)
			if nil != err {
				log.Fatalf("fail to get quota, err: %v", err)
			}
			atomic.AddInt64(&allReq, 1)
			if result.Get().Code == api.QuotaResultOk {
				atomic.AddInt64(&passReq, 1)
			}
			//每个线程按照间隔时间进行请求
			if sleepWindow[id] > 0 {
				time.Sleep(sleepWindow[id])
			}
		}
	}
}
