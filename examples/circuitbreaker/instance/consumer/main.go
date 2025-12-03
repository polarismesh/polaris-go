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
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

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
	debug           bool
)

func initArgs() {
	flag.StringVar(&selfNamespace, "selfNamespace", "default", "selfNamespace")
	flag.StringVar(&selfService, "selfService", "CircuitBreakerInstanceCaller", "selfService")
	flag.BoolVar(&selfRegister, "selfRegister", true, "selfRegister")
	flag.StringVar(&calleeNamespace, "calleeNamespace", "default", "calleeNamespace")
	flag.StringVar(&calleeService, "calleeService", "CircuitBreakerCallee", "calleeService")
	flag.IntVar(&port, "port", 18080, "port")
	flag.StringVar(&token, "token", "", "token")
	flag.StringVar(&configPath, "config", "./polaris.yaml", "path for config file")
	flag.BoolVar(&debug, "debug", false, "debug")
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

// reportServiceCallResult 上报服务调用结果的辅助方法
func (svr *PolarisClient) reportServiceCallResult(instance model.Instance, retStatus model.RetStatus, statusCode int, delay time.Duration) {
	ret := &polaris.ServiceCallResult{
		ServiceCallResult: model.ServiceCallResult{
			EmptyInstanceGauge: model.EmptyInstanceGauge{},
			CalledInstance:     instance,
			Method:             "/echo",
			RetStatus:          retStatus,
		},
	}
	ret.SetDelay(delay)
	ret.SetRetCode(int32(statusCode))
	if err := svr.consumer.UpdateServiceCallResult(ret); err != nil {
		log.Printf("do report service call result : %+v", err)
	} else {
		log.Printf("report service call result success: instance=%s:%d, status=%v, retCode=%d, delay=%v",
			instance.GetHost(), instance.GetPort(), ret.RetStatus, ret.GetRetCode(), delay)
	}
}

func (svr *PolarisClient) discoverInstance() (model.Instance, error) {
	svr.printAllInstances()
	getOneRequest := &polaris.GetOneInstanceRequest{}
	getOneRequest.Namespace = calleeNamespace
	getOneRequest.Service = calleeService
	oneInstResp, err := svr.consumer.GetOneInstance(getOneRequest)
	if err != nil {
		log.Printf("[error] fail to getOneInstance, err is %v", err)
		return nil, err
	}
	instance := oneInstResp.GetInstance()
	if instance == nil {
		log.Printf("[error] fail to getOneInstance, instance is nil")
		return nil, fmt.Errorf("Consumer.GetOneInstance empty")
	}
	log.Printf("getOneInstance is %s:%d, ishealthy:%v", instance.GetHost(),
		instance.GetPort(), instance.IsHealthy())
	return instance, nil
}

func (svr *PolarisClient) runWebServer() {
	http.HandleFunc("/echo", func(rw http.ResponseWriter, r *http.Request) {
		log.Printf("start to invoke getOneInstance operation")
		instance, err := svr.discoverInstance()
		if err != nil || instance == nil {
			rw.WriteHeader(http.StatusInternalServerError)
			instanceIsNil := instance == nil
			msg := fmt.Sprintf("fail to getOneInstance, err is %v, instance is nil:%v", err, instanceIsNil)
			_, _ = rw.Write([]byte(fmt.Sprintf("[error] discover instance fail : %s", msg)))
			return
		}
		start := time.Now()
		resp, err := http.Get(fmt.Sprintf("http://%s:%d/echo", instance.GetHost(), instance.GetPort()))

		if err != nil {
			log.Printf("[error] send request to %s:%d fail : %s",
				instance.GetHost(), instance.GetPort(), err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[error] send request to %s:%d fail : %s",
				instance.GetHost(), instance.GetPort(), err)))

			time.Sleep(time.Millisecond * time.Duration(rand.Intn(10)))

			// 上报服务调用结果
			delay := time.Since(start)
			svr.reportServiceCallResult(instance, model.RetFail, http.StatusInternalServerError, delay)

			// 上报熔断结果，用于熔断计算
			svr.reportCircuitBreak(instance, model.RetFail, strconv.Itoa(http.StatusInternalServerError), start)
			return
		}
		time.Sleep(time.Millisecond * time.Duration(rand.Intn(10)))

		defer resp.Body.Close()

		// 上报服务调用结果
		delay := time.Since(start)
		retStatus := model.RetSuccess
		if resp.StatusCode == http.StatusTooManyRequests {
			retStatus = model.RetFlowControl
		} else if resp.StatusCode != http.StatusOK {
			retStatus = model.RetFail
		}
		svr.reportServiceCallResult(instance, retStatus, resp.StatusCode, delay)

		// 上报熔断结果，用于熔断计算
		if resp.StatusCode != http.StatusOK {
			svr.reportCircuitBreak(instance, model.RetFail,
				strconv.Itoa(resp.StatusCode), start)
		} else {
			svr.reportCircuitBreak(instance, model.RetSuccess,
				strconv.Itoa(http.StatusOK), start)
		}

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("[error] read resp from %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[error] read resp from %s:%d fail : %s",
				instance.GetHost(), instance.GetPort(), err)))
			return
		}
		log.Printf("echo success, resp: %+v", string(data))
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write(data)
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

func (svr *PolarisClient) printAllInstances() {
	req := &polaris.GetInstancesRequest{
		GetInstancesRequest: model.GetInstancesRequest{
			Service:   calleeService,
			Namespace: calleeNamespace,
		},
	}
	instancesResp, err := svr.consumer.GetInstances(req)
	if err != nil {
		log.Printf("[error] fail to getInstances for request [%+v], err is %v\n", req, err)
		return
	}
	log.Printf("printAllInstances get [%d] instances", len(instancesResp.GetInstances()))

	for _, ins := range instancesResp.GetInstances() {
		cbStatus := "close"
		if ins.GetCircuitBreakerStatus() != nil {
			cbStatus = ins.GetCircuitBreakerStatus().GetStatus().String()
		}
		log.Printf("%s:%d. cb status: %s\n", ins.GetHost(), ins.GetPort(), cbStatus)
	}
}

func (svr *PolarisClient) reportCircuitBreak(instance model.Instance, status model.RetStatus,
	retCode string, start time.Time) {
	insRes, err := model.NewInstanceResource(&model.ServiceKey{
		Namespace: calleeNamespace,
		Service:   calleeService,
	}, &model.ServiceKey{
		Namespace: selfNamespace,
		Service:   selfService,
	}, "http", instance.GetHost(), instance.GetPort())
	if err != nil {
		log.Printf("[error] fail to createInstanceResource, err is %v", err)
		return
	}
	log.Printf("report circuitBreaker [%v] for instance %s/%s:%d "+
		"caller [%s.%s] "+
		"delay [%v] "+
		"circuitBreaker status [%v]\n",
		status,
		instance.GetService(),
		instance.GetHost(), instance.GetPort(),
		selfNamespace, selfService, time.Since(start),
		instance.GetCircuitBreakerStatus())

	if err := svr.circuitBreaker.Report(&model.ResourceStat{
		Delay:     time.Since(start),
		RetStatus: status,
		RetCode:   retCode,
		Resource:  insRes,
	}); err != nil {
		log.Printf("report circuitbreak for service %v failed: %v",
			insRes, err)
	}
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
	if debug {
		// 设置日志级别为DEBUG
		if err := api.SetLoggersLevel(api.DebugLog); err != nil {
			log.Printf("fail to set log level to DEBUG, err is %v", err)
		} else {
			log.Printf("successfully set log level to DEBUG")
		}
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
