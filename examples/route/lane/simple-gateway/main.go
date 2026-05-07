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

// Package main 泳道路由简化网关示例（基于 GetOneInstance）。
//
// 与 gateway 示例不同，本示例使用 ConsumerAPI.GetOneInstance 一步完成
// 服务发现 + 路由过滤（含泳道路由）+ 负载均衡，不需要手动调用
// ProcessRouters 和 ProcessLoadBalance。
//
// 在作为泳道入口的场景下，HTTP Method / Header / Query / Cookie / Path / 主调 IP
// 六类输入会被自动转换为 GetOneInstanceRequest 的 Arguments，lane router 的
// TrafficMatchRule 会据此进行流量识别和染色。
//
// 用法：
//
//	./bin -selfNamespace=default -selfService=LaneRouterGatewayService -port=48096
//
// 请求格式（路径第一段为下游服务名，会被路由到对应的泳道实例）：
//
//	http://localhost:48096/{targetService}/{path...}
//
// 请求示例：
//
//	curl http://localhost:48096/LaneEchoClient/echo
//	curl -H "warmup-user: gray" http://localhost:48096/LaneEchoClient/echo
package main

import (
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
	// trafficStainHeader 是泳道染色标签 HTTP 请求头名称。
	// 上游或网关会在请求头中透传该标签，格式：{laneGroupName}/{laneRuleName}。
	trafficStainHeader = "service-lane"
)

var (
	selfNamespace string
	selfService   string
	namespace     string
	port          int64
	debug         bool
)

func initArgs() {
	flag.StringVar(&selfNamespace, "selfNamespace", "default", "网关自身的 namespace（用于泳道入口匹配）")
	flag.StringVar(&selfService, "selfService", "LaneRouterGatewayService", "网关自身的服务名（泳道入口服务）")
	flag.StringVar(&namespace, "namespace", "default", "目标服务的 namespace")
	flag.Int64Var(&port, "port", 48096, "网关 HTTP 监听端口")
	flag.BoolVar(&debug, "debug", false, "是否开启 Polaris SDK debug 日志")
}

// SimpleLaneGateway 使用 GetOneInstance 的简化泳道网关。
type SimpleLaneGateway struct {
	consumer      polaris.ConsumerAPI
	selfNamespace string
	selfService   string
	namespace     string
}

// Run 启动网关 HTTP 服务。
func (gw *SimpleLaneGateway) Run() {
	http.HandleFunc("/", gw.handleProxy)
	log.Printf("[INFO] start simple lane gateway, port: %d, selfService: %s/%s",
		port, gw.selfNamespace, gw.selfService)
	if err := http.ListenAndServe(fmt.Sprintf("0.0.0.0:%d", port), nil); err != nil {
		log.Fatalf("[ERROR] fail to run gateway, err is %v", err)
	}
}

// handleProxy 接收客户端请求并转发到目标服务的泳道实例。
// 路径格式：/{targetService}/{path...}
func (gw *SimpleLaneGateway) handleProxy(rw http.ResponseWriter, r *http.Request) {
	// 1. 解析目标服务名和转发路径
	targetService, forwardPath, ok := parsePath(r.URL.Path)
	if !ok {
		http.Error(rw, "invalid path: expected /{targetService}/{path...}", http.StatusBadRequest)
		return
	}
	log.Printf("[INFO] received request: %s %s → self=%s/%s, targetService=%s, forwardPath=%s",
		r.Method, r.URL.Path, gw.selfNamespace, gw.selfService, targetService, forwardPath)

	// 2. 构造 GetOneInstance 请求。作为泳道入口，必须：
	//    - 设置 SourceService 为自身服务（lane router 据此匹配入口）
	//    - 把 HTTP Header / Query 参数通过 AddArguments 传入，供 TrafficMatchRule
	//      做流量识别和染色
	getOneReq := &polaris.GetOneInstanceRequest{}
	getOneReq.Namespace = gw.namespace
	getOneReq.Service = targetService
	getOneReq.SourceService = &model.ServiceInfo{
		Namespace: gw.selfNamespace,
		Service:   gw.selfService,
	}
	getOneReq.AddArguments(buildRouteArguments(r)...)

	// 3. GetOneInstance 一步完成服务发现 + 路由（含泳道）+ 负载均衡
	oneInstResp, err := gw.consumer.GetOneInstance(getOneReq)
	if err != nil {
		log.Printf("[ERROR] fail to getOneInstance for %s: %v", targetService, err)
		// ErrCodeAPIInstanceNotFound 表示路由链过滤后没有任何可用实例
		// (例如 STRICT 泳道匹配但无实例), 应当返回 HTTP 503 而不是 500,
		// 以便调用方能区分"没有候选实例"与"网关自身异常".
		if sdkErr, ok := err.(model.SDKError); ok && sdkErr.ErrorCode() == model.ErrCodeAPIInstanceNotFound {
			http.Error(rw, fmt.Sprintf("no instance available: %v", err), http.StatusServiceUnavailable)
			return
		}
		http.Error(rw, fmt.Sprintf("fail to getOneInstance: %v", err), http.StatusInternalServerError)
		return
	}
	instance := oneInstResp.GetInstance()
	if instance == nil {
		log.Printf("[WARN] no instance selected for %s", targetService)
		http.Error(rw, "no available instance", http.StatusServiceUnavailable)
		return
	}
	laneLabel := instance.GetMetadata()["lane"]
	if laneLabel == "" {
		laneLabel = "(baseline)"
	}
	log.Printf("[INFO] selected instance: %s:%d, lane=%s, metadata=%v",
		instance.GetHost(), instance.GetPort(), laneLabel, instance.GetMetadata())

	// 4. 构建上游请求并透传染色标签
	callResult := &polaris.ServiceCallResult{}
	callResult.SetCalledInstance(instance)
	startTime := time.Now()

	upstreamURL := fmt.Sprintf("http://%s:%d%s", instance.GetHost(), instance.GetPort(), forwardPath)
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}
	upstreamReq, err := http.NewRequest(r.Method, upstreamURL, r.Body)
	if err != nil {
		log.Printf("[ERROR] fail to create upstream request: %v", err)
		http.Error(rw, fmt.Sprintf("fail to create upstream request: %v", err), http.StatusInternalServerError)
		return
	}

	// 复制原始请求头
	for k, vs := range r.Header {
		for _, v := range vs {
			upstreamReq.Header.Add(k, v)
		}
	}

	// 透传泳道染色标签。本例作为入口, 原始请求通常不带 service-lane,
	// 需要根据路由结果把染色后的 stainLabel 补上给下游。
	// 从选中实例的 metadata["lane"] 提取短格式标签透传, lane router 的
	// matchByStainLabel 会在 stainLabelIndex 精确匹配失败后, 回落按
	// DefaultLabelValue 匹配, 短格式足以让下游路由到同一泳道。
	if r.Header.Get(trafficStainHeader) == "" {
		if lane := instance.GetMetadata()["lane"]; lane != "" {
			upstreamReq.Header.Set(trafficStainHeader, lane)
			log.Printf("[INFO] propagate stain label from instance metadata: %s=%s",
				trafficStainHeader, lane)
		}
	}

	// 5. 发送请求到下游实例
	resp, err := http.DefaultClient.Do(upstreamReq)
	callResult.SetDelay(time.Since(startTime))

	if err != nil {
		log.Printf("[ERROR] forward request to %s:%d fail: %v", instance.GetHost(), instance.GetPort(), err)
		gw.reportResult(callResult, model.RetFail, -1)
		http.Error(rw, fmt.Sprintf("forward request fail: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[ERROR] read response from %s:%d fail: %v", instance.GetHost(), instance.GetPort(), err)
		gw.reportResult(callResult, model.RetFail, -1)
		http.Error(rw, fmt.Sprintf("read response fail: %v", err), http.StatusBadGateway)
		return
	}

	gw.reportResult(callResult, model.RetSuccess, int32(resp.StatusCode))
	log.Printf("[INFO] upstream %s:%d ok, status=%d, bytes=%d",
		instance.GetHost(), instance.GetPort(), resp.StatusCode, len(data))

	// 6. 将下游响应返回给客户端：拼成单行 msg（self/lane/host:port/callee addr/callee lane/callee resp）
	// 注意: 不能照搬下游 Content-Length,否则 net/http 会按下游 body 长度截断/提前关连接。
	for k, vs := range resp.Header {
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
	log.Printf("resp to caller: %s, bytes~=%d", msg, len(msg))
	_, _ = rw.Write([]byte(msg))
}

func (gw *SimpleLaneGateway) reportResult(result *polaris.ServiceCallResult, retStatus model.RetStatus, retCode int32) {
	result.SetRetStatus(retStatus)
	result.SetRetCode(retCode)
	if err := gw.consumer.UpdateServiceCallResult(result); err != nil {
		log.Printf("[ERROR] fail to UpdateServiceCallResult: %v", err)
	}
}

// parsePath 从请求路径中解析目标服务名和转发路径。
// 路径格式：/{targetService}/{path...}
// 例如：/LaneEchoClient/echo → ("LaneEchoClient", "/echo", true)
func parsePath(urlPath string) (targetService string, forwardPath string, ok bool) {
	path := strings.TrimPrefix(urlPath, "/")
	if path == "" {
		return "", "", false
	}
	idx := strings.Index(path, "/")
	if idx < 0 {
		return path, "/", true
	}
	return path[:idx], path[idx:], true
}

// buildRouteArguments 将 HTTP Method / Header / Query / Cookie / Path / 主调 IP
// 六类输入转为路由 Arguments，供泳道规则中 TrafficMatchRule 的流量识别使用。
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
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	initArgs()
	flag.Parse()

	if debug {
		if err := api.SetLoggersLevel(api.DebugLog); err != nil {
			log.Printf("[WARN] 设置日志级别为 DEBUG 失败: %v", err)
		} else {
			log.Printf("[INFO] 已设置 Polaris SDK 日志级别为 DEBUG")
		}
	}

	consumer, err := polaris.NewConsumerAPI()
	if err != nil {
		log.Fatalf("fail to create consumerAPI: %v", err)
	}
	defer consumer.Destroy()

	gw := &SimpleLaneGateway{
		consumer:      consumer,
		selfNamespace: selfNamespace,
		selfService:   selfService,
		namespace:     namespace,
	}
	gw.Run()
}
