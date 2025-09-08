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
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

var (
	selfNamespace   string
	selfService     string
	selfRegister    bool
	calleeNamespace string
	calleeService   string
	port            int
	token           string
	configPath      string
)

func initArgs() {
	flag.StringVar(&selfNamespace, "selfNamespace", "default", "selfNamespace")
	flag.StringVar(&selfService, "selfService", "CircuitBreakerServiceCaller", "selfService")
	flag.BoolVar(&selfRegister, "selfRegister", true, "selfRegister")
	flag.StringVar(&calleeNamespace, "calleeNamespace", "default", "calleeNamespace")
	flag.StringVar(&calleeService, "calleeService", "CircuitBreakerCallee", "calleeService")
	flag.IntVar(&port, "port", 18080, "port")
	flag.StringVar(&token, "token", "", "token")
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

// PolarisClient is a consumer of the circuit breaker calleeService.
type PolarisClient struct {
	provider       polaris.ProviderAPI
	host           string
	isShutdown     bool
	consumer       polaris.ConsumerAPI
	circuitBreaker polaris.CircuitBreakerAPI
	webSvr         *http.Server
}

func (svr *PolarisClient) discoverInstance() (string, error) {
	getOneRequest := &polaris.GetOneInstanceRequest{}
	getOneRequest.Namespace = calleeNamespace
	getOneRequest.Service = calleeService
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

func (svr *PolarisClient) runWebServer() {
	dealF := svr.circuitBreaker.MakeFunctionDecorator(func(ctx context.Context, args interface{}) (interface{}, error) {
		resp, err := http.Get(fmt.Sprintf("http://%+v/echo", args))
		if resp != nil {
			defer resp.Body.Close()
		}
		if err != nil {
			return nil, err
		}
		data, _ := io.ReadAll(resp.Body)
		return commonResponse{
			code: resp.StatusCode,
			body: string(data),
		}, nil
	}, &api.RequestContext{
		RequestContext: model.RequestContext{
			Caller: &model.ServiceKey{
				Namespace: selfNamespace,
				Service:   selfService,
			},
			Callee: &model.ServiceKey{
				Namespace: calleeNamespace,
				Service:   calleeService,
			},
			CodeConvert: &customCodeConvert{},
		},
	})

	http.HandleFunc("/echo", func(rw http.ResponseWriter, r *http.Request) {
		log.Printf("receive echo request from client:%s", r.RemoteAddr)
		endpoint, err := svr.discoverInstance()
		if err != nil {
			log.Printf("[error] fail to discover instance, err is %v", err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[error] discover instance fail : %s", err)))
			return
		}
		ret, abort, err := dealF(context.Background(), endpoint)
		if err != nil {
			log.Printf("[error] fail to invoke dealF, err is %v", err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[error] fail : %s", err)))
			return
		}
		if abort != nil {
			log.Printf("abort is %v", abort)
			rw.WriteHeader(abort.GetFallbackCode())
			for k, v := range abort.GetFallbackHeaders() {
				rw.Header().Add(k, v)
			}
			_, _ = rw.Write([]byte(abort.GetFallbackBody()))
			return
		}
		log.Printf("invoke dealF success, ret is %+v", ret)
		rw.WriteHeader(http.StatusOK)
		resp, _ := ret.(commonResponse)
		_, _ = rw.Write([]byte(resp.body))
		return
	})
	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		log.Fatalf("[ERROR]fail to listen tcp, err is %v", err)
	}

	go func() {
		log.Printf("[INFO] start http server, listen port is %v", ln.Addr().(*net.TCPAddr).Port)
		if err := svr.webSvr.Serve(ln); err != nil {
			svr.isShutdown = false
			log.Fatalf("[ERROR]fail to run webServer, err is %v", err)
		}
	}()
}

func (svr *PolarisClient) runMainLoop() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, []os.Signal{
		syscall.SIGINT, syscall.SIGTERM,
		syscall.SIGSEGV,
	}...)

	for s := range ch {
		log.Printf("catch signal(%+v), stop servers", s)
		if selfRegister {
			svr.isShutdown = true
			svr.deregisterService()
		}
		_ = svr.webSvr.Close()
		return
	}
}

func (svr *PolarisClient) registerService() {
	log.Printf("start to invoke register operation")
	tmpHost, err := getLocalHost(svr.provider.SDKContext().GetConfig().GetGlobal().GetServerConnector().GetAddresses()[0])
	if err != nil {
		panic(fmt.Errorf("error occur while fetching localhost: %v", err))
	}
	svr.host = tmpHost
	registerRequest := &polaris.InstanceRegisterRequest{}
	registerRequest.Service = selfService
	registerRequest.Namespace = selfNamespace
	registerRequest.Host = svr.host
	registerRequest.Port = port
	registerRequest.ServiceToken = token
	resp, err := svr.provider.RegisterInstance(registerRequest)
	if err != nil {
		log.Fatalf("fail to register instance, err is %v", err)
	}
	log.Printf("register response: instanceId %s", resp.InstanceID)
}

func (svr *PolarisClient) deregisterService() {
	log.Printf("start to invoke deregister operation")
	deregisterRequest := &polaris.InstanceDeRegisterRequest{}
	deregisterRequest.Service = selfService
	deregisterRequest.Namespace = selfNamespace
	deregisterRequest.Host = svr.host
	deregisterRequest.Port = port
	deregisterRequest.ServiceToken = token
	if err := svr.provider.Deregister(deregisterRequest); err != nil {
		log.Fatalf("fail to deregister instance, err is %v", err)
	}
	log.Printf("deregister successfully.")
}

func main() {
	initArgs()
	flag.Parse()
	if len(calleeNamespace) == 0 || len(calleeService) == 0 {
		log.Print("calleeNamespace and calleeService are required")
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
	provider := polaris.NewProviderAPIByContext(sdkCtx)
	defer provider.Destroy()

	consumer := polaris.NewConsumerAPIByContext(sdkCtx)
	circuitBreaker := polaris.NewCircuitBreakerAPIByContext(sdkCtx)
	defer func() {
		sdkCtx.Destroy()
	}()

	svr := &PolarisClient{
		provider:       provider,
		consumer:       consumer,
		circuitBreaker: circuitBreaker,
		webSvr:         &http.Server{},
	}

	if selfRegister {
		svr.registerService()
	}
	svr.runWebServer()
	svr.runMainLoop()
}

func getLocalHost(serverAddr string) (string, error) {
	conn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		return "", err
	}
	localAddr := conn.LocalAddr().String()
	colonIdx := strings.LastIndex(localAddr, ":")
	if colonIdx > 0 {
		return localAddr[:colonIdx], nil
	}
	return localAddr, nil
}
