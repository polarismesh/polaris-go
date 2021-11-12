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

	"github.com/polarismesh/polaris-go/api"
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

	locationInfo := consumer.SDKContext().GetEngine().GetContext().GetCurrentLocation()

	log.Printf("%#v\n%s\n%s\n%d", locationInfo.GetLocation(), locationInfo.IsLocationReady(), locationInfo.IsLocationInitialized(), locationInfo.GetStatus())

	log.Printf("start to invoke getInstance operation")
	getRequest := &api.GetInstancesRequest{}
	getRequest.Namespace = namespace
	getRequest.Service = service
	instResp, err := consumer.GetInstances(getRequest)
	if nil != err {
		log.Fatalf("fail to getInstance, err is %v", err)
	}
	instances := instResp.GetInstances()
	for i := range instances {
		log.Printf("instance is %s:%d", instances[i].GetHost(), instances[i].GetPort())
	}
}
