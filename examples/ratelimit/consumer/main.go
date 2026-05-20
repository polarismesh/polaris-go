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
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	mrand "math/rand"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/pkg/model"
)

// newReqID 生成一个 8 字符的请求 ID，用于把同一次请求的多条日志串起来.
func newReqID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
	}
	return hex.EncodeToString(b[:])
}

// formatHeaders 把 http.Header 压缩成单行字符串，避免多行打印交织.
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

// formatQuery 把 query 参数压缩成单行字符串.
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

var (
	namespace string
	service   string
	token     string
	port      int64
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "QpsRatelimitEchoServer", "service")
	// token 参数仅为兼容 verify_ratelimit.sh 的统一调用约定（与 provider-* 对齐），
	// consumer 自身不直接使用 token；鉴权由 polaris.yaml 中的 token 字段（受 ${POLARIS_TOKEN} 占位符控制）传给 SDK.
	flag.StringVar(&token, "token", "", "token (only used for CLI compatibility)")
	flag.Int64Var(&port, "port", 18080, "port")
}

// PolarisConsumer is a consumer of polaris
type PolarisConsumer struct {
	consumer  polaris.ConsumerAPI
	namespace string
	service   string
	webSvr    *http.Server
}

// Run starts the consumer
func (svr *PolarisConsumer) Run() {
	go svr.runWebServer()
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, []os.Signal{
		syscall.SIGINT, syscall.SIGTERM,
		syscall.SIGSEGV,
	}...)

	for s := range ch {
		svr.consumer.Destroy()
		log.Printf("catch signal(%+v), stop servers", s)
		_ = svr.webSvr.Close()
		return
	}
}

func (svr *PolarisConsumer) runWebServer() {
	// /echo /health /slow 三个 path 共用同一套"服务发现 + HTTP 转发"逻辑.
	// 透传 provider 返回的状态码（包含 429），让上游 curl 能直接看到限流效果.
	for _, path := range []string{"/echo", "/health", "/slow"} {
		method := path
		http.HandleFunc(path, func(rw http.ResponseWriter, r *http.Request) {
			svr.forwardToProvider(rw, r, method)
		})
	}

	log.Printf("start run web server, port : %d", port)

	webSvr := &http.Server{Addr: fmt.Sprintf("0.0.0.0:%d", port), Handler: nil}
	svr.webSvr = webSvr
	if err := webSvr.ListenAndServe(); err != nil {
		log.Fatalf("[ERROR]fail to run webServer, err is %v", err)
	}
}

// forwardToProvider 用 polaris 服务发现选实例，按相同 path（含 query string）转发 HTTP 请求.
// 关键点：**透传** provider 的状态码（特别是 429），上游 curl 才能看到真实限流结果.
func (svr *PolarisConsumer) forwardToProvider(rw http.ResponseWriter, r *http.Request, method string) {
	// 为本次请求生成唯一 ID，所有日志带 [reqID] 前缀，便于在并发场景下用 grep 串起整条调用链
	reqID := newReqID()

	// 读取请求 body（GET 一般无 body，但保持兼容 POST/PUT 等方法）
	var bodyBytes []byte
	if r.Body != nil {
		var readErr error
		bodyBytes, readErr = ioutil.ReadAll(r.Body)
		if readErr != nil {
			body := fmt.Sprintf("[error] read request body fail: %v", readErr)
			log.Printf("[%s] [error] read request body fail: %v", reqID, readErr)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(body))
			log.Printf("[%s] >>> reply to client %s: status=%d, body=%s", reqID, r.RemoteAddr, http.StatusInternalServerError, body)
			return
		}
		_ = r.Body.Close()
	}

	// 单行汇总"收到的请求"：方法 + URL + query + headers + body
	log.Printf("[%s] <<< recv from %s | method=%s url=%s query=%s headers=%s body=%s",
		reqID, r.RemoteAddr, r.Method, r.URL.String(), formatQuery(r.URL.Query()), formatHeaders(r.Header), string(bodyBytes))

	getOneRequest := &polaris.GetOneInstanceRequest{}
	getOneRequest.Namespace = namespace
	getOneRequest.Service = service
	oneInstResp, err := svr.consumer.GetOneInstance(getOneRequest)
	if err != nil {
		body := fmt.Sprintf("[error] fail to getOneInstance, err is %v", err)
		log.Printf("[%s] [error] fail to getOneInstance, err is %v", reqID, err)
		rw.WriteHeader(http.StatusInternalServerError)
		_, _ = rw.Write([]byte(body))
		log.Printf("[%s] >>> reply to client %s: status=%d, body=%s", reqID, r.RemoteAddr, http.StatusInternalServerError, body)
		return
	}
	instance := oneInstResp.GetInstance()
	if instance == nil {
		log.Printf("[%s] [error] no available instance for service %s", reqID, service)
		rw.WriteHeader(http.StatusServiceUnavailable)
		_, _ = rw.Write([]byte("no available instance"))
		log.Printf("[%s] >>> reply to client %s: status=%d, body=%s", reqID, r.RemoteAddr, http.StatusServiceUnavailable, "no available instance")
		return
	}
	log.Printf("[%s] picked instance %s:%d", reqID, instance.GetHost(), instance.GetPort())

	// 拼接目标 URL，保留请求原始 path + query string（让 /slow?ms=1500 能透传）
	rawQuery := ""
	if r.URL.RawQuery != "" {
		rawQuery = "?" + r.URL.RawQuery
	}
	targetURL := fmt.Sprintf("http://%s:%d%s%s",
		instance.GetHost(), instance.GetPort(), r.URL.Path, rawQuery)

	start := time.Now()
	// 透传：使用原始请求方法 + 原始 body
	var reqBody io.Reader
	if len(bodyBytes) > 0 {
		reqBody = bytes.NewReader(bodyBytes)
	}
	req, err := http.NewRequest(r.Method, targetURL, reqBody)
	if err != nil {
		body := fmt.Sprintf("[error] new request fail: %v", err)
		log.Printf("[%s] [error] new request fail: %v", reqID, err)
		rw.WriteHeader(http.StatusInternalServerError)
		_, _ = rw.Write([]byte(body))
		log.Printf("[%s] >>> reply to client %s: status=%d, body=%s", reqID, r.RemoteAddr, http.StatusInternalServerError, body)
		return
	}
	// 透传请求头
	req.Header = r.Header.Clone()
	log.Printf("[%s] ==> forward to provider: %s %s", reqID, r.Method, targetURL)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		body := fmt.Sprintf("[error] send request to %s fail: %v", targetURL, err)
		log.Printf("[%s] [error] send request to %s fail: %v", reqID, targetURL, err)
		rw.WriteHeader(http.StatusInternalServerError)
		_, _ = rw.Write([]byte(body))
		log.Printf("[%s] >>> reply to client %s: status=%d, body=%s", reqID, r.RemoteAddr, http.StatusInternalServerError, body)

		time.Sleep(time.Millisecond * time.Duration(mrand.Intn(10)))
		delay := time.Since(start)
		ret := &polaris.ServiceCallResult{
			ServiceCallResult: model.ServiceCallResult{
				EmptyInstanceGauge: model.EmptyInstanceGauge{},
				CalledInstance:     instance,
				Method:             method,
				RetStatus:          model.RetFail,
			},
		}
		ret.SetRetCode(int32(http.StatusInternalServerError))
		ret.SetDelay(delay)
		if err := svr.consumer.UpdateServiceCallResult(ret); err != nil {
			log.Printf("do report service call result : %+v", err)
		}
		return
	}
	defer resp.Body.Close()

	delay := time.Since(start)

	ret := &polaris.ServiceCallResult{
		ServiceCallResult: model.ServiceCallResult{
			EmptyInstanceGauge: model.EmptyInstanceGauge{},
			CalledInstance:     instance,
			Method:             method,
			RetStatus:          model.RetSuccess,
		},
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		ret.RetStatus = model.RetFlowControl
	}
	ret.SetDelay(delay)
	ret.SetRetCode(int32(resp.StatusCode))
	if err := svr.consumer.UpdateServiceCallResult(ret); err != nil {
		log.Printf("do report service call result : %+v", err)
	}

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		body := fmt.Sprintf("[error] read resp fail: %v", err)
		log.Printf("[%s] [error] read resp from %s fail: %v", reqID, targetURL, err)
		rw.WriteHeader(http.StatusInternalServerError)
		_, _ = rw.Write([]byte(body))
		log.Printf("[%s] >>> reply to client %s: status=%d, body=%s", reqID, r.RemoteAddr, http.StatusInternalServerError, body)
		return
	}

	// 单行汇总"收到的 provider 响应"：状态码 + 耗时 + 响应头 + body
	log.Printf("[%s] <== recv from provider | status=%d cost=%v headers=%s body=%s",
		reqID, resp.StatusCode, delay, formatHeaders(resp.Header), string(data))

	// 透传 provider 响应头给上游 client（保持 Content-Type 等关键字段一致）
	for k, vs := range resp.Header {
		for _, v := range vs {
			rw.Header().Add(k, v)
		}
	}
	// 透传 provider 状态码 + body，让 curl 能直接看到限流（429）
	rw.WriteHeader(resp.StatusCode)
	_, _ = rw.Write(data)

	// 单行汇总"自身回给上游 client 的响应"
	log.Printf("[%s] >>> reply to client %s: status=%d body=%s", reqID, r.RemoteAddr, resp.StatusCode, string(data))
}

func main() {
	initArgs()
	flag.Parse()
	if len(namespace) == 0 || len(service) == 0 {
		log.Print("namespace and service are required")
		return
	}
	consumer, err := polaris.NewConsumerAPI()
	// 或者使用以下方法,则不需要创建配置文件
	// consumer, err = api.NewConsumerAPIByAddress("127.0.0.1:8091")

	if err != nil {
		log.Fatalf("fail to create consumerAPI, err is %v", err)
	}
	defer consumer.Destroy()

	svr := &PolarisConsumer{
		consumer:  consumer,
		namespace: namespace,
		service:   service,
	}

	svr.Run()

}
