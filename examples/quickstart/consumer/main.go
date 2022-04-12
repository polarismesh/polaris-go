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
	"fmt"
	"io/ioutil"
	"log"
	"net/http"

	"github.com/polarismesh/polaris-go/api"
)

var (
	namespace string
	service   string
	port      int64
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "EchoServerGolang", "service")
}

// PolarisConsumer 主调方的封装
type PolarisConsumer struct {
	consumer  api.ConsumerAPI
	namespace string
	service   string
}

// Run 启动 consumer 的 http 服务
func (svr *PolarisConsumer) Run() {
	svr.runWebServer()
}

// runWebServer consumer 的 http 服务，通过对该接口的调用，触发完成 consumer 对 provide 的调用并返回调用结果给用户
func (svr *PolarisConsumer) runWebServer() {
	http.HandleFunc("/echo", func(rw http.ResponseWriter, r *http.Request) {
		log.Printf("[info] start to invoke getOneInstance operation")
		getOneRequest := &api.GetOneInstanceRequest{}
		getOneRequest.Namespace = namespace
		getOneRequest.Service = service
		oneInstResp, err := svr.consumer.GetOneInstance(getOneRequest)
		if err != nil {
			log.Printf("[info] fail to getOneInstance, err is %v", err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(err.Error()))
			return
		}
		instance := oneInstResp.GetInstance()
		if nil != instance {
			log.Printf("[info] instance getOneInstance is %s:%d", instance.GetHost(), instance.GetPort())
		}

		// 通过获取到的 provider 的实例 ip、port 对其发起调用
		resp, err := http.Get(fmt.Sprintf("http://%s:%d/echo", instance.GetHost(), instance.GetPort()))
		if err != nil {
			log.Printf("[error] send request to %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(err.Error()))
			return
		}

		defer resp.Body.Close()

		data, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Printf("[error] read resp from %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(err.Error()))
			return
		}

		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write(data)
	})

	if err := http.ListenAndServe(fmt.Sprintf("0.0.0.0:%d", 18080), nil); err != nil {
		log.Fatalf("[ERROR] fail to run webServer, err is %v", err)
	}
}

func main() {
	initArgs()
	flag.Parse()
	if len(namespace) == 0 || len(service) == 0 {
		log.Print("namespace and service are required")
		return
	}
	consumer, err := api.NewConsumerAPI()
	// 或者使用以下方法,则不需要创建配置文件
	//consumer, err = api.NewConsumerAPIByAddress("127.0.0.1:8091")

	if err != nil {
		log.Fatalf("fail to create consumerAPI, err is %v", err)
	}
	defer consumer.Destroy()

	svr := &PolarisConsumer{
		consumer:  consumer,
		namespace: namespace,
		service:   service,
	}

	svr.Run()

}
