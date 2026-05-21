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
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/api"
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

// logIncomingRequest 单行打印收到的请求：方法/URL/query/headers/body，都带 reqID 前缀.
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

var (
	namespace string
	service   string
	token     string
	port      int64
	debug     bool
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "QpsRatelimitEchoServer", "service")
	// 当北极星开启鉴权时，需要配置此参数完成相关的权限检查
	flag.StringVar(&token, "token", "", "token")
	flag.Int64Var(&port, "port", 0, "port")
	flag.BoolVar(&debug, "debug", false, "是否开启 Polaris SDK debug 日志")
}

// PolarisProvider implements the Provider interface.
type PolarisProvider struct {
	provider  polaris.ProviderAPI
	limiter   polaris.LimitAPI
	namespace string
	service   string
	host      string
	port      int
}

// Run . execute
func (svr *PolarisProvider) Run() {
	tmpHost, err := getLocalHost(svr.provider.SDKContext().GetConfig().GetGlobal().GetServerConnector().GetAddresses()[0])
	if err != nil {
		panic(fmt.Errorf("error occur while fetching localhost: %v", err))
	}

	svr.host = tmpHost
	svr.runWebServer()
	svr.registerService()
}

func (svr *PolarisProvider) runWebServer() {
	// /echo + /health 共用同一套限流处理：构建 quotaReq → GetQuota → 处理结果.
	// 抽 helper 后，新增匹配维度（如 path / caller-*）只需改一处 buildQuotaRequest.
	for _, p := range []string{"/echo", "/health"} {
		path := p
		http.HandleFunc(path, func(rw http.ResponseWriter, r *http.Request) {
			svr.handleQuota(rw, r, path)
		})
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		log.Fatalf("[ERROR]fail to listen tcp, err is %v", err)
	}

	svr.port = ln.Addr().(*net.TCPAddr).Port

	go func() {
		log.Printf("[INFO] start http server, listen port is %v", svr.port)
		if err := http.Serve(ln, nil); err != nil {
			log.Fatalf("[ERROR]fail to run webServer, err is %v", err)
		}
	}()
}

// handleQuota 处理一次限流判定 + 业务返回；适用于 /echo 和 /health（路径不同，但限流逻辑一致）.
func (svr *PolarisProvider) handleQuota(rw http.ResponseWriter, r *http.Request, method string) {
	reqID := newReqID()
	logIncomingRequest(reqID, r, method)

	quotaReq := buildQuotaRequest(r, namespace, service, method)

	start := time.Now()
	resp, err := svr.limiter.GetQuota(quotaReq)
	if err != nil {
		body := fmt.Sprintf("[error] fail to GetQuota, err is %v", err)
		log.Printf("[%s] [error] fail to GetQuota: %v", reqID, err)
		rw.WriteHeader(http.StatusInternalServerError)
		_, _ = rw.Write([]byte(body))
		logReply(reqID, r.RemoteAddr, http.StatusInternalServerError, body)
		return
	}
	defer resp.Release()
	log.Printf("[%s] limiter resp | cost=%s code=%d info=%s | quota_req: ns=%s svc=%s method=%v labels=%v",
		reqID, time.Since(start).String(), resp.Get().Code, resp.Get().Info,
		quotaReq.GetNamespace(), quotaReq.GetService(), quotaReq.GetMethod(), quotaReq.GetLabels())

	if resp.Get().Code != model.QuotaResultOk {
		body := http.StatusText(http.StatusTooManyRequests)
		rw.WriteHeader(http.StatusTooManyRequests)
		_, _ = rw.Write([]byte(body))
		logReply(reqID, r.RemoteAddr, http.StatusTooManyRequests, body)
		return
	}

	body := fmt.Sprintf("Hello, I'm QpsRatelimitEchoServer Provider, My host : %s:%d", svr.host, svr.port)
	rw.WriteHeader(http.StatusOK)
	_, _ = rw.Write([]byte(body))
	logReply(reqID, r.RemoteAddr, http.StatusOK, body)
}

func (svr *PolarisProvider) registerService() {
	log.Printf("start to invoke register operation")
	registerRequest := &polaris.InstanceRegisterRequest{}
	registerRequest.Service = service
	registerRequest.Namespace = namespace
	registerRequest.Host = svr.host
	registerRequest.Port = svr.port
	registerRequest.ServiceToken = token
	resp, err := svr.provider.RegisterInstance(registerRequest)
	if err != nil {
		log.Fatalf("fail to register instance, err is %v", err)
	}
	log.Printf("register response: instanceId %s", resp.InstanceID)
}

func (svr *PolarisProvider) deregisterService() {
	log.Printf("start to invoke deregister operation")
	deregisterRequest := &polaris.InstanceDeRegisterRequest{}
	deregisterRequest.Service = service
	deregisterRequest.Namespace = namespace
	deregisterRequest.Host = svr.host
	deregisterRequest.Port = svr.port
	deregisterRequest.ServiceToken = token
	if err := svr.provider.Deregister(deregisterRequest); err != nil {
		log.Fatalf("fail to deregister instance, err is %v", err)
	}
	log.Printf("deregister successfully.")
}

func (svr *PolarisProvider) runMainLoop() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, []os.Signal{
		syscall.SIGINT, syscall.SIGTERM,
		syscall.SIGSEGV,
	}...)

	for s := range ch {
		log.Printf("catch signal(%+v), stop servers", s)
		svr.deregisterService()
		return
	}
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	initArgs()
	flag.Parse()
	if len(namespace) == 0 || len(service) == 0 {
		log.Print("namespace and service are required")
		return
	}
	if debug {
		if err := api.SetLoggersLevel(api.DebugLog); err != nil {
			log.Printf("[WARN] 设置日志级别为 DEBUG 失败: %v", err)
		} else {
			log.Printf("[INFO] 已设置 Polaris SDK 日志级别为 DEBUG")
		}
	}
	provider, err := polaris.NewProviderAPI()
	// 或者使用以下方法,则不需要创建配置文件
	// provider, err = polaris.NewProviderAPIByAddress("127.0.0.1:8091")

	if err != nil {
		log.Fatalf("fail to create consumerAPI, err is %v", err)
	}

	limit := polaris.NewLimitAPIByContext(provider.SDKContext())

	defer func() {
		provider.Destroy()
		limit.Destroy()
	}()

	svr := &PolarisProvider{
		provider:  provider,
		limiter:   limit,
		namespace: namespace,
		service:   service,
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

func convertHeaders(header map[string][]string) map[string]string {
	meta := make(map[string]string)
	for k, v := range header {
		meta[strings.ToLower(k)] = v[0]
	}
	return meta
}

// X-Polaris-Caller-* header 命名前缀；与 consumer 端的 injectCallerHeaders 一一对应.
const (
	headerCallerService    = "X-Polaris-Caller-Service"
	headerCallerIP         = "X-Polaris-Caller-IP"
	headerCallerMetaPrefix = "X-Polaris-Caller-Metadata-" // 后接 key
)

// buildQuotaRequest 把 HTTP 请求的所有可匹配维度塞进 quota 请求，让 polaris 限流规则能命中：
//
//   - METHOD          : 请求 path（quotaReq.SetMethod / SDK 内部按 ArgumentTypeMethod 转 MatchArgument_METHOD）
//   - HEADER          : 所有请求头（X-Polaris-Caller-* 系列除外，避免污染 HEADER 维度）
//   - QUERY           : URL 查询参数
//   - CALLER_SERVICE  : X-Polaris-Caller-Service header
//   - CALLER_IP       : X-Polaris-Caller-IP header
//   - CALLER_METADATA : X-Polaris-Caller-Metadata-<key> header
//
// polaris 规则的多个 arguments 之间是 AND 关系——任一维度不匹配就跳过该规则.
func buildQuotaRequest(r *http.Request, ns, svc, method string) *model.QuotaRequestImpl {
	quotaReq := polaris.NewQuotaRequest().(*model.QuotaRequestImpl)
	quotaReq.SetNamespace(ns)
	quotaReq.SetService(svc)
	quotaReq.SetMethod(method)

	// HEADER：跳过 X-Polaris-Caller-* 系列，避免它们既被当 caller 维度又被当 header 维度
	for k, v := range r.Header {
		if len(v) == 0 {
			continue
		}
		lk := strings.ToLower(k)
		if strings.HasPrefix(strings.ToLower(k), strings.ToLower("X-Polaris-Caller")) {
			continue
		}
		quotaReq.AddArgument(model.BuildHeaderArgument(lk, v[0]))
	}

	// QUERY
	for k, v := range r.URL.Query() {
		if len(v) == 0 {
			continue
		}
		quotaReq.AddArgument(model.BuildQueryArgument(k, v[0]))
	}

	// CALLER_SERVICE：约定 header value 形如 "ns/svc"（兼容缺省 ns 时只填 svc）
	if cs := r.Header.Get(headerCallerService); cs != "" {
		ns, svcName := splitCallerService(cs)
		quotaReq.AddArgument(model.BuildCallerServiceArgument(ns, svcName))
	}

	// CALLER_IP：优先取 header；缺省时回落到 r.RemoteAddr 的 IP 部分
	if cip := r.Header.Get(headerCallerIP); cip != "" {
		quotaReq.AddArgument(model.BuildCallerIPArgument(cip))
	} else if remoteIP := stripPort(r.RemoteAddr); remoteIP != "" {
		quotaReq.AddArgument(model.BuildCallerIPArgument(remoteIP))
	}

	// CALLER_METADATA：所有 X-Polaris-Caller-Metadata-<key> header 拆解出来
	for k, v := range r.Header {
		if len(v) == 0 {
			continue
		}
		// http.Header 会规范化为 X-Polaris-Caller-Metadata-Xxx；按规范化前缀做大小写无关比较
		if !strings.EqualFold(k[:min(len(k), len(headerCallerMetaPrefix))], headerCallerMetaPrefix) {
			continue
		}
		metaKey := k[len(headerCallerMetaPrefix):]
		if metaKey == "" {
			continue
		}
		// 统一小写：HTTP header key 经 net/http 规范化后会变 Title-Case（如 "Env"），
		// 而限流规则里通常按小写键写（如 "env"）；这里 lower-case 让两侧对齐，避免大小写不匹配漏匹配.
		quotaReq.AddArgument(model.BuildCallerMetadataArgument(strings.ToLower(metaKey), v[0]))
	}

	return quotaReq
}

// splitCallerService 解析 "ns/svc" 形式；缺省 ns 时返回 ("", svc).
func splitCallerService(value string) (ns, svc string) {
	if i := strings.Index(value, "/"); i > 0 {
		return value[:i], value[i+1:]
	}
	return "", value
}

// stripPort 从 "ip:port" 形式提取 ip；纯 ip 则原样返回；解析失败返回空串.
func stripPort(addr string) string {
	if addr == "" {
		return ""
	}
	if i := strings.LastIndex(addr, ":"); i > 0 {
		return addr[:i]
	}
	return addr
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
