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

// Package main 熔断主调示例（InvokeHandler 手动编排写法）。
//
// 该 demo 展示 MakeInvokeHandler 的用法，与 MakeFunctionDecorator（自动编排）
// 形成对照。二者均覆盖三种熔断级别——服务级 (SERVICE)、接口级 (METHOD)、
// 实例级 (INSTANCE)——但控制粒度不同：
//
// MakeFunctionDecorator（自动编排，见 newCircuitBreakerCaller）：
//
//	将"熔断检查 → 业务调用 → 熔断上报"封装为单一 DecoratorFunction，
//	适合标准同步调用场景。
//
// MakeInvokeHandler（手动编排，本 demo）：
//
//	返回 InvokeHandler，暴露 AcquirePermission / OnSuccess / OnError
//	三个生命周期钩子，由调用方按需编排：
//
//	   handler := MakeInvokeHandler(reqCtx)
//	   pass, aborted, err := handler.AcquirePermission()
//	   // ... 业务调用 ...
//	   handler.OnSuccess(respCtx) 或 handler.OnError(respCtx)
//
//	适用场景：gRPC interceptor、自定义中间件、异步调用模型等
//	需要精细控制"检查时刻"与"上报时刻"的场合。
//
// 暴露的 HTTP 接口（仅作 demo 链路）：
//
//	/echo  ：InvokeHandler 手动编排 + 服务发现 + UpdateServiceCallResult
//	/order ：同上
//	/info  ：同上
//	/slow  ：同上
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

func initArgs() {
	flag.StringVar(&selfNamespace, "selfNamespace", "default", "selfNamespace")
	flag.StringVar(&selfService, "selfService", "CircuitBreakerInvokeHandlerCaller", "selfService")
	flag.BoolVar(&selfRegister, "selfRegister", false, "selfRegister")
	flag.StringVar(&calleeNamespace, "calleeNamespace", "default", "calleeNamespace")
	flag.StringVar(&calleeService, "calleeService", "CircuitBreakerCallee", "calleeService")
	flag.IntVar(&port, "port", 18081, "port")
	flag.StringVar(&token, "token", "", "token")
	flag.StringVar(&configPath, "config", "./polaris.yaml", "path for config file")
	flag.BoolVar(&debug, "debug", false, "是否开启 Polaris SDK debug 日志")
}

// PolarisClient 持有 InvokeHandler 手动编排所需的 SDK 句柄。
type PolarisClient struct {
	provider       polaris.ProviderAPI
	consumer       polaris.ConsumerAPI
	circuitBreaker polaris.CircuitBreakerAPI
	// invokers 存储每个接口路径对应的 InvokeHandler。
	invokers   map[string]model.InvokeHandler
	host       string
	isShutdown bool
	webSvr     *http.Server
}

// commonResponse 业务调用返回值，供 CodeConvert.OnSuccess 从中抽取 retCode。
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

// discoverInstance 从 polaris-go 服务发现中获取一个目标实例，并打印当前所有实例的熔断状态。
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

// printAllInstances 打印当前被调服务下全部实例的熔断状态。
func (svr *PolarisClient) printAllInstances() {
	req := &polaris.GetInstancesRequest{
		GetInstancesRequest: model.GetInstancesRequest{
			Service:   calleeService,
			Namespace: calleeNamespace,
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
// delay 为调用耗时，method 为被调接口路径。
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

// runWebServer 为每个接口路径创建 InvokeHandler，注册到 net/http。
// 每个 handler 内部手动编排熔断生命周期，分为以下四步：
//
//  1. AcquirePermission()              — 调用前检查熔断器是否放行
//  2. 服务发现 + HTTP 调用             — 实际业务逻辑
//  3. ConsumerAPI.UpdateServiceCallResult() — 上报服务调用结果（负载均衡/健康检查）
//  4. OnSuccess() / OnError()          — 上报熔断统计结果（触发熔断状态翻转）
//
// 在 OnSuccess/OnError 中传入 Instance 后，SDK 内部会按以下顺序统一上报：
//   - 服务级 ServiceResource         （Caller→Callee）
//   - 接口级 MethodResource (path)   （RequestContext.Method 非空时）
//   - 实例级 InstanceResource        （ResponseContext.Instance 非 nil 时）
func (svr *PolarisClient) runWebServer() {
	paths := []string{"/echo", "/order", "/info", "/slow", "/forbidden"}
	svr.invokers = make(map[string]model.InvokeHandler, len(paths))

	for _, path := range paths {
		path := path
		// 创建 InvokeHandler：每个 path 独立一个 handler，
		// Method 非空 → 启用接口级熔断。
		handler := svr.circuitBreaker.MakeInvokeHandler(&api.RequestContext{
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
				// Method 非空 → 启用接口级熔断；为空时仅参与服务级/实例级熔断。
				Method: path,
			},
		})
		svr.invokers[path] = handler

		http.HandleFunc(path, func(rw http.ResponseWriter, r *http.Request) {
			log.Printf("[%s] received from %s", path, r.RemoteAddr)
			invoker := svr.invokers[path]

			// ── 第一步：熔断检查 ──
			// 内部按 服务级 → 接口级 顺序检查，任一级熔断打开即短路返回。
			pass, aborted, err := invoker.AcquirePermission()
			if err != nil {
				log.Printf("[%s] AcquirePermission error: %v", path, err)
				rw.WriteHeader(http.StatusInternalServerError)
				_, _ = rw.Write([]byte(fmt.Sprintf("[error] acquire permission fail: %s", err)))
				return
			}
			if !pass {
				// 被熔断拦截：分两种情况
				//   1. 规则配置了 fallback：透传状态码 / headers / body
				//   2. 规则无 fallback（含半开配额耗尽路径）：返回 503 默认响应
				log.Printf("[%s] circuit breaker blocked", path)
				if aborted.HasFallback() {
					rw.WriteHeader(aborted.GetFallbackCode())
					for k, v := range aborted.GetFallbackHeaders() {
						rw.Header().Add(k, v)
					}
					_, _ = rw.Write([]byte(aborted.GetFallbackBody()))
					return
				}
				rw.WriteHeader(http.StatusServiceUnavailable)
				_, _ = rw.Write([]byte("circuit breaker open: " + aborted.GetError().Error()))
				return
			}

			// ── 第二步：服务发现 ──
			instance, err := svr.discoverInstance()
			if err != nil {
				log.Printf("[%s] discoverInstance error: %v", path, err)
				rw.WriteHeader(http.StatusInternalServerError)
				_, _ = rw.Write([]byte(fmt.Sprintf("[error] discover instance fail: %s", err)))
				return
			}

			// ── 第三步：发起 HTTP 调用 ──
			url := fmt.Sprintf("http://%s:%d%s", instance.GetHost(), instance.GetPort(), path)
			start := time.Now()
			resp, httpErr := http.Get(url)
			delay := time.Since(start)

			if resp != nil {
				defer resp.Body.Close()
			}

			if httpErr != nil {
				log.Printf("[%s] http call error: %v", path, httpErr)
				// 上报服务调用结果
				svr.reportServiceCallResult(instance, model.RetFail, http.StatusInternalServerError, delay, path)
				// ── 第四步：上报熔断结果（失败）──
				// 在 ResponseContext.Instance 中传入实际命中的实例，
				// SDK 会据此触发实例级熔断统计。
				invoker.OnError(&model.ResponseContext{
					Duration: delay,
					Err:      httpErr,
					Instance: instance,
				})
				rw.WriteHeader(http.StatusInternalServerError)
				_, _ = rw.Write([]byte(fmt.Sprintf("[error] http request fail: %s", httpErr)))
				return
			}

			data, _ := io.ReadAll(resp.Body)
			// LB / 健康检查只把 5xx 当作"实例侧故障"，4xx 一律视为成功（请求侧问题）
			retStatus := model.RetSuccess
			if resp.StatusCode >= http.StatusInternalServerError {
				retStatus = model.RetFail
			}
			svr.reportServiceCallResult(instance, retStatus, resp.StatusCode, delay, path)

			// ── 第四步：按状态码区分走 OnError / OnSuccess ──
			// 5xx：走 OnError → SDK 内部用 retCode="-1" 哨兵触发熔断（无需依赖具体规则）
			// 4xx / 2xx：走 OnSuccess → retCode 透传真实状态码，由用户配置的 ErrorConditions 决定
			if resp.StatusCode >= http.StatusInternalServerError {
				invoker.OnError(&model.ResponseContext{
					Duration: delay,
					Err: fmt.Errorf("upstream %s:%d returned %d",
						instance.GetHost(), instance.GetPort(), resp.StatusCode),
					Instance: instance,
				})
			} else {
				// Result 字段传入 commonResponse，供 CodeConvert.OnSuccess
				// 抽取 retCode；SDK 据此匹配 ErrorCondition.RET_CODE 规则。
				invoker.OnSuccess(&model.ResponseContext{
					Duration: delay,
					Result: commonResponse{
						code: resp.StatusCode,
						body: string(data),
					},
					Instance: instance,
				})
			}

			rw.WriteHeader(resp.StatusCode)
			_, _ = rw.Write(data)
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
