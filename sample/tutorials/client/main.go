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
	"fmt"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

//主流程
func main() {
	var err error
	//参数校验
	argsWithoutProg := os.Args[1:]
	if len(argsWithoutProg) < 3 {
		log.Fatalf("using %s <namespace> <service> <requestNum>", os.Args[0])
	}
	namespace := argsWithoutProg[0]
	service := argsWithoutProg[1]
	var requestNum int
	requestNum, err = strconv.Atoi(argsWithoutProg[2])
	if err != nil {
		log.Fatalf("fail to convert requestNum %s to int, err: %v", argsWithoutProg[0])
	}
	if requestNum <= 0 {
		log.Fatalf("requestNum must be greater than 0, which is %d now", requestNum)
	}

	//创建consumerAPI，大多数情况下，每个进程创建一个全局的consumerAPI即可
	//通过consumeAPI获取服务实例信息是线程安全的
	var consumer api.ConsumerAPI
	consumer, err = api.NewConsumerAPI()
	if err != nil {
		log.Fatalf("fail to create consumerAPI, err: %v", err)
	}
	//最后需要调用destroy方法，防止连接、内存泄漏
	defer consumer.Destroy()

	var flowId uint64

	//向服务发起requestNum次请求
	for i := 0; i < requestNum; i++ {
		log.Printf("%d request", i)

		//调用consumerAPI，获取一个demo服务实例
		getOneInstanceReq := &api.GetOneInstanceRequest{}
		getOneInstanceReq.Namespace = namespace
		getOneInstanceReq.Service = service
		getOneInstanceReq.FlowID = atomic.AddUint64(&flowId, 1)
		var resp *model.InstancesResponse
		resp, err = consumer.GetOneInstance(getOneInstanceReq)
		if err != nil {
			log.Fatalf("fail to getOneInstance of service(Namespace=%s, ServiceName=%s), err: %v",
				namespace, service, err)
		}
		targetInstance := resp.Instances[0]

		//服务调用结果，用于在后面进行调用结果上报
		svcCallResult := &api.ServiceCallResult{}
		//将服务上报对象设置为获取到的实例
		svcCallResult.SetCalledInstance(targetInstance)

		log.Printf("sending request to instance %s:%d", targetInstance.GetHost(), targetInstance.GetPort())
		requestStartTime := time.Now()
		//发起http请求的地址
		httpAddr := fmt.Sprintf("http://%s:%d", targetInstance.GetHost(), targetInstance.GetPort())
		httpResp, err := http.Get(httpAddr)
		//设置调用耗时
		svcCallResult.SetDelay(time.Since(requestStartTime))
		if err != nil {
			log.Printf("fail to get response from http server %s", httpAddr)
			//如果报错了，设置结果为fial
			svcCallResult.SetRetStatus(api.RetFail)
			//设置访问错误的返回码
			svcCallResult.SetRetCode(-1)
		} else {
			var body []byte
			body, err = ioutil.ReadAll(httpResp.Body)
			if err != nil {
				log.Printf("fail to read resp body from %s", httpAddr)
				//读取返回内容失败，设置结果为fail
				svcCallResult.SetRetStatus(api.RetFail)
				//设置读取失败的返回码
				svcCallResult.SetRetCode(-2)
			} else {
				log.Printf("resp from %s: %s", httpAddr, string(body))
				//成功读取返回内容，设置返回码
				svcCallResult.SetRetCode(0)
				//设置结果为success
				svcCallResult.SetRetStatus(api.RetSuccess)
			}
		}

		//最后上报调用结果
		err = consumer.UpdateServiceCallResult(svcCallResult)
		if err != nil {
			log.Fatalf("fail to UpdateServiceCallResult, err is %v", err)
		}
	}
}
