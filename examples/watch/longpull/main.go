/**
 * Tencent is pleased to support the open source community by making Polaris available.
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
	"sync"
	"sync/atomic"
	"time"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/api"
)

var (
	namespace string
	service   string
	waitIndex uint64
	waitTime  time.Duration
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "", "namespace")
	flag.StringVar(&service, "service", "", "service")
	flag.Uint64Var(&waitIndex, "waitIndex", 0, "waitIndex")
	flag.DurationVar(&waitTime, "waitTime", 10*time.Second, "waitTime")
}

func registerInstance(svcName string, host string, port int32, provider polaris.ProviderAPI) string {
	log.Printf("start to invoke register operation")
	registerRequest := &polaris.InstanceRegisterRequest{}
	registerRequest.Service = svcName
	registerRequest.Namespace = namespace
	registerRequest.Host = host
	registerRequest.Port = int(port)
	resp, err := provider.Register(registerRequest)
	if err != nil {
		log.Fatalf("fail to register instance to service %s, err is %v", svcName, err)
	}
	log.Printf("register response: service %s, instanceId %s", svcName, resp.InstanceID)
	return resp.InstanceID
}

func deregisterService(svcName string, instanceId string, provider polaris.ProviderAPI) {
	log.Printf("start to invoke deregister operation")
	deregisterRequest := &polaris.InstanceDeRegisterRequest{}
	deregisterRequest.InstanceID = instanceId
	if err := provider.Deregister(deregisterRequest); err != nil {
		log.Fatalf("fail to deregister instance to service %s, err is %v", svcName, err)
	}
	log.Printf("deregister successfully to service %s, id=%s", svcName, instanceId)
}

const svcCount = 10

var port int32 = 1000

func main() {
	initArgs()
	flag.Parse()
	if len(namespace) == 0 || len(service) == 0 {
		log.Print("namespace and service are required")
		return
	}
	consumer, err := polaris.NewConsumerAPI()
	if err != nil {
		log.Fatalf("fail to create consumerAPI, err is %v", err)
	}
	defer consumer.Destroy()

	var index uint64 = waitIndex

	provider := polaris.NewProviderAPIByContext(consumer.SDKContext())
	for i := 0; i < svcCount; i++ {
		go func(svcName string) {
			time.Sleep(5 * time.Second)
			instId1 := registerInstance(svcName, "127.0.0.1", atomic.AddInt32(&port, 1), provider)
			instId2 := registerInstance(svcName, "127.0.0.1", atomic.AddInt32(&port, 1), provider)
			instId3 := registerInstance(svcName, "127.0.0.1", atomic.AddInt32(&port, 1), provider)
			time.Sleep(10 * time.Second)
			deregisterService(svcName, instId1, provider)
			time.Sleep(10 * time.Second)
			deregisterService(svcName, instId2, provider)
			time.Sleep(10 * time.Second)
			deregisterService(svcName, instId3, provider)
		}(fmt.Sprintf("%s-%d", service, i))
	}
	wg := &sync.WaitGroup{}
	wg.Add(svcCount)
	for j := 0; j < svcCount; j++ {
		go func(svcName string) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				req := &polaris.WatchAllInstancesRequest{}
				req.Service = svcName
				req.Namespace = namespace
				req.WaitTime = waitTime
				req.WaitIndex = index
				req.WatchMode = api.WatchModeLongPull
				resp, err := consumer.WatchAllInstances(req)
				if err != nil {
					log.Fatalf("fail to watch all instances, svc %s, err: %s", svcName, err)
				}
				instanceResp := resp.InstancesResponse()
				index = instanceResp.HashValue
				log.Printf("svc %s, instances count is %d, next watch index %d", svcName, len(instanceResp.Instances), index)
				for i, instance := range instanceResp.Instances {
					log.Printf("svc %s, instance %d is %s:%d", svcName, i, instance.GetHost(), instance.GetPort())
				}

				log.Printf("svc %s, watch id is %d\n", svcName, resp.WatchId())
				resp.CancelWatch()
			}
		}(fmt.Sprintf("%s-%d", service, j))
	}
	log.Printf("start to wait finish")
	wg.Wait()
}
