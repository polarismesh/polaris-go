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

	"github.com/polarismesh/polaris-go"
)

var (
	namespace string
	service   string
	port      int64
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "RateLimitEchoServer", "service")
	flag.Int64Var(&port, "port", 18080, "port")
}

// PolarisConsumer is a consumer of ratelimit service
type PolarisConsumer struct {
	consumer  polaris.ConsumerAPI
	namespace string
	service   string
}

// Run starts the consumer
func (svr *PolarisConsumer) Run() {
	svr.runWebServer()
}

func (svr *PolarisConsumer) runWebServer() {
	http.HandleFunc("/echo", func(rw http.ResponseWriter, r *http.Request) {
		log.Printf("start to invoke getOneInstance operation")
		getOneRequest := &polaris.GetOneInstanceRequest{}
		getOneRequest.Namespace = namespace
		getOneRequest.Service = service
		oneInstResp, err := svr.consumer.GetOneInstance(getOneRequest)
		if err != nil {
			log.Printf("[error] fail to getOneInstance, err is %v", err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[error] fail to getOneInstance, err is %v", err)))
			return
		}
		instance := oneInstResp.GetInstance()
		if nil != instance {
			log.Printf("instance getOneInstance is %s:%d", instance.GetHost(), instance.GetPort())
		}

		req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s:%d/echo", instance.GetHost(), instance.GetPort()), nil)
		if err != nil {
			log.Printf("[errot] new request to %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[errot] send request to %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)))
			return
		}

		req.Header = r.Header
		log.Printf("[info] req header : %v", req.Header)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("[errot] send request to %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[errot] send request to %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)))
			return
		}

		defer resp.Body.Close()

		data, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Printf("[error] read resp from %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[error] read resp from %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)))
			return
		}
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write(data)
	})

	log.Printf("start run web server, port : %d", port)

	if err := http.ListenAndServe(fmt.Sprintf("0.0.0.0:%d", port), nil); err != nil {
		log.Fatalf("[ERROR]fail to run webServer, err is %v", err)
	}
}

func main() {
	initArgs()
	flag.Parse()
	if len(namespace) == 0 || len(service) == 0 {
		log.Print("namespace and service are required")
		return
	}
	consumer, err := polaris.NewConsumerAPI()
	// 或者使用以下方法,则不需要创建配置文件
	// consumer, err = polaris.NewConsumerAPIByAddress("127.0.0.1:8091")

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
