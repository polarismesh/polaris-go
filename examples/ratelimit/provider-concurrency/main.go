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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/polarismesh/polaris-go"
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

// 演示并发数限流的 provider，对齐 polaris-java 的并发数限流用法。
//
// 与 provider-qps 的关键差异：
//   1. 注册的服务名为 ConcurrencyEchoServer，避免和原 QPS 限流 demo 冲突
//   2. **必须** 在请求结束时调用 future.Release()，否则并发计数会泄漏
//   3. 提供 /slow 接口，sleep 一段时间模拟长耗时业务，便于观察并发数维度的限流效果

var (
	namespace string
	service   string
	token     string
	port      int64
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "ConcurrencyEchoServer", "service")
	// 当北极星开启鉴权时，需要配置此参数完成相关的权限检查
	flag.StringVar(&token, "token", "", "token")
	flag.Int64Var(&port, "port", 0, "port")
}

// PolarisProvider 演示并发数限流场景下的 provider 实现.
type PolarisProvider struct {
	provider  polaris.ProviderAPI
	limiter   polaris.LimitAPI
	namespace string
	service   string
	host      string
	port      int
}

// Run 启动主流程.
func (svr *PolarisProvider) Run() {
	tmpHost, err := getLocalHost(svr.provider.SDKContext().GetConfig().GetGlobal().GetServerConnector().GetAddresses()[0])
	if err != nil {
		panic(fmt.Errorf("error occur while fetching localhost: %v", err))
	}

	svr.host = tmpHost
	svr.runWebServer()
	svr.registerService()
}

// applyLimit 调用限流 API 获取配额；若通过返回 future（必须由调用方 defer future.Release()），
// 若被限流返回 nil 并已经写好响应.
func (svr *PolarisProvider) applyLimit(reqID string, rw http.ResponseWriter, r *http.Request, method string) polaris.QuotaFuture {
	quotaReq := polaris.NewQuotaRequest().(*model.QuotaRequestImpl)
	quotaReq.SetMethod(method)
	headers := convertHeaders(r.Header)
	for k, v := range headers {
		quotaReq.AddArgument(model.BuildHeaderArgument(strings.ToLower(k), v))
	}
	quotaReq.SetNamespace(namespace)
	quotaReq.SetService(service)

	start := time.Now()
	future, err := svr.limiter.GetQuota(quotaReq)
	if err != nil {
		body := fmt.Sprintf("[error] fail to GetQuota, err is %v", err)
		log.Printf("[%s] [error] fail to GetQuota: %v", reqID, err)
		rw.WriteHeader(http.StatusInternalServerError)
		_, _ = rw.Write([]byte(body))
		logReply(reqID, r.RemoteAddr, http.StatusInternalServerError, body)
		return nil
	}
	resp := future.Get()
	log.Printf("[%s] limiter resp | cost=%s code=%d info=%s | quota_req: ns=%s svc=%s method=%v labels=%v",
		reqID, time.Since(start).String(), resp.Code, resp.Info,
		quotaReq.GetNamespace(), quotaReq.GetService(), quotaReq.GetMethod(), quotaReq.GetLabels())

	if resp.Code != model.QuotaResultOk {
		body := http.StatusText(http.StatusTooManyRequests)
		rw.WriteHeader(http.StatusTooManyRequests)
		_, _ = rw.Write([]byte(body))
		logReply(reqID, r.RemoteAddr, http.StatusTooManyRequests, body)
		return nil
	}
	return future
}

func (svr *PolarisProvider) runWebServer() {
	// /echo 用于轻量验证；不阻塞业务逻辑，限流通过后立刻 Release，方便观察 QPS 与并发数的差异.
	http.HandleFunc("/echo", func(rw http.ResponseWriter, r *http.Request) {
		reqID := newReqID()
		logIncomingRequest(reqID, r, "/echo")
		future := svr.applyLimit(reqID, rw, r, "/echo")
		if future == nil {
			return
		}
		// 关键点：并发数限流必须 defer Release 归还配额，否则会泄漏计数
		defer future.Release()

		body := fmt.Sprintf("Hello, I'm ConcurrencyEchoServer Provider, My host : %s:%d", svr.host, svr.port)
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte(body))
		logReply(reqID, r.RemoteAddr, http.StatusOK, body)
	})

	// /slow 用 sleep 模拟长耗时业务，方便在并发数维度看到限流效果.
	// 例如设置并发上限为 2，发起 10 个 ?ms=2000 的并发请求，应有 8 个被限流.
	http.HandleFunc("/slow", func(rw http.ResponseWriter, r *http.Request) {
		reqID := newReqID()
		logIncomingRequest(reqID, r, "/slow")
		future := svr.applyLimit(reqID, rw, r, "/slow")
		if future == nil {
			return
		}
		defer future.Release()

		ms := parseInt(r.URL.Query().Get("ms"), 1000)
		time.Sleep(time.Duration(ms) * time.Millisecond)

		body := fmt.Sprintf("slept %dms on host %s:%d", ms, svr.host, svr.port)
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte(body))
		logReply(reqID, r.RemoteAddr, http.StatusOK, body)
	})

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
	initArgs()
	flag.Parse()
	if len(namespace) == 0 || len(service) == 0 {
		log.Print("namespace and service are required")
		return
	}
	provider, err := polaris.NewProviderAPI()

	if err != nil {
		log.Fatalf("fail to create providerAPI, err is %v", err)
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

// parseInt 将字符串解析为整数；解析失败时返回 fallback.
func parseInt(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 {
		return fallback
	}
	return v
}
