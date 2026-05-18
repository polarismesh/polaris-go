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
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
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

var (
	namespace     string
	service       string
	selfNamespace string
	selfService   string
	port          int64
	debug         bool
	metadata      string
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "AuthEchoServer", "service")
	flag.StringVar(&selfNamespace, "selfNamespace", "default", "selfNamespace")
	flag.StringVar(&selfService, "selfService", "AuthEchoClient", "selfService")
	flag.Int64Var(&port, "port", 38080, "port")
	flag.BoolVar(&debug, "debug", false, "是否开启 Polaris SDK debug 日志")
	flag.StringVar(&metadata, "callerMetadata", "env=dev,version=1.0.0", "callerMetadata")
}

// PolarisConsumer is a consumer of polaris
type PolarisConsumer struct {
	consumer       polaris.ConsumerAPI
	namespace      string
	service        string
	webSvr         *http.Server
	callerMetadata map[string]string
}

// newRequestWithCallerHeader 构造一个携带主调身份信息的 HTTP 请求：
//   - X-Polaris-Caller-Namespace: 主调命名空间
//   - X-Polaris-Caller-Service:   主调服务名
//   - X-Caller-Meta-<Key>:        主调元数据（可被 provider 用于鉴权 / 路由匹配）
//
// 同时会把 incoming（上游传入到本 consumer 的）请求头透传给下游 provider，
// 但会跳过 hop-by-hop 头与 consumer 自身要写入的身份头，避免被上游伪造。
// 当 incoming 为 nil 时，不做任何透传（保持向后兼容）。
func (svr *PolarisConsumer) newRequestWithCallerHeader(method, url string, incoming *http.Request) (*http.Request, error) {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, err
	}
	// 1) 先透传上游 header，便于链路追踪 / 业务 header 转发
	if incoming != nil {
		for k, vs := range incoming.Header {
			if isHopByHopHeader(k) {
				continue
			}
			if isConsumerOwnedHeader(k) {
				// consumer 自己会重写这些 header，禁止被上游伪造
				continue
			}
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
	}
	// 2) 再写入 consumer 自身的身份信息，覆盖上游同名 header
	req.Header.Set("X-Polaris-Caller-Namespace", selfNamespace)
	req.Header.Set("X-Polaris-Caller-Service", selfService)
	// 删除可能从上游透传过来的旧 X-Caller-Meta-*，避免污染
	for k := range req.Header {
		if strings.HasPrefix(strings.ToLower(k), "x-caller-meta-") {
			req.Header.Del(k)
		}
	}
	for k, v := range svr.callerMetadata {
		req.Header.Set("X-Caller-Meta-"+k, v)
	}
	req.Header.Set("User-Agent", fmt.Sprintf("polaris-go-consumer/%s/%s", svr.namespace, svr.service))
	return req, nil
}

// hopByHopHeaders 是 RFC 7230 中定义的 hop-by-hop 头，转发时必须丢弃。
// 参考： https://datatracker.ietf.org/doc/html/rfc7230#section-6.1
var hopByHopHeaders = map[string]struct{}{
	"connection":          {},
	"proxy-connection":    {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"te":                  {},
	"trailer":             {},
	"transfer-encoding":   {},
	"upgrade":             {},
	// 下面这些不是严格意义上的 hop-by-hop，但同样不该原样透传
	"host":           {},
	"content-length": {},
}

func isHopByHopHeader(name string) bool {
	_, ok := hopByHopHeaders[strings.ToLower(name)]
	return ok
}

// isConsumerOwnedHeader 表示这些 header 由 consumer 自身写入，
// 不允许从上游透传，避免身份伪造。
func isConsumerOwnedHeader(name string) bool {
	lower := strings.ToLower(name)
	switch lower {
	case "x-polaris-caller-namespace", "x-polaris-caller-service", "user-agent":
		return true
	}
	return false
}

// dumpIncomingHeaders 把请求头按 key 排序后序列化为单行 JSON，便于日志检索。
func dumpIncomingHeaders(h http.Header) string {
	if len(h) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		out[k] = strings.Join(h.Values(k), ",")
	}
	b, err := json.Marshal(out)
	if err != nil {
		return fmt.Sprintf("%+v", h)
	}
	return string(b)
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
	// 新增 API：支持通过请求体传入 namespace 和 service
	http.HandleFunc("/echo-with-body", func(rw http.ResponseWriter, r *http.Request) {
		log.Printf("receive echo-with-body request from client:%s", r.RemoteAddr)
		log.Printf("[CALLER] incoming headers from %s: %s", r.RemoteAddr, dumpIncomingHeaders(r.Header))

		// 定义请求体结构
		type EchoRequest struct {
			Namespace string `json:"namespace"`
			Service   string `json:"service"`
		}

		// 解析请求体
		var req EchoRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			log.Printf("[error] fail to decode request body, err is %v", err)
			rw.WriteHeader(http.StatusBadRequest)
			_, _ = rw.Write([]byte(fmt.Sprintf("[error] fail to decode request body, err is %v", err)))
			return
		}
		defer r.Body.Close()

		// 验证参数
		if req.Namespace == "" || req.Service == "" {
			log.Printf("[error] namespace and service are required")
			rw.WriteHeader(http.StatusBadRequest)
			_, _ = rw.Write([]byte("[error] namespace and service are required"))
			return
		}

		log.Printf("request params: namespace=%s, service=%s", req.Namespace, req.Service)

		// 获取服务实例
		getOneRequest := &polaris.GetOneInstanceRequest{}
		getOneRequest.Namespace = req.Namespace
		getOneRequest.Service = req.Service
		oneInstResp, err := svr.consumer.GetOneInstance(getOneRequest)
		if err != nil {
			log.Printf("[error] fail to getOneInstance, err is %v", err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[error] fail to getOneInstance, err is %v", err)))
			return
		}
		instance := oneInstResp.GetInstance()
		if nil != instance {
			// [CALLEE] 被调实例选址结果
			log.Printf("[CALLEE] selected instance: namespace=%s service=%s host=%s port=%d instanceId=%s healthy=%v isolate=%v weight=%d protocol=%s version=%s metadata=%s",
				req.Namespace, req.Service,
				instance.GetHost(), instance.GetPort(),
				instance.GetId(), instance.IsHealthy(), instance.IsIsolated(),
				instance.GetWeight(), instance.GetProtocol(), instance.GetVersion(),
				jsonStr(instance.GetMetadata()))
		}

		targetURL := fmt.Sprintf("http://%s:%d/echo", instance.GetHost(), instance.GetPort())
		log.Printf("[CALLEE] sending request: url=%s", targetURL)
		start := time.Now()
		providerReq, err := svr.newRequestWithCallerHeader(http.MethodGet, targetURL, r)
		if err != nil {
			log.Printf("[CALLEE][error] build request fail : %s", err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[error] build request fail : %s", err)))
			return
		}
		log.Printf("[CALLER] outgoing headers: callerNs=%q callerSvc=%q metadata=%s all=%s",
			providerReq.Header.Get("X-Polaris-Caller-Namespace"),
			providerReq.Header.Get("X-Polaris-Caller-Service"),
			jsonStr(svr.callerMetadata),
			dumpIncomingHeaders(providerReq.Header))
		resp, err := http.DefaultClient.Do(providerReq)
		if err != nil {
			log.Printf("[CALLEE][error] send request to %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[error] send request to %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)))

			time.Sleep(time.Millisecond * time.Duration(rand.Intn(10)))
			delay := time.Since(start)

			ret := &polaris.ServiceCallResult{
				ServiceCallResult: model.ServiceCallResult{
					EmptyInstanceGauge: model.EmptyInstanceGauge{},
					CalledInstance:     instance,
					Method:             "/echo",
					RetStatus:          model.RetFail,
				},
			}
			ret.SetDelay(delay)
			ret.SetRetCode(int32(http.StatusInternalServerError))
			if err := svr.consumer.UpdateServiceCallResult(ret); err != nil {
				log.Printf("do report service call result : %+v", err)
			}
			return
		}
		time.Sleep(time.Millisecond * time.Duration(rand.Intn(10)))
		delay := time.Since(start)

		// [CALLEE] 被调返回的状态行 / Header
		log.Printf("[CALLEE] response from %s:%d : status=%q code=%d proto=%s contentLength=%d delay=%s",
			instance.GetHost(), instance.GetPort(),
			resp.Status, resp.StatusCode, resp.Proto, resp.ContentLength, delay)
		log.Printf("[CALLEE] response headers: authResult=%q authInfo=%q server=%q contentType=%q",
			resp.Header.Get("X-Auth-Result"), resp.Header.Get("X-Auth-Info"),
			resp.Header.Get("Server"), resp.Header.Get("Content-Type"))

		ret := &polaris.ServiceCallResult{
			ServiceCallResult: model.ServiceCallResult{
				EmptyInstanceGauge: model.EmptyInstanceGauge{},
				CalledInstance:     instance,
				Method:             "/echo",
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
		} else {
			log.Printf("do report service call result : %+v", ret)
		}

		defer resp.Body.Close()

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("[CALLEE][error] read resp from %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[error] read resp from %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)))
			return
		}
		log.Printf("[CALLEE] response body: bytes=%d body=%s", len(data), truncate(string(data), 512))
		// 透传 provider 的状态码（200 / 403 / 500 等），让上层调用方（curl/前端/网关）能区分
		// "调用成功"与"被鉴权拒绝"等不同结果。同时透传 X-Auth-Result / X-Auth-Info 让外部
		// 调试时能直接看到鉴权决策。
		if v := resp.Header.Get("X-Auth-Result"); v != "" {
			rw.Header().Set("X-Auth-Result", v)
		}
		if v := resp.Header.Get("X-Auth-Info"); v != "" {
			rw.Header().Set("X-Auth-Info", v)
		}
		rw.WriteHeader(resp.StatusCode)
		_, _ = rw.Write(data)
	})

	http.HandleFunc("/echo", func(rw http.ResponseWriter, r *http.Request) {
		log.Printf("receive echo request from client:%s", r.RemoteAddr)
		log.Printf("[CALLER] incoming headers from %s: %s", r.RemoteAddr, dumpIncomingHeaders(r.Header))
		// DiscoverEchoServer
		getOneRequest := &polaris.GetOneInstanceRequest{}
		getOneRequest.Namespace = namespace
		getOneRequest.Service = service
		oneInstResp, err := svr.consumer.GetOneInstance(getOneRequest)
		if err != nil {
			log.Printf("[error] fail to getOneInstance, err is %v", err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[error] fail to getOneInstance, err is %v", err)))
			return
		}
		instance := oneInstResp.GetInstance()
		if nil != instance {
			// [CALLEE] 被调实例选址结果
			log.Printf("[CALLEE] selected instance: namespace=%s service=%s host=%s port=%d instanceId=%s healthy=%v isolate=%v weight=%d protocol=%s version=%s metadata=%s",
				namespace, service,
				instance.GetHost(), instance.GetPort(),
				instance.GetId(), instance.IsHealthy(), instance.IsIsolated(),
				instance.GetWeight(), instance.GetProtocol(), instance.GetVersion(),
				jsonStr(instance.GetMetadata()))
		}

		targetURL := fmt.Sprintf("http://%s:%d/echo", instance.GetHost(), instance.GetPort())
		log.Printf("[CALLEE] sending request: url=%s", targetURL)
		start := time.Now()
		providerReq, err := svr.newRequestWithCallerHeader(http.MethodGet, targetURL, r)
		if err != nil {
			log.Printf("[CALLEE][error] build request fail : %s", err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[error] build request fail : %s", err)))
			return
		}
		log.Printf("[CALLER] outgoing headers: callerNs=%q callerSvc=%q metadata=%s all=%s",
			providerReq.Header.Get("X-Polaris-Caller-Namespace"),
			providerReq.Header.Get("X-Polaris-Caller-Service"),
			jsonStr(svr.callerMetadata),
			dumpIncomingHeaders(providerReq.Header))
		resp, err := http.DefaultClient.Do(providerReq)
		if err != nil {
			log.Printf("[CALLEE][error] send request to %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[errot] send request to %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)))

			time.Sleep(time.Millisecond * time.Duration(rand.Intn(10)))
			delay := time.Since(start)

			ret := &polaris.ServiceCallResult{
				ServiceCallResult: model.ServiceCallResult{
					EmptyInstanceGauge: model.EmptyInstanceGauge{},
					CalledInstance:     instance,
					Method:             "/echo",
					RetStatus:          model.RetFail,
				},
			}
			ret.SetDelay(delay)
			ret.SetRetCode(int32(http.StatusInternalServerError))
			if err := svr.consumer.UpdateServiceCallResult(ret); err != nil {
				log.Printf("do report service call result : %+v", err)
			}
			return
		}
		time.Sleep(time.Millisecond * time.Duration(rand.Intn(10)))
		delay := time.Since(start)

		// [CALLEE] 被调返回的状态行 / Header
		log.Printf("[CALLEE] response from %s:%d : status=%q code=%d proto=%s contentLength=%d delay=%s",
			instance.GetHost(), instance.GetPort(),
			resp.Status, resp.StatusCode, resp.Proto, resp.ContentLength, delay)
		log.Printf("[CALLEE] response headers: authResult=%q authInfo=%q server=%q contentType=%q",
			resp.Header.Get("X-Auth-Result"), resp.Header.Get("X-Auth-Info"),
			resp.Header.Get("Server"), resp.Header.Get("Content-Type"))

		ret := &polaris.ServiceCallResult{
			ServiceCallResult: model.ServiceCallResult{
				EmptyInstanceGauge: model.EmptyInstanceGauge{},
				CalledInstance:     instance,
				Method:             "/echo",
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
		} else {
			log.Printf("do report service call result : %+v", ret)
		}

		defer resp.Body.Close()

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("[CALLEE][error] read resp from %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[error] read resp from %s:%d fail : %s", instance.GetHost(), instance.GetPort(), err)))
			return
		}
		log.Printf("[CALLEE] response body: bytes=%d body=%s", len(data), truncate(string(data), 512))
		// 透传 provider 的状态码（200 / 403 / 500 等），让上层调用方（curl/前端/网关）能区分
		// "调用成功"与"被鉴权拒绝"等不同结果。同时透传 X-Auth-Result / X-Auth-Info 让外部
		// 调试时能直接看到鉴权决策。
		if v := resp.Header.Get("X-Auth-Result"); v != "" {
			rw.Header().Set("X-Auth-Result", v)
		}
		if v := resp.Header.Get("X-Auth-Info"); v != "" {
			rw.Header().Set("X-Auth-Info", v)
		}
		rw.WriteHeader(resp.StatusCode)
		_, _ = rw.Write(data)
	})

	log.Printf("start run web server, port : %d", port)

	webSvr := &http.Server{Addr: fmt.Sprintf("0.0.0.0:%d", port), Handler: nil}
	svr.webSvr = webSvr
	if err := webSvr.ListenAndServe(); err != nil {
		log.Fatalf("[ERROR]fail to run webServer, err is %v", err)
	}
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	initArgs()
	flag.Parse()
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

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
		// callerMetadata 主调侧自带的元数据，会以 X-Caller-Meta-<Key>: <Value> 的形式透传给被调
		callerMetadata: parseMetadata(metadata),
	}

	svr.Run()
}

// jsonStr 把任意值序列化为紧凑 JSON，序列化失败时回退到 fmt 表达式，便于日志单行打印
func jsonStr(v interface{}) string {
	if v == nil {
		return "null"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%+v", v)
	}
	return string(b)
}

// truncate 把字符串截断到 max 长度，避免 body 过长污染日志
func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

// parseMetadata 把 "k1=v1,k2=v2" 解析成 map；忽略空串、无 = 或空 key。
func parseMetadata(raw string) map[string]string {
	out := map[string]string{}
	if raw == "" {
		return out
	}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		idx := strings.Index(pair, "=")
		if idx <= 0 {
			continue
		}
		k := strings.TrimSpace(pair[:idx])
		v := strings.TrimSpace(pair[idx+1:])
		if k == "" {
			continue
		}
		out[k] = v
	}
	return out
}
