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
	"fmt"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"sync/atomic"
	"time"
)

const logLevel = api.InfoLog


func GetInstanceEvent(ch <-chan model.SubScribeEvent) (model.SubScribeEvent, error) {
	select {
	case e := <-ch:
		return e, nil
	default:
		return nil, nil
	}
}

//主入口函数
func main() {
	go func() {
		log.Println(http.ListenAndServe("localhost:6070", nil))
	}()
	var err error
	argsWithoutProg := os.Args[1:]
	if len(argsWithoutProg) < 2 {
		log.Fatalf("using %s <namespace> <service>", os.Args[0])
	}
	namespace := argsWithoutProg[0]
	service := argsWithoutProg[1]
	err = api.GetBaseLogger().SetLogLevel(logLevel)
	if nil != err {
		log.Fatalf("fail to SetLogLevel, err is %v", err)
	}
	cfg := api.NewConfiguration()
	//设置负载均衡算法为hashRing
	cfg.GetConsumer().GetLoadbalancer().SetType(api.LBPolicyRingHash)
	cfg.GetConsumer().GetSubScribe().SetType(config.SubscribeLocalChannel)
	//创建consumerAPI实例
	//注意该实例所有方法都是协程安全，一般用户进程只需要创建一个consumerAPI,重复使用即可
	//切勿每次调用之前都创建一个consumerAPI
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	if nil != err {
		log.Fatalf("fail to create ConsumerAPI by default configuration, err is %v", err)
	}
	defer consumer.Destroy()

	hashKey := []byte("abc")

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
	//进行服务发现，获取单一服务实例
	//getInstResp, err := consumer.GetOneInstance(getInstancesReq)
	//if nil != err {
	//	log.Fatalf("fail to sync GetOneInstance, err is %v", err)
	//}
	//fmt.Println(getInstResp.GetInstances())

	key := model.ServiceKey{
		Namespace: namespace,
		Service:   service,
	}
	watchReq := api.WatchServiceRequest{}
	watchReq.Key = key
	watchRsp, err := consumer.WatchService(&watchReq)
	if err != nil {
		fmt.Println("GetEvent err: ", err)
		return
	}
	ch := watchRsp.EventChannel
	for {
		event, err := GetInstanceEvent(ch)
		if err != nil {
			fmt.Println(err)
			time.Sleep(time.Second * 1)
			continue
		}
		if event == nil {
			time.Sleep(time.Second * 3)
			continue
		}
		eType := event.GetSubScribeEventType()
		fmt.Println(eType)
		if eType == api.EventInstance {
			insEvent := event.(*model.InstanceEvent)
			if insEvent.AddEvent != nil {
				fmt.Println("==========add instances: ")
				for _, v := range insEvent.AddEvent.Instances {
					fmt.Println(v.GetId(), v.GetHost(), v.GetPort())
				}
			}
			if insEvent.UpdateEvent != nil {
				fmt.Println("==========update instances: ")
				for _, v := range insEvent.UpdateEvent.UpdateList {
					fmt.Printf("host:%s before: %s after:%s", v.After.GetHost(), v.Before.GetRevision(), v.After.GetRevision())
				}
			}
			if insEvent.DeleteEvent != nil {
				fmt.Println("==========delete instances: ")
				for _, v := range insEvent.DeleteEvent.Instances {
					fmt.Println(v.GetId(), v.GetHost(), v.GetPort())
				}
			}
		}
		time.Sleep(time.Second * 1)
	}

	log.Printf("success to sync get one instance")

}
