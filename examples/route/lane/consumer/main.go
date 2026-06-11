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
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

// newReqID 生成 8 字符请求 ID，用于串起同一次请求的多条日志.
func newReqID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
	}
	return hex.EncodeToString(b[:])
}

// formatHeaders 把 http.Header 压缩为单行字符串.
func formatHeaders(h http.Header) string {
	if len(h) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, strings.Join(h[k], ",")))
	}
	return "{" + strings.Join(parts, "; ") + "}"
}

// formatQuery 把 query 参数压缩为单行字符串.
func formatQuery(q map[string][]string) string {
	if len(q) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, strings.Join(q[k], ",")))
	}
	return "{" + strings.Join(parts, "; ") + "}"
}

// logIncomingRequest 单行打印收到的请求：方法/URL/query/headers/body/cookie，都带 reqID 前缀.
func logIncomingRequest(reqID string, r *http.Request, method string) {
	var bodyStr string
	if r.Body != nil {
		bodyBytes, err := ioutil.ReadAll(r.Body)
		if err == nil {
			bodyStr = string(bodyBytes)
		}
		_ = r.Body.Close()
	}
	log.Printf("[%s] <<< recv from %s | path=%s method=%s url=%s query=%s headers=%s body=%s",
		reqID, r.RemoteAddr, method, r.Method, r.URL.String(), formatQuery(r.URL.Query()), formatHeaders(r.Header), bodyStr)
}

// logReply 单行打印自身回给上游 client 的响应.
func logReply(reqID, remoteAddr string, status int, body string) {
	log.Printf("[%s] >>> reply to client %s: status=%d body=%s", reqID, remoteAddr, status, body)
}

// trafficStainHeader 是上游或网关透传的泳道染色标签 HTTP 请求头名称。
// 值格式：{laneGroupName}/{laneRuleName}，例如 "gray/rule1"。
const trafficStainHeader = "service-lane"

// reqIDHeader 全链路追踪请求 ID，贯穿所有中间跳。
const reqIDHeader = "X-Request-ID"

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
	reqID := r.Header.Get(reqIDHeader)
	if reqID == "" {
		reqID = newReqID()
	}
	logIncomingRequest(reqID, r, "/echo")

	// consumer 自身的泳道标签：空串视为基线实例
	myLaneLabel := lane
	if myLaneLabel == "" {
		myLaneLabel = "(baseline)"
	}
	log.Printf("[%s] received request from %s, self=%s/%s, myLane=%s",
		reqID, r.RemoteAddr, selfNamespace, selfService, myLaneLabel)

	// 1. 获取全量实例
	getAllReq := &polaris.GetAllInstancesRequest{}
	getAllReq.Namespace = namespace
	getAllReq.Service = service
	instancesResp, err := svr.consumer.GetAllInstances(getAllReq)
	if err != nil {
		body := fmt.Sprintf("fail to getAllInstances: %v", err)
		log.Printf("[%s] [error] fail to getAllInstances: %v", reqID, err)
		http.Error(rw, body, http.StatusInternalServerError)
		logReply(reqID, r.RemoteAddr, http.StatusInternalServerError, body)
		return
	}
	log.Printf("[%s] all instances count: %d", reqID, len(instancesResp.Instances))

	// 2. 构建路由请求
	routerReq := &polaris.ProcessRoutersRequest{}
	routerReq.DstInstances = instancesResp
	routerReq.SourceService.Service = selfService
	routerReq.SourceService.Namespace = selfNamespace

	// 3. 从 HTTP 请求头中提取泳道染色标签
	//    上游（或网关）会在请求头中透传 service-lane: {groupName}/{ruleName}
	stainLabel := r.Header.Get(trafficStainHeader)
	if stainLabel != "" {
		log.Printf("[%s] found stain label in header: %s=%s", reqID, trafficStainHeader, stainLabel)
		// 把染色标签作为 HEADER 类型的 Argument 上报，供泳道路由插件按 ArgumentType 识别。
		// lane router 的染色检测会遍历 RouteArguments 找 (Header|Query|Cookie) + key=service-lane。
		routerReq.AddArguments(model.BuildHeaderArgument(trafficStainHeader, stainLabel))
	} else {
		log.Printf("[%s] no stain label in header, lane router will try traffic matching rules", reqID)
		// 将 HTTP header、query 参数转为路由 Arguments，供泳道规则 TrafficMatchRule 匹配
		routerReq.AddArguments(buildRouteArguments(r)...)
	}

	// 4. 执行路由过滤
	routedResp, err := svr.router.ProcessRouters(routerReq)
	if err != nil {
		body := fmt.Sprintf("fail to processRouters: %v", err)
		log.Printf("[%s] [error] fail to processRouters: %v", reqID, err)
		http.Error(rw, body, http.StatusInternalServerError)
		logReply(reqID, r.RemoteAddr, http.StatusInternalServerError, body)
		return
	}
	log.Printf("[%s] routed instances count: %d", reqID, len(routedResp.Instances))
	for i, inst := range routedResp.Instances {
		log.Printf("[%s]   [%d] %s:%d metadata=%v", reqID, i, inst.GetHost(), inst.GetPort(), inst.GetMetadata())
	}

	// 5. 负载均衡 + 实际调用
	buf := &bytes.Buffer{}
	for i := 0; i < times; i++ {
		lbReq := &polaris.ProcessLoadBalanceRequest{}
		lbReq.DstInstances = routedResp
		lbReq.LbPolicy = config.DefaultLoadBalancerWR
		oneInstResp, err := svr.router.ProcessLoadBalance(lbReq)
		if err != nil {
			body := fmt.Sprintf("fail to processLoadBalance: %v", err)
			log.Printf("[%s] [error] fail to processLoadBalance: %v", reqID, err)
			if sdkErr, ok := err.(model.SDKError); ok && sdkErr.ErrorCode() == model.ErrCodeAPIInstanceNotFound {
				http.Error(rw, fmt.Sprintf("no instance available: %v", err), http.StatusServiceUnavailable)
				logReply(reqID, r.RemoteAddr, http.StatusServiceUnavailable, body)
				return
			}
			http.Error(rw, body, http.StatusInternalServerError)
			logReply(reqID, r.RemoteAddr, http.StatusInternalServerError, body)
			return
		}
		instance := oneInstResp.GetInstance()
		if instance == nil {
			log.Printf("[%s] [warn] no instance selected", reqID)
			continue
		}
		log.Printf("[%s] selected instance: %s:%d metadata=%v", reqID, instance.GetHost(), instance.GetPort(), instance.GetMetadata())

		// 构建下游请求：将染色标签透传给下游（泳道染色标签在微服务调用链中需要透传）
		func() {
			callResult := &polaris.ServiceCallResult{}
			callResult.SetCalledInstance(instance)
			startTime := time.Now()

			upstreamReq, _ := http.NewRequestWithContext(r.Context(), http.MethodGet,
				fmt.Sprintf("http://%s:%d/echo", instance.GetHost(), instance.GetPort()), nil)
			// 将 reqID 注入下游请求头，便于全链路日志串联
			upstreamReq.Header.Set(reqIDHeader, reqID)
			// 泳道染色标签透传:
			//
			// 优先级(由高到低),与 gateway/main.go 一致:
			//  1. 上游透传来的 service-lane header (stainLabel 变量已从 Header 取出).
			//  2. SDK lane router 本次路由链写入的 RouteMetadata["service-lane"] —— 完整
			//     格式 "{groupName}/{ruleName}",供下游精确匹配.在 consumer 场景这主要
			//     走 passthrough 路径 (因 consumer 不是流量入口,不会首次染色),仍提供
			//     非空兜底以防上游 header 与 RouteMetadata 不一致.
			//  3. 兜底:选中实例 metadata 的 "lane" 短格式 .
			propagateLabel := stainLabel
			if propagateLabel == "" {
				if routeMeta := routedResp.RouteMetadata; routeMeta != nil {
					if v, ok := routeMeta[trafficStainHeader]; ok && v != "" {
						propagateLabel = v
					}
				}
			}
			if propagateLabel == "" {
				propagateLabel = instance.GetMetadata()["lane"]
			}
			if propagateLabel != "" {
				upstreamReq.Header.Set(trafficStainHeader, propagateLabel)
			}

			resp, err := http.DefaultClient.Do(upstreamReq)
			callResult.SetDelay(time.Since(startTime))

			if err != nil {
				log.Printf("[%s] [error] send request to %s:%d fail: %s", reqID, instance.GetHost(), instance.GetPort(), err)
				svr.reportResult(callResult, model.RetFail, -1)
				_, _ = buf.WriteString(fmt.Sprintf("send request fail: %s\n", err))
				return
			}
			defer resp.Body.Close()
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				log.Printf("[%s] read resp from %s:%d fail: %s", reqID, instance.GetHost(), instance.GetPort(), err)
				svr.reportResult(callResult, model.RetFail, -1)
				_, _ = buf.WriteString(fmt.Sprintf("read resp fail: %s\n", err))
				return
			}

			svr.reportResult(callResult, model.RetSuccess, int32(resp.StatusCode))
			log.Printf("[%s] upstream %s:%d ok, status=%d, bytes=%d",
				reqID, instance.GetHost(), instance.GetPort(), resp.StatusCode, len(data))
			// consumer 自己的一行信息：self/lane/host:port/callee addr/callee lane/callee resp
			calleeLane := instance.GetMetadata()["lane"]
			if calleeLane == "" {
				calleeLane = "(baseline)"
			}
			msg := fmt.Sprintf("Hello, I'm %s. lane=%s, host=%s:%d, callee addr:%s:%d, callee lane=%s, callee resp=%s",
				selfService, myLaneLabel, svr.host, svr.port,
				instance.GetHost(), instance.GetPort(), calleeLane, string(data))
			log.Printf("[%s] resp to caller(in-loop): %s, bytes~=%d", reqID, msg, len(msg))
			_, _ = buf.WriteString(msg)
			if len(msg) == 0 || msg[len(msg)-1] != '\n' {
				_ = buf.WriteByte('\n')
			}
			time.Sleep(30 * time.Millisecond)
		}()
	}

	rw.WriteHeader(http.StatusOK)
	log.Printf("[%s] resp to caller, bytes=%d", reqID, buf.Len())
	_, _ = rw.Write(buf.Bytes())
	logReply(reqID, r.RemoteAddr, http.StatusOK, buf.String())
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
