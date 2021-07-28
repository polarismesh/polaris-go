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
	"github.com/polarismesh/polaris-go/api"
	plog "github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

const logLevel = api.InfoLog

//主入口函数
func main() {
	go func() {
		log.Println(http.ListenAndServe("localhost:6070", nil))
	}()
	var err error
	argsWithoutProg := os.Args[1:]
	if len(argsWithoutProg) < 3 {
		log.Fatalf("using %s <namespace> <service> <seconds>", os.Args[0])
	}
	namespace := argsWithoutProg[0]
	service := argsWithoutProg[1]
	secondsStr := argsWithoutProg[2]
	seconds, err := strconv.Atoi(secondsStr)
	if nil != err {
		log.Fatalf("fail to convert seconds %s to int, err %v", secondsStr, err)
	}
	err = plog.GetBaseLogger().SetLogLevel(logLevel)
	if nil != err {
		log.Fatalf("fail to SetLogLevel, err is %v", err)
	}
	//创建consumerAPI实例
	//注意该实例所有方法都是协程安全，一般用户进程只需要创建一个consumerAPI,重复使用即可
	//切勿每次调用之前都创建一个consumerAPI
	//默认使用权重随机负载均衡算法
	consumer, err := api.NewConsumerAPI()
	if nil != err {
		log.Fatalf("fail to create ConsumerAPI by default configuration, err is %v", err)
	}
	defer consumer.Destroy()

	deadline := time.Now().Add(time.Duration(seconds) * time.Second)

	for {
		if time.Now().After(deadline) {
			break
		}
		var flowId uint64
		var getAllInstancesReq *api.GetAllInstancesRequest
		getAllInstancesReq = &api.GetAllInstancesRequest{}
		getAllInstancesReq.FlowID = atomic.AddUint64(&flowId, 1)
		getAllInstancesReq.Namespace = namespace
		getAllInstancesReq.Service = service

		getAllInstResp, err := consumer.GetAllInstances(getAllInstancesReq)
		if nil != err {
			log.Fatalf("fail to sync GetAllInstances, err is %v", err)
		}
		log.Printf("success to sync GetAllInstances, count is %d, revision is %s\n", len(getAllInstResp.Instances),
			getAllInstResp.Revision)

		// 优先使用availableInsArr, 适当使用halfOpenInsArr (半开实例涉及到熔断状态恢复)
		// availableInsArr为空、 halfOpenInsArr也为空   --  使用circuitbreakerInsArr
		// 使用circuitbreakerInsArr也为空， 使用unhealthyInsArr
		// 隔离和权重为0的节点，一定不能使用

		// 可用实例arr
		var availableInsArr []model.Instance
		// 半开实例arr
		var halfOpenInsArr []model.Instance
		// 熔断实例arr
		var circuitbreakerInsArr []model.Instance
		//不健康实例arr
		var unhealthyInsArr []model.Instance
		if len(getAllInstResp.Instances) > 0 {
			for i, inst := range getAllInstResp.Instances {
				log.Printf("sync instance %d is id=%s, address=%s:%d\n", i, inst.GetId(), inst.GetHost(), inst.GetPort())
				//如何过滤出不可用的节点

				// 去掉被隔离的节点
				if inst.IsIsolated() == true {
					continue
				}

				// 去掉权重为0的节点
				if inst.GetWeight() <= 0 {
					continue
				}

				// 去掉不健康的节点
				if inst.IsHealthy() != true {
					unhealthyInsArr = append(unhealthyInsArr, inst)
					continue
				}

				// 检查节点的熔断状态
				if inst.GetCircuitBreakerStatus() != nil {
					cb := inst.GetCircuitBreakerStatus()
					// 去掉已经熔断的节点
					if cb.GetStatus() == model.Open {
						circuitbreakerInsArr = append(circuitbreakerInsArr, inst)
						continue
					}

					// 过滤掉半开的节点
					if cb.GetStatus() == model.HalfOpen {
						halfOpenInsArr = append(halfOpenInsArr, inst)
						continue
					}
				}

				availableInsArr = append(availableInsArr, inst)
			}
		}
		time.Sleep(1 * time.Second)
	}
	log.Printf("success to sync get all instances")

}
