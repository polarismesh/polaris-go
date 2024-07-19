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
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/pkg/model"
)

var (
	namespace string
	service   string
	token     string
	port      int64
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "RateLimitEchoServer", "service")
	// 当北极星开启鉴权时，需要配置此参数完成相关的权限检查
	flag.StringVar(&token, "token", "", "token")
	flag.Int64Var(&port, "port", 0, "port")
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
	http.HandleFunc("/echo", func(rw http.ResponseWriter, r *http.Request) {
		quotaReq := polaris.NewQuotaRequest().(*model.QuotaRequestImpl)
		quotaReq.SetMethod("/echo")
		headers := convertHeaders(r.Header)
		for k, v := range headers {
			quotaReq.AddArgument(model.BuildHeaderArgument(strings.ToLower(k), v))
		}
		quotaReq.SetNamespace(namespace)
		quotaReq.SetService(service)

		log.Printf("[info] get quota req : ns=%s, svc=%s, method=%v, labels=%v",
			quotaReq.GetNamespace(), quotaReq.GetService(), quotaReq.GetMethod(), quotaReq.GetLabels())
		start := time.Now()
		resp, err := svr.limiter.GetQuota(quotaReq)
		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[error] fail to GetQuota, err is %v", err)))
			return
		}
		log.Printf("[info] %s get quota resp : code=%d, info=%s", time.Since(start).String(), resp.Get().Code, resp.Get().Info)

		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(fmt.Sprintf("[error] fail to GetQuota, err is %v", err)))
			return
		}

		if resp.Get().Code != model.QuotaResultOk {
			rw.WriteHeader(http.StatusTooManyRequests)
			_, _ = rw.Write([]byte(http.StatusText(http.StatusTooManyRequests)))
			return
		}

		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte(fmt.Sprintf("Hello, I'm RateLimitEchoServer Provider, My host : %s:%d", svr.host, svr.port)))
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
