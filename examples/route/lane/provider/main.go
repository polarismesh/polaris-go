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

var (
	namespace string
	service   string
	port      int
	token     string
	lane      string
	debug     bool
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "LaneEchoServer", "service name")
	flag.IntVar(&port, "port", 0, "service port, 0 means random")
	flag.StringVar(&token, "token", "", "service token for auth")
	// lane 参数：可选值为 "gray"、"stable" 或 ""（空字符串表示基线实例，不携带泳道标签）
	flag.StringVar(&lane, "lane", "", "lane label value, e.g. gray / stable / (empty for baseline)")
	flag.BoolVar(&debug, "debug", false, "是否开启 Polaris SDK debug 日志")
}

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

// logIncomingRequest 单行打印收到的请求：方法/URL/query/headers/body/cookie，都带 reqID 前缀.
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

// reqIDHeader 全链路追踪请求 ID，贯穿所有中间跳。
const reqIDHeader = "X-Request-ID"

// LaneProvider 泳道路由 provider 示例
type LaneProvider struct {
	provider  polaris.ProviderAPI
	namespace string
	service   string
	host      string
	port      int
	lane      string
}

// Run 启动服务
func (svr *LaneProvider) Run() {
	tmpHost, err := getLocalHost(
		svr.provider.SDKContext().GetConfig().GetGlobal().GetServerConnector().GetAddresses()[0])
	if err != nil {
		panic(fmt.Errorf("error occur while fetching localhost: %v", err))
	}
	svr.host = tmpHost
	svr.runWebServer()
	svr.registerService()
	svr.runMainLoop()
}

func (svr *LaneProvider) registerService() {
	log.Printf("start to invoke register operation, lane=%q", svr.lane)
	req := &polaris.InstanceRegisterRequest{}
	req.Service = svr.service
	req.Namespace = svr.namespace
	req.Host = svr.host
	req.Port = svr.port
	req.ServiceToken = token
	// 根据泳道参数决定是否携带 lane 元数据
	if svr.lane != "" {
		req.Metadata = map[string]string{"lane": svr.lane}
	} else {
		req.Metadata = map[string]string{}
	}
	// 首次注册容易因服务端连接抖动(尤其远程 Polaris)失败,重试 5 次避免进程立即挂掉。
	const maxRetry = 5
	var resp *model.InstanceRegisterResponse
	var err error
	for i := 1; i <= maxRetry; i++ {
		resp, err = svr.provider.RegisterInstance(req)
		if err == nil {
			break
		}
		log.Printf("[WARN] register instance attempt %d/%d failed: %v", i, maxRetry, err)
		if i < maxRetry {
			time.Sleep(2 * time.Second)
		}
	}
	if err != nil {
		log.Fatalf("fail to register instance after %d retries, err is %v", maxRetry, err)
	}
	log.Printf("register response: instanceId=%s, host=%s:%d, lane=%q, metadata=%v",
		resp.InstanceID, svr.host, svr.port, svr.lane, req.Metadata)
}

func (svr *LaneProvider) deregisterService() {
	log.Printf("start to invoke deregister operation")
	req := &polaris.InstanceDeRegisterRequest{}
	req.Service = svr.service
	req.Namespace = svr.namespace
	req.Host = svr.host
	req.Port = svr.port
	req.ServiceToken = token
	if err := svr.provider.Deregister(req); err != nil {
		log.Fatalf("fail to deregister instance, err is %v", err)
	}
	log.Printf("deregister successfully")
}

func (svr *LaneProvider) runWebServer() {
	http.HandleFunc("/echo", func(rw http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get(reqIDHeader)
		if reqID == "" {
			reqID = newReqID()
		}
		logIncomingRequest(reqID, r, "/echo")
		rw.WriteHeader(http.StatusOK)
		laneLabel := svr.lane
		if laneLabel == "" {
			laneLabel = "(baseline)"
		}
		msg := fmt.Sprintf("Hello, I'm %s. lane=%s, host=%s:%d", svr.service, laneLabel, svr.host, svr.port)
		log.Printf("[%s] handle echo: remote=%s, self=%s/%s, lane=%s",
			reqID, r.RemoteAddr, svr.namespace, svr.service, laneLabel)
		_, _ = rw.Write([]byte(msg))
		logReply(reqID, r.RemoteAddr, http.StatusOK, msg)
	})

	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", svr.port))
	if err != nil {
		log.Fatalf("[ERROR] fail to listen tcp, err is %v", err)
	}
	svr.port = ln.Addr().(*net.TCPAddr).Port

	go func() {
		log.Printf("[INFO] start http server, port=%d, lane=%q", svr.port, svr.lane)
		if err := http.Serve(ln, nil); err != nil {
			log.Fatalf("[ERROR] fail to run webServer, err is %v", err)
		}
	}()
}

func (svr *LaneProvider) runMainLoop() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM, syscall.SIGSEGV)
	for s := range ch {
		log.Printf("catch signal(%+v), stop servers", s)
		svr.deregisterService()
		return
	}
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
	initArgs()
	flag.Parse()
	if namespace == "" || service == "" {
		log.Fatal("namespace and service are required")
	}

	if debug {
		if err := api.SetLoggersLevel(api.DebugLog); err != nil {
			log.Printf("[WARN] 设置日志级别为 DEBUG 失败: %v", err)
		} else {
			log.Printf("[INFO] 已设置 Polaris SDK 日志级别为 DEBUG")
		}
	}

	provider, err := polaris.NewProviderAPI()
	if err != nil {
		log.Fatalf("fail to create providerAPI, err is %v", err)
	}
	defer provider.Destroy()

	svr := &LaneProvider{
		provider:  provider,
		namespace: namespace,
		service:   service,
		port:      port,
		lane:      lane,
	}
	svr.Run()
}

func getLocalHost(serverAddr string) (string, error) {
	conn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	localAddr := conn.LocalAddr().String()
	colonIdx := strings.LastIndex(localAddr, ":")
	if colonIdx > 0 {
		return localAddr[:colonIdx], nil
	}
	return localAddr, nil
}
