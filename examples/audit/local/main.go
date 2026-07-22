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
	"os"
	"time"

	"github.com/golang/protobuf/ptypes/wrappers"
	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	"github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"google.golang.org/grpc"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/test/mock"
)

const (
	// mockHost mock Polaris 服务端监听地址
	mockHost = "127.0.0.1"
	// mockPort mock Polaris 服务端端口，需与 polaris.yaml 中 serverConnector.addresses 一致
	mockPort = 18091
	// namespace 测试命名空间
	namespace = "Production"
	// serviceName 测试服务名
	serviceName = "DemoAuditService"
	// auditLogPath 审计日志文件路径，需与 polaris.yaml 中 callAuditLog.rotateOutputPath 一致
	auditLogPath = "./polaris/log/audit/polaris-audit.log"
	// callerServiceName 主调服务名（用于审计日志主调方信息）
	callerServiceName = "caller-service"
	// callerIP 主调方 IP（用于审计日志主调方 IP）
	callerIP = "10.0.1.5"
)

var (
	// debug 是否开启 Polaris SDK debug 日志
	debug bool
)

// initArgs 初始化命令行参数。
func initArgs() {
	flag.BoolVar(&debug, "debug", false, "是否开启 Polaris SDK debug 日志")
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	initArgs()
	flag.Parse()

	if debug {
		if err := api.SetLoggersLevel(api.DebugLog); err != nil {
			log.Printf("[WARN] 设置日志级别为 DEBUG 失败: %v", err)
		} else {
			log.Printf("[INFO] 已设置 Polaris SDK 日志级别为 DEBUG")
		}
	}

	// 1. 启动本地 mock Polaris 服务端（自包含，无需外部 Polaris 部署）
	mockAddr := fmt.Sprintf("%s:%d", mockHost, mockPort)
	grpcServer := startMockServer(mockAddr, namespace, serviceName)
	defer grpcServer.GracefulStop()
	log.Printf("[INFO] mock polaris server listening on %s", mockAddr)

	// 2. 创建 ConsumerAPI（从 polaris.yaml 加载配置，已启用 callAuditLog 插件）
	consumer, err := polaris.NewConsumerAPIByFile("polaris.yaml")
	if err != nil {
		log.Fatalf("create consumerAPI fail: %v", err)
	}
	defer consumer.Destroy()

	// 3. 从 mock 服务端获取一个实例
	getReq := &polaris.GetOneInstanceRequest{}
	getReq.Namespace = namespace
	getReq.Service = serviceName
	resp, err := consumer.GetOneInstance(getReq)
	if err != nil {
		log.Fatalf("GetOneInstance fail: %v", err)
	}
	instances := resp.GetInstances()
	if len(instances) == 0 {
		log.Fatalf("no instance returned from mock server")
	}
	inst := instances[0]
	log.Printf("[INFO] got instance %s:%d id=%s", inst.GetHost(), inst.GetPort(), inst.GetId())

	// 4. 上报服务调用结果，触发 callAuditLog 写审计日志
	callResult := &polaris.ServiceCallResult{}
	callResult.SetCalledInstance(inst)
	callResult.SetRetStatus(model.RetSuccess)
	callResult.SetRetCode(0)
	callResult.SetDelay(35 * time.Millisecond)
	callResult.SetMethod("/api/demo/get")
	callResult.SetCallerService(&model.ServiceInfo{Namespace: namespace, Service: callerServiceName})
	callResult.SetCalledIP(callerIP)
	callResult.SetTimestamp(time.Now())
	if err := consumer.UpdateServiceCallResult(callResult); err != nil {
		log.Fatalf("UpdateServiceCallResult fail: %v", err)
	}
	log.Printf("[INFO] UpdateServiceCallResult done, expecting audit log at %s", auditLogPath)

	// 5. 等待异步审计日志刷盘（callAuditLog 后台 goroutine 写盘）
	time.Sleep(2 * time.Second)

	// 6. 读取并校验审计日志
	data, err := os.ReadFile(auditLogPath)
	if err != nil {
		log.Fatalf("[FAIL] 读取审计日志失败: %v（审计日志未生成，集成测试失败）", err)
	}
	if len(data) == 0 {
		log.Fatalf("[FAIL] 审计日志为空，集成测试失败")
	}
	fmt.Println("=== 审计日志内容 ===")
	fmt.Println(string(data))
	fmt.Printf("=== 集成测试通过：审计日志已生成于 %s ===\n", auditLogPath)
}

// startMockServer 启动本地 mock Polaris gRPC 服务端，注册命名空间、服务与测试实例。
// addr 服务端监听地址；ns 命名空间；svc 服务名。
// 返回 grpc.Server，调用方负责 GracefulStop。
func startMockServer(addr, ns, svc string) *grpc.Server {
	server := grpc.NewServer(grpc.MaxConcurrentStreams(100000))
	mockServer := mock.NewNamingServer()
	// 注册系统发现服务及其实例，供 SDK 连接器寻址
	token := mockServer.RegisterServerService(config.ServerDiscoverService)
	mockServer.RegisterServerInstance(mockHost, mockPort, config.ServerDiscoverService, token, true)
	mockServer.RegisterServerServices(mockHost, mockPort)
	// 注册被测命名空间与服务
	mockServer.RegisterNamespace(&apimodel.Namespace{
		Name:    &wrappers.StringValue{Value: ns},
		Comment: &wrappers.StringValue{Value: "for callAuditLog integration test"},
	})
	testSvc := &service_manage.Service{
		Name:      &wrappers.StringValue{Value: svc},
		Namespace: &wrappers.StringValue{Value: ns},
		Token:     &wrappers.StringValue{Value: "test-token"},
	}
	mockServer.RegisterService(testSvc)
	mockServer.GenTestInstances(testSvc, 1)
	service_manage.RegisterPolarisGRPCServer(server, mockServer)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("mock server listen fail: %v", err)
	}
	go func() {
		if err := server.Serve(listener); err != nil {
			log.Printf("mock server serve exit: %v", err)
		}
	}()
	return server
}
