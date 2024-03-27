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
	"sync/atomic"
	"time"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/pkg/model"
)

var (
	namespace string
	service   string
	token     string
	port      int32 = 10000
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "WatchInstanceServer", "service")
	flag.StringVar(&token, "token", "", "token")
}

func main() {
	initArgs()
	flag.Parse()
	if len(namespace) == 0 || len(service) == 0 {
		log.Print("namespace and service are required")
		return
	}
	sdkCtx, err := polaris.NewSDKContext()
	if nil != err {
		log.Fatalf("fail to create sdk context, err is %v", err)
	}
	defer sdkCtx.Destroy()

	consumer := polaris.NewConsumerAPIByContext(sdkCtx)
	provider := polaris.NewProviderAPIByContext(sdkCtx)

	go func() {
		for i := 0; i < 5; i++ {
			addAndRemoveInstance(provider)
		}
	}()
	go watchInstance(consumer)

	time.Sleep(time.Second * 30)
}

// watchInstance 监听实例变化
func watchInstance(consumer polaris.ConsumerAPI) {
	resp, err := consumer.WatchService(&polaris.WatchServiceRequest{
		WatchServiceRequest: model.WatchServiceRequest{
			Key: model.ServiceKey{
				Namespace: namespace,
				Service:   service,
			},
			AuthToken: token,
		},
	})

	if err != nil {
		log.Fatal(err)
	}

	log.Printf("receive instance list : %+v", resp.GetAllInstancesResp.GetInstances())

	for event := range resp.EventChannel {
		insEvent, ok := event.(*model.InstanceEvent)
		if !ok {
			continue
		}

		if insEvent.AddEvent != nil {
			log.Printf("[consumer] receive instance add event : %+v", insEvent.AddEvent.Instances[0].GetInstanceKey())
		}
		if insEvent.UpdateEvent != nil {
			log.Printf("[consumer] receive instance update event : %+v", insEvent.UpdateEvent)
		}
		if insEvent.DeleteEvent != nil {
			log.Printf("[consumer] receive instance delete event : %+v", insEvent.DeleteEvent.Instances[0].GetInstanceKey())
		}

	}
}

// addAndRemoveInstance 注册实例并注销实例
func addAndRemoveInstance(provider polaris.ProviderAPI) {
	registerRequest := &polaris.InstanceRegisterRequest{}
	registerRequest.Service = service
	registerRequest.Namespace = namespace
	registerRequest.Host = "localhost"
	registerRequest.Port = int(port)
	registerRequest.InstanceId = fmt.Sprintf("localhost:%d", port)
	atomic.AddInt32(&port, 1)
	registerRequest.ServiceToken = token
	resp, err := provider.Register(registerRequest)
	if err != nil {
		log.Fatalf("[provider] fail to register instance to service %s, err is %v", service, err)
	}
	log.Printf("[provider] register response: service %s, instanceId %s", service, resp.InstanceID)
	time.Sleep(3 * time.Second)

	deregisterRequest := &polaris.InstanceDeRegisterRequest{}
	deregisterRequest.InstanceID = resp.InstanceID
	deregisterRequest.ServiceToken = token
	if err = provider.Deregister(deregisterRequest); err != nil {
		log.Fatalf("[provider] fail to deregister instance to service %s, err is %v", service, err)
	}
	log.Printf("[provider] deregister successfully to service %s, id=%s", service, resp.InstanceID)
	time.Sleep(2 * time.Second)
}
