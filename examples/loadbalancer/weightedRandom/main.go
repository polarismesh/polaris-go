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
	"fmt"
	"io/ioutil"
	"log"
	"net/http"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/pkg/config"
)

var (
	namespace  string
	service    string
	port       int64
	token      string
	lbPolicy   string
	hashKey    string
	configPath string
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "LoadBalanceEchoServer", "service")
	flag.Int64Var(&port, "port", 17080, "port")
	flag.StringVar(&token, "token", "", "token")
	flag.StringVar(&lbPolicy, "lbPolicy", "weightedRandom", "loadBalancer plugin")
	flag.StringVar(&hashKey, "hashKey", "example-hash-key", "hashKey")
	flag.StringVar(&configPath, "config", "./polaris.yaml", "path for config file")
}

// PolarisConsumer .
type PolarisConsumer struct {
	consumer  polaris.ConsumerAPI
	router    polaris.RouterAPI
	namespace string
	service   string
}

// Run .
func (svr *PolarisConsumer) Run() {
	svr.runWebServer()
}

func (svr *PolarisConsumer) runWebServer() {
	http.HandleFunc("/echo", func(rw http.ResponseWriter, r *http.Request) {
		log.Printf("start to invoke getLoadBalancer instance operation")
		getAllRequest := &polaris.GetAllInstancesRequest{}
		getAllRequest.Namespace = namespace
		getAllRequest.Service = service
		instancesResp, err := svr.consumer.GetAllInstances(getAllRequest)
		if nil != err {
			log.Printf("[error] fail to getAllInstances, err is %v", err)
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte(fmt.Sprintf("fail to getAllInstances, err is %v", err)))
			return
		}
		log.Printf("all instances count %d", len(instancesResp.Instances))
		for i, inst := range instancesResp.Instances {
			cbStatus := "none"
			if inst.GetCircuitBreakerStatus() != nil {
				cbStatus = fmt.Sprintf("%d", inst.GetCircuitBreakerStatus().GetStatus())
			}
			log.Printf("instance[%d]: %s:%d weight=%d healthy=%v isolated=%v circuitBreaker=%s",
				i, inst.GetHost(), inst.GetPort(), inst.GetWeight(), inst.IsHealthy(), inst.IsIsolated(), cbStatus)
		}
		lbRequest := &polaris.ProcessLoadBalanceRequest{}
		lbRequest.DstInstances = instancesResp
		lbRequest.LbPolicy = lbPolicy
		log.Printf("loadBalancer request %s", mustJson(lbRequest))
		lbResp, err := svr.router.ProcessLoadBalance(lbRequest)
		if nil != err || lbResp.GetInstances() == nil {
			log.Printf("[error] fail to processLoadBalance, err is %v", err)
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte(fmt.Sprintf("fail to processLoadBalance, err is %v", err)))
			return
		}
		instance := lbResp.GetInstance()
		log.Printf("processLoadBalance getOneInstance is %s:%d", instance.GetHost(), instance.GetPort())
		if !instance.IsHealthy() {
			log.Printf("handleLoadBalancer get random instance unHealthy instance: %s:%d",
				instance.GetHost(), instance.GetPort())
		}

		resp, err := http.Get(fmt.Sprintf("http://%s:%d/echo", instance.GetHost(), instance.GetPort()))
		if err != nil {
			log.Printf("[error] send request to %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[error] send request to %s:%d fail : %s",
				instance.GetHost(), instance.GetPort(), err)))
			return
		}
		defer resp.Body.Close()

		data, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Printf("[error] read resp from %s:%d fail: %s", instance.GetHost(), instance.GetPort(), err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[error] read resp from %s:%d fail : %s",
				instance.GetHost(), instance.GetPort(), err)))
			return
		}
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write(data)
	})

	http.HandleFunc("/call", func(rw http.ResponseWriter, r *http.Request) {
		log.Printf("start to invoke getOneInstance operation")
		getOneRequest := &polaris.GetOneInstanceRequest{}
		getOneRequest.Namespace = namespace
		getOneRequest.Service = service
		getOneRequest.LbPolicy = lbPolicy
		getOneRequest.HashKey = []byte(hashKey)
		oneInstResp, err := svr.consumer.GetOneInstance(getOneRequest)
		if nil != err {
			log.Printf("[error] fail to getOneInstance, err is %v", err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("fail to getOneInstance, err is %v", err)))
			return
		}
		instance := oneInstResp.GetInstance()
		if instance == nil {
			log.Printf("[error] getOneInstance returned nil instance")
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte("getOneInstance returned nil instance"))
			return
		}
		log.Printf("getOneInstance is %s:%d weight=%d healthy=%v isolated=%v",
			instance.GetHost(), instance.GetPort(), instance.GetWeight(), instance.IsHealthy(), instance.IsIsolated())

		resp, err := http.Get(fmt.Sprintf("http://%s:%d/echo", instance.GetHost(), instance.GetPort()))
		if err != nil {
			log.Printf("[error] send request to %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[error] send request to %s:%d fail : %s",
				instance.GetHost(), instance.GetPort(), err)))
			return
		}
		defer resp.Body.Close()

		data, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Printf("[error] read resp from %s:%d fail: %s", instance.GetHost(), instance.GetPort(), err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[error] read resp from %s:%d fail : %s",
				instance.GetHost(), instance.GetPort(), err)))
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
	cfg, err := config.LoadConfigurationByFile(configPath)
	if err != nil {
		log.Fatalf("load configuration by file %s failed: %v", configPath, err)
	}
	sdkCtx, err := polaris.NewSDKContextByConfig(cfg)
	if err != nil {
		log.Fatalf("fail to create sdkContext, err is %v", err)
	}
	defer sdkCtx.Destroy()

	svr := &PolarisConsumer{
		consumer:  polaris.NewConsumerAPIByContext(sdkCtx),
		router:    polaris.NewRouterAPIByContext(sdkCtx),
		namespace: namespace,
		service:   service,
	}

	svr.Run()

}

func mustJson(v interface{}) string {
	d, _ := json.Marshal(v)
	return string(d)
}
