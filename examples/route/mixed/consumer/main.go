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

// Package main 混合路由示例 consumer。
//
// 使用 ConsumerAPI.GetOneInstance 一步完成「服务发现 → 路由过滤（lane router +
// ruleBasedRouter + nearbyBasedRouter 链式组合）→ 负载均衡」。
//
// 验证目标：当 lane router、规则路由、就近路由同时启用时，三者过滤条件按
// AND 复合生效；lane router 输出的 cluster (含 `lane=val` metadata 或
// instanceFilter) 会作为 ruleBasedRouter / nearbyBasedRouter 的输入继续叠加。
package main

import (
	"errors"
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

const trafficStainHeader = "service-lane"

var (
	namespace     string
	service       string
	selfNamespace string
	selfService   string
	port          int
	token         string
	debug         bool
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "目标服务的 namespace")
	flag.StringVar(&service, "service", "MixedRouteEchoServer", "目标服务名")
	flag.StringVar(&selfNamespace, "selfNamespace", "default", "当前服务的 namespace")
	flag.StringVar(&selfService, "selfService", "MixedRouteEchoClient", "当前服务名,用于 Polaris 注册和路由匹配")
	flag.IntVar(&port, "port", 18080, "consumer HTTP 监听端口,0 表示随机")
	flag.StringVar(&token, "token", "", "token (北极星开启鉴权时使用)")
	flag.BoolVar(&debug, "debug", false, "是否开启 Polaris SDK debug 日志")
}

// MixedConsumer 混合路由示例 consumer。
type MixedConsumer struct {
	consumer  polaris.ConsumerAPI
	provider  polaris.ProviderAPI
	namespace string
	service   string
	host      string
	port      int
}

// Run 启动 HTTP 服务、向 Polaris 注册自身、阻塞等待退出信号。
func (svr *MixedConsumer) Run() {
	tmpHost, err := getLocalHost(svr.consumer.SDKContext().GetConfig().GetGlobal().GetServerConnector().GetAddresses()[0])
	if err != nil {
		panic(fmt.Errorf("error occur while fetching localhost: %v", err))
	}
	svr.host = tmpHost
	svr.runWebServer()
	svr.registerService()
	svr.runMainLoop()
}

// runWebServer 启动 /echo 接口,把请求 Header / Query 转为 Polaris 路由 Arguments。
func (svr *MixedConsumer) runWebServer() {
	http.HandleFunc("/echo", svr.handleEcho)

	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		log.Fatalf("[ERROR] fail to listen tcp, err is %v", err)
	}
	svr.port = ln.Addr().(*net.TCPAddr).Port
	go func() {
		log.Printf("[INFO] start mixed consumer, port=%d, self=%s/%s, downstream=%s/%s",
			svr.port, selfNamespace, selfService, svr.namespace, svr.service)
		if err := http.Serve(ln, nil); err != nil {
			log.Fatalf("[ERROR] fail to run webServer, err is %v", err)
		}
	}()
}

// registerService 向 Polaris 注册当前 consumer 实例,便于 ruleBasedRouter 按
// SourceService 匹配 inbound 规则。consumer 自身不带 lane / env 元数据,所有
// 路由判定都依赖请求上的 Header / Query。
func (svr *MixedConsumer) registerService() {
	log.Printf("start to invoke register operation")
	req := &polaris.InstanceRegisterRequest{}
	req.Service = selfService
	req.Namespace = selfNamespace
	req.Host = svr.host
	req.Port = svr.port
	req.ServiceToken = token
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
	log.Printf("register response: instanceId=%s, host=%s:%d", resp.InstanceID, svr.host, svr.port)
}

// deregisterService 进程退出前从 Polaris 反注册。
func (svr *MixedConsumer) deregisterService() {
	log.Printf("start to invoke deregister operation")
	req := &polaris.InstanceDeRegisterRequest{}
	req.Service = selfService
	req.Namespace = selfNamespace
	req.Host = svr.host
	req.Port = svr.port
	req.ServiceToken = token
	if err := svr.provider.Deregister(req); err != nil {
		log.Fatalf("fail to deregister instance, err is %v", err)
	}
	log.Printf("deregister successfully")
}

// runMainLoop 阻塞等待 SIGINT / SIGTERM 后反注册再退出。
func (svr *MixedConsumer) runMainLoop() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM, syscall.SIGSEGV)
	for s := range ch {
		log.Printf("catch signal(%+v), stop servers", s)
		svr.deregisterService()
		return
	}
}

// handleEcho 把请求转为路由参数,用 GetOneInstance 选实例,再转发到下游 provider。
// SDK 内部按 polaris.yaml 的 chain 顺序执行:
//
//	beforeChain: laneRouter
//	chain:       ruleBasedRouter, nearbyBasedRouter
//	afterChain:  filterOnlyRouter
//
// 只要 provider 实例同时满足 lane 元数据(若染色) ∩ env 规则匹配 ∩ 就近条件,
// 才会被选中。任意一环匹配失败,GetOneInstance 返回 ErrCodeAPIInstanceNotFound。
func (svr *MixedConsumer) handleEcho(rw http.ResponseWriter, r *http.Request) {
	log.Printf("[ECHO] received from %s, headers=%v, query=%v", r.RemoteAddr, r.Header, r.URL.Query())

	getOneRequest := &polaris.GetOneInstanceRequest{}
	getOneRequest.Namespace = svr.namespace
	getOneRequest.Service = svr.service
	getOneRequest.SourceService = &model.ServiceInfo{
		Namespace: selfNamespace,
		Service:   selfService,
	}
	getOneRequest.AddArguments(buildRouteArguments(r)...)

	oneInstResp, err := svr.consumer.GetOneInstance(getOneRequest)
	if err != nil {
		log.Printf("[error] fail to getOneInstance: %v", err)
		// ErrCodeAPIInstanceNotFound 表示路由过滤后没有候选实例(三链中至少一环
		// 没匹配到任何实例),返回 503 让调用方区分"无可用实例"与"网关内部错误"。
		cause := err
		if unwrapped := errors.Unwrap(err); unwrapped != nil {
			cause = unwrapped
		}
		if sdkErr, ok := cause.(model.SDKError); ok && sdkErr.ErrorCode() == model.ErrCodeAPIInstanceNotFound {
			http.Error(rw, fmt.Sprintf("no instance available: %v", err), http.StatusServiceUnavailable)
			return
		}
		http.Error(rw, fmt.Sprintf("fail to getOneInstance: %v", err), http.StatusInternalServerError)
		return
	}
	instance := oneInstResp.GetInstance()
	if instance == nil {
		http.Error(rw, "no available instance", http.StatusServiceUnavailable)
		return
	}
	log.Printf("[ECHO] selected instance: %s:%d, metadata=%v", instance.GetHost(), instance.GetPort(), instance.GetMetadata())

	// 透传染色标签到下游(本 demo 中下游就是 provider,不再有第二跳,但保留以
	// 与生产链路保持一致的写法)。
	upstreamReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet,
		fmt.Sprintf("http://%s:%d/echo", instance.GetHost(), instance.GetPort()), nil)
	if err != nil {
		http.Error(rw, fmt.Sprintf("build upstream request fail: %v", err), http.StatusInternalServerError)
		return
	}
	if stain := r.Header.Get(trafficStainHeader); stain != "" {
		upstreamReq.Header.Set(trafficStainHeader, stain)
	} else if lane := instance.GetMetadata()["lane"]; lane != "" {
		upstreamReq.Header.Set(trafficStainHeader, lane)
	}

	callResult := &polaris.ServiceCallResult{}
	callResult.SetCalledInstance(instance)
	startTime := time.Now()
	resp, err := http.DefaultClient.Do(upstreamReq)
	callResult.SetDelay(time.Since(startTime))
	if err != nil {
		log.Printf("[error] send request to %s:%d fail: %s", instance.GetHost(), instance.GetPort(), err)
		svr.reportResult(callResult, model.RetFail, -1)
		http.Error(rw, fmt.Sprintf("send request fail: %s", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		svr.reportResult(callResult, model.RetFail, -1)
		http.Error(rw, fmt.Sprintf("read resp fail: %s", err), http.StatusInternalServerError)
		return
	}
	svr.reportResult(callResult, model.RetSuccess, int32(resp.StatusCode))

	rw.WriteHeader(http.StatusOK)
	msg := fmt.Sprintf("MixedConsumer host=%s:%d, callee=%s:%d, callee_metadata=%v, callee_resp=%s",
		svr.host, svr.port, instance.GetHost(), instance.GetPort(), instance.GetMetadata(), string(data))
	log.Printf("[ECHO] resp to caller: %s", msg)
	_, _ = rw.Write([]byte(msg))
}

// reportResult 把调用结果上报回 Polaris,用于熔断器统计。
func (svr *MixedConsumer) reportResult(result *polaris.ServiceCallResult, retStatus model.RetStatus, retCode int32) {
	result.SetRetStatus(retStatus)
	result.SetRetCode(retCode)
	if err := svr.consumer.UpdateServiceCallResult(result); err != nil {
		log.Printf("[error] fail to UpdateServiceCallResult: %v", err)
	}
}

// buildRouteArguments 把 HTTP Header / Query 转成 polaris-go 路由参数。
// lane router 通过 Header `service-lane` 识别染色;ruleBasedRouter 通过
// $query.<k> / $header.<k> 匹配 inbound 规则的 sources;nearbyBasedRouter
// 不依赖参数,但保留以便 lane / rule 联动时 source metadata 完整。
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

// getLocalHost 通过 TCP 连接 Polaris 服务端反推本机能与之通信的 IP。
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
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
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

	svr := &MixedConsumer{
		consumer:  polaris.NewConsumerAPIByContext(sdkCtx),
		provider:  polaris.NewProviderAPIByContext(sdkCtx),
		namespace: namespace,
		service:   service,
	}
	svr.Run()
}
