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
//  3. 将 HTTP Header / Query 参数转为路由 Arguments，供泳道规则的
//     TrafficMatchRule 进行流量识别和染色。
//  4. 通过 ProcessRouters 执行泳道路由过滤。
//  5. 通过 ProcessLoadBalance 选取单个实例。
//  6. 将请求转发给选中的实例，并在 Header 中透传泳道染色标签。
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
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
	// 1. 解析目标服务名和转发路径
	targetService, forwardPath, ok := parsePath(r.URL.Path)
	if !ok {
		http.Error(rw, "invalid path: expected /{targetService}/{path...}", http.StatusBadRequest)
		return
	}
	log.Printf("[INFO] received request: %s %s → targetService=%s, forwardPath=%s",
		r.Method, r.URL.Path, targetService, forwardPath)

	// 2. 获取目标服务全量实例
	getAllReq := &polaris.GetAllInstancesRequest{}
	getAllReq.Namespace = gw.namespace
	getAllReq.Service = targetService
	instancesResp, err := gw.consumer.GetAllInstances(getAllReq)
	if err != nil {
		log.Printf("[ERROR] fail to getAllInstances for %s: %v", targetService, err)
		http.Error(rw, fmt.Sprintf("fail to getAllInstances: %v", err), http.StatusInternalServerError)
		return
	}
	log.Printf("[INFO] %s all instances count: %d", targetService, len(instancesResp.Instances))

	// 3. 构建路由请求
	routerReq := &polaris.ProcessRoutersRequest{}
	routerReq.DstInstances = instancesResp
	routerReq.SourceService.Service = gw.selfService
	routerReq.SourceService.Namespace = gw.selfNamespace

	// 4. 网关作为泳道入口（染色点），将 HTTP Header / Query 参数转为路由 Arguments，
	//    供泳道规则中的 TrafficMatchRule 进行流量识别和染色
	routerReq.AddArguments(buildRouteArguments(r)...)

	// 5. 执行路由过滤（泳道路由 + 规则路由）
	routedResp, err := gw.router.ProcessRouters(routerReq)
	if err != nil {
		log.Printf("[ERROR] fail to processRouters for %s: %v", targetService, err)
		http.Error(rw, fmt.Sprintf("fail to processRouters: %v", err), http.StatusInternalServerError)
		return
	}
	log.Printf("[INFO] %s routed instances count: %d, routeMetadata=%v",
		targetService, len(routedResp.Instances), routedResp.RouteMetadata)

	// 6. 负载均衡，选取单个实例
	lbReq := &polaris.ProcessLoadBalanceRequest{}
	lbReq.DstInstances = routedResp
	lbReq.LbPolicy = config.DefaultLoadBalancerWR
	oneInstResp, err := gw.router.ProcessLoadBalance(lbReq)
	if err != nil {
		log.Printf("[ERROR] fail to processLoadBalance for %s: %v", targetService, err)
		http.Error(rw, fmt.Sprintf("fail to processLoadBalance: %v", err), http.StatusInternalServerError)
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

	// 透传泳道染色标签：
	// 如果原始请求已携带 service-lane header（如直接染色场景），则已通过上面的 header 复制透传，无需处理。
	// 如果原始请求未携带（流量匹配染色场景），则需要根据路由结果中的 RouteMetadata
	// 获取泳道路由器写回的完整 stainLabel（格式：groupName/ruleName）。
	if r.Header.Get(trafficStainHeader) == "" {
		// 优先从 RouteMetadata 获取泳道路由器写回的完整 stainLabel
		if stainLabel := routedResp.RouteMetadata[trafficStainHeader]; stainLabel != "" {
			upstreamReq.Header.Set(trafficStainHeader, stainLabel)
			log.Printf("[INFO] propagate stain label from routeMetadata: %s=%s",
				trafficStainHeader, stainLabel)
		} else if lane := instance.GetMetadata()["lane"]; lane != "" {
			// 回退：使用短格式（仅 lane 值），下游 matchByStainLabel 支持短格式查找
			upstreamReq.Header.Set(trafficStainHeader, lane)
			log.Printf("[INFO] propagate stain label from instance metadata: %s=%s",
				trafficStainHeader, lane)
		}
	}

	// 8. 发送请求到下游实例
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

	// 9. 将下游响应返回给客户端
	for k, vs := range resp.Header {
		for _, v := range vs {
			rw.Header().Add(k, v)
		}
	}
	rw.WriteHeader(resp.StatusCode)
	_, _ = rw.Write(data)
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

// buildRouteArguments 将 HTTP Header 和 Query 参数转为路由 Arguments，
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
	return args
}

func main() {
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
