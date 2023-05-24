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
	"encoding/json"
	"flag"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/plugin/servicerouter/zeroprotect"
)

var (
	namespace string
	service   string
	token     string
	insCount  int64
	basePort  = 8080
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "DiscoverEchoServer", "service")
	// 当北极星开启鉴权时，需要配置此参数完成相关的权限检查
	flag.StringVar(&token, "token", "", "token")
	flag.Int64Var(&insCount, "ins_count", 10, "ins_count")
}

func main() {
	initArgs()

	sdkCtx, err := polaris.NewSDKContext()
	if err != nil {
		log.Fatal(err)
	}
	provider := polaris.NewProviderAPIByContext(sdkCtx)
	consumer := polaris.NewConsumerAPIByContext(sdkCtx)
	router := polaris.NewRouterAPIByContext(sdkCtx)

	wait, _ := createInstances(provider)
	// 等待
	wait.Wait()
	// 等待 SDK 和 服务端的数据同步，等待两个周期确保一定可以获取到最新的数据
	time.Sleep(5 * time.Second)

	zeroprotectIns := map[string]struct{}{}
	allReq := &polaris.GetAllInstancesRequest{}
	allReq.Service = service
	allReq.Namespace = namespace
	resp, err := consumer.GetAllInstances(allReq)
	if err != nil {
		log.Fatal(err)
	}
	lastHeartbeatTime := int64(0)
	// 实例全部不健康
	for i := range resp.GetInstances() {
		ins := resp.GetInstances()[i]
		if ins.IsHealthy() {
			log.Fatalf("ins: %s still health", ins.GetId())
		}
		beatTime := ins.GetMetadata()[zeroprotect.MetadataInstanceLastHeartbeatTime]
		sec, _ := strconv.ParseInt(beatTime, 10, 64)
		if lastHeartbeatTime <= sec {
			lastHeartbeatTime = sec
		}
	}
	for i := range resp.GetInstances() {
		ins := resp.GetInstances()[i]
		beatTime := ins.GetMetadata()[zeroprotect.MetadataInstanceLastHeartbeatTime]
		sec, _ := strconv.ParseInt(beatTime, 10, 64)
		if zeroprotect.NeedZeroProtect(lastHeartbeatTime, sec, 2) {
			zeroprotectIns[ins.GetId()] = struct{}{}
		}
	}

	log.Printf("====== expect zero protect ======\n%s\n====== expect zero protect ======", zeroprotectIns)

	useConsumerAPI(consumer, zeroprotectIns)
	useRouterAPI(consumer, router, zeroprotectIns)
}

func useConsumerAPI(consumer polaris.ConsumerAPI, zeroprotectIns map[string]struct{}) {
	insReq := &polaris.GetInstancesRequest{}
	insReq.Service = service
	insReq.Namespace = namespace

	// 由于 polaris.yaml afterChain 配置的为 zeroProtectRouter，因此这里不会在走原先默认的全死全活路由策略
	resp, err := consumer.GetInstances(insReq)
	if err != nil {
		log.Fatal(err)
	}

	insJson, _ := json.Marshal(resp.GetInstances())
	log.Printf("====== useConsumerAPI zero protect ======\n%s\n====== useConsumerAPI zero protect ======", string(insJson))

	if len(resp.GetInstances()) == int(insCount) {
		log.Fatal("zero protect fail")
	}

	if len(zeroprotectIns) != len(resp.GetInstances()) {
		log.Fatalf("zero protect recover instance count:%d not expect:%d", len(resp.GetInstances()), len(zeroprotectIns))
	}
}

func useRouterAPI(consumer polaris.ConsumerAPI, router polaris.RouterAPI, zeroprotectIns map[string]struct{}) {
	allReq := &polaris.GetAllInstancesRequest{}
	allReq.Service = service
	allReq.Namespace = namespace
	resp, err := consumer.GetAllInstances(allReq)
	if err != nil {
		log.Fatal(err)
	}
	// 实例全部不健康
	for i := range resp.GetInstances() {
		ins := resp.GetInstances()[i]
		if ins.IsHealthy() {
			log.Fatalf("ins: %s still health", ins.GetId())
		}
	}

	routeReq := &polaris.ProcessRoutersRequest{}
	routeReq.DstInstances = resp
	// 默认配置文件走 zeroProtectRouter
	routeReq.Routers = []string{}
	routeResp, err := router.ProcessRouters(routeReq)
	if err != nil {
		log.Fatal(err)
	}

	insJson, _ := json.Marshal(routeResp.GetInstances())
	log.Printf("====== useRouterAPI zero protect ======\n%s\n====== useRouterAPI zero protect ======", string(insJson))

	if len(routeResp.GetInstances()) == int(insCount) {
		log.Fatal("zero protect fail")
	}

	if len(zeroprotectIns) != len(routeResp.GetInstances()) {
		log.Fatalf("zero protect recover instance count:%d not expect:%d", len(routeResp.GetInstances()), len(zeroprotectIns))
	}
	routeReq = &polaris.ProcessRoutersRequest{}
	routeReq.DstInstances = resp
	// 显示设置走全死全活路由
	routeReq.Routers = []string{config.DefaultServiceRouterFilterOnly}
	routeResp, err = router.ProcessRouters(routeReq)
	if err != nil {
		log.Fatal(err)
	}

	insJson, _ = json.Marshal(routeResp.GetInstances())
	log.Printf("====== useRouterAPI not zero protect ======\n%s\n====== useRouterAPI not zero protect ======", string(insJson))
	if len(routeResp.GetInstances()) != int(insCount) {
		log.Fatal("filterOnly fail")
	}

	if len(zeroprotectIns) == len(routeResp.GetInstances()) {
		log.Fatal("can't use zero protect recover instance")
	}
}

func createInstances(provider polaris.ProviderAPI) (*sync.WaitGroup, *sync.Map) {
	wait := &sync.WaitGroup{}
	wait.Add(int(insCount))

	beatTime := &sync.Map{}
	for i := 0; i < int(insCount); i++ {
		registerRequest := &polaris.InstanceRegisterRequest{}
		registerRequest.Service = service
		registerRequest.Namespace = namespace
		registerRequest.Host = "127.0.0.1"
		registerRequest.Port = basePort + i
		registerRequest.ServiceToken = token
		registerRequest.SetTTL(2)
		resp, err := provider.Register(registerRequest)
		if err != nil {
			log.Fatal(err)
		}
		// 维持三次心跳
		go func(id string, port, a int) {
			defer wait.Done()
			for tick := 0; tick < 2*a; tick++ {
				beatRequest := &polaris.InstanceHeartbeatRequest{}
				beatRequest.InstanceID = id
				beatRequest.Service = service
				beatRequest.Namespace = namespace
				beatRequest.Host = "127.0.0.1"
				beatRequest.Port = port

				if err := provider.Heartbeat(beatRequest); err != nil {
					log.Printf("%s heartbeat fail : %+v", beatRequest, err)
				}

				beatTime.Store(id, time.Now().Unix())
				time.Sleep(2 * time.Second)
			}
			// 等待实例转为不健康
			for tick := 0; tick < 3; tick++ {
				time.Sleep(3 * time.Second)
			}
		}(resp.InstanceID, basePort+i, i)
	}

	return wait, beatTime
}
