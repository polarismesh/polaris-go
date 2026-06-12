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
	cryptorand "crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math/rand"
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

func initArgs() {
	flag.StringVar(&selfNamespace, "selfNamespace", "default", "selfNamespace")
	flag.StringVar(&selfService, "selfService", "CircuitBreakerInstanceCaller", "selfService")
	flag.BoolVar(&selfRegister, "selfRegister", true, "selfRegister")
	flag.StringVar(&calleeNamespace, "calleeNamespace", "default", "calleeNamespace")
	flag.StringVar(&calleeService, "calleeService", "CircuitBreakerCallee", "calleeService")
	flag.IntVar(&port, "port", 18080, "port")
	flag.StringVar(&token, "token", "", "token")
	flag.StringVar(&configPath, "config", "./polaris.yaml", "path for config file")
	flag.BoolVar(&debug, "debug", false, "debug")
}

// PolarisClient is a consumer of the circuit breaker calleeService.
type PolarisClient struct {
	provider       polaris.ProviderAPI
	host           string
	isShutdown     bool
	consumer       polaris.ConsumerAPI
	circuitBreaker polaris.CircuitBreakerAPI
	webSvr         *http.Server
}

// reportServiceCallResult 上报服务调用结果的辅助方法
func (svr *PolarisClient) reportServiceCallResult(instance model.Instance, retStatus model.RetStatus, statusCode int, delay time.Duration) {
	ret := &polaris.ServiceCallResult{
		ServiceCallResult: model.ServiceCallResult{
			EmptyInstanceGauge: model.EmptyInstanceGauge{},
			CalledInstance:     instance,
			Method:             "/echo",
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

func (svr *PolarisClient) discoverInstance() (model.Instance, error) {
	svr.printAllInstances()
	getOneRequest := &polaris.GetOneInstanceRequest{}
	getOneRequest.Namespace = calleeNamespace
	getOneRequest.Service = calleeService
	oneInstResp, err := svr.consumer.GetOneInstance(getOneRequest)
	if err != nil {
		log.Printf("[error] fail to getOneInstance, err is %v", err)
		return nil, err
	}
	instance := oneInstResp.GetInstance()
	if instance == nil {
		log.Printf("[error] fail to getOneInstance, instance is nil")
		return nil, fmt.Errorf("Consumer.GetOneInstance empty")
	}
	log.Printf("getOneInstance is %s:%d, ishealthy:%v", instance.GetHost(),
		instance.GetPort(), instance.IsHealthy())
	return instance, nil
}

func (svr *PolarisClient) runWebServer() {
	http.HandleFunc("/echo", func(rw http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get(reqIDHeader)
		if reqID == "" {
			reqID = newReqID()
		}
		logIncomingRequest(reqID, r, "/echo")
		log.Printf("[%s] start to invoke getOneInstance operation", reqID)
		instance, err := svr.discoverInstance()
		if err != nil || instance == nil {
			rw.WriteHeader(http.StatusInternalServerError)
			instanceIsNil := instance == nil
			msg := fmt.Sprintf("fail to getOneInstance, err is %v, instance is nil:%v", err, instanceIsNil)
			_, _ = rw.Write([]byte(fmt.Sprintf("[error] discover instance fail : %s", msg)))
			logReply(reqID, r.RemoteAddr, http.StatusInternalServerError, msg)
			return
		}
		start := time.Now()
		upstreamReq, httpErr := http.NewRequestWithContext(r.Context(), http.MethodGet,
			fmt.Sprintf("http://%s:%d/echo", instance.GetHost(), instance.GetPort()), nil)
		if httpErr != nil {
			log.Printf("[%s] build upstream req fail: %s", reqID, httpErr)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[error] build upstream req fail : %s", httpErr)))
			logReply(reqID, r.RemoteAddr, http.StatusInternalServerError, httpErr.Error())
			return
		}
		// 显式注入 reqID，让下游 provider 能在日志中串联同一条请求的处理链
		upstreamReq.Header.Set(reqIDHeader, reqID)
		resp, err := http.DefaultClient.Do(upstreamReq)

		if err != nil {
			log.Printf("[%s] [error] send request to %s:%d fail : %s", reqID,
				instance.GetHost(), instance.GetPort(), err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[error] send request to %s:%d fail : %s",
				instance.GetHost(), instance.GetPort(), err)))
			logReply(reqID, r.RemoteAddr, http.StatusInternalServerError, err.Error())

			time.Sleep(time.Millisecond * time.Duration(rand.Intn(10)))

			// 上报服务调用结果（LB / 健康检查路径）
			delay := time.Since(start)
			svr.reportServiceCallResult(instance, model.RetFail, http.StatusInternalServerError, delay)

			// 上报熔断结果（熔断器路径）：使用 SDK 内部约定的 "-1" 哨兵值，
			// SDK 端 block_counter.parseRetStatus 会在挂有 RET_CODE 类条件的块中
			// 直接将 -1 计为 RetFail，无需依赖具体规则配置。
			svr.reportCircuitBreak(instance, model.RetFail, "-1", start)
			return
		}
		time.Sleep(time.Millisecond * time.Duration(rand.Intn(10)))

		defer resp.Body.Close()

		// 上报服务调用结果（LB / 健康检查路径）
		// 只把 5xx 当作"实例侧故障"，4xx（参数 / 鉴权类）不应让 LB 降低实例权重
		// 或健康检查摘除实例。429 仍按 RetFlowControl 表达限流路径。
		// 与熔断器路径独立：熔断由后续 reportCircuitBreak 携带的 retCode + 规则决定。
		delay := time.Since(start)
		retStatus := model.RetSuccess
		if resp.StatusCode == http.StatusTooManyRequests {
			retStatus = model.RetFlowControl
		} else if resp.StatusCode >= http.StatusInternalServerError {
			retStatus = model.RetFail
		}
		svr.reportServiceCallResult(instance, retStatus, resp.StatusCode, delay)

		// 上报熔断结果（熔断器路径）：按 5xx / 4xx / 2xx 三类分别上报，
		// 让"区分 4xx/5xx"语义与统一装饰器写法的 demo 对齐：
		//   - 5xx：RetFail + 真实状态码 → 命中规则的 RANGE/EXACT/REGEX/IN/NOT_IN 类
		//   - 4xx：RetSuccess + 真实状态码 → 不计入熔断（用户可显式配 IN [400,500] 纳入）
		//   - 2xx：RetSuccess + 真实状态码 → 规则不命中，计为成功
		// 实际是否计入熔断完全由用户配置的 BlockConfig.ErrorConditions 决定。
		switch {
		case resp.StatusCode >= http.StatusInternalServerError:
			svr.reportCircuitBreak(instance, model.RetFail,
				strconv.Itoa(resp.StatusCode), start)
		default:
			svr.reportCircuitBreak(instance, model.RetSuccess,
				strconv.Itoa(resp.StatusCode), start)
		}

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("[%s] [error] read resp from %s:%d fail : %s", reqID,
				instance.GetHost(), instance.GetPort(), err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[error] read resp from %s:%d fail : %s",
				instance.GetHost(), instance.GetPort(), err)))
			logReply(reqID, r.RemoteAddr, http.StatusInternalServerError, err.Error())
			return
		}
		log.Printf("[%s] echo success, resp: %+v", reqID, string(data))
		rw.WriteHeader(http.StatusOK)
		logReply(reqID, r.RemoteAddr, http.StatusOK, string(data))
		_, _ = rw.Write(data)
	})

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
		log.Printf("[error] fail to getInstances for request [%+v], err is %v\n", req, err)
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

func (svr *PolarisClient) reportCircuitBreak(instance model.Instance, status model.RetStatus,
	retCode string, start time.Time) {
	insRes, err := model.NewInstanceResource(&model.ServiceKey{
		Namespace: calleeNamespace,
		Service:   calleeService,
	}, &model.ServiceKey{
		Namespace: selfNamespace,
		Service:   selfService,
	}, "http", instance.GetHost(), instance.GetPort())
	if err != nil {
		log.Printf("[error] fail to createInstanceResource, err is %v", err)
		return
	}
	log.Printf("report circuitBreaker [%v] for instance %s/%s:%d "+
		"caller [%s.%s] "+
		"delay [%v] "+
		"circuitBreaker status [%v]\n",
		status,
		instance.GetService(),
		instance.GetHost(), instance.GetPort(),
		selfNamespace, selfService, time.Since(start),
		instance.GetCircuitBreakerStatus())

	if err := svr.circuitBreaker.Report(&model.ResourceStat{
		Delay:     time.Since(start),
		RetStatus: status,
		RetCode:   retCode,
		Resource:  insRes,
	}); err != nil {
		log.Printf("report circuitbreak for service %v failed: %v",
			insRes, err)
	}
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
		log.Fatalf("fail to register instance, err is %v", err)
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
		log.Fatalf("fail to deregister instance, err is %v", err)
	}
	log.Printf("deregister successfully.")
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
	initArgs()
	flag.Parse()
	if len(calleeNamespace) == 0 || len(calleeService) == 0 {
		log.Print("calleeNamespace and calleeService are required")
		return
	}
	if debug {
		// 设置日志级别为DEBUG
		if err := api.SetLoggersLevel(api.DebugLog); err != nil {
			log.Printf("fail to set log level to DEBUG, err is %v", err)
		} else {
			log.Printf("successfully set log level to DEBUG")
		}
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
	if _, err := cryptorand.Read(b[:]); err != nil {
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
