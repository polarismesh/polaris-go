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
	"strings"
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
	port          int64
	token         string
	debug         bool
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "RouteEchoServer", "service")
	flag.StringVar(&selfNamespace, "selfNamespace", "default", "selfNamespace")
	flag.StringVar(&selfService, "selfService", "RouteEchoClient", "selfService")
	flag.Int64Var(&port, "port", 18070, "port")
	flag.StringVar(&token, "token", "", "token")
	flag.BoolVar(&debug, "debug", false, "debug")
}

// PolarisConsumer .
// 本示例通过 GetOneInstance 直接获取一个实例（由 SDK 内部完成规则路由 + 负载均衡），
// 适合业务只需要"取一个可用实例"的轻量调用场景。
// 如需手动分开调用 ProcessRouters + ProcessLoadBalance，参考同级目录下的 consumer/ 示例。
type PolarisConsumer struct {
	consumer  polaris.ConsumerAPI
	namespace string
	service   string
	host      string
	port      int
}

// Run .
func (svr *PolarisConsumer) Run() {
	tmpHost, err := getLocalHost(svr.consumer.SDKContext().GetConfig().GetGlobal().GetServerConnector().GetAddresses()[0])
	if nil != err {
		panic(fmt.Errorf("error occur while fetching localhost: %v", err))
	}
	svr.host = tmpHost
	svr.runWebServer()
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

	log.Printf("[INFO] start http server, listen port is %v", svr.port)
	if err := http.Serve(ln, nil); err != nil {
		log.Fatalf("[ERROR]fail to run webServer, err is %v", err)
	}
}

// handleEcho 处理 echo 请求
// 规则路由的参数来源于 HTTP Header 与 Query，通过 AddArguments 传给 SDK，
// SDK 会根据 Polaris 控制台上配置的 inbound/outbound 路由规则进行实例过滤。
func (svr *PolarisConsumer) handleEcho(rw http.ResponseWriter, r *http.Request) {
	log.Printf("start to invoke getOneInstance operation")

	getOneRequest := &polaris.GetOneInstanceRequest{}
	getOneRequest.Namespace = namespace
	getOneRequest.Service = service
	// 设置主调方服务信息，用于匹配 inbound 路由规则中的 sources
	getOneRequest.SourceService = &model.ServiceInfo{
		Namespace: selfNamespace,
		Service:   selfService,
	}
	// 从 HTTP 请求中提取 header / query 作为路由参数
	getOneRequest.AddArguments(convertRouteArguments(r)...)

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
	log.Printf("instance getOneInstance is %s:%d, metadata: %v",
		instance.GetHost(), instance.GetPort(), instance.GetMetadata())

	var buf bytes.Buffer
	loc := svr.consumer.SDKContext().GetValueContext().GetCurrentLocation().GetLocation()
	locStr, _ := json.Marshal(loc)
	msg := fmt.Sprintf("RouteEchoServer Consumer, MyLocInfo's : %s, host : %s:%d => ",
		string(locStr), svr.host, svr.port)
	buf.WriteString(msg)

	// 服务调用结果，用于在后面进行调用结果上报
	svcCallResult := &polaris.ServiceCallResult{}
	svcCallResult.SetCalledInstance(instance)

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
	_, _ = rw.Write(buf.Bytes())
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

	if debug {
		if err := api.SetLoggersLevel(api.DebugLog); err != nil {
			log.Printf("fail to set log level to DEBUG, err is %v", err)
		} else {
			log.Printf("successfully set log level to DEBUG")
		}
	}

	svcRouter := sdkCtx.GetConfig().GetConsumer().GetServiceRouter()
	log.Printf("service router config: %+v", jsonEncode(svcRouter))

	svr := &PolarisConsumer{
		consumer:  polaris.NewConsumerAPIByContext(sdkCtx),
		namespace: namespace,
		service:   service,
	}

	svr.Run()
}

// convertRouteArguments 把 HTTP Header 与 URL Query 转换成 polaris-go 路由参数
func convertRouteArguments(r *http.Request) []model.Argument {
	arguments := make([]model.Argument, 0, 4)

	for k, vs := range r.Header {
		if len(vs) == 0 {
			continue
		}
		arguments = append(arguments, model.BuildHeaderArgument(strings.ToLower(k), vs[0]))
	}
	for k, vs := range r.URL.Query() {
		if len(vs) == 0 {
			continue
		}
		arguments = append(arguments, model.BuildQueryArgument(strings.ToLower(k), vs[0]))
	}
	log.Printf("total arguments count: %d, %v", len(arguments), arguments)
	return arguments
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
