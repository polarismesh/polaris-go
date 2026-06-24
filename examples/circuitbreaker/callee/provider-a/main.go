/**
 * Tencent is pleased to support the open source community by making polaris-go available.
 *
 * Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
 *
 * Licensed under the BSD 3-Clause License (the "License");
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
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/polarismesh/polaris-go/pkg/config"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/api"
)

var (
	namespace  string
	service    string
	token      string
	port       int64
	configPath string
	debug      bool
	// tcpProbePort/udpProbePort：独立的 TCP/UDP 主动探测端口（0=不启动）。
	// 用于熔断主动探测验证：HTTP 的 /switch?openError 只改 HTTP 响应不关端口，TCP/UDP 探测对它不敏感，
	// 故另起受 openTcpError/openUdpError 开关控制的探测端口，使 TCP/UDP 探测也能随故障变化。
	tcpProbePort int64
	udpProbePort int64
)

// reqIDHeader 全链路追踪请求 ID，贯穿所有中间跳。
// 入口优先从 header 读；没有则本地生成；向下游发请求时显式注入。
const reqIDHeader = "X-Request-ID"

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "CircuitBreakerCallee", "service")
	// 当北极星开启鉴权时，需要配置此参数完成相关的权限检查
	flag.StringVar(&token, "token", "", "token")
	flag.Int64Var(&port, "port", 0, "port")
	flag.StringVar(&configPath, "config", "./polaris.yaml", "path for config file")
	flag.BoolVar(&debug, "debug", false, "是否开启 Polaris SDK debug 日志")
	flag.Int64Var(&tcpProbePort, "tcp-probe-port", 0, "独立 TCP 主动探测端口（0=不启动）")
	flag.Int64Var(&udpProbePort, "udp-probe-port", 0, "独立 UDP 主动探测端口（0=不启动）")
}

// PolarisProvider is a provider for polaris
type PolarisProvider struct {
	provider  polaris.ProviderAPI
	namespace string
	service   string
	host      string
	port      int
	// /echo 的失败开关（保持向后兼容）。1=返回 500，0=返回 200。
	needErr int32
	// /order 的失败开关，用于接口级熔断 demo 中验证"两个接口各自规则生效"。
	// 1=返回 500，0=返回 200。默认与 needErr 一致。
	needErrOrder int32
	// /slow 接口的人为延迟（毫秒）。0=立即返回；>0 时 sleep 该时长后再返回 200，
	// 用于"时延（DELAY）触发熔断"用例。
	slowDelayMs int64
	isShutdown  bool
	webSvr      *http.Server
	// TCP/UDP 主动探测端口的故障开关（1=探测失败/不回包，0=正常回包）。
	needTcpErr int32
	needUdpErr int32
	tcpLn      net.Listener
	udpConn    *net.UDPConn
}

// Run . execute
func (svr *PolarisProvider) Run() {
	tmpHost, err := getLocalHost(svr.provider.SDKContext().GetConfig().GetGlobal().GetServerConnector().GetAddresses()[0])
	if err != nil {
		panic(fmt.Errorf("error occur while fetching localhost: %v", err))
	}

	svr.host = tmpHost
	svr.runWebServer()
	svr.runTCPProbeServer()
	svr.runUDPProbeServer()
	svr.registerService()
}

// runTCPProbeServer 启动独立 TCP 主动探测端口（tcpProbePort>0 时）。
// 探测器连上后发送 send 数据，本 server 在 needTcpErr=0 时回 "tcp-ok"，=1 时直接关闭连接不回包
// （探测端读到空数据 ≠ 期望的 receive，判定探测失败），以此模拟"实例 TCP 探测故障"。
func (svr *PolarisProvider) runTCPProbeServer() {
	if tcpProbePort <= 0 {
		return
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", tcpProbePort))
	if err != nil {
		log.Fatalf("[ERROR] fail to listen tcp probe port %d, err is %v", tcpProbePort, err)
	}
	svr.tcpLn = ln
	log.Printf("[INFO] start tcp probe server, listen port is %d", tcpProbePort)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				if svr.isShutdown {
					return
				}
				continue
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				buf := make([]byte, 256)
				_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
				_, _ = c.Read(buf)
				if atomic.LoadInt32(&svr.needTcpErr) == 1 {
					// 故障：不回包，直接关闭
					return
				}
				_, _ = c.Write([]byte("tcp-ok"))
			}(conn)
		}
	}()
}

// runUDPProbeServer 启动独立 UDP 主动探测端口（udpProbePort>0 时）。
// 探测器发来 send 数据，本 server 在 needUdpErr=0 时回 "udp-ok"，=1 时不回包（探测端读超时拿空 ≠
// 期望的 receive，判定探测失败），以此模拟"实例 UDP 探测故障"。
func (svr *PolarisProvider) runUDPProbeServer() {
	if udpProbePort <= 0 {
		return
	}
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("0.0.0.0:%d", udpProbePort))
	if err != nil {
		log.Fatalf("[ERROR] fail to resolve udp probe addr %d, err is %v", udpProbePort, err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		log.Fatalf("[ERROR] fail to listen udp probe port %d, err is %v", udpProbePort, err)
	}
	svr.udpConn = conn
	log.Printf("[INFO] start udp probe server, listen port is %d", udpProbePort)
	go func() {
		buf := make([]byte, 256)
		for {
			n, remote, err := conn.ReadFromUDP(buf)
			if err != nil {
				if svr.isShutdown {
					return
				}
				continue
			}
			_ = n
			if atomic.LoadInt32(&svr.needUdpErr) == 1 {
				// 故障：不回包
				continue
			}
			_, _ = conn.WriteToUDP([]byte("udp-ok"), remote)
		}
	}()
}

func (svr *PolarisProvider) runWebServer() {
	http.HandleFunc("/echo", func(rw http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get(reqIDHeader)
		if reqID == "" {
			reqID = newReqID()
		}
		logIncomingRequest(reqID, r, "/echo")
		var msg string
		var status int
		if atomic.LoadInt32(&svr.needErr) == 1 {
			msg = fmt.Sprintf("status code: 500, Fatal, My host : %s:%d", svr.host, svr.port)
			status = http.StatusInternalServerError
		} else {
			msg = fmt.Sprintf("status code: 200, Hello, My host : %s:%d", svr.host, svr.port)
			status = http.StatusOK
		}
		rw.WriteHeader(status)
		logReply(reqID, r.RemoteAddr, status, msg)
		_, _ = rw.Write([]byte(msg))
	})

	// /order：用于接口级熔断 demo 验证"两个接口各自规则生效"。
	// 与 /echo 共享 Provider 进程，但故障开关独立（needErrOrder）。
	http.HandleFunc("/order", func(rw http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get(reqIDHeader)
		if reqID == "" {
			reqID = newReqID()
		}
		logIncomingRequest(reqID, r, "/order")
		var msg string
		var status int
		if atomic.LoadInt32(&svr.needErrOrder) == 1 {
			msg = fmt.Sprintf("status code: 500, Fatal, My host : %s:%d, path: /order", svr.host, svr.port)
			status = http.StatusInternalServerError
		} else {
			msg = fmt.Sprintf("status code: 200, Hello, My host : %s:%d, path: /order", svr.host, svr.port)
			status = http.StatusOK
		}
		rw.WriteHeader(status)
		logReply(reqID, r.RemoteAddr, status, msg)
		_, _ = rw.Write([]byte(msg))
	})

	// /info：固定返回 500，用于验证"没有配置规则的接口不会被熔断"。
	// 没有任何故障开关，由 verify_circuitbreaker.sh 在测试时通过不创建对应规则
	// + 在 consumer 侧禁用默认实例熔断来验证该路径永远不会被 abort。
	http.HandleFunc("/info", func(rw http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get(reqIDHeader)
		if reqID == "" {
			reqID = newReqID()
		}
		logIncomingRequest(reqID, r, "/info")
		msg := fmt.Sprintf("status code: 500, Fatal, My host : %s:%d, path: /info", svr.host, svr.port)
		rw.WriteHeader(http.StatusInternalServerError)
		logReply(reqID, r.RemoteAddr, http.StatusInternalServerError, msg)
		_, _ = rw.Write([]byte(msg))
	})

	// /forbidden：固定返回 403，用于验证"4xx 路径不被默认 RANGE 500~599 规则计入熔断"。
	// 没有故障开关，与 /info 镜像但状态码改为 4xx；
	// verify_circuitbreaker.sh 据此连发请求验证：4xx 透传后规则不命中、不触发熔断。
	http.HandleFunc("/forbidden", func(rw http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get(reqIDHeader)
		if reqID == "" {
			reqID = newReqID()
		}
		logIncomingRequest(reqID, r, "/forbidden")
		msg := fmt.Sprintf("status code: 403, Forbidden, My host : %s:%d, path: /forbidden",
			svr.host, svr.port)
		rw.WriteHeader(http.StatusForbidden)
		logReply(reqID, r.RemoteAddr, http.StatusForbidden, msg)
		_, _ = rw.Write([]byte(msg))
	})

	// /slow：根据 svr.slowDelayMs 在返回前先 sleep 指定毫秒数，全部返回 200。
	// 用于演示 ErrorCondition.input_type=DELAY 时的熔断：把规则阈值设到 sleep
	// 时长以下，调用就会被 SDK 计为 RetTimeout，从而触发熔断。
	http.HandleFunc("/slow", func(rw http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get(reqIDHeader)
		if reqID == "" {
			reqID = newReqID()
		}
		logIncomingRequest(reqID, r, "/slow")
		delay := atomic.LoadInt64(&svr.slowDelayMs)
		if delay > 0 {
			time.Sleep(time.Duration(delay) * time.Millisecond)
		}
		msg := fmt.Sprintf("status code: 200, Hello, My host : %s:%d, path: /slow, delay: %dms",
			svr.host, svr.port, delay)
		rw.WriteHeader(http.StatusOK)
		logReply(reqID, r.RemoteAddr, http.StatusOK, msg)
		_, _ = rw.Write([]byte(msg))
	})

	http.HandleFunc("/switch", func(rw http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get(reqIDHeader)
		if reqID == "" {
			reqID = newReqID()
		}
		logIncomingRequest(reqID, r, "/switch")
		// /switch 支持以下查询参数（可任选其一或多个组合）：
		//   openError       —— 翻转 /echo 的故障开关（true=500/false=200）
		//   openErrorOrder  —— 翻转 /order 的故障开关
		//   slowDelayMs     —— 设置 /slow 的人为延迟（整数毫秒；0 表示立即返回）
		// 兼容历史：未传的字段保持原状不动。
		var parts []string
		if val := r.URL.Query().Get("openError"); val != "" {
			if val == "true" {
				atomic.StoreInt32(&svr.needErr, 1)
				parts = append(parts, "echo=500")
			} else {
				atomic.StoreInt32(&svr.needErr, 0)
				parts = append(parts, "echo=200")
			}
		}
		if val := r.URL.Query().Get("openErrorOrder"); val != "" {
			if val == "true" {
				atomic.StoreInt32(&svr.needErrOrder, 1)
				parts = append(parts, "order=500")
			} else {
				atomic.StoreInt32(&svr.needErrOrder, 0)
				parts = append(parts, "order=200")
			}
		}
		if val := r.URL.Query().Get("openTcpError"); val != "" {
			if val == "true" {
				atomic.StoreInt32(&svr.needTcpErr, 1)
				parts = append(parts, "tcp=fail")
			} else {
				atomic.StoreInt32(&svr.needTcpErr, 0)
				parts = append(parts, "tcp=ok")
			}
		}
		if val := r.URL.Query().Get("openUdpError"); val != "" {
			if val == "true" {
				atomic.StoreInt32(&svr.needUdpErr, 1)
				parts = append(parts, "udp=fail")
			} else {
				atomic.StoreInt32(&svr.needUdpErr, 0)
				parts = append(parts, "udp=ok")
			}
		}
		if val := r.URL.Query().Get("slowDelayMs"); val != "" {
			if ms, err := strconv.ParseInt(val, 10, 64); err == nil && ms >= 0 {
				atomic.StoreInt64(&svr.slowDelayMs, ms)
				parts = append(parts, fmt.Sprintf("slow=%dms", ms))
			} else {
				parts = append(parts, fmt.Sprintf("slow=invalid(%s)", val))
			}
		}
		msg := fmt.Sprintf("switch updated: %s", strings.Join(parts, ","))
		rw.WriteHeader(http.StatusOK)
		logReply(reqID, r.RemoteAddr, http.StatusOK, msg)
		_, _ = rw.Write([]byte(msg))
	})

	http.HandleFunc("/health", func(rw http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get(reqIDHeader)
		if reqID == "" {
			reqID = newReqID()
		}
		logIncomingRequest(reqID, r, "/health")
		msg := fmt.Sprintf("health status:up, My host : %s:%d", svr.host, svr.port)
		rw.WriteHeader(http.StatusOK)
		logReply(reqID, r.RemoteAddr, http.StatusOK, msg)
		_, _ = rw.Write([]byte(msg))
	})

	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		log.Fatalf("[ERROR]fail to listen tcp, err is %v", err)
	}

	svr.port = ln.Addr().(*net.TCPAddr).Port

	// /api/* 用例 8/9/10 演示端点：catch-all 路由统一处理
	// 与 /echo 共享 needErr 开关（不区分 path），返回 200/500
	// 行为，便于 case_protocol/case_method/case_pathtype 三个 case 通过
	// provider_set_error 翻转错误状态来验证 SDK 熔断行为。
	http.HandleFunc("/api/", func(rw http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get(reqIDHeader)
		if reqID == "" {
			reqID = newReqID()
		}
		logIncomingRequest(reqID, r, r.URL.Path)
		var msg string
		var status int
		if atomic.LoadInt32(&svr.needErr) == 1 {
			msg = fmt.Sprintf("status code: 500, Fatal, path=%s, host=%s:%d", r.URL.Path, svr.host, svr.port)
			status = http.StatusInternalServerError
		} else {
			msg = fmt.Sprintf("status code: 200, Hello, path=%s, host=%s:%d", r.URL.Path, svr.host, svr.port)
			status = http.StatusOK
		}
		rw.WriteHeader(status)
		logReply(reqID, r.RemoteAddr, status, msg)
		_, _ = rw.Write([]byte(msg))
	})

	go func() {
		log.Printf("[INFO] start http server, listen port is %v", svr.port)
		svr.webSvr = &http.Server{Handler: nil}
		if err := svr.webSvr.Serve(ln); err != nil {
			svr.isShutdown = false
			log.Fatalf("[ERROR]fail to run webServer, err is %v", err)
		}
	}()
}

// serviceList 解析 --service 参数：支持逗号分隔的多个服务名，本实例会同时注册到每一个服务。
// 用于让同一个 provider 进程为多个被调服务提供实例（例如熔断主动探测验证中 SERVICE/METHOD/INSTANCE
// 三级用例各用独立被调服务，但共用同一组 provider 进程）。
func serviceList() []string {
	var out []string
	for _, s := range strings.Split(service, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		out = append(out, service)
	}
	return out
}

func (svr *PolarisProvider) registerService() {
	log.Printf("start to invoke register operation")
	for _, svc := range serviceList() {
		registerRequest := &polaris.InstanceRegisterRequest{}
		registerRequest.Service = svc
		registerRequest.Namespace = namespace
		registerRequest.Host = svr.host
		registerRequest.Port = svr.port
		registerRequest.ServiceToken = token
		resp, err := svr.provider.RegisterInstance(registerRequest)
		if err != nil {
			log.Fatalf("fail to register instance for service %s, err is %v", svc, err)
		}
		log.Printf("register response: service %s instanceId %s", svc, resp.InstanceID)
	}
}

func (svr *PolarisProvider) deregisterService() {
	log.Printf("start to invoke deregister operation")
	for _, svc := range serviceList() {
		deregisterRequest := &polaris.InstanceDeRegisterRequest{}
		deregisterRequest.Service = svc
		deregisterRequest.Namespace = namespace
		deregisterRequest.Host = svr.host
		deregisterRequest.Port = svr.port
		deregisterRequest.ServiceToken = token
		if err := svr.provider.Deregister(deregisterRequest); err != nil {
			log.Fatalf("fail to deregister instance for service %s, err is %v", svc, err)
		}
		log.Printf("deregister successfully for service %s", svc)
	}
}

func (svr *PolarisProvider) runMainLoop() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, []os.Signal{
		syscall.SIGINT, syscall.SIGTERM,
		syscall.SIGSEGV,
	}...)

	for s := range ch {
		log.Printf("catch signal(%+v), stop servers", s)
		svr.isShutdown = true
		svr.deregisterService()
		_ = svr.webSvr.Close()
		if svr.tcpLn != nil {
			_ = svr.tcpLn.Close()
		}
		if svr.udpConn != nil {
			_ = svr.udpConn.Close()
		}
		return
	}
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
	if len(namespace) == 0 || len(service) == 0 {
		log.Print("namespace and service are required")
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
	// 或者使用以下方法,则不需要创建配置文件
	//provider, err = polaris.NewProviderAPIByAddress("127.0.0.1:8091")
	//if err != nil {
	//	log.Fatalf("fail to create providerAPI, err is %v", err)
	//}
	defer provider.Destroy()

	svr := &PolarisProvider{
		provider:  provider,
		namespace: namespace,
		service:   service,
		// provider-a 默认 200，provider-b 默认 500（由各自 main.go 初始化值控制）
		needErr:      0,
		needErrOrder: 0,
		slowDelayMs:  0,
	}

	svr.Run()

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
