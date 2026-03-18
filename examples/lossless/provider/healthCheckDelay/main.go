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
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/polarismesh/polaris-go"
)

var (
	namespace string
	service   string
	token     string
	port      int64
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "LosslessHealthDelayServer", "service")
	// 当北极星开启鉴权时，需要配置此参数完成相关的权限检查
	flag.StringVar(&token, "token", "", "token")
	flag.Int64Var(&port, "port", 19080, "port")
}

// PolarisProvider is an example of provider
type PolarisProvider struct {
	provider         polaris.ProviderAPI
	namespace        string
	service          string
	host             string
	port             int
	isShutdown       bool
	webSvr           *http.Server
	healthCheckCount int32 // 健康检查计数器（使用int32以支持原子操作）
	needErr          int32 // 是否需要返回错误状态码（使用int32以支持原子操作）
}

// Run starts the provider
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
	http.HandleFunc("/health", func(rw http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&svr.healthCheckCount, 1)
		if count == 1 {
			// 第一次健康检查返回失败
			rw.WriteHeader(http.StatusServiceUnavailable)
			msg := fmt.Sprintf("Health check failed (first attempt), host: %s:%d", svr.host, svr.port)
			log.Printf("get health request from client address: %s, response: %s (503)", r.RemoteAddr, msg)
			_, _ = rw.Write([]byte(msg))
			return
		}
		// 后续健康检查返回成功
		rw.WriteHeader(http.StatusOK)
		msg := fmt.Sprintf("Hello, I'm DiscoverEchoServer Provider, My host : %s:%d", svr.host, svr.port)
		log.Printf("get health request from client address: %s, response:%s", r.RemoteAddr, msg)
		_, _ = rw.Write([]byte(msg))
	})

	http.HandleFunc("/echo", func(rw http.ResponseWriter, r *http.Request) {
		var msg string
		if atomic.LoadInt32(&svr.needErr) == 1 {
			msg = fmt.Sprintf("status code: 500, Fatal, My host : %s:%d", svr.host, svr.port)
			rw.WriteHeader(http.StatusInternalServerError)
		} else {
			msg = fmt.Sprintf("status code: 200, Hello, My host : %s:%d", svr.host, svr.port)
			rw.WriteHeader(http.StatusOK)
		}
		log.Printf("get echo request from client address: %s, response:%s", r.RemoteAddr, msg)
		_, _ = rw.Write([]byte(msg))
	})

	http.HandleFunc("/switch", func(rw http.ResponseWriter, r *http.Request) {
		var msg string
		val := r.URL.Query().Get("openError")
		if val == "true" {
			atomic.StoreInt32(&svr.needErr, 1)
			msg = fmt.Sprintf("echo request status code set to 500")
		} else {
			atomic.StoreInt32(&svr.needErr, 0)
			msg = fmt.Sprintf("echo request status code set to 200")
		}
		log.Printf("get switch request from client address: %s, response:%s", r.RemoteAddr, msg)
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte(msg))
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
			if !svr.isShutdown {
				log.Fatalf("[ERROR]fail to run webServer, err is %v", err)
			} else {
				// 正常关闭时，只打印日志，不要 Fatalf
				log.Printf("[ERROR]fail to run webServer, err is %v", err)
			}
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
	registerRequest.SetTTL(1)
	log.Printf("registerRequest: %+v", jsonStr(registerRequest))
	resp, err := svr.provider.LosslessRegister(registerRequest)
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
	initArgs()
	flag.Parse()
	if len(namespace) == 0 || len(service) == 0 {
		log.Print("namespace and service are required")
		return
	}
	provider, err := polaris.NewProviderAPI()
	// 或者使用以下方法,则不需要创建配置文件
	// provider, err = api.NewProviderAPIByAddress("127.0.0.1:8091")

	if err != nil {
		log.Fatalf("fail to create providerAPI, err is %v", err)
	}
	defer provider.Destroy()
	svr := &PolarisProvider{
		provider:  provider,
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

func providedInstanceId(namespace, service, host string, port int) string {
	return fmt.Sprintf("%s#%s#%s#%d", namespace, service, host, port)
}

func jsonStr(v interface{}) string {
	str, _ := json.Marshal(v)
	return string(str)
}
