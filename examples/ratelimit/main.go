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
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/algorithm/rand"
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

var (
	namespace string
	service   string
	labels    string
	//并发数
	concurrency int
	//多久获取一次配额
	interval int
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "", "service")
	flag.StringVar(&labels, "labels", "", "labels")
	flag.IntVar(&concurrency, "concurrency", 1, "concurrency")
	flag.IntVar(&interval, "interval", 20, "interval")
}

//限流模拟程序
func main() {
	initArgs()
	flag.Parse()
	if len(namespace) == 0 || len(service) == 0 {
		log.Print("namespace and service are required")
		return
	}
	labelsMap, err := ParseLabels(labels)
	if err != nil {
		log.Fatalf("fail to parse labels %s, err: %v", labels, err)
	}
	log.Printf("labels: %v", labelsMap)
	quotaReq = api.NewQuotaRequest()
	quotaReq.SetLabels(labelsMap)
	quotaReq.SetNamespace(namespace)
	quotaReq.SetService(service)
	limitAPI, err = api.NewLimitAPI()
	if nil != err {
		log.Fatalf("fail to create limitAPI, err: %v", err)
	}
	scalableRand := rand.NewScalableRand()
	sleepWindow = make([]time.Duration, concurrency)
	for i := 0; i < concurrency; i++ {
		factor := scalableRand.Intn(3) + 1
		sleepWindow[i] = time.Duration(factor)*time.Millisecond + time.Duration(interval)*time.Millisecond
	}
	wg := &sync.WaitGroup{}
	wg.Add(concurrency)
	stop := make(chan struct{})
	for i := 0; i < concurrency; i++ {
		go getQuota(wg, stop, i)
	}
	//合建chan
	c := make(chan os.Signal, 1)
	//监听所有信号
	signal.Notify(c)
	var previousIntervalPass int64
	var previousAllReq int64
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
L1:
	for {
		select {
		case <-ticker.C:
			currentIntervalPass := atomic.LoadInt64(&passReq)
			currentAllReq := atomic.LoadInt64(&allReq)
			log.Printf("intervalMs pass: %d, all %d",
				currentIntervalPass-previousIntervalPass, currentAllReq-previousAllReq)
			previousIntervalPass = currentIntervalPass
			previousAllReq = currentAllReq
		case s := <-c:
			log.Printf("receive quit signal %v", s)
			break L1
		}
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
			log.Printf("thread %d request quota-ret : %d", id, result.Get().Code)
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

//解析标签列表
func ParseLabels(labelsStr string) (map[string]string, error) {
	strLabels := strings.Split(labelsStr, ",")
	labels := make(map[string]string, len(strLabels))
	for _, strLabel := range strLabels {
		if len(strLabel) == 0 {
			continue
		}
		labelKv := strings.Split(strLabel, ":")
		if len(labelKv) != 2 {
			return nil, fmt.Errorf("invalid kv pair str %s", strLabel)
		}
		labels[labelKv[0]] = labelKv[1]
	}
	return labels, nil
}
