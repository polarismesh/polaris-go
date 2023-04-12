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
	"sync"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
)

var (
	namespace string
	waitIndex uint64
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "Polaris", "namespace")
	flag.Uint64Var(&waitIndex, "waitIndex", 0, "waitIndex")
}

const svcCount = 1

var port int32 = 1000

type TestListener struct {
	wg *sync.WaitGroup
}

// OnInstancesUpdate notify when service instances changed
func (t *TestListener) OnServicesUpdate(resp *model.ServicesResponse) {
	defer t.wg.Done()
	log.Printf("receive namespace %s, services change, new count is %d, revision : %s", namespace, len(resp.GetValue()), resp.GetRevision())
	// for _, svc := range resp.GetValue() {
	// 	log.Printf("namespace %s, svc %s", svc.Namespace, svc.Service)
	// }
}

func main() {
	initArgs()
	flag.Parse()
	consumer, err := polaris.NewConsumerAPI()
	if err != nil {
		log.Fatalf("fail to create consumerAPI, err is %v", err)
	}
	defer consumer.Destroy()

	wg := &sync.WaitGroup{}
	wg.Add(10)
	req := &polaris.WatchAllServicesRequest{}
	req.Namespace = namespace
	req.WatchMode = api.WatchModeNotify
	req.ServicesListener = &TestListener{
		wg: wg,
	}
	resp, err := consumer.WatchAllServices(req)
	if err != nil {
		log.Fatalf("fail to watch all services, namespace %s, err: %s", namespace, err)
	}
	servicesResp := resp.ServicesResponse()
	log.Printf("namespace %s, services count is %d", namespace, len(servicesResp.GetValue()))
	// for _, svc := range servicesResp.GetValue() {
	// 	log.Printf("namespace %s, svc %s", svc.Namespace, svc.Service)
	// }
	wg.Wait()
}
