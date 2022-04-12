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
	"strings"
	"time"

	"github.com/polarismesh/polaris-go/api"
)

var (
	namespace string
	service   string
	host      string
	port      int
	token     string
)

func initArgs() {
	// 设置 provier 所在的命名空间
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	// 设置 provider 的服务名称
	flag.StringVar(&service, "service", "EchoServerGolang", "service")
	// 设置 consumer 的 http-server 的监听端口
	flag.IntVar(&port, "port", 7879, "port")
	// 当北极星开启鉴权时，需要配置此参数完成相关的权限检查
	flag.StringVar(&token, "token", "", "token")
}

// PolarisProvider 被调服务的封装，该 Provider 是一个 http 服务
type PolarisProvider struct {
	provider  api.ProviderAPI
	namespace string
	service   string
	host      string
	port      int
}

// Run 启动 provider
// step 1: 获取 provier 的 IP 信息
// step 2: 将 provier 注册到北极星中
// step 3: 启动 provider 的 http 服务
func (svr *PolarisProvider) Run() {
	tmpHost, err := getLocalHost(svr.provider.SDKContext().GetConfig().GetGlobal().GetServerConnector().GetAddresses()[0])
	if err != nil {
		panic(fmt.Errorf("error occur while fetching localhost: %v", err))
	}

	host = tmpHost
	svr.registerService()
	svr.runWebServer()
}

// runWebServer 启动 http-server 服务，提供 /echo 路径进行调用
func (svr *PolarisProvider) runWebServer() {
	http.HandleFunc("/echo", func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte("Hello, I'm EchoServerGolang Provider"))
	})

	if err := http.ListenAndServe(fmt.Sprintf("0.0.0.0:%d", svr.port), nil); err != nil {
		log.Fatalf("[ERROR]fail to run webServer, err is %v", err)
	}
}

// registerService 将 provider 注册到北极星当中
func (svr *PolarisProvider) registerService() {
	log.Printf("start to invoke register operation")
	registerRequest := &api.InstanceRegisterRequest{}
	registerRequest.Service = service
	registerRequest.Namespace = namespace
	registerRequest.Host = host
	registerRequest.Port = port
	registerRequest.ServiceToken = token
	registerRequest.SetTTL(10)
	resp, err := svr.provider.Register(registerRequest)
	if err != nil {
		log.Fatalf("fail to register instance, err is %v", err)
	}
	log.Printf("register response: instanceId %s", resp.InstanceID)

	go svr.doHeartbeat()
}

// doHeartbeat 进行心跳上报，维持该 provider 在北极星的健康状态
func (svr *PolarisProvider) doHeartbeat() {
	log.Printf("start to invoke heartbeat operation")
	ticker := time.NewTicker(time.Duration(5 * time.Second))
	defer ticker.Stop()
	for range ticker.C {
		heartbeatRequest := &api.InstanceHeartbeatRequest{}
		heartbeatRequest.Namespace = namespace
		heartbeatRequest.Service = service
		heartbeatRequest.Host = host
		heartbeatRequest.Port = port
		heartbeatRequest.ServiceToken = token
		err := svr.provider.Heartbeat(heartbeatRequest)
		if err != nil {
			log.Printf("[ERROR] fail to heartbeat instance, err is %v", err)
		}
	}

}

func main() {
	initArgs()
	flag.Parse()
	if len(namespace) == 0 || len(service) == 0 {
		log.Print("namespace and service are required")
		return
	}
	provider, err := api.NewProviderAPI()
	// 或者使用以下方法,则不需要创建配置文件
	//provider, err = api.NewProviderAPIByAddress("127.0.0.1:8091")

	if err != nil {
		log.Fatalf("fail to create consumerAPI, err is %v", err)
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
