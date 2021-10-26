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
	"sync"
	"time"

	"github.com/polarismesh/polaris-go/api"
)

var (
	namespace string
	service   string
	host      string
	port      int
	token     string
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "tdsql-ops-server", "service")
	flag.StringVar(&host, "host", "127.0.0.1", "host")
	flag.IntVar(&port, "port", 7879, "port")
	flag.StringVar(&token, "token", "", "token")
}

func main() {
	initArgs()
	flag.Parse()
	if len(namespace) == 0 || len(service) == 0 {
		log.Print("namespace and service are required")
		return
	}
	provider, err := api.NewProviderAPI()
	if nil != err {
		log.Fatalf("fail to create consumerAPI, err is %v", err)
	}
	defer provider.Destroy()

	log.Printf("start to invoke register operation")
	registerRequest := &api.InstanceRegisterRequest{}
	registerRequest.Service = service
	registerRequest.Namespace = namespace
	registerRequest.Host = host
	registerRequest.Port = port
	registerRequest.ServiceToken = token
	registerRequest.SetTTL(10)
	resp, err := provider.Register(registerRequest)
	if nil != err {
		log.Fatalf("fail to register instance, err is %v", err)
	}
	log.Printf("register response: instanceId %s", resp.InstanceID)

	log.Printf("start to invoke heartbeat operation")
	for i := 0; i < 10; i++ {
		log.Printf("do heartbeat, %d time", i)
		heartbeatRequest := &api.InstanceHeartbeatRequest{}
		heartbeatRequest.InstanceID = resp.InstanceID
		heartbeatRequest.Namespace = namespace
		heartbeatRequest.Service = service
		heartbeatRequest.Host = host
		heartbeatRequest.Port = port
		heartbeatRequest.ServiceToken = token
		err = provider.Heartbeat(heartbeatRequest)
		if nil != err {
			log.Fatalf("fail to heartbeat instance, err is %v", err)
		}
		time.Sleep(2 * time.Second)
	}

	log.Printf("start to invoke deregister operation")
	// deregisterRequest := &api.InstanceDeRegisterRequest{}
	// deregisterRequest.Service = service
	// deregisterRequest.Namespace = namespace
	// deregisterRequest.Host = host
	// deregisterRequest.Port = port
	// deregisterRequest.ServiceToken = token
	// err = provider.Deregister(deregisterRequest)
	// if nil != err {
	// 	log.Fatalf("fail to deregister instance, err is %v", err)
	// }

	wait := &sync.WaitGroup{}
	wait.Add(1)
	wait.Wait()
}
