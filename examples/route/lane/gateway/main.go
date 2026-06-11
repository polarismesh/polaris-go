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

// Package main 泳道网关入口示例。
//
// LaneGateway 是泳道路由的入口（染色点），负责根据 HTTP 请求头匹配泳道规则，
// 将流量染色后路由到对应泳道的下游服务实例。
//
// 用法：
//
//	./bin -selfNamespace=default -selfService=LaneRouterGateway -port=48090
//
// 请求格式：
//
//	http://localhost:48090/{targetService}/{path...}
//
// 请求示例：
//
//	# 基线路由（无 Header）
//	curl http://localhost:48090/LaneCallerService/lane/caller/rest
//
//	# 路由到 gray 泳道
//	curl -H "color:gray" http://localhost:48090/LaneCallerService/lane/caller/rest
//
//	# 路由到 blue 泳道
//	curl -H "color:blue" http://localhost:48090/LaneCallerService/lane/caller/rest
//
// 路由流程：
//  1. 从请求 URL 的第一段路径提取目标服务名（如 LaneCallerService）。
//  2. 获取目标服务的全量实例。
//  3. 将 HTTP Method / Header / Query / Cookie / Path / 主调 IP 六类输入转为路由
//     Arguments，供泳道规则的 TrafficMatchRule 进行流量识别和染色。
//  4. 通过 ProcessRouters 执行泳道路由过滤。
//  5. 通过 ProcessLoadBalance 选取单个实例。
//  6. 将请求转发给选中的实例，并在 Header 中透传泳道染色标签 (短格式, 取自
//     instance metadata 的 lane 值; 下游 lane router 的 matchByStainLabel 会
//     按 DefaultLabelValue 回落匹配)。
package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

const (
	// trafficStainHeader 是泳道染色标签 HTTP 请求头名称。
	// 上游或网关会在请求头中透传该标签，格式：{laneGroupName}/{laneRuleName}。
	trafficStainHeader = "service-lane"
	// reqIDHeader 全链路追踪请求 ID，贯穿所有中间跳。
	reqIDHeader = "X-Request-ID"
)

var (
	selfNamespace string
	selfService   string
	namespace     string
	port          int64
	token         string
	debug         bool
)

func initArgs() {
	flag.StringVar(&selfNamespace, "selfNamespace", "default", "网关自身的 namespace（用于泳道入口匹配）")
	flag.StringVar(&selfService, "selfService", "LaneRouterGateway", "网关自身的服务名（泳道入口服务）")
	flag.StringVar(&namespace, "namespace", "default", "目标服务的 namespace")
	flag.Int64Var(&port, "port", 48080, "网关 HTTP 监听端口")
	flag.StringVar(&token, "token", "", "token（北极星开启鉴权时使用）")
	flag.BoolVar(&debug, "debug", false, "是否开启 Polaris SDK debug 日志")
}

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

// LaneGateway 泳道网关入口
type LaneGateway struct {
	consumer      polaris.ConsumerAPI
	router        polaris.RouterAPI
	selfNamespace string
	selfService   string
	namespace     string
}

// Run 启动网关 HTTP 服务
func (gw *LaneGateway) Run() {
	http.HandleFunc("/", gw.handleProxy)
	log.Printf("[INFO] start lane gateway, port: %d, selfService: %s/%s",
		port, gw.selfNamespace, gw.selfService)
	if err := http.ListenAndServe(fmt.Sprintf("0.0.0.0:%d", port), nil); err != nil {
		log.Fatalf("[ERROR] fail to run gateway, err is %v", err)
	}
}

// handleProxy 接收客户端请求并转发到目标服务的泳道实例。
//
// 请求路径格式：/{targetService}/{path...}
// 例如：/LaneCallerService/lane/caller/rest → 路由到 LaneCallerService 的某个泳道实例，
// 转发路径为 /lane/caller/rest。
func (gw *LaneGateway) handleProxy(rw http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get(reqIDHeader)
	if reqID == "" {
		reqID = newReqID()
	}
	logIncomingRequest(reqID, r, r.URL.Path)

	// 1. 解析目标服务名和转发路径
	targetService, forwardPath, ok := parsePath(r.URL.Path)
	if !ok {
		body := "invalid path: expected /{targetService}/{path...}"
		http.Error(rw, body, http.StatusBadRequest)
		logReply(reqID, r.RemoteAddr, http.StatusBadRequest, body)
		return
	}
	log.Printf("[%s] self=%s/%s → targetService=%s, forwardPath=%s",
		reqID, gw.selfNamespace, gw.selfService, targetService, forwardPath)

	// 2. 获取目标服务全量实例
	getAllReq := &polaris.GetAllInstancesRequest{}
	getAllReq.Namespace = gw.namespace
	getAllReq.Service = targetService
	instancesResp, err := gw.consumer.GetAllInstances(getAllReq)
	if err != nil {
		body := fmt.Sprintf("fail to getAllInstances: %v", err)
		log.Printf("[%s] [error] fail to getAllInstances for %s: %v", reqID, targetService, err)
		http.Error(rw, body, http.StatusInternalServerError)
		logReply(reqID, r.RemoteAddr, http.StatusInternalServerError, body)
		return
	}
	log.Printf("[%s] targetService=%s all instances count: %d", reqID, targetService, len(instancesResp.Instances))

	// 3. 构建路由请求
	routerReq := &polaris.ProcessRoutersRequest{}
	routerReq.DstInstances = instancesResp
	routerReq.SourceService.Service = gw.selfService
	routerReq.SourceService.Namespace = gw.selfNamespace

	// 4. 网关作为泳道入口（染色点），将 HTTP Method / Header / Query / Cookie / Path /
	//    主调 IP 六类输入转为路由 Arguments，供泳道规则中的 TrafficMatchRule
	//    进行流量识别和染色
	routerReq.AddArguments(buildRouteArguments(r)...)

	// 5. 执行路由过滤（泳道路由 + 规则路由）
	routedResp, err := gw.router.ProcessRouters(routerReq)
	if err != nil {
		body := fmt.Sprintf("fail to processRouters: %v", err)
		log.Printf("[%s] [error] fail to processRouters for %s: %v", reqID, targetService, err)
		http.Error(rw, body, http.StatusInternalServerError)
		logReply(reqID, r.RemoteAddr, http.StatusInternalServerError, body)
		return
	}
	log.Printf("[%s] targetService=%s routed instances count: %d",
		reqID, targetService, len(routedResp.Instances))

	// 6. 负载均衡，选取单个实例
	lbReq := &polaris.ProcessLoadBalanceRequest{}
	lbReq.DstInstances = routedResp
	lbReq.LbPolicy = config.DefaultLoadBalancerWR
	oneInstResp, err := gw.router.ProcessLoadBalance(lbReq)
	if err != nil {
		body := fmt.Sprintf("fail to processLoadBalance: %v", err)
		log.Printf("[%s] [error] fail to processLoadBalance for %s: %v", reqID, targetService, err)
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
		body := "no available instance"
		log.Printf("[%s] [warn] no instance selected for %s", reqID, targetService)
		http.Error(rw, body, http.StatusServiceUnavailable)
		logReply(reqID, r.RemoteAddr, http.StatusServiceUnavailable, body)
		return
	}
	laneLabel := instance.GetMetadata()["lane"]
	if laneLabel == "" {
		laneLabel = "(baseline)"
	}
	log.Printf("[%s] selected instance: %s:%d, lane=%s, metadata=%v",
		reqID, instance.GetHost(), instance.GetPort(), laneLabel, instance.GetMetadata())

	// 7. 构建转发请求
	callResult := &polaris.ServiceCallResult{}
	callResult.SetCalledInstance(instance)
	startTime := time.Now()

	upstreamURL := fmt.Sprintf("http://%s:%d%s", instance.GetHost(), instance.GetPort(), forwardPath)
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}
	upstreamReq, err := http.NewRequest(r.Method, upstreamURL, r.Body)
	if err != nil {
		body := fmt.Sprintf("fail to create upstream request: %v", err)
		log.Printf("[%s] [error] fail to create upstream request: %v", reqID, err)
		http.Error(rw, body, http.StatusInternalServerError)
		logReply(reqID, r.RemoteAddr, http.StatusInternalServerError, body)
		return
	}

	// 复制原始请求头
	for k, vs := range r.Header {
		for _, v := range vs {
			upstreamReq.Header.Add(k, v)
		}
	}
	// 将 reqID 注入下游请求头，便于全链路日志串联
	upstreamReq.Header.Set(reqIDHeader, reqID)

	// 透传泳道染色标签:
	//
	// 优先级(由高到低):
	//  1. 原始请求已携带 service-lane (下游直接染色场景) → 上面 header 复制环节已透传.
	//  2. SDK lane router 本次路由链写入的完整 stain label
	//     (InstancesResponse.RouteMetadata["service-lane"], 格式 "{groupName}/{ruleName}")
	//     → 精确匹配,下游 matchByStainLabel 直接命中 stainLabelIndex,无歧义.
	//  3. 兜底:从选中实例 metadata 读短格式 "lane" 值 .
	//
	if r.Header.Get(trafficStainHeader) == "" {
		propagated := ""
		if routeMeta := routedResp.RouteMetadata; routeMeta != nil {
			if v, ok := routeMeta[trafficStainHeader]; ok && v != "" {
				propagated = v
			}
		}
		if propagated == "" {
			propagated = instance.GetMetadata()["lane"]
		}
		if propagated != "" {
			upstreamReq.Header.Set(trafficStainHeader, propagated)
			log.Printf("[%s] propagate stain label: %s=%s", reqID, trafficStainHeader, propagated)
		}
	}

	// 8. 发送请求到下游实例
	resp, err := http.DefaultClient.Do(upstreamReq)
	callResult.SetDelay(time.Since(startTime))

	if err != nil {
		body := fmt.Sprintf("forward request fail: %v", err)
		log.Printf("[%s] [error] forward request to %s:%d fail: %v", reqID, instance.GetHost(), instance.GetPort(), err)
		gw.reportResult(callResult, model.RetFail, -1)
		http.Error(rw, body, http.StatusBadGateway)
		logReply(reqID, r.RemoteAddr, http.StatusBadGateway, body)
		return
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		body := fmt.Sprintf("read response fail: %v", err)
		log.Printf("[%s] [error] read response from %s:%d fail: %v", reqID, instance.GetHost(), instance.GetPort(), err)
		gw.reportResult(callResult, model.RetFail, -1)
		http.Error(rw, body, http.StatusBadGateway)
		logReply(reqID, r.RemoteAddr, http.StatusBadGateway, body)
		return
	}

	gw.reportResult(callResult, model.RetSuccess, int32(resp.StatusCode))
	log.Printf("[%s] upstream %s:%d ok, status=%d, bytes=%d",
		reqID, instance.GetHost(), instance.GetPort(), resp.StatusCode, len(data))

	// 9. 将下游响应返回给客户端：拼成单行 msg（self/lane/host:port/callee addr/callee lane/callee resp）
	// 注意: 不能照搬下游 Content-Length,否则 net/http 会按下游 body 长度截断/提前关连接。
	for k, vs := range resp.Header {
		// 跳过 Content-Length / Transfer-Encoding,让 Go 按我们新 body 重新计算。
		if strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Transfer-Encoding") {
			continue
		}
		for _, v := range vs {
			rw.Header().Add(k, v)
		}
	}
	rw.WriteHeader(resp.StatusCode)
	msg := fmt.Sprintf("Hello, I'm %s. lane=%s, host=0.0.0.0:%d, callee addr:%s:%d, callee lane=%s, callee resp=%s",
		gw.selfService, "(entry)", port, instance.GetHost(), instance.GetPort(), laneLabel,
		string(data))
	log.Printf("[%s] resp to caller: %s, bytes~=%d", reqID, msg, len(msg))
	_, _ = rw.Write([]byte(msg))
	logReply(reqID, r.RemoteAddr, resp.StatusCode, msg)
}

func (gw *LaneGateway) reportResult(result *polaris.ServiceCallResult, retStatus model.RetStatus, retCode int32) {
	result.SetRetStatus(retStatus)
	result.SetRetCode(retCode)
	if err := gw.consumer.UpdateServiceCallResult(result); err != nil {
		log.Printf("[ERROR] fail to UpdateServiceCallResult: %v", err)
	}
}

// parsePath 从请求路径中解析目标服务名和转发路径。
// 路径格式：/{targetService}/{path...}
// 例如：/LaneCallerService/lane/caller/rest → ("LaneCallerService", "/lane/caller/rest", true)
func parsePath(urlPath string) (targetService string, forwardPath string, ok bool) {
	path := strings.TrimPrefix(urlPath, "/")
	if path == "" {
		return "", "", false
	}
	idx := strings.Index(path, "/")
	if idx < 0 {
		// 只有服务名，转发路径为 /
		return path, "/", true
	}
	return path[:idx], path[idx:], true
}

// buildRouteArguments 将 HTTP Method / Header / Query / Cookie / Path / 主调 IP
// 六类输入转为路由 Arguments，用于泳道规则中 TrafficMatchRule 的流量识别。
// 与 polaris-java LaneUtils 保持一致的 6 类匹配维度。
func buildRouteArguments(r *http.Request) []model.Argument {
	args := make([]model.Argument, 0, 16)
	// 1. $method —— HTTP 方法（GET/POST/...)
	args = append(args, model.BuildMethodArgument(r.Method))
	// 2. Header —— 遍历所有请求头，key 统一小写避免大小写踩坑
	for k, vs := range r.Header {
		if len(vs) == 0 {
			continue
		}
		args = append(args, model.BuildHeaderArgument(strings.ToLower(k), vs[0]))
	}
	// 3. Query —— URL 查询参数
	for k, vs := range r.URL.Query() {
		if len(vs) == 0 {
			continue
		}
		args = append(args, model.BuildQueryArgument(strings.ToLower(k), vs[0]))
	}
	// 4. Cookie —— Go 标准库已帮我们把 Cookie header 解析成 (name, value) 对
	for _, c := range r.Cookies() {
		args = append(args, model.BuildCookieArgument(c.Name, c.Value))
	}
	// 5. $Path —— 使用完整 URL path（含目标服务名前缀），与规则作者的直觉一致
	args = append(args, model.BuildPathArgument(r.URL.Path))
	// 6. $caller_ip —— 从 RemoteAddr 拆出 host 部分（剥掉端口号）
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || host == "" {
		host = r.RemoteAddr
	}
	args = append(args, model.BuildCallerIPArgument(host))
	return args
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
	initArgs()
	flag.Parse()

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

	gw := &LaneGateway{
		consumer:      polaris.NewConsumerAPIByContext(sdkCtx),
		router:        polaris.NewRouterAPIByContext(sdkCtx),
		selfNamespace: selfNamespace,
		selfService:   selfService,
		namespace:     namespace,
	}
	gw.Run()
}
