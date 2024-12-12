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
	"context"
	"flag"
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/config"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/polarismesh/polaris-go"
)

var (
	namespace  string
	service    string
	host       string
	token      string
	metadata   string
	port       int
	configPath string
)

func initArgs() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&service, "service", "LoadBalanceEchoServer", "service")
	// 当北极星开启鉴权时，需要配置此参数完成相关的权限检查
	flag.StringVar(&token, "token", "", "token")
	flag.StringVar(&metadata, "metadata", "", "key1=value1&key2=value2")
	flag.IntVar(&port, "port", 0, "port")
	flag.StringVar(&host, "host", "", "host")
	flag.StringVar(&configPath, "config", "./polaris.yaml", "path for config file")
}

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

// PolarisProvider is a provider for route
type PolarisProvider struct {
	provider  polaris.ProviderAPI
	namespace string
	service   string
	host      string
	port      int
	cancels   []context.CancelFunc
}

// Run . execute
func (svr *PolarisProvider) Run() {
	if len(host) == 0 {
		tmpHost, err := getLocalHost(svr.provider.SDKContext().GetConfig().GetGlobal().GetServerConnector().GetAddresses()[0])
		if nil != err {
			panic(fmt.Errorf("error occur while fetching localhost: %v", err))
		}

		host = tmpHost
		svr.host = tmpHost
	}
	svr.runWebServer()
	svr.registerService()
	svr.runMainLoop()
}

func (svr *PolarisProvider) registerService() {
	log.Printf("start to invoke register operation")
	registerRequest := &polaris.InstanceRegisterRequest{}
	registerRequest.Service = service
	registerRequest.Namespace = namespace
	registerRequest.Host = host
	registerRequest.Port = svr.port
	registerRequest.ServiceToken = token
	registerRequest.Metadata = convertMetadatas()
	registerRequest.SetTTL(1)
	resp, err := svr.provider.RegisterInstance(registerRequest)
	if nil != err {
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

func (svr *PolarisProvider) runWebServer() {
	http.HandleFunc("/echo", func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusOK)
		msg := fmt.Sprintf("Hello, I'm %s Provider, My host : %s:%d\n", svr.service, svr.host, svr.port)
		_, _ = rw.Write([]byte(msg))
	})

	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", svr.port))
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

func (svr *PolarisProvider) runMainLoop() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, []os.Signal{
		syscall.SIGINT, syscall.SIGTERM,
		syscall.SIGSEGV,
	}...)

	for s := range ch {
		log.Printf("catch signal(%+v), stop servers", s)

		for i := range svr.cancels {
			svr.cancels[i]()
		}

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
	cfg, err := config.LoadConfigurationByFile(configPath)
	if err != nil {
		log.Fatalf("load configuration by file %s failed: %v", configPath, err)
	}
	sdkCtx, err := polaris.NewSDKContextByConfig(cfg)
	if err != nil {
		log.Fatalf("fail to create sdkContext, err is %v", err)
	}
	defer sdkCtx.Destroy()

	provider := polaris.NewProviderAPIByContext(sdkCtx)
	defer provider.Destroy()

	svr := &PolarisProvider{
		provider:  provider,
		namespace: namespace,
		service:   service,
		host:      host,
		port:      port,
		cancels:   make([]context.CancelFunc, 0),
	}
	svr.Run()
}

func getLocalHost(serverAddr string) (string, error) {
	conn, err := net.Dial("tcp", serverAddr)
	if nil != err {
		return "", err
	}
	localAddr := conn.LocalAddr().String()
	colonIdx := strings.LastIndex(localAddr, ":")
	if colonIdx > 0 {
		return localAddr[:colonIdx], nil
	}
	return localAddr, nil
}
