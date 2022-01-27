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
	"github.com/polarismesh/polaris-go/pkg/model"
)

var (
	namespace     string
	service       string
	selfNamespace string
	selfService   string
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "", "service")

	flag.StringVar(&selfNamespace, "selfNamespace", "default", "selfNamespace")
	flag.StringVar(&selfService, "selfService", "", "selfService")
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
	getInstancesRequest.SourceService = &model.ServiceInfo{
		Service:   selfService,
		Namespace: selfNamespace,
		Metadata: map[string]string{
			"env": "dev",
		},
	}
	allInstResp, err := consumer.GetInstances(getInstancesRequest)
	if nil != err {
		log.Fatalf("fail to GetInstancesRequest, err is %v", err)
	}
	instances := allInstResp.GetInstances()
	if len(instances) > 0 {
		for i, instance := range instances {
			log.Printf("instance GetInstances %d is %s:%d metadata=>%#v", i, instance.GetHost(), instance.GetPort(), instance.GetMetadata())
		}
	}

}
