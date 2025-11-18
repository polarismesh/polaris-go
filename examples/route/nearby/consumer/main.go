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
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
)

const (
	// defaultRequestTimeout HTTP请求超时时间
	defaultRequestTimeout = 5 * time.Second
	// sleepAfterRequest 请求后的等待时间
	sleepAfterRequest = 30 * time.Millisecond
)

var (
	namespace     string
	service       string
	selfNamespace string
	selfService   string
	selfRegister  bool
	port          int64
	token         string
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "RouteNearbyEchoServer", "service")
	flag.BoolVar(&selfRegister, "selfRegister", false, "selfRegister")
	flag.StringVar(&selfNamespace, "selfNamespace", "default", "selfNamespace")
	flag.StringVar(&selfService, "selfService", "RouteNearbyEchoClient", "selfService")
	flag.Int64Var(&port, "port", 18080, "port")
	flag.StringVar(&token, "token", "", "token")
}

// PolarisConsumer .
type PolarisConsumer struct {
	consumer   polaris.ConsumerAPI
	provider   polaris.ProviderAPI
	namespace  string
	service    string
	host       string
	port       int
	isShutdown bool
}

// Run .
func (svr *PolarisConsumer) Run() {
	tmpHost, err := getLocalHost(svr.consumer.SDKContext().GetConfig().GetGlobal().GetServerConnector().GetAddresses()[0])
	if nil != err {
		panic(fmt.Errorf("error occur while fetching localhost: %v", err))
	}

	svr.host = tmpHost
	if selfRegister {
		svr.registerService()
	}
	svr.runWebServer()
	svr.runMainLoop()
}

func (svr *PolarisConsumer) registerService() {
	log.Printf("start to invoke register operation")
	registerRequest := &polaris.InstanceRegisterRequest{}
	registerRequest.Service = selfService
	registerRequest.Namespace = selfNamespace
	registerRequest.Host = svr.host
	registerRequest.Port = svr.port
	registerRequest.ServiceToken = token
	resp, err := svr.provider.RegisterInstance(registerRequest)
	if err != nil {
		log.Fatalf("fail to register instance, err is %v", err)
	}
	log.Printf("register response: instanceId %s", resp.InstanceID)
}

func (svr *PolarisConsumer) deregisterService() {
	log.Printf("start to invoke deregister operation")
	deregisterRequest := &polaris.InstanceDeRegisterRequest{}
	deregisterRequest.Service = selfService
	deregisterRequest.Namespace = selfNamespace
	deregisterRequest.Host = svr.host
	deregisterRequest.Port = svr.port
	deregisterRequest.ServiceToken = token
	if err := svr.provider.Deregister(deregisterRequest); err != nil {
		log.Fatalf("fail to deregister instance, err is %v", err)
	}
	log.Printf("deregister successfully.")
}

func (svr *PolarisConsumer) runMainLoop() {
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
		return
	}
}

func (svr *PolarisConsumer) reportResult(svcCallResult *polaris.ServiceCallResult, retStatus model.RetStatus,
	retCode int32) {
	svcCallResult.SetRetStatus(retStatus)
	svcCallResult.SetRetCode(retCode)
	err := svr.consumer.UpdateServiceCallResult(svcCallResult)
	if err != nil {
		log.Printf("[error] fail to UpdateServiceCallResult, err is %v, res:%s", err, jsonEncode(svcCallResult))
	} else {
		log.Printf("UpdateServiceCallResult success, res:%s", jsonEncode(svcCallResult))
	}
}

// callInstance 调用指定的服务实例
func (svr *PolarisConsumer) callInstance(instance model.Instance) ([]byte, error) {
	client := &http.Client{
		Timeout: defaultRequestTimeout,
	}

	url := fmt.Sprintf("http://%s:%d/echo", instance.GetHost(), instance.GetPort())
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("send request to %s:%d fail: %w", instance.GetHost(), instance.GetPort(), err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read resp from %s:%d fail: %w", instance.GetHost(), instance.GetPort(), err)
	}

	return data, nil
}

func (svr *PolarisConsumer) runWebServer() {
	http.HandleFunc("/echo", svr.handleEcho)

	log.Printf("start run web server, port : %d", port)

	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		log.Fatalf("[ERROR]fail to listen tcp, err is %v", err)
	}

	svr.port = ln.Addr().(*net.TCPAddr).Port

	go func() {
		log.Printf("[INFO] start http server, listen port is %v", svr.port)
		if err := http.Serve(ln, nil); err != nil {
			if !svr.isShutdown {
				log.Fatalf("[ERROR]fail to run webServer, err is %v", err)
			}
		}
	}()
}

// handleEcho 处理echo请求
func (svr *PolarisConsumer) handleEcho(rw http.ResponseWriter, r *http.Request) {
	log.Printf("start to invoke getOneInstance operation")

	// 获取服务实例
	getOneRequest := &polaris.GetOneInstanceRequest{}
	getOneRequest.Namespace = namespace
	getOneRequest.Service = service
	oneInstResp, err := svr.consumer.GetOneInstance(getOneRequest)
	if err != nil {
		log.Printf("[error] fail to getOneInstance, err is %v", err)
		http.Error(rw, fmt.Sprintf("fail to getOneInstance, err is %v", err), http.StatusInternalServerError)
		return
	}

	instance := oneInstResp.GetInstance()
	if instance == nil {
		log.Printf("[error] no available instance")
		http.Error(rw, "no available instance", http.StatusServiceUnavailable)
		return
	}

	log.Printf("instance getOneInstance is %s:%d", instance.GetHost(), instance.GetPort())

	// 构建响应头部信息
	var buf bytes.Buffer
	loc := svr.consumer.SDKContext().GetValueContext().GetCurrentLocation().GetLocation()
	locStr, err := json.Marshal(loc)
	if err != nil {
		log.Printf("[warn] fail to marshal location, err is %v", err)
		locStr = []byte("unknown")
	}

	msg := fmt.Sprintf("RouteNearbyEchoServer Consumer, MyLocInfo's : %s, host : %s:%d => ", string(locStr), svr.host, svr.port)
	buf.WriteString(msg)

	// 服务调用结果，用于在后面进行调用结果上报
	svcCallResult := &polaris.ServiceCallResult{}
	svcCallResult.SetCalledInstance(instance)

	// 调用远程服务
	requestStartTime := time.Now()
	data, err := svr.callInstance(instance)
	svcCallResult.SetDelay(time.Since(requestStartTime))

	if err != nil {
		log.Printf("[error] %v", err)
		svr.reportResult(svcCallResult, api.RetFail, -1)
		http.Error(rw, err.Error(), http.StatusBadGateway)
		return
	}

	log.Printf("read resp from %s:%d, data:%s", instance.GetHost(), instance.GetPort(), string(data))
	svr.reportResult(svcCallResult, api.RetSuccess, 0)

	buf.Write(data)
	buf.WriteByte('\n')

	// 模拟处理延迟
	time.Sleep(sleepAfterRequest)

	rw.WriteHeader(http.StatusOK)
	rw.Write(buf.Bytes())
}

func main() {
	initArgs()
	flag.Parse()
	if len(namespace) == 0 || len(service) == 0 {
		log.Print("namespace and service are required")
		return
	}
	sdkCtx, err := polaris.NewSDKContext()
	if nil != err {
		log.Fatalf("fail to create sdk context, err is %v", err)
	}
	defer sdkCtx.Destroy()

	svcRouter := sdkCtx.GetConfig().GetConsumer().GetServiceRouter()
	log.Printf("service router config: %+v", jsonEncode(svcRouter))
	loc := sdkCtx.GetConfig().GetGlobal().GetLocation()
	log.Printf("location config: %+v", jsonEncode(loc))
	statReporter := sdkCtx.GetConfig().GetGlobal().GetStatReporter()
	log.Printf("stat reporter config: %+v", jsonEncode(statReporter))

	svr := &PolarisConsumer{
		consumer:  polaris.NewConsumerAPIByContext(sdkCtx),
		provider:  polaris.NewProviderAPIByContext(sdkCtx),
		namespace: namespace,
		service:   service,
	}

	svr.Run()

}

func convertQuery(rawQuery string) map[string]string {
	meta := make(map[string]string)
	if len(rawQuery) == 0 {
		return meta
	}
	tokens := strings.Split(rawQuery, "&")
	for _, token := range tokens {
		if token == "" {
			continue
		}
		values := strings.Split(token, "=")
		if len(values) >= 2 {
			meta[values[0]] = values[1]
		} else if len(values) == 1 {
			meta[values[0]] = ""
		}
	}
	return meta
}

func getLocalHost(serverAddr string) (string, error) {
	conn, err := net.Dial("tcp", serverAddr)
	if nil != err {
		return "", err
	}
	localAddr := conn.LocalAddr().String()
	colonIdx := strings.LastIndex(localAddr, ":")
	if colonIdx > 0 {
		return localAddr[:colonIdx], nil
	}
	return localAddr, nil
}

func jsonEncode(data interface{}) string {
	buf, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	return string(buf)
}
