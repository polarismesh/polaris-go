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
	"flag"
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/config"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
)

var (
	namespace  string
	service    string
	port       int64
	configPath string
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "CircuitBreakerServiceServer", "service")
	flag.Int64Var(&port, "port", 18080, "port")
	flag.StringVar(&configPath, "config", "./polaris.yaml", "path for config file")
}

type customCodeConvert struct {
}

func (c *customCodeConvert) OnSuccess(val interface{}) string {
	log.Printf("customCodeConvert OnSuccess get input[%+v]\n", val)
	resp, ok := val.(commonResponse)
	if !ok {
		return "500"
	}
	return strconv.Itoa(resp.code)
}

func (c *customCodeConvert) OnError(err error) string {
	log.Printf("customCodeConvert OnError get input[%+v]\n", err)
	return "400"
}

type commonResponse struct {
	code int
	body string
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

func (svr *PolarisConsumer) discoverInstance() (string, error) {
	getOneRequest := &polaris.GetOneInstanceRequest{}
	getOneRequest.Namespace = namespace
	getOneRequest.Service = service
	oneInstResp, err := svr.consumer.GetOneInstance(getOneRequest)
	if err != nil {
		log.Printf("[error] fail to getOneInstance, err is %v", err)
		return "", err
	}
	instance := oneInstResp.GetInstance()
	if nil != instance {
		log.Printf("instance getOneInstance is %s:%d", instance.GetHost(), instance.GetPort())
	}
	return fmt.Sprintf("%s:%d", instance.GetHost(), instance.GetPort()), nil
}

func (svr *PolarisConsumer) runWebServer() {
	dealF := svr.circuitbreak.MakeFunctionDecorator(func(ctx context.Context, args interface{}) (interface{}, error) {
		resp, err := http.Get(fmt.Sprintf("http://%+v/echo", args))
		if resp != nil {
			defer resp.Body.Close()
		}
		if err != nil {
			return nil, err
		}
		data, _ := ioutil.ReadAll(resp.Body)
		return commonResponse{
			code: resp.StatusCode,
			body: string(data),
		}, nil
	}, &api.RequestContext{
		RequestContext: model.RequestContext{
			Callee: &model.ServiceKey{
				Namespace: namespace,
				Service:   service,
			},
			CodeConvert: &customCodeConvert{},
		},
	})

	http.HandleFunc("/echo", func(rw http.ResponseWriter, r *http.Request) {
		log.Printf("start to invoke getOneInstance operation")
		endpoint, err := svr.discoverInstance()
		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[errot] discover instance fail : %s", err)))
			return
		}
		ret, abort, err := dealF(context.Background(), endpoint)
		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[errot] fail : %s", err)))
			return
		}
		if abort != nil {
			rw.WriteHeader(abort.GetFallbackCode())
			for k, v := range abort.GetFallbackHeaders() {
				rw.Header().Add(k, v)
			}
			_, _ = rw.Write([]byte(abort.GetFallbackBody()))
			return
		}
		rw.WriteHeader(http.StatusOK)
		resp, _ := ret.(commonResponse)
		_, _ = rw.Write([]byte(resp.body))
		return
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
