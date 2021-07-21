/**
 * Tencent is pleased to support the open source community by making CL5 available.
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
	//serverAddr := argsWithoutProg[0]
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
	err = plog.GetBaseLogger().SetLogLevel(logLevel)
	if nil != err {
		log.Fatalf("fail to SetLogLevel, err is %v", err)
	}
	//获取默认配置
	provider, err := api.NewProviderAPI()
	if nil != err {
		log.Fatalf("fail to create provider api by default config file, err %v", err)
	}
	defer provider.Destroy()
	//执行心跳上报
	//注意：启用心跳上报需要在注册节点的时候设置TTL参数，例子TTL设置2秒  heartbeat周期1.5秒
	for i := 0; i < count; i++ {
		log.Printf("start to heartbeat, index %d", i)
		hbRequest := &api.InstanceHeartbeatRequest{}
		hbRequest.Namespace = namespace
		hbRequest.Service = service
		hbRequest.Host = ip
		hbRequest.Port = port
		hbRequest.ServiceToken = token
		if err = provider.Heartbeat(hbRequest); nil != err {
			log.Printf("fail to heartbeat, error is %v", err)
		} else {
			log.Printf("success to call heartbeat for index %d", i)
		}
		<-time.After(1500 * time.Millisecond)
	}

}
