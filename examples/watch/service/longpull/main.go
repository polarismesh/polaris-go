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
	"log"
	"time"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/api"
)

var (
	namespace string
	waitIndex uint64
	waitTime  time.Duration
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "", "namespace")
	flag.Uint64Var(&waitIndex, "waitIndex", 0, "waitIndex")
	flag.DurationVar(&waitTime, "waitTime", 60*time.Second, "waitTime")
}

func main() {
	initArgs()
	flag.Parse()
	consumer, err := polaris.NewConsumerAPI()
	if err != nil {
		log.Fatalf("fail to create consumerAPI, err is %v", err)
	}
	defer consumer.Destroy()

	var index uint64 = waitIndex

	for {
		req := &polaris.WatchAllServicesRequest{}
		req.Namespace = namespace
		req.WaitTime = waitTime
		req.WaitIndex = index
		req.WatchMode = api.WatchModeLongPull
		resp, err := consumer.WatchAllServices(req)
		if err != nil {
			log.Fatalf("fail to watch all instances, namespace %s, err: %s", namespace, err)
		}
		servicesResp := resp.ServicesResponse()
		index = servicesResp.GetHashValue()
		log.Printf("namespace %s, services count is %d, revision : %s, next watch index %d", namespace,
			len(servicesResp.GetValue()), resp.ServicesResponse().GetRevision(), index)
		// for _, svc := range servicesResp.GetValue() {
		// 	log.Printf("namespace %s, svc %s", svc.Namespace, svc.Service)
		// }

		log.Printf("namespace %s, watch id is %d\n", namespace, resp.WatchId())
		resp.CancelWatch()
	}
}
