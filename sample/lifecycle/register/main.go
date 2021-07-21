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
)

const logLevel = api.InfoLog

//主入口函数
func main() {
	go func() {
		log.Println(http.ListenAndServe("localhost:6070", nil))
	}()

	argsWithoutProg := os.Args[1:]
	if len(argsWithoutProg) < 5 {
		log.Fatalf("using %s <namespace> <service> <service-token> <ip> <port>", os.Args[0])
	}
	var err error
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
	//获取默认配置
	provider, err := api.NewProviderAPI()
	if nil != err {
		log.Fatalf("fail to create provider api by default config file, err %v", err)
	}
	defer provider.Destroy()
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
