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

// Package main 熔断主调示例（统一装饰器写法）。
//
// 该 demo 同时覆盖三种熔断级别——服务级 (SERVICE)、接口级 (METHOD)、
// 实例级 (INSTANCE)——并通过同一种装饰器编程模型表达：
//
//  1. 业务回调挂在 CircuitBreakerAPI.MakeFunctionDecorator 上；
//  2. RequestContext.Method 决定是否启用接口级熔断（留空表示仅服务级 / 实例级）；
//  3. customer func 内通过 model.GetInvokeContext(ctx).SetInstance(ins)
//     把"本次实际命中的实例"回填给装饰器；装饰器结束阶段会自动按服务级、
//     接口级、实例级三种 Resource 上报，从而触发 composite breaker 对应级别
//     的熔断统计。
//
// 当业务只关心"服务级 / 接口级"熔断时，可以不调用 SetInstance；
// 此时装饰器跳过实例级上报，行为与历史完全一致（向后兼容）。
//
// 暴露的 HTTP 接口（仅作 demo 链路）：
//
//	/echo  ：MakeFunctionDecorator + RequestContext.Method=/echo  + SetInstance
//	/order ：MakeFunctionDecorator + RequestContext.Method=/order + SetInstance
//	/info  ：MakeFunctionDecorator + RequestContext.Method=/info  + SetInstance
//	/slow  ：MakeFunctionDecorator + RequestContext.Method=/slow  + SetInstance
//
// 散装写法（自行调用 CircuitBreakerAPI.Check / Report /
// ConsumerAPI.UpdateServiceCallResult）SDK 仍保留可用，
// 存量客户零改动；该写法的旧示例见 examples/circuitbreaker/oldInstanceCircuitBreakerCaller。
package main

import (
	"context"
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

// reqIDHeader 全链路追踪请求 ID，贯穿所有中间跳。
// 入口优先从 header 读；没有则本地生成；向下游发请求时显式注入。
const reqIDHeader = "X-Request-ID"

// reqIDCtxKey ctx 上挂载的 reqID 键名，避免与其它 context value 冲突。
type reqIDCtxKey struct{}

func initArgs() {
	flag.StringVar(&selfNamespace, "selfNamespace", "default", "selfNamespace")
	flag.StringVar(&selfService, "selfService", "CircuitBreakerCaller", "selfService")
	flag.BoolVar(&selfRegister, "selfRegister", false, "selfRegister")
	flag.StringVar(&calleeNamespace, "calleeNamespace", "default", "calleeNamespace")
	flag.StringVar(&calleeService, "calleeService", "CircuitBreakerCallee", "calleeService")
	flag.IntVar(&port, "port", 18080, "port")
	flag.StringVar(&token, "token", "", "token")
	flag.StringVar(&configPath, "config", "./polaris.yaml", "path for config file")
	flag.BoolVar(&debug, "debug", false, "是否开启 Polaris SDK debug 日志")
}

// PolarisClient 持有装饰器主调所需的 SDK 句柄。
type PolarisClient struct {
	provider       polaris.ProviderAPI
	consumer       polaris.ConsumerAPI
	circuitBreaker polaris.CircuitBreakerAPI
	host           string
	isShutdown     bool
	webSvr         *http.Server
}

// commonResponse 由 customer func 返回；customCodeConvert.OnSuccess 据此抽取
// retCode，SDK 会用 retCode 命中规则的 ErrorCondition.RET_CODE 判定调用是否
// 计为失败。返回值同时携带原始 body，便于调用方观察底层 provider 的真实响应。
type commonResponse struct {
	code int
	body string
}

type customCodeConvert struct{}

// OnSuccess 业务回调返回 nil error 时被调用，把 commonResponse.code 转换成字符串 retCode。
func (c *customCodeConvert) OnSuccess(val interface{}) string {
	resp, ok := val.(commonResponse)
	if !ok {
		return "500"
	}
	return strconv.Itoa(resp.code)
}

// OnError 业务回调返回非 nil error 时被调用。
//
// 这里返回一个明显非 HTTP 状态码的占位值（"-1"），原因：
//   - 业务回调拿不到底层 HTTP 状态码（http.Get 失败时还没拿到响应）；
//   - 若返回 "400" / "500" 这类真实状态码，会被 ErrorCondition.RET_CODE 类规则
//     误命中或漏命中，扰乱熔断判定；
//   - 用 "-1" 作为"非 HTTP 状态码"明示标记，规则可按需挂 EXACT="-1" 显式纳入。
func (c *customCodeConvert) OnError(err error) string {
	log.Printf("customCodeConvert OnError input: %v", err)
	return "-1"
}

// discoverInstance 从 polaris-go 服务发现里取一个目标实例，并打印当前所有实例
// 的熔断状态，便于运行 demo 时观察实例级熔断的状态翻转。
func (svr *PolarisClient) discoverInstance() (model.Instance, error) {
	svr.printAllInstances()
	getOneRequest := &polaris.GetOneInstanceRequest{}
	getOneRequest.Namespace = calleeNamespace
	getOneRequest.Service = calleeService
	oneInstResp, err := svr.consumer.GetOneInstance(getOneRequest)
	if err != nil {
		return nil, fmt.Errorf("getOneInstance fail: %w", err)
	}
	instance := oneInstResp.GetInstance()
	if instance == nil {
		return nil, fmt.Errorf("getOneInstance empty result")
	}
	log.Printf("getOneInstance is %s:%d, healthy=%v", instance.GetHost(),
		instance.GetPort(), instance.IsHealthy())
	return instance, nil
}

// printAllInstances 打印当前被调服务下全部实例的熔断状态，便于观察 INSTANCE 级
// 熔断器何时进入 OPEN/HALF_OPEN/CLOSE。
func (svr *PolarisClient) printAllInstances() {
	req := &polaris.GetInstancesRequest{
		GetInstancesRequest: model.GetInstancesRequest{
			Service:         calleeService,
			Namespace:       calleeNamespace,
			SkipRouteFilter: true,
		},
	}
	instancesResp, err := svr.consumer.GetInstances(req)
	if err != nil {
		log.Printf("[error] fail to getInstances for request [%+v]: %v", req, err)
		return
	}
	log.Printf("printAllInstances get [%d] instances", len(instancesResp.GetInstances()))
	for _, ins := range instancesResp.GetInstances() {
		cbStatus := "close"
		isHealthy := ins.IsHealthy()
		isIsolated := ins.IsIsolated()
		if ins.GetCircuitBreakerStatus() != nil {
			cbStatus = ins.GetCircuitBreakerStatus().GetStatus().String()

		}
		log.Printf("  %s:%d cb_status=%s healthy:%v, isolated:%v", ins.GetHost(), ins.GetPort(), cbStatus, isHealthy,
			isIsolated)
	}
}

// reportServiceCallResult 上报服务调用结果，供负载均衡和健康检查使用。
// instance 为被调实例，retStatus 为调用结果状态，statusCode 为 HTTP 状态码，
// delay 为调用耗时，method 为被调接口路径（对应该 demo 展示的接口级熔断维度）。
func (svr *PolarisClient) reportServiceCallResult(instance model.Instance, retStatus model.RetStatus, statusCode int, delay time.Duration, method string) {
	ret := &polaris.ServiceCallResult{
		ServiceCallResult: model.ServiceCallResult{
			EmptyInstanceGauge: model.EmptyInstanceGauge{},
			CalledInstance:     instance,
			Method:             method,
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

// makeDecorator 构造一个装饰器，把 path 同时写入 BlockConfig.api.path 维度（接口级）
// 和 customer func 实际访问的 URL，并在 customer func 内部 SetInstance 让实例级也生效。
//
// HTTP 状态码处理策略（区分 4xx 与 5xx）：
//   - 网络错 / TCP 失败：return error → 装饰器走 OnError → SDK 内部约定 retCode="-1" 哨兵
//   - 5xx：return error → 走 OnError；retCode="-1" 哨兵直接命中 RET_CODE 类规则计入熔断
//   - 4xx：return commonResponse, nil → 走 OnSuccess；retCode 透传真实状态码（"401"/"404" 等），
//     由用户配置的规则决定是否纳入熔断（默认 RANGE 500~599 不命中 4xx，不计入）
//   - 2xx：return commonResponse, nil → 走 OnSuccess；retCode 透传真实状态码，规则不命中 → 计为成功
//
// 装饰器结束阶段，SDK 会按以下顺序统一上报：
//   - 服务级 ServiceResource         （Caller→Callee）
//   - 接口级 MethodResource (path)   （RequestContext.Method 非空时）
//   - 实例级 InstanceResource        （customer func 调用 SetInstance 时）
//
// 根据 path 推断 protocol/method 的字
//
//		/api/protocol/{proto}    → protocol = {proto} , method = "*"
//		/api/method/{method}     → protocol = "*"      , method = {method}
//		/api/pathtype/...        → protocol = "*"      , method = "*"（规则按 path type 区分）
//		其它 /echo /order /info /slow /forbidden   → protocol = "*" , method = "*"（向后兼容）
//	  其它 /echo /order /info /slow /forbidden   → protocol = "*" , method = "*"（向后兼容）
func inferProtocolMethod(path string) (protocol, method string) {
	switch {
	case strings.HasPrefix(path, "/api/protocol/"):
		return strings.TrimPrefix(path, "/api/protocol/"), "*"
	case strings.HasPrefix(path, "/api/method/"):
		return "*", strings.TrimPrefix(path, "/api/method/")
	case strings.HasPrefix(path, "/api/pathtype/"):
		return "*", "*"
	default:
		return "*", "*"
	}
}

func (svr *PolarisClient) makeDecorator(path string) model.DecoratorFunction {
	protocol, method := inferProtocolMethod(path)
	return svr.circuitBreaker.MakeFunctionDecorator(
		func(ctx context.Context, args interface{}) (interface{}, error) {
			instance, err := svr.discoverInstance()
			if err != nil {
				return nil, err
			}
			// 关键：把所选实例回填给装饰器，让 SDK 在 OnSuccess/OnError
			// 阶段自动按 InstanceResource 上报，触发实例级熔断统计。
			if ic := model.GetInvokeContext(ctx); ic != nil {
				ic.SetInstance(instance)
			}
			url := fmt.Sprintf("http://%s:%d%s", instance.GetHost(), instance.GetPort(), path)
			start := time.Now()
			// 从 ctx 取 reqID（由入口 handler 注入），构造带 X-Request-ID header 的请求，
			// 实现 consumer→provider 全链路日志串联
			reqID, _ := ctx.Value(reqIDCtxKey{}).(string)
			upstreamReq, httpErr := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if httpErr != nil {
				return nil, httpErr
			}
			upstreamReq.Header.Set(reqIDHeader, reqID)
			resp, httpErr := http.DefaultClient.Do(upstreamReq)
			delay := time.Since(start)
			if resp != nil {
				defer resp.Body.Close()
			}
			if httpErr != nil {
				// 网络错按 RetFail 上报给 LB / 健康检查；
				// 同时通过返回 error 让装饰器走 OnError → SDK 内部 retCode="-1" 哨兵触发熔断
				svr.reportServiceCallResult(instance, model.RetFail, http.StatusInternalServerError, delay, path)
				return nil, httpErr
			}
			data, _ := io.ReadAll(resp.Body)
			// LB / 健康检查只把 5xx 当作"实例侧故障"，4xx 一律视为成功（请求侧问题）
			retStatus := model.RetSuccess
			if resp.StatusCode >= http.StatusInternalServerError {
				retStatus = model.RetFail
			}
			svr.reportServiceCallResult(instance, retStatus, resp.StatusCode, delay, path)

			// 5xx 路径：返回 error 让装饰器走 OnError，SDK 内部用 retCode="-1" 哨兵触发熔断；
			// 4xx / 2xx 路径：返回 commonResponse 让装饰器走 OnSuccess，retCode 透传真实状态码，
			//                让用户配置的 BlockConfig.ErrorConditions 决定是否纳入熔断
			body := commonResponse{code: resp.StatusCode, body: string(data)}
			if resp.StatusCode >= http.StatusInternalServerError {
				return nil, fmt.Errorf("upstream %s:%d returned %d: %s",
					instance.GetHost(), instance.GetPort(), resp.StatusCode, body.body)
			}
			return body, nil
		},
		&api.RequestContext{
			RequestContext: model.RequestContext{
				Caller: &model.ServiceKey{
					Namespace: selfNamespace,
					Service:   selfService,
				},
				Callee: &model.ServiceKey{
					Namespace: calleeNamespace,
					Service:   calleeService,
				},
				CodeConvert: &customCodeConvert{},
				Path:        path,
				Protocol:    protocol,
				HTTPMethod:  method,
			},
		},
	)
}

// writeResult 把装饰器返回值写回到 HTTP 响应；统一处理"被熔断（abort）"、
// "底层调用失败（err）"、"成功透传 commonResponse" 三类分支。
//
// 熔断拦截分两种情况：
//  1. 规则配置了 fallback 响应：abort.HasFallback()==true，按服务端下发的
//     code / headers / body 透传；
//  2. 规则未配置 fallback（含半开态配额耗尽路径）：abort.HasFallback()==false，
//     此时统一返回 503 + "circuit breaker open" 文案，避免 0 状态码引发上游误判。
//
// 返回 (status, body) 便于调用方在 logReply 中按 HTTP 真实响应记录。
func writeResult(rw http.ResponseWriter, ret interface{}, abort *model.CallAborted, err error) (int, string) {
	// 注意：熔断拦截时装饰器同时返回 abort != nil 且 err == ErrorCallAborted（非 nil），
	// 因此必须先判 abort 再判 err，否则 fallback / 503 分支永远走不到，
	// 会把熔断响应错当成普通业务错误返回（表现为 fallback 永远不下发）。
	if abort != nil {
		if abort.HasFallback() {
			// 服务端下发了 fallback 响应：透传状态码 / headers / body
			status := abort.GetFallbackCode()
			fbBody := abort.GetFallbackBody()
			// 必须先写 header 再 WriteHeader：net/http 在 WriteHeader 后追加的 header 会被丢弃。
			for k, v := range abort.GetFallbackHeaders() {
				rw.Header().Add(k, v)
			}
			rw.WriteHeader(status)
			_, _ = rw.Write([]byte(fbBody))
			return status, fbBody
		}
		// 无 fallback 时（含半开配额耗尽）：返回 503 默认响应
		body := "circuit breaker open: " + abort.GetError().Error()
		rw.WriteHeader(http.StatusServiceUnavailable)
		_, _ = rw.Write([]byte(body))
		return http.StatusServiceUnavailable, body
	}
	// 非熔断的真实业务错误（abort 为 nil 但底层调用返回 err）
	if err != nil {
		body := fmt.Sprintf("[error] fail : %s", err)
		rw.WriteHeader(http.StatusInternalServerError)
		_, _ = rw.Write([]byte(body))
		return http.StatusInternalServerError, body
	}
	resp, _ := ret.(commonResponse)
	// 透传 provider 的真实状态码与 body：脚本/curl 据此能直接区分 200/5xx，
	// 不必额外解析 body。
	rw.WriteHeader(resp.code)
	_, _ = rw.Write([]byte(resp.body))
	return resp.code, resp.body
}

// runWebServer 给四种 demo 接口各挂一个装饰器，再注册到 net/http。
func (svr *PolarisClient) runWebServer() {
	dealEcho := svr.makeDecorator("/echo")
	dealOrder := svr.makeDecorator("/order")
	dealInfo := svr.makeDecorator("/info")
	dealSlow := svr.makeDecorator("/slow")
	// /forbidden 用于"4xx 不触发熔断"的端到端验证：provider 永远返回 403，
	// 在默认 RANGE 500~599 规则下，连续访问应全部 fail 但不出现 abort。
	dealForbidden := svr.makeDecorator("/forbidden")
	// 用例 8/9/10 演示端点：path 段编码 protocol / method / 路径匹配方式。
	// makeDecorator 内部通过 inferProtocolMethod 自动从 path 推断
	// RequestContext.Protocol / HTTPMethod，避免在 main.go 硬编码 18 条规则。
	dealProtoHTTP := svr.makeDecorator("/api/protocol/http")
	dealProtoDUBBO := svr.makeDecorator("/api/protocol/dubbo")
	dealProtoGRPC := svr.makeDecorator("/api/protocol/grpc")
	dealProtoTHRIFT := svr.makeDecorator("/api/protocol/thrift")
	dealMethGET := svr.makeDecorator("/api/method/GET")
	dealMethPOST := svr.makeDecorator("/api/method/POST")
	dealMethPUT := svr.makeDecorator("/api/method/PUT")
	dealMethPATCH := svr.makeDecorator("/api/method/PATCH")
	dealMethDELETE := svr.makeDecorator("/api/method/DELETE")
	dealMethHEAD := svr.makeDecorator("/api/method/HEAD")
	dealMethOPTIONS := svr.makeDecorator("/api/method/OPTIONS")
	dealMethTRACE := svr.makeDecorator("/api/method/TRACE")
	dealMethCONNECT := svr.makeDecorator("/api/method/CONNECT")
	// 路径匹配方式维度：5 个端点各匹配一种 MatchString
	//  · EXACT      请求 /api/pathtype/exact      == 规则 value /api/pathtype/exact
	//  · REGEX      请求 /api/pathtype/regex/abc  匹配 ^/api/pathtype/regex/.*
	//  · NOT_EQUALS 请求 /api/pathtype/something  != 规则 value /api/pathtype/never_match_anything
	//  · IN         请求 /api/pathtype/in1        ∈ {in1,in2}
	//  · NOT_IN     请求 /api/pathtype/allowed    ∉ {forbidden1,forbidden2}
	dealPathEXACT := svr.makeDecorator("/api/pathtype/exact")
	dealPathREGEX := svr.makeDecorator("/api/pathtype/regex/abc")
	dealPathNOTEQUALS := svr.makeDecorator("/api/pathtype/something")
	dealPathIN := svr.makeDecorator("/api/pathtype/in1")
	dealPathNOTIN := svr.makeDecorator("/api/pathtype/allowed")

	for path, deal := range map[string]model.DecoratorFunction{
		"/echo":      dealEcho,
		"/order":     dealOrder,
		"/info":      dealInfo,
		"/slow":      dealSlow,
		"/forbidden": dealForbidden,
		// 用例 8：协议维度（4 个 protocol 各 1 个端点）
		"/api/protocol/http":   dealProtoHTTP,
		"/api/protocol/dubbo":  dealProtoDUBBO,
		"/api/protocol/grpc":   dealProtoGRPC,
		"/api/protocol/thrift": dealProtoTHRIFT,
		// 用例 9：方法维度（9 个 HTTP method 各 1 个端点）
		"/api/method/GET":     dealMethGET,
		"/api/method/POST":    dealMethPOST,
		"/api/method/PUT":     dealMethPUT,
		"/api/method/PATCH":   dealMethPATCH,
		"/api/method/DELETE":  dealMethDELETE,
		"/api/method/HEAD":    dealMethHEAD,
		"/api/method/OPTIONS": dealMethOPTIONS,
		"/api/method/TRACE":   dealMethTRACE,
		"/api/method/CONNECT": dealMethCONNECT,
		// 用例 10：路径匹配方式维度（5 种 MatchString 各 1 个正向端点）
		"/api/pathtype/exact":     dealPathEXACT,
		"/api/pathtype/regex/abc": dealPathREGEX,
		"/api/pathtype/something": dealPathNOTEQUALS,
		"/api/pathtype/in1":       dealPathIN,
		"/api/pathtype/allowed":   dealPathNOTIN,
	} {
		path := path
		deal := deal
		http.HandleFunc(path, func(rw http.ResponseWriter, r *http.Request) {
			reqID := r.Header.Get(reqIDHeader)
			if reqID == "" {
				reqID = newReqID()
			}
			logIncomingRequest(reqID, r, path)
			// 用 ctx 把 reqID 透传给 deal 内部的下游 HTTP 调用，便于全链路日志串联
			ctx := context.WithValue(context.Background(), reqIDCtxKey{}, reqID)
			ret, abort, err := deal(ctx, nil)
			status, body := writeResult(rw, ret, abort, err)
			logReply(reqID, r.RemoteAddr, status, body)
		})
	}

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
		log.Fatalf("fail to register instance: %v", err)
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
		log.Fatalf("fail to deregister instance: %v", err)
	}
	log.Printf("deregister successfully.")
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
	if len(calleeNamespace) == 0 || len(calleeService) == 0 {
		log.Print("calleeNamespace and calleeService are required")
		return
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

// newReqID 生成 8 字符唯一 ID，作为整条请求处理链的追踪标识。
func newReqID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
	}
	return hex.EncodeToString(b[:])
}

// logIncomingRequest 单行打印收到的请求：reqID、客户端地址、path、method、URL、
// query、headers、body。handler 入口处调用一次即可贯穿全函数日志。
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
		reqID, r.RemoteAddr, method, r.Method, r.URL.String(),
		formatQuery(r.URL.Query()), formatHeaders(r.Header), bodyStr)
}

// logReply 单行打印返回给客户端的响应：reqID、remoteAddr、status、body。
// 在 rw.Write 之前调用，避免 rw.Write 失败后日志与实际不一致。
func logReply(reqID, remoteAddr string, status int, body string) {
	log.Printf("[%s] >>> reply to client %s: status=%d body=%s", reqID, remoteAddr, status, body)
}

// formatHeaders 压缩 http.Header 为单行字符串，便于日志展示。
func formatHeaders(h http.Header) string {
	if len(h) == 0 {
		return ""
	}
	var parts []string
	for k, vs := range h {
		for _, v := range vs {
			parts = append(parts, fmt.Sprintf("%s=%s", k, sanitizeHeaderValue(v)))
		}
	}
	return strings.Join(parts, ",")
}

// formatQuery 压缩 query 参数为单行字符串。
func formatQuery(q map[string][]string) string {
	if len(q) == 0 {
		return ""
	}
	var parts []string
	for k, vs := range q {
		for _, v := range vs {
			parts = append(parts, fmt.Sprintf("%s=%s", k, v))
		}
	}
	return strings.Join(parts, ",")
}

// sanitizeHeaderValue 过滤 header 注入风险字符（CR/LF/空格），避免日志被伪造换行切断。
func sanitizeHeaderValue(v string) string {
	v = strings.ReplaceAll(v, "\r", "")
	v = strings.ReplaceAll(v, "\n", "")
	v = strings.ReplaceAll(v, " ", "_")
	return v
}
