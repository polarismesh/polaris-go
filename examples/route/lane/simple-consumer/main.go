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

// Package main 泳道路由简化消费端示例（基于 GetOneInstance）。
//
// 与 consumer 示例不同，本示例使用 ConsumerAPI.GetOneInstance 一步完成
// 服务发现 + 路由过滤（含泳道路由）+ 负载均衡，无需手动调用 ProcessRouters 和
// ProcessLoadBalance，适用于不需要手动控制路由流程的场景。
//
// 作为多跳链路的中间节点（如 gateway → simple-consumer → provider）时：
//   - 本实例会把自己注册到 Polaris，供上游网关发现；
//   - 收到的 HTTP Header 会通过 AddArguments 传给 SDK，lane router 可据此识别染色；
//   - 已染色标签（service-lane）会被透传给下游请求，供下游 SDK 按泳道路由。
//
// 用法：
//
//	./bin -namespace=default -service=LaneEchoServer -selfService=SimpleLaneEchoClient -port=19082
//	./bin -namespace=default -service=LaneEchoServer -selfService=SimpleLaneEchoClient -port=19083 -lane=gray
//
// 请求示例：
//
//	curl http://127.0.0.1:19082/echo
//	curl -H "service-lane: lane-go-example/gray" http://127.0.0.1:19082/echo
package main

import (
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
	// trafficStainHeader 是泳道染色标签 HTTP 请求头名称。
	trafficStainHeader = "service-lane"
)

var (
	namespace     string
	service       string
	selfNamespace string
	selfService   string
	port          int
	token         string
	lane          string
	debug         bool
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "目标服务的 namespace")
	flag.StringVar(&service, "service", "LaneEchoServer", "目标服务名")
	flag.StringVar(&selfNamespace, "selfNamespace", "default", "当前服务的 namespace")
	flag.StringVar(&selfService, "selfService", "SimpleLaneEchoClient", "当前服务名，用于 Polaris 注册和流量入口匹配")
	flag.IntVar(&port, "port", 19082, "consumer HTTP 监听端口，0 表示随机")
	flag.StringVar(&token, "token", "", "token（北极星开启鉴权时使用）")
	// lane 参数：可选值为 "gray"、"stable" 或 ""（空字符串表示基线实例，不携带泳道标签）
	flag.StringVar(&lane, "lane", "", "lane label value, e.g. gray / stable / (empty for baseline)")
	flag.BoolVar(&debug, "debug", false, "是否开启 Polaris SDK debug 日志")
}

// SimpleLaneConsumer 使用 GetOneInstance 的简化泳道消费端
type SimpleLaneConsumer struct {
	consumer      polaris.ConsumerAPI
	provider      polaris.ProviderAPI
	namespace     string
	service       string
	selfNamespace string
	selfService   string
	host          string
	port          int
}

// Run 启动服务：解析本地地址 → 启动 HTTP → 注册 Polaris → 等待信号退出
func (svr *SimpleLaneConsumer) Run() {
	tmpHost, err := getLocalHost(
		svr.consumer.SDKContext().GetConfig().GetGlobal().GetServerConnector().GetAddresses()[0])
	if err != nil {
		panic(fmt.Errorf("error occur while fetching localhost: %v", err))
	}
	svr.host = tmpHost
	svr.runWebServer()
	svr.registerService()
	svr.runMainLoop()
}

// runWebServer 启动 HTTP 服务（非阻塞）
func (svr *SimpleLaneConsumer) runWebServer() {
	http.HandleFunc("/echo", svr.handleEcho)

	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		log.Fatalf("[ERROR] fail to listen tcp, err is %v", err)
	}
	svr.port = ln.Addr().(*net.TCPAddr).Port

	go func() {
		log.Printf("[INFO] start simple lane consumer, port=%d, self=%s/%s, downstream=%s/%s",
			svr.port, svr.selfNamespace, svr.selfService, svr.namespace, svr.service)
		if err := http.Serve(ln, nil); err != nil {
			log.Fatalf("[ERROR] fail to run webServer, err is %v", err)
		}
	}()
}

// registerService 向 Polaris 注册当前 simple-consumer 实例
func (svr *SimpleLaneConsumer) registerService() {
	log.Printf("start to invoke register operation, lane=%q", lane)
	req := &polaris.InstanceRegisterRequest{}
	req.Service = svr.selfService
	req.Namespace = svr.selfNamespace
	req.Host = svr.host
	req.Port = svr.port
	req.ServiceToken = token
	// 根据泳道参数决定是否携带 lane 元数据
	if lane != "" {
		req.Metadata = map[string]string{"lane": lane}
	} else {
		req.Metadata = map[string]string{}
	}
	// 首次注册容易因服务端连接抖动(尤其远程 Polaris)失败,重试 5 次避免进程立即挂掉。
	const maxRetry = 5
	var resp *model.InstanceRegisterResponse
	var err error
	for i := 1; i <= maxRetry; i++ {
		resp, err = svr.provider.RegisterInstance(req)
		if err == nil {
			break
		}
		log.Printf("[WARN] register instance attempt %d/%d failed: %v", i, maxRetry, err)
		if i < maxRetry {
			time.Sleep(2 * time.Second)
		}
	}
	if err != nil {
		log.Fatalf("fail to register instance after %d retries, err is %v", maxRetry, err)
	}
	log.Printf("register response: instanceId=%s, host=%s:%d, lane=%q, metadata=%v",
		resp.InstanceID, svr.host, svr.port, lane, req.Metadata)
}

// deregisterService 从 Polaris 反注册当前实例
func (svr *SimpleLaneConsumer) deregisterService() {
	log.Printf("start to invoke deregister operation")
	req := &polaris.InstanceDeRegisterRequest{}
	req.Service = svr.selfService
	req.Namespace = svr.selfNamespace
	req.Host = svr.host
	req.Port = svr.port
	req.ServiceToken = token
	if err := svr.provider.Deregister(req); err != nil {
		log.Fatalf("fail to deregister instance, err is %v", err)
	}
	log.Printf("deregister successfully")
}

// runMainLoop 等待退出信号并执行反注册
func (svr *SimpleLaneConsumer) runMainLoop() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM, syscall.SIGSEGV)
	for s := range ch {
		log.Printf("catch signal(%+v), stop servers", s)
		svr.deregisterService()
		return
	}
}

func (svr *SimpleLaneConsumer) handleEcho(rw http.ResponseWriter, r *http.Request) {
	// simple-consumer 自身的泳道标签：空串视为基线实例
	myLaneLabel := lane
	if myLaneLabel == "" {
		myLaneLabel = "(baseline)"
	}
	log.Printf("received request from %s, self=%s/%s, myLane=%s, headers=%v",
		r.RemoteAddr, svr.selfNamespace, svr.selfService, myLaneLabel, r.Header)

	// 把上游 HTTP Header / Query 转为 Arguments 传给 SDK，
	// lane router 据此做流量识别 / 染色标签匹配
	instance, err := svr.getOneInstance(r)
	if err != nil {
		log.Printf("[error] fail to getOneInstance: %v", err)
		http.Error(rw, fmt.Sprintf("fail to getOneInstance: %v", err), http.StatusInternalServerError)
		return
	}
	if instance == nil {
		log.Printf("[warn] no instance selected")
		http.Error(rw, "no available instance", http.StatusServiceUnavailable)
		return
	}
	laneLabel := instance.GetMetadata()["lane"]
	if laneLabel == "" {
		laneLabel = "(baseline)"
	}
	log.Printf("selected instance: %s:%d, lane=%s, metadata=%v",
		instance.GetHost(), instance.GetPort(), laneLabel, instance.GetMetadata())

	// 构建上游请求
	callResult := &polaris.ServiceCallResult{}
	callResult.SetCalledInstance(instance)
	startTime := time.Now()

	upstreamReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet,
		fmt.Sprintf("http://%s:%d/echo", instance.GetHost(), instance.GetPort()), nil)
	if err != nil {
		log.Printf("[error] fail to create upstream request: %v", err)
		http.Error(rw, fmt.Sprintf("fail to create request: %v", err), http.StatusInternalServerError)
		return
	}

	// 透传已有的染色标签给下游（若存在）。
	// 若原请求无染色标签但本次路由命中了泳道实例，则用 lane 元数据值作为短格式标签透传。
	if stain := r.Header.Get(trafficStainHeader); stain != "" {
		upstreamReq.Header.Set(trafficStainHeader, stain)
	} else if lane := instance.GetMetadata()["lane"]; lane != "" {
		upstreamReq.Header.Set(trafficStainHeader, lane)
	}

	resp, err := http.DefaultClient.Do(upstreamReq)
	callResult.SetDelay(time.Since(startTime))

	if err != nil {
		log.Printf("[error] send request to %s:%d fail: %s", instance.GetHost(), instance.GetPort(), err)
		svr.reportResult(callResult, model.RetFail, -1)
		http.Error(rw, fmt.Sprintf("send request fail: %s", err), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[error] read resp from %s:%d fail: %s", instance.GetHost(), instance.GetPort(), err)
		svr.reportResult(callResult, model.RetFail, -1)
		http.Error(rw, fmt.Sprintf("read resp fail: %s", err), http.StatusInternalServerError)
		return
	}

	svr.reportResult(callResult, model.RetSuccess, int32(resp.StatusCode))
	log.Printf("upstream %s:%d ok, status=%d, bytes=%d",
		instance.GetHost(), instance.GetPort(), resp.StatusCode, len(data))
	rw.WriteHeader(http.StatusOK)
	msg := fmt.Sprintf("Hello, I'm %s. lane=%s, host=%s:%d, callee addr:%s:%d, callee lane=%s, callee resp=%s",
		svr.selfService, myLaneLabel, svr.host, svr.port, instance.GetHost(), instance.GetPort(), laneLabel,
		string(data))
	log.Printf("resp to caller: %s, bytes~=%d", msg, len(msg))
	_, _ = rw.Write([]byte(msg))
}

// getOneInstance 通过 ConsumerAPI.GetOneInstance 获取单个可用实例。
// SDK 内部会自动执行路由过滤（包括泳道路由）和负载均衡。
//
// 关键：把当前请求的 HTTP Header / Query 通过 AddArguments 传入，lane router 才能
// 按 TrafficMatchRule 或 service-lane 染色标签选到正确的泳道实例。
func (svr *SimpleLaneConsumer) getOneInstance(r *http.Request) (model.Instance, error) {
	getOneRequest := &polaris.GetOneInstanceRequest{}
	getOneRequest.Namespace = svr.namespace
	getOneRequest.Service = svr.service
	getOneRequest.SourceService = &model.ServiceInfo{
		Namespace: svr.selfNamespace,
		Service:   svr.selfService,
	}
	getOneRequest.AddArguments(buildRouteArguments(r)...)
	oneInstResp, err := svr.consumer.GetOneInstance(getOneRequest)
	if err != nil {
		return nil, fmt.Errorf("fail to getOneInstance: %w", err)
	}
	return oneInstResp.GetInstance(), nil
}

func (svr *SimpleLaneConsumer) reportResult(result *polaris.ServiceCallResult, retStatus model.RetStatus, retCode int32) {
	result.SetRetStatus(retStatus)
	result.SetRetCode(retCode)
	if err := svr.consumer.UpdateServiceCallResult(result); err != nil {
		log.Printf("[error] fail to UpdateServiceCallResult: %v", err)
	}
}

// buildRouteArguments 将 HTTP Header 和 Query 参数转为路由 Arguments，
// 供泳道规则中 TrafficMatchRule 的流量识别和 service-lane 染色标签使用。
func buildRouteArguments(r *http.Request) []model.Argument {
	args := make([]model.Argument, 0, 8)
	for k, vs := range r.Header {
		if len(vs) == 0 {
			continue
		}
		args = append(args, model.BuildHeaderArgument(strings.ToLower(k), vs[0]))
	}
	for k, vs := range r.URL.Query() {
		if len(vs) == 0 {
			continue
		}
		args = append(args, model.BuildQueryArgument(strings.ToLower(k), vs[0]))
	}
	return args
}

// getLocalHost 通过建立 TCP 连接解析本机能与 Polaris 通信的 IP，
// 用于向 Polaris 注册时上报真实 host。
func getLocalHost(serverAddr string) (string, error) {
	conn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	localAddr := conn.LocalAddr().String()
	colonIdx := strings.LastIndex(localAddr, ":")
	if colonIdx > 0 {
		return localAddr[:colonIdx], nil
	}
	return localAddr, nil
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	initArgs()
	flag.Parse()
	if namespace == "" || service == "" {
		log.Fatal("namespace and service are required")
	}

	if debug {
		if err := api.SetLoggersLevel(api.DebugLog); err != nil {
			log.Printf("[WARN] 设置日志级别为 DEBUG 失败: %v", err)
		} else {
			log.Printf("[INFO] 已设置 Polaris SDK 日志级别为 DEBUG")
		}
	}

	sdkCtx, err := polaris.NewSDKContext()
	if err != nil {
		log.Fatalf("fail to create sdk context: %v", err)
	}
	defer sdkCtx.Destroy()

	svr := &SimpleLaneConsumer{
		consumer:      polaris.NewConsumerAPIByContext(sdkCtx),
		provider:      polaris.NewProviderAPIByContext(sdkCtx),
		namespace:     namespace,
		service:       service,
		selfNamespace: selfNamespace,
		selfService:   selfService,
	}
	svr.Run()
}
