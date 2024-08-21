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
	"strconv"
	"time"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/pkg/model"
)

var (
	namespace string
	service   string
	port      int64
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "CircuitBreakerEchoServer", "service")
	flag.Int64Var(&port, "port", 18080, "port")
}

// PolarisConsumer is a consumer of the circuit breaker service.
type PolarisConsumer struct {
	consumer     polaris.ConsumerAPI
	circuitbreak polaris.CircuitBreakerAPI
	namespace    string
	service      string
}

// Run is the consumer's main function.
func (svr *PolarisConsumer) Run() {
	svr.runWebServer()
}

func (svr *PolarisConsumer) discoverInstance() (string, model.Resource, error) {
	req := &polaris.GetInstancesRequest{}
	req.Namespace = namespace
	req.Service = service

	instancesResp, err := svr.consumer.GetInstances(req)
	if err != nil {
		log.Printf("[error] fail to getInstances, err is %v", err)
		return "", nil, err
	}
	for _, ins := range instancesResp.GetInstances() {
		cbStatus := "close"
		if ins.GetCircuitBreakerStatus() != nil {
			cbStatus = ins.GetCircuitBreakerStatus().GetStatus().String()
		}
		log.Printf("%s:%d. cb status: %s", ins.GetHost(), ins.GetPort(), cbStatus)
	}

	getOneRequest := &polaris.GetOneInstanceRequest{}
	getOneRequest.Namespace = namespace
	getOneRequest.Service = service
	// 允许返回熔断半开的实例
	getOneRequest.IncludeCircuitBreakInstances = true
	oneInstResp, err := svr.consumer.GetOneInstance(getOneRequest)
	if err != nil {
		log.Printf("[error] fail to getOneInstance, err is %v", err)
		return "", nil, err
	}
	instance := oneInstResp.GetInstance()
	if nil != instance {
		log.Printf("instance getOneInstance is %s:%d", instance.GetHost(), instance.GetPort())
	}

	insRes, _ := model.NewInstanceResource(&model.ServiceKey{
		Namespace: namespace,
		Service:   service,
	}, nil, "http", instance.GetHost(), instance.GetPort())

	return fmt.Sprintf("%s:%d", instance.GetHost(), instance.GetPort()), insRes, nil
}

func (svr *PolarisConsumer) runWebServer() {
	http.HandleFunc("/echo", func(rw http.ResponseWriter, r *http.Request) {
		log.Printf("start to invoke getOneInstance operation")
		endpoint, insRes, err := svr.discoverInstance()
		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[errot] discover instance fail : %s", err)))
			return
		}
		start := time.Now()
		resp, err := http.Get(fmt.Sprintf("http://%+v/echo", endpoint))
		if resp != nil {
			defer resp.Body.Close()
		}
		if err != nil {
			svr.circuitbreak.Report(&model.ResourceStat{
				Delay:     time.Since(start),
				RetStatus: model.RetFail,
				RetCode:   strconv.Itoa(http.StatusInternalServerError),
				Resource:  insRes,
			})
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[errot] discover instance fail : %s", err)))
			return
		}
		if resp.StatusCode != http.StatusOK {
			svr.circuitbreak.Report(&model.ResourceStat{
				Delay:     time.Since(start),
				RetStatus: model.RetFail,
				RetCode:   strconv.Itoa(resp.StatusCode),
				Resource:  insRes,
			})
		} else {
			svr.circuitbreak.Report(&model.ResourceStat{
				Delay:     time.Since(start),
				RetStatus: model.RetSuccess,
				RetCode:   strconv.Itoa(http.StatusOK),
				Resource:  insRes,
			})
		}
		ret, _ := ioutil.ReadAll(resp.Body)
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte(ret))
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
	sdkCtx, err := polaris.NewSDKContext()
	if err != nil {
		log.Fatalf("fail to create consumerAPI, err is %v", err)
	}
	consumer := polaris.NewConsumerAPIByContext(sdkCtx)
	circuitBreaker := polaris.NewCircuitBreakerAPIByContext(sdkCtx)
	defer func() {
		sdkCtx.Destroy()
	}()

	svr := &PolarisConsumer{
		consumer:     consumer,
		circuitbreak: circuitBreaker,
		namespace:    namespace,
		service:      service,
	}

	svr.Run()
}
