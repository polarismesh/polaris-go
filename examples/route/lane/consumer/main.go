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

// Package main 泳道路由消费端示例。
//
// LaneConsumer 是全链路灰度中的中间层服务，注册为 LaneEchoClient，
// 接收来自网关（LaneRouterGateway）的请求，透传泳道染色标签，
// 再路由到下游服务 LaneEchoServer。
//
// 用法：
//
//	./bin -namespace=default -service=LaneEchoServer \
//	      -selfNamespace=default -selfService=LaneEchoClient -port=19080
//
// 请求示例（携带泳道染色标签）：
//
//	# 路由到 lane=gray 泳道
//	curl -H "service-lane: gray/rule1" http://127.0.0.1:19080/echo
//
//	# 路由到 lane=stable 泳道
//	curl -H "service-lane: stable/rule2" http://127.0.0.1:19080/echo
//
//	# 不携带标签，路由到基线
//	curl http://127.0.0.1:19080/echo
//
// 泳道路由优先级：
//  1. 若请求携带 service-lane 响应头，直接按染色标签匹配已有泳道规则。
//  2. 若未携带，则通过泳道规则中的 TrafficMatchRule 进行流量识别，
//     命中后染色，再路由到对应泳道实例。
//  3. 未命中规则 → 回退基线（无 lane 元数据的实例）。
package main

import (
	"bytes"
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
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

const (
	// trafficStainHeader 是上游或网关透传的泳道染色标签 HTTP 请求头名称。
	// 值格式：{laneGroupName}/{laneRuleName}，例如 "gray/rule1"。
	trafficStainHeader = "service-lane"
)

var (
	namespace     string
	service       string
	selfNamespace string
	selfService   string
	port          int
	token         string
	times         int
	lane          string
	debug         bool
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "目标服务的 namespace")
	flag.StringVar(&service, "service", "LaneEchoServer", "目标服务名")
	flag.StringVar(&selfNamespace, "selfNamespace", "default", "当前服务的 namespace")
	flag.StringVar(&selfService, "selfService", "LaneEchoClient", "当前服务名，用于 Polaris 注册和流量入口匹配")
	flag.IntVar(&port, "port", 19080, "consumer HTTP 监听端口，0 表示随机")
	flag.StringVar(&token, "token", "", "token（北极星开启鉴权时使用）")
	flag.IntVar(&times, "times", 1, "每次请求的转发次数")
	// lane 参数：可选值为 "gray"、"stable" 或 ""（空字符串表示基线实例，不携带泳道标签）
	flag.StringVar(&lane, "lane", "", "lane label value, e.g. gray / stable / (empty for baseline)")
	flag.BoolVar(&debug, "debug", false, "是否开启 Polaris SDK debug 日志")
}

// LaneConsumer 泳道路由消费端
type LaneConsumer struct {
	consumer  polaris.ConsumerAPI
	router    polaris.RouterAPI
	provider  polaris.ProviderAPI
	namespace string
	service   string
	host      string
	port      int
}

// Run 启动服务：解析本地地址 → 启动 HTTP → 注册 Polaris → 等待信号退出
func (svr *LaneConsumer) Run() {
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
func (svr *LaneConsumer) runWebServer() {
	http.HandleFunc("/echo", svr.handleEcho)

	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		log.Fatalf("[ERROR] fail to listen tcp, err is %v", err)
	}
	svr.port = ln.Addr().(*net.TCPAddr).Port

	go func() {
		log.Printf("[INFO] start lane consumer web server, port=%d, selfService=%s/%s",
			svr.port, selfNamespace, selfService)
		if err := http.Serve(ln, nil); err != nil {
			log.Fatalf("[ERROR] fail to run webServer, err is %v", err)
		}
	}()
}

// registerService 向 Polaris 注册当前 consumer 实例
func (svr *LaneConsumer) registerService() {
	log.Printf("start to invoke register operation, lane=%q", lane)
	req := &polaris.InstanceRegisterRequest{}
	req.Service = selfService
	req.Namespace = selfNamespace
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

// deregisterService 从 Polaris 反注册当前 consumer 实例
func (svr *LaneConsumer) deregisterService() {
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

// runMainLoop 等待退出信号并执行反注册
func (svr *LaneConsumer) runMainLoop() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM, syscall.SIGSEGV)
	for s := range ch {
		log.Printf("catch signal(%+v), stop servers", s)
		svr.deregisterService()
		return
	}
}

func (svr *LaneConsumer) handleEcho(rw http.ResponseWriter, r *http.Request) {
	// consumer 自身的泳道标签：空串视为基线实例
	myLaneLabel := lane
	if myLaneLabel == "" {
		myLaneLabel = "(baseline)"
	}
	log.Printf("received request from %s, self=%s/%s, myLane=%s, headers=%v",
		r.RemoteAddr, selfNamespace, selfService, myLaneLabel, r.Header)

	// 1. 获取全量实例
	getAllReq := &polaris.GetAllInstancesRequest{}
	getAllReq.Namespace = namespace
	getAllReq.Service = service
	instancesResp, err := svr.consumer.GetAllInstances(getAllReq)
	if err != nil {
		log.Printf("[error] fail to getAllInstances: %v", err)
		http.Error(rw, fmt.Sprintf("fail to getAllInstances: %v", err), http.StatusInternalServerError)
		return
	}
	log.Printf("all instances count: %d", len(instancesResp.Instances))

	// 2. 构建路由请求
	routerReq := &polaris.ProcessRoutersRequest{}
	routerReq.DstInstances = instancesResp
	routerReq.SourceService.Service = selfService
	routerReq.SourceService.Namespace = selfNamespace

	// 3. 从 HTTP 请求头中提取泳道染色标签
	//    上游（或网关）会在请求头中透传 service-lane: {groupName}/{ruleName}
	stainLabel := r.Header.Get(trafficStainHeader)
	if stainLabel != "" {
		log.Printf("found stain label in header: %s=%s", trafficStainHeader, stainLabel)
		// 将染色标签注入 EnvironmentVariables，供泳道路由插件读取
		routerReq.EnvironmentVariables = map[string]string{
			trafficStainHeader: stainLabel,
		}
	} else {
		log.Printf("no stain label in header, lane router will try traffic matching rules")
		// 将 HTTP header、query 参数转为路由 Arguments，供泳道规则 TrafficMatchRule 匹配
		routerReq.AddArguments(buildRouteArguments(r)...)
	}

	// 4. 执行路由过滤
	routedResp, err := svr.router.ProcessRouters(routerReq)
	if err != nil {
		log.Printf("[error] fail to processRouters: %v", err)
		http.Error(rw, fmt.Sprintf("fail to processRouters: %v", err), http.StatusInternalServerError)
		return
	}
	log.Printf("routed instances count: %d, routeMetadata=%v", len(routedResp.Instances), routedResp.RouteMetadata)
	for i, inst := range routedResp.Instances {
		log.Printf("  [%d] %s:%d metadata=%v", i, inst.GetHost(), inst.GetPort(), inst.GetMetadata())
	}

	// 确定要透传的 stainLabel：
	// 优先使用从上游收到的原始染色标签，其次使用路由结果中写回的 routeMetadata
	propagateLabel := stainLabel
	if propagateLabel == "" {
		if meta := routedResp.RouteMetadata; meta != nil {
			propagateLabel = meta[trafficStainHeader]
		}
	}

	// 5. 负载均衡 + 实际调用
	buf := &bytes.Buffer{}
	for i := 0; i < times; i++ {
		lbReq := &polaris.ProcessLoadBalanceRequest{}
		lbReq.DstInstances = routedResp
		lbReq.LbPolicy = config.DefaultLoadBalancerWR
		oneInstResp, err := svr.router.ProcessLoadBalance(lbReq)
		if err != nil {
			log.Printf("[error] fail to processLoadBalance: %v", err)
			http.Error(rw, fmt.Sprintf("fail to processLoadBalance: %v", err), http.StatusInternalServerError)
			return
		}
		instance := oneInstResp.GetInstance()
		if instance == nil {
			log.Printf("[warn] no instance selected")
			continue
		}
		log.Printf("selected instance: %s:%d metadata=%v", instance.GetHost(), instance.GetPort(), instance.GetMetadata())

		// 构建下游请求：将染色标签透传给下游（泳道染色标签在微服务调用链中需要透传）
		func() {
			callResult := &polaris.ServiceCallResult{}
			callResult.SetCalledInstance(instance)
			startTime := time.Now()

			upstreamReq, _ := http.NewRequestWithContext(r.Context(), http.MethodGet,
				fmt.Sprintf("http://%s:%d/echo", instance.GetHost(), instance.GetPort()), nil)
			// 泳道染色标签透传：使用已确定的 propagateLabel（优先上游传入，其次路由结果写回）
			if propagateLabel != "" {
				upstreamReq.Header.Set(trafficStainHeader, propagateLabel)
			}

			resp, err := http.DefaultClient.Do(upstreamReq)
			callResult.SetDelay(time.Since(startTime))

			if err != nil {
				log.Printf("[error] send request to %s:%d fail: %s", instance.GetHost(), instance.GetPort(), err)
				svr.reportResult(callResult, model.RetFail, -1)
				_, _ = buf.WriteString(fmt.Sprintf("send request fail: %s\n", err))
				return
			}
			defer resp.Body.Close()
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				log.Printf("read resp from %s:%d fail: %s", instance.GetHost(), instance.GetPort(), err)
				svr.reportResult(callResult, model.RetFail, -1)
				_, _ = buf.WriteString(fmt.Sprintf("read resp fail: %s\n", err))
				return
			}

			svr.reportResult(callResult, model.RetSuccess, int32(resp.StatusCode))
			log.Printf("upstream %s:%d ok, status=%d, bytes=%d",
				instance.GetHost(), instance.GetPort(), resp.StatusCode, len(data))
			// consumer 自己的一行信息：self/lane/host:port/callee addr/callee lane/callee resp
			calleeLane := instance.GetMetadata()["lane"]
			if calleeLane == "" {
				calleeLane = "(baseline)"
			}
			msg := fmt.Sprintf("Hello, I'm %s. lane=%s, host=%s:%d, callee addr:%s:%d, callee lane=%s, callee resp=%s",
				selfService, myLaneLabel, svr.host, svr.port,
				instance.GetHost(), instance.GetPort(), calleeLane, string(data))
			log.Printf("resp to caller: %s, bytes~=%d", msg, len(msg))
			_, _ = buf.WriteString(msg)
			if len(msg) == 0 || msg[len(msg)-1] != '\n' {
				_ = buf.WriteByte('\n')
			}
			time.Sleep(30 * time.Millisecond)
		}()
	}

	rw.WriteHeader(http.StatusOK)
	log.Printf("resp to caller, bytes=%d", buf.Len())
	_, _ = rw.Write(buf.Bytes())
}

func (svr *LaneConsumer) reportResult(result *polaris.ServiceCallResult, retStatus model.RetStatus, retCode int32) {
	result.SetRetStatus(retStatus)
	result.SetRetCode(retCode)
	if err := svr.consumer.UpdateServiceCallResult(result); err != nil {
		log.Printf("[error] fail to UpdateServiceCallResult: %v", err)
	}
}

// buildRouteArguments 将 HTTP header 和 query 参数转为路由 Arguments，
// 用于泳道规则中 TrafficMatchRule 的流量识别。
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
	log.Printf("built route arguments count: %d", len(args))
	return args
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

	svr := &LaneConsumer{
		consumer:  polaris.NewConsumerAPIByContext(sdkCtx),
		router:    polaris.NewRouterAPIByContext(sdkCtx),
		provider:  polaris.NewProviderAPIByContext(sdkCtx),
		namespace: namespace,
		service:   service,
	}
	svr.Run()
}

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
