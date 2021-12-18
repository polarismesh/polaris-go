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
	"log"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
)

var (
	namespace string
	service   string
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "", "service")
}

func main() {
	initArgs()
	flag.Parse()
	if len(namespace) == 0 || len(service) == 0 {
		log.Print("namespace and service are required")
		return
	}
	consumer, err := api.NewConsumerAPI()
	if nil != err {
		log.Fatalf("fail to create consumerAPI, err is %v", err)
	}
	defer consumer.Destroy()

	log.Printf("start to invoke GetInstancesRequest operation")
	getInstancesRequest := &api.GetInstancesRequest{}
	getInstancesRequest.Namespace = namespace
	getInstancesRequest.Service = service
	allInstResp, err := consumer.GetInstances(getInstancesRequest)
	if nil != err {
		log.Fatalf("fail to GetInstances, err is %v", err)
	}
	instances := allInstResp.GetInstances()

	var errInstance model.Instance

	if len(instances) > 0 {
		for i, instance := range instances {
			log.Printf("choose instances %s:%d to circuirbreaker", instance.GetHost(), instance.GetPort())
			errInstance = instances[i]
			break
		}
	}

	for i := 0; i < 20; i++ {
		errCode := int32(500)
		delay := time.Duration(time.Second)
		callRet, err := api.NewServiceCallResult(consumer.SDKContext(), api.InstanceRequest{
			ServiceKey: model.ServiceKey{
				Namespace: namespace,
				Service:   service,
			},
			InstanceID: errInstance.GetId(),
			IP:         errInstance.GetHost(),
			Port:       uint16(errInstance.GetPort()),
		})
		if err != nil {
			log.Fatalf("fail to NewServiceCallResult, err is %v", err)
		}
		callRet.RetCode = &errCode
		callRet.Delay = &delay
		callRet.RetStatus = api.RetFail

		if err := consumer.UpdateServiceCallResult(callRet); err != nil {
			log.Fatalf("fail to UpdateServiceCallResult, err is %v", err)
		}
	}

	time.Sleep(time.Duration(5 * time.Second))

	getInstancesRequest = &api.GetInstancesRequest{}
	getInstancesRequest.Namespace = namespace
	getInstancesRequest.Service = service
	allInstResp, err = consumer.GetInstances(getInstancesRequest)
	if nil != err {
		log.Fatalf("fail to GetInstances, err is %v", err)
	}
	instances = allInstResp.GetInstances()

	if len(instances) > 0 {
		for i, instance := range instances {
			log.Printf("instance GetInstances %d is %s:%d", i, instance.GetHost(), instance.GetPort())
		}
	}

}
