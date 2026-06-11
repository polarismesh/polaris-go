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
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
)

var (
	namespace        string
	service          string
	token            string
	port             int64
	registerMetadata string
	debug            bool
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "AuthEchoServer", "service")
	// 当北极星开启鉴权时，需要配置此参数完成相关的权限检查
	flag.StringVar(&token, "token", "", "token")
	flag.Int64Var(&port, "port", 0, "port")
	// registerMetadata 指定注册时上报到服务端的实例 metadata，格式 "k1=v1,k2=v2"。
	// 这份 metadata 同时被 polaris-go SDK 登记到本端，用于鉴权 CALLEE_METADATA 维度取值。
	flag.StringVar(&registerMetadata, "register-metadata", "", "register-time metadata, format: k1=v1,k2=v2")
	flag.BoolVar(&debug, "debug", false, "是否开启 Polaris SDK debug 日志")
}

// parseRegisterMetadata 把 "k1=v1,k2=v2" 解析成 map；忽略空串、无 = 或空 key。
func parseRegisterMetadata(raw string) map[string]string {
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

// PolarisProvider 鉴权示例 provider，先做服务注册，
// 然后对每个 /echo 请求调用 AuthAPI.Authenticate 完成被调侧鉴权拦截。
type PolarisProvider struct {
	provider   polaris.ProviderAPI
	authAPI    polaris.AuthAPI
	namespace  string
	service    string
	host       string
	port       int
	isShutdown bool
	webSvr     *http.Server
}

// Run 启动 provider
func (svr *PolarisProvider) Run() {
	tmpHost, err := getLocalHost(svr.provider.SDKContext().GetConfig().GetGlobal().GetServerConnector().GetAddresses()[0])
	if err != nil {
		panic(fmt.Errorf("error occur while fetching localhost: %v", err))
	}

	svr.host = tmpHost
	svr.runWebServer()
	svr.registerService()
}

// authenticate 调用 AuthAPI 对当前 HTTP 请求做鉴权检查，
// 命中黑白名单规则时直接返回 403 / 拒绝原因；放行时返回 nil。
func (svr *PolarisProvider) authenticate(r *http.Request) (*model.AuthenticateResponse, error) {
	remoteIP := extractRemoteIP(r)
	log.Printf("[CALLER] incoming request: remoteAddr=%s remoteIP=%s method=%s path=%s ua=%q referer=%q",
		r.RemoteAddr, remoteIP, r.Method, r.URL.Path, r.Header.Get("User-Agent"), r.Header.Get("Referer"))
	// 打印收到的全部请求头，便于端到端排查链路上 header 是否如期透传
	log.Printf("[CALLER] incoming headers: %s", dumpHeaders(r.Header))

	req := &polaris.AuthenticateRequest{}
	req.Namespace = svr.namespace
	req.Service = svr.service
	req.Method = r.Method
	req.Path = r.URL.Path
	req.Protocol = "HTTP"

	// 主调服务（caller）信息：从约定的 Header 中提取，便于演示 CALLER_SERVICE / CALLER_METADATA 维度
	callerNs := strings.TrimSpace(r.Header.Get("X-Polaris-Caller-Namespace"))
	callerSvc := strings.TrimSpace(r.Header.Get("X-Polaris-Caller-Service"))
	if callerSvc != "" {
		req.SourceService = &model.ServiceInfo{
			Namespace: callerNs,
			Service:   callerSvc,
			Metadata:  map[string]string{},
		}
		// 主调元数据：约定以 X-Caller-Meta-<Key>: <Value> 的方式透传
		for k, v := range r.Header {
			if !strings.HasPrefix(k, "X-Caller-Meta-") || len(v) == 0 {
				continue
			}
			metaKey := strings.ToLower(k[len("X-Caller-Meta-"):])
			req.SourceService.Metadata[metaKey] = v[0]
		}
		log.Printf("[CALLER] caller service: namespace=%q service=%q metadata=%s",
			req.SourceService.Namespace, req.SourceService.Service, jsonStr(req.SourceService.Metadata))
	} else {
		log.Printf("[CALLER] caller service: <unknown> (X-Polaris-Caller-Service header not set)")
	}

	// 打印 query / cookie 概览，便于排查规则匹配问题。
	// query 使用按 key 排序的单行 JSON，与 [CALLER] incoming headers 保持同一风格。
	log.Printf("[CALLER] query: %s", dumpQuery(r.URL.Query()))
	if cookies := r.Cookies(); len(cookies) > 0 {
		cookieKV := make(map[string]string, len(cookies))
		for _, c := range cookies {
			cookieKV[c.Name] = c.Value
		}
		log.Printf("[CALLER] cookies: %s", jsonStr(cookieKV))
	}

	// 8 维 Argument：Header / Query / Cookie / CallerIP / Custom
	// Go http.Header 会 canonicalize key（"user" → "User"），但 Polaris 规则的 key 通常按用户原始大小写填写。
	// 这里同时上报 canonical 与小写两份，确保规则匹配的鲁棒性。
	for k, vs := range r.Header {
		if len(vs) == 0 {
			continue
		}
		req.AddArgument(model.BuildHeaderArgument(k, vs[0]))
		lk := strings.ToLower(k)
		if lk != k {
			req.AddArgument(model.BuildHeaderArgument(lk, vs[0]))
		}
	}
	for k, vs := range r.URL.Query() {
		if len(vs) == 0 {
			continue
		}
		req.AddArgument(model.BuildQueryArgument(k, vs[0]))
	}
	for _, c := range r.Cookies() {
		req.AddArgument(model.BuildCookieArgument(c.Name, c.Value))
	}
	if remoteIP := extractRemoteIP(r); remoteIP != "" {
		req.AddArgument(model.BuildCallerIPArgument(remoteIP))
	}
	// 自定义流量标签：约定以 X-Custom-Arg-<Key>: <Value> 透传，用于 CUSTOM 维度匹配。
	// Go http.Header 会 canonicalize，因此这里统一按 canonical 前缀识别，后缀转小写做 key。
	for k, vs := range r.Header {
		if !strings.HasPrefix(k, "X-Custom-Arg-") || len(vs) == 0 {
			continue
		}
		argKey := strings.ToLower(k[len("X-Custom-Arg-"):])
		if argKey == "" {
			continue
		}
		req.AddArgument(model.BuildCustomArgument(argKey, vs[0]))
	}

	return svr.authAPI.Authenticate(req)
}

func (svr *PolarisProvider) runWebServer() {
	// echoHandler：响应所有非 /auth-info 的路径（如 /echo、/echo-allow-vip、/echo-callee-prod 等），
	// 让 verify_auth.sh 可以用不同 path 访问同一 provider，配合服务端规则的 EXACT api.path
	// 实现"同 service 下多条规则按 path 互不干扰"的复用模型。
	echoHandler := func(rw http.ResponseWriter, r *http.Request) {
		callerDesc := describeCaller(r)

		// 1. 鉴权拦截
		resp, err := svr.authenticate(r)
		if err != nil {
			log.Printf("[ERROR] authenticate fail: caller=%s err=%v", callerDesc, err)
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("authenticate fail: %v", err)))
			return
		}
		if resp != nil && !resp.IsAllowed() {
			log.Printf("[DENY] caller=%s path=%s reason=%q", callerDesc, r.URL.Path, resp.GetInfo())
			rw.Header().Set("X-Auth-Result", "forbidden")
			rw.Header().Set("X-Auth-Info", resp.GetInfo())
			rw.WriteHeader(http.StatusForbidden)
			_, _ = rw.Write([]byte(fmt.Sprintf("forbidden: %s", resp.GetInfo())))
			return
		}

		// 2. 鉴权放行 → 业务逻辑
		rw.Header().Set("X-Auth-Result", "ok")
		rw.WriteHeader(http.StatusOK)
		msg := fmt.Sprintf("Hello, I'm %s Provider, My host : %s:%d, path: %s",
			svr.service, svr.host, svr.port, r.URL.Path)
		log.Printf("[ALLOW] caller=%s path=%s response=%s", callerDesc, r.URL.Path, msg)
		_, _ = rw.Write([]byte(msg))
	}
	// "/" 作为兜底匹配，让所有 /echo / /echo-* 路径都进入 echoHandler；
	// /auth-info 因为是精确注册，DefaultServeMux 会优先匹配它，不会进 echoHandler。
	http.HandleFunc("/", echoHandler)

	// 调试接口：返回当前服务的鉴权配置摘要（chain / enable），便于脚本/控制台核对
	http.HandleFunc("/auth-info", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		cfg := svr.provider.SDKContext().GetConfig().GetProvider().GetAuth()
		body := map[string]interface{}{
			"namespace": svr.namespace,
			"service":   svr.service,
			"enable":    cfg != nil && cfg.IsEnable(),
		}
		if cfg != nil {
			body["chain"] = cfg.GetChain()
		}
		_ = json.NewEncoder(rw).Encode(body)
	})

	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		log.Fatalf("[ERROR]fail to listen tcp, err is %v", err)
	}

	svr.port = ln.Addr().(*net.TCPAddr).Port

	go func() {
		log.Printf("[INFO] start http server, listen port is %v", svr.port)
		svr.webSvr = &http.Server{Handler: nil}
		if err := svr.webSvr.Serve(ln); err != nil {
			svr.isShutdown = false
			log.Fatalf("[ERROR]fail to run webServer, err is %v", err)
		}
	}()
}

// fetchBlockAllowRule placeholder removed; see /auth-info handler above.

func (svr *PolarisProvider) registerService() {
	log.Printf("start to invoke register operation")
	registerRequest := &polaris.InstanceRegisterRequest{}
	registerRequest.Service = service
	registerRequest.Namespace = namespace
	registerRequest.Host = svr.host
	registerRequest.Port = svr.port
	registerRequest.ServiceToken = token
	registerRequest.SetTTL(1)
	// 注册带上 metadata 一方面让规则 CALLEE_METADATA 在服务端可见，
	// 另一方面被 polaris-go SDK 登记到本端用于鉴权取值。
	if md := parseRegisterMetadata(registerMetadata); len(md) > 0 {
		registerRequest.Metadata = md
	}
	log.Printf("registerRequest: %+v", jsonStr(registerRequest))
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
		svr.isShutdown = true
		svr.deregisterService()
		_ = svr.webSvr.Close()
		return
	}
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
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
	if err != nil {
		log.Fatalf("fail to create providerAPI, err is %v", err)
	}
	defer provider.Destroy()

	// 复用同一个 SDKContext 创建 AuthAPI，避免重复拉规则
	authAPI := polaris.NewAuthAPIByContext(provider.SDKContext())
	defer authAPI.Destroy()

	svr := &PolarisProvider{
		provider:  provider,
		authAPI:   authAPI,
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
	defer conn.Close()
	localAddr := conn.LocalAddr().String()
	colonIdx := strings.LastIndex(localAddr, ":")
	if colonIdx > 0 {
		return localAddr[:colonIdx], nil
	}
	return localAddr, nil
}

// extractRemoteIP 从 RemoteAddr 中拆出纯 IP，去掉 :port
func extractRemoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// describeCaller 生成主调的简短描述，形如：
//
//	default/ConsumerA@10.0.0.1:54321
//
// 当主调身份未知时回退到 RemoteAddr。
func describeCaller(r *http.Request) string {
	callerNs := strings.TrimSpace(r.Header.Get("X-Polaris-Caller-Namespace"))
	callerSvc := strings.TrimSpace(r.Header.Get("X-Polaris-Caller-Service"))
	if callerSvc == "" {
		return fmt.Sprintf("<unknown>@%s", r.RemoteAddr)
	}
	if callerNs == "" {
		callerNs = "default"
	}
	return fmt.Sprintf("%s/%s@%s", callerNs, callerSvc, r.RemoteAddr)
}

func jsonStr(v interface{}) string {
	str, _ := json.Marshal(v)
	return string(str)
}

// dumpHeaders 把 http.Header 按 key 排序后序列化为单行 JSON，便于日志检索 / 与 consumer 侧对照。
// 同名多值 header（如 Set-Cookie）以英文逗号拼接。
func dumpHeaders(h http.Header) string {
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

// dumpQuery 把 URL query 参数按 key 排序后序列化为单行 JSON，与 dumpHeaders 同语义、同风格。
// 同名多值参数（?a=1&a=2）以英文逗号拼接。
func dumpQuery(q url.Values) string {
	if len(q) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		out[k] = strings.Join(q[k], ",")
	}
	b, err := json.Marshal(out)
	if err != nil {
		return fmt.Sprintf("%+v", q)
	}
	return string(b)
}
