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
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/polarismesh/polaris-go/api"
)

var (
	namespace string
	service   string
	token     string
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "", "service")
	flag.StringVar(&token, "token", "", "token")
}

const (
	host          = "127.0.0.1"
	startPort     = 2001
	instanceCount = 5
)

func main() {
	initArgs()
	flag.Parse()
	if len(namespace) == 0 || len(service) == 0 {
		log.Print("namespace and service are required")
		return
	}
	consumer, err := api.NewConsumerAPI()
	if err != nil {
		log.Fatalf("fail to create consumerAPI, err is %v", err)
	}
	defer consumer.Destroy()

	// share one context with the consumerAPI
	provider := api.NewProviderAPIByContext(consumer.SDKContext())

	log.Printf("start to register instances, count %d", instanceCount)
	for i := 0; i < instanceCount; i++ {
		registerRequest := &api.InstanceRegisterRequest{}
		registerRequest.Service = service
		registerRequest.Namespace = namespace
		registerRequest.Host = host
		registerRequest.Port = startPort + i
		registerRequest.ServiceToken = token
		registerRequest.SetHealthy(true)
		resp, err := provider.Register(registerRequest)
		if err != nil {
			log.Fatalf("fail to register instance %d, err is %v", i, err)
		}
		log.Printf("register instance %d response: instanceId %s", i, resp.InstanceID)
	}

	log.Printf("start to wait server sync instances")
	time.Sleep(5 * time.Second)

	log.Printf("start http health check server")
	go func() {
		err = startHTTPServer(fmt.Sprintf("%s:%d", host, startPort))
		if err != nil {
			log.Fatalf("fail to start http health check server, err is %v", err)
		}
	}()

	log.Printf("start first time get all instance")
	getAllRequest := &api.GetAllInstancesRequest{}
	getAllRequest.Namespace = namespace
	getAllRequest.Service = service
	allInstResp, err := consumer.GetAllInstances(getAllRequest)
	if err != nil {
		log.Fatalf("fail to getAllInstances, err is %v", err)
	}
	instances := allInstResp.GetInstances()
	if len(instances) > 0 {
		for i, instance := range instances {
			var statusStr = "close"
			if nil != instance.GetCircuitBreakerStatus() {
				statusStr = instance.GetCircuitBreakerStatus().GetStatus().String()
			}
			log.Printf("instance before activehealthcheck %d is %s:%d, status is %s", i,
				instance.GetHost(), instance.GetPort(), statusStr)
		}
	}

	log.Printf("start to health check instances")
	time.Sleep(5 * time.Second)
	instances = allInstResp.GetInstances()
	if len(instances) > 0 {
		for i, instance := range instances {
			var statusStr = "close"
			if nil != instance.GetCircuitBreakerStatus() {
				statusStr = instance.GetCircuitBreakerStatus().GetStatus().String()
			}
			log.Printf("instance after activehealthcheck %d is %s:%d, status is %s", i,
				instance.GetHost(), instance.GetPort(), statusStr)
		}
	}
}

// startHttpServer 启动一个Http服务
func startHTTPServer(address string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		log.Printf("receive health http detection")
		w.WriteHeader(http.StatusOK)
	})
	log.Printf("httpserver ready, addr %s", address)
	err := http.ListenAndServe(address, mux)
	if err != nil {
		log.Printf("httpserver err %v", err)
	}
	return err
}
