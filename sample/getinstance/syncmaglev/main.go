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
	"github.com/polarismesh/polaris-go/api"
	plog "github.com/polarismesh/polaris-go/pkg/log"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

const logLevel = plog.InfoLog

//主入口函数
func main() {
	go func() {
		log.Println(http.ListenAndServe("localhost:6070", nil))
	}()
	var err error
	argsWithoutProg := os.Args[1:]
	if len(argsWithoutProg) < 3 {
		log.Fatalf("using %s <namespace> <service> <seconds>", os.Args[0])
	}
	namespace := argsWithoutProg[0]
	service := argsWithoutProg[1]
	seconds, err := strconv.Atoi(argsWithoutProg[2])
	if nil != err {
		log.Fatalf("fail to convert seconds %s to int, err %v", argsWithoutProg[2], err)
	}
	err = plog.GetBaseLogger().SetLogLevel(logLevel)
	if nil != err {
		log.Fatalf("fail to SetLogLevel, err is %v", err)
	}
	cfg := api.NewConfiguration()
	//设置负载均衡算法为maglev
	cfg.GetConsumer().GetLoadbalancer().SetType(api.LBPolicyMaglev)
	cfg.GetConsumer().GetServiceRouter().SetEnableRecoverAll(false)
	//创建consumerAPI实例
	//注意该实例所有方法都是协程安全，一般用户进程只需要创建一个consumerAPI,重复使用即可
	//切勿每次调用之前都创建一个consumerAPI
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	if nil != err {
		log.Fatalf("fail to create ConsumerAPI by default configuration, err is %v", err)
	}
	defer consumer.Destroy()

	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	hashKey := []byte("abc")
	for {
		if time.Now().After(deadline) {
			break
		}
		var flowId uint64
		var getInstancesReq *api.GetOneInstanceRequest
		getInstancesReq = &api.GetOneInstanceRequest{}
		getInstancesReq.FlowID = atomic.AddUint64(&flowId, 1)
		getInstancesReq.Namespace = namespace
		getInstancesReq.Service = service
		getInstancesReq.HashKey = hashKey
		//假如用户需要使用规则路由等能力，则可以在这里通过SourceService属性设置主调服务的过滤标签
		//getInstancesReq.SourceService = &model.ServiceInfo{
		//	Namespace: "Development",
		//	Service:   "trpc.fcgi.FcgiOnBoardServer.x",
		//	Metadata: map[string]string{
		//		"env": "32d4ffcd",
		//	},
		//}
		startTime := time.Now()
		//进行服务发现，获取单一服务实例
		getInstResp, err := consumer.GetOneInstance(getInstancesReq)
		if nil != err {
			log.Fatalf("fail to sync GetOneInstance, err is %v", err)
		}
		consumeDuration := time.Since(startTime)
		log.Printf("success to sync GetOneInstance by maglev hash, count is %d, consume is %v\n",
			len(getInstResp.Instances), consumeDuration)
		targetInstance := getInstResp.Instances[0]
		log.Printf("sync instance is id=%s, address=%s:%d\n",
			targetInstance.GetId(), targetInstance.GetHost(), targetInstance.GetPort())
		//构造请求，进行服务调用结果上报
		svcCallResult := &api.ServiceCallResult{}
		//设置被调的实例信息
		svcCallResult.SetCalledInstance(targetInstance)
		//设置服务调用结果，枚举，成功或者失败
		svcCallResult.SetRetStatus(api.RetSuccess)
		//设置服务调用返回码
		svcCallResult.SetRetCode(0)
		//设置服务调用时延信息
		svcCallResult.SetDelay(consumeDuration)
		//执行调用结果上报
		err = consumer.UpdateServiceCallResult(svcCallResult)
		if nil != err {
			log.Fatalf("fail to UpdateServiceCallResult, err is %v", err)
		}
		time.Sleep(1 * time.Second)
	}
	log.Printf("success to sync get one instance")

}
