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

// Package main 混合路由示例 provider。
//
// 同 examples/route/rule/provider，但通过 -metadata 同时声明 lane / env / region
// 三类元数据，配合 lane router、ruleBasedRouter、nearbyBasedRouter 三链共存。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/api"
)

var (
	namespace string
	service   string
	host      string
	port      int
	token     string
	metadata  string
	debug     bool
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "MixedRouteEchoServer", "service")
	flag.IntVar(&port, "port", 0, "service port")
	flag.StringVar(&token, "token", "", "token (北极星开启鉴权时使用)")
	flag.StringVar(&metadata, "metadata", "", "key1=value1&key2=value2")
	flag.BoolVar(&debug, "debug", false, "是否开启 Polaris SDK debug 日志")
}

// convertMetadatas 把 "k1=v1&k2=v2" 转成 map[string]string。空串返回空 map。
func convertMetadatas() map[string]string {
	if len(metadata) == 0 {
		return map[string]string{}
	}
	values := strings.Split(metadata, "&")

	meta := make(map[string]string)
	for i := range values {
		entry := strings.Split(values[i], "=")
		if len(entry) == 2 {
			meta[entry[0]] = entry[1]
		}
	}
	return meta
}

// PolarisProvider 混合路由示例 provider。
type PolarisProvider struct {
	provider  polaris.ProviderAPI
	namespace string
	service   string
	host      string
	port      int
}

// Run 启动 HTTP 服务、注册到 Polaris、阻塞等待退出信号。
func (svr *PolarisProvider) Run() {
	tmpHost, err := getLocalHost(svr.provider.SDKContext().GetConfig().GetGlobal().GetServerConnector().GetAddresses()[0])
	if err != nil {
		panic(fmt.Errorf("error occur while fetching localhost: %v", err))
	}

	host = tmpHost
	svr.host = tmpHost
	svr.runWebServer()
	svr.registerService()
	svr.runMainLoop()
}

// registerService 向 Polaris 注册当前实例，并把命令行 -metadata 透传过去。
func (svr *PolarisProvider) registerService() {
	log.Printf("start to invoke register operation")
	registerRequest := &polaris.InstanceRegisterRequest{}
	registerRequest.Service = service
	registerRequest.Namespace = namespace
	registerRequest.Host = host
	registerRequest.Port = svr.port
	registerRequest.ServiceToken = token
	registerRequest.Metadata = convertMetadatas()
	resp, err := svr.provider.RegisterInstance(registerRequest)
	if err != nil {
		log.Fatalf("fail to register instance, err is %v", err)
	}
	log.Printf("register response: instanceId=%s, host=%s:%d, metadata=%v",
		resp.InstanceID, svr.host, svr.port, registerRequest.Metadata)
}

// deregisterService 进程退出前从 Polaris 反注册。
func (svr *PolarisProvider) deregisterService() {
	log.Printf("start to invoke deregister operation")
	deregisterRequest := &polaris.InstanceDeRegisterRequest{}
	deregisterRequest.Service = service
	deregisterRequest.Namespace = namespace
	deregisterRequest.Host = host
	deregisterRequest.Port = svr.port
	deregisterRequest.ServiceToken = token
	if err := svr.provider.Deregister(deregisterRequest); err != nil {
		log.Fatalf("fail to deregister instance, err is %v", err)
	}
	log.Printf("deregister successfully")
}

// runWebServer 启动一个简易 HTTP 服务，把 metadata 与所在地域信息回显给调用方。
func (svr *PolarisProvider) runWebServer() {
	http.HandleFunc("/echo", func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusOK)
		loc := svr.provider.SDKContext().GetValueContext().GetCurrentLocation().GetLocation()
		locStr, _ := json.Marshal(loc)
		msg := fmt.Sprintf("Hello, I'm MixedRouteEchoServer, metadata=%q, loc=%s, host=%s:%d",
			metadata, string(locStr), svr.host, svr.port)
		log.Printf("[ECHO] from %s, response: %s", r.RemoteAddr, msg)
		_, _ = rw.Write([]byte(msg))
	})

	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", svr.port))
	if err != nil {
		log.Fatalf("[ERROR] fail to listen tcp, err is %v", err)
	}
	svr.port = ln.Addr().(*net.TCPAddr).Port
	go func() {
		log.Printf("[INFO] start http server, listen port is %v", svr.port)
		if err := http.Serve(ln, nil); err != nil {
			log.Fatalf("[ERROR] fail to run webServer, err is %v", err)
		}
	}()
}

// runMainLoop 等待退出信号，触发反注册后 return。
func (svr *PolarisProvider) runMainLoop() {
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

	svr := &PolarisProvider{
		provider:  provider,
		namespace: namespace,
		service:   service,
		host:      host,
		port:      port,
	}
	svr.Run()
}

// getLocalHost 通过建立到 Polaris 服务端的 TCP 连接，反推本机能与之通信的 IP，
// 用于向 Polaris 注册时上报真实 host。
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
