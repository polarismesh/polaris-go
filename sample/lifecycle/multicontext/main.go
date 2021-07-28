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
	"context"
	"github.com/polarismesh/polaris-go/api"
	plog "github.com/polarismesh/polaris-go/pkg/log"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strconv"
	"sync"
	"time"
)

const logLevel = api.InfoLog

//主入口函数
func main() {
	go func() {
		log.Println(http.ListenAndServe("localhost:6070", nil))
	}()

	argsWithoutProg := os.Args[1:]
	if len(argsWithoutProg) < 6 {
		log.Fatalf("using %s <namespace> <service> <service-token> <ip> <port> <count>", os.Args[0])
	}
	var err error
	//设置日志级别为Debug
	err = plog.GetBaseLogger().SetLogLevel(logLevel)
	if nil != err {
		log.Fatalf("fail to SetLogLevel, err is %v", err)
	}
	namespace := argsWithoutProg[0]
	service := argsWithoutProg[1]
	token := argsWithoutProg[2]
	ip := argsWithoutProg[3]
	port, err := strconv.Atoi(argsWithoutProg[4])
	if nil != err {
		log.Fatalf("fail to convert port %s to int, err %v", argsWithoutProg[4], err)
	}
	count, err := strconv.Atoi(argsWithoutProg[5])
	if nil != err {
		log.Fatalf("fail to convert count %s to int, err %v", argsWithoutProg[5], err)
	}
	//获取默认配置
	provider, err := api.NewProviderAPI()
	if nil != err {
		log.Fatalf("fail to create provider api by default config file, err %v", err)
	}
	//对于同时存在provider和consumer的场景，建议两个API都使用同一个Context来进行创建
	//通过同一个Context创建的API，只需要在一个地方去destroy就可以
	defer provider.Destroy()
	consumer := api.NewConsumerAPIByContext(provider.SDKContext())
	ctx, cancel := context.WithCancel(context.Background())
	wg := &sync.WaitGroup{}
	workerCount := 2
	wg.Add(workerCount)
	for i := 0; i < workerCount; i++ {
		//同步进行服务发现
		go doGetInstance(consumer, namespace, service, ctx, wg)
	}
	hbCtx, hbCancel := context.WithCancel(context.Background())
	//注册服务
	doRegister(provider, namespace, service, ip, port, token)
	//心跳上报
	go doHeartBeat(provider, count, namespace, service, ip, port, token, hbCancel)
	<-hbCtx.Done()
	cancel()
	wg.Wait()
	//反注册服务
	doDeRegister(provider, namespace, service, ip, port, token)
}

//获取服务实例
func doGetInstance(
	consumer api.ConsumerAPI, namespace string, service string, ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		default:
			time.Sleep(500 * time.Millisecond)
			var getInstancesReq *api.GetOneInstanceRequest
			getInstancesReq = &api.GetOneInstanceRequest{}
			getInstancesReq.Namespace = namespace
			getInstancesReq.Service = service
			startTime := time.Now()
			//进行服务发现，获取单一服务实例
			getInstResp, err := consumer.GetOneInstance(getInstancesReq)
			if nil != err {
				log.Fatalf("fail to sync GetOneInstance, err is %v", err)
			}
			consumeDuration := time.Since(startTime)
			log.Printf("success to sync GetOneInstance, count is %d, consume is %v\n",
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
		}
	}
}

//服务实例注册
func doRegister(
	provider api.ProviderAPI, namespace string, service string, ip string, port int, token string) {
	//注册服务
	request := &api.InstanceRegisterRequest{}
	request.Namespace = namespace
	request.Service = service
	request.ServiceToken = token
	request.Host = ip
	request.Port = port
	request.SetTTL(2)
	resp, err := provider.Register(request)
	if nil != err {
		log.Fatalf("fail to register instance, err %v", err)
	}
	log.Printf("success to register instance, id is %s", resp.InstanceID)
}

//服务实例上报心跳
func doHeartBeat(provider api.ProviderAPI, count int, namespace string,
	service string, ip string, port int, token string, cancel context.CancelFunc) {
	defer cancel()
	//执行心跳上报
	for i := 0; i < count; i++ {
		log.Printf("start to heartbeat, index %d", i)
		hbRequest := &api.InstanceHeartbeatRequest{}
		hbRequest.Namespace = namespace
		hbRequest.Service = service
		hbRequest.Host = ip
		hbRequest.Port = port
		hbRequest.ServiceToken = token
		if err := provider.Heartbeat(hbRequest); nil != err {
			log.Printf("fail to heartbeat, error is %v", err)
		} else {
			log.Printf("success to call heartbeat for index %d", i)
		}
		<-time.After(1500 * time.Millisecond)
	}
}

//服务实例反注册
func doDeRegister(
	provider api.ProviderAPI, namespace string, service string, ip string, port int, token string) {
	//反注册服务
	request := &api.InstanceDeRegisterRequest{}
	request.Namespace = namespace
	request.Service = service
	request.ServiceToken = token
	request.Host = ip
	request.Port = port
	err := provider.Deregister(request)
	if nil != err {
		log.Fatalf("fail to deregister instance, err %v", err)
	}
	log.Printf("success to deregister instance")
}
