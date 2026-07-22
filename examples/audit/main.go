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
	"os"
	"time"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

var (
	// server 远程 Polaris 服务端地址，格式 <host>:<port>，覆盖 polaris.yaml 中的 serverConnector.addresses
	server string
	// namespace 被调服务所在命名空间
	namespace string
	// serviceName 被调服务名，远程服务端须已注册该服务且存在健康实例
	serviceName string
	// callerService 主调服务名，写入审计日志的主调方信息
	callerService string
	// callerIP 主调方 IP，写入审计日志的主调方 IP
	callerIP string
	// method 本次调用的接口方法，写入审计日志
	method string
	// auditLogPath 审计日志文件路径，需与 polaris.yaml 中 callAuditLog.rotateOutputPath 一致
	auditLogPath string
	// debug 是否开启 Polaris SDK debug 日志
	debug bool
)

// initArgs 初始化命令行参数。
func initArgs() {
	flag.StringVar(&server, "server", "", "远程 Polaris 服务端地址，格式 <host>:<port>（必填），覆盖 polaris.yaml 中的服务端地址")
	flag.StringVar(&namespace, "namespace", "default", "被调服务所在命名空间")
	flag.StringVar(&serviceName, "service", "DemoService", "被调服务名（远程服务端须已注册并有健康实例）")
	flag.StringVar(&callerService, "caller-service", "caller-service", "主调服务名（写入审计日志主调方信息）")
	flag.StringVar(&callerIP, "caller-ip", "10.0.1.5", "主调方 IP（写入审计日志主调方 IP）")
	flag.StringVar(&method, "method", "/api/demo/get", "本次调用的接口方法（写入审计日志）")
	flag.StringVar(&auditLogPath, "audit-log", "./polaris/log/audit/polaris-audit.log",
		"审计日志文件路径，需与 polaris.yaml 中 callAuditLog.rotateOutputPath 一致")
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

	// 1. 校验必填参数：远程服务端地址
	if server == "" {
		log.Printf("[FAIL] 缺少必填参数 -server（远程 Polaris 服务端地址）")
		flag.Usage()
		os.Exit(1)
	}

	// 2. 创建连接远程服务端的 ConsumerAPI（保留 polaris.yaml 中的 callAuditLog 插件配置）
	consumer := newRemoteConsumer(server)
	defer consumer.Destroy()
	log.Printf("[INFO] connected to remote polaris server %s, namespace=%s service=%s", server, namespace, serviceName)

	// 3. 从远程服务端获取一个实例
	getReq := &polaris.GetOneInstanceRequest{}
	getReq.Namespace = namespace
	getReq.Service = serviceName
	resp, err := consumer.GetOneInstance(getReq)
	if err != nil {
		log.Fatalf("GetOneInstance fail: %v", err)
	}
	instances := resp.GetInstances()
	if len(instances) == 0 {
		log.Fatalf("no instance returned from remote server for %s/%s（请确认远程服务端已注册该服务且存在健康实例）",
			namespace, serviceName)
	}
	inst := instances[0]
	log.Printf("[INFO] got instance %s:%d id=%s", inst.GetHost(), inst.GetPort(), inst.GetId())

	// 4. 上报服务调用结果，触发 callAuditLog 写审计日志
	callResult := &polaris.ServiceCallResult{}
	callResult.SetCalledInstance(inst)
	callResult.SetRetStatus(model.RetSuccess)
	callResult.SetRetCode(0)
	callResult.SetDelay(35 * time.Millisecond)
	callResult.SetMethod(method)
	callResult.SetCallerService(&model.ServiceInfo{Namespace: namespace, Service: callerService})
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
		log.Fatalf("[FAIL] 读取审计日志失败: %v（审计日志未生成，验证失败）", err)
	}
	if len(data) == 0 {
		log.Fatalf("[FAIL] 审计日志为空，验证失败")
	}
	fmt.Println("=== 审计日志内容 ===")
	fmt.Println(string(data))
	fmt.Printf("=== 验证通过：审计日志已生成于 %s ===\n", auditLogPath)
}

// newRemoteConsumer 基于 polaris.yaml 创建连接远程服务端的 ConsumerAPI。
// addr 远程服务端地址（<host>:<port>），用于覆盖配置文件中的 serverConnector.addresses；
// 从文件加载配置可保留 callAuditLog 插件配置，仅替换服务端地址。
// 创建失败时直接终止进程；返回可用的 polaris.ConsumerAPI，调用方负责 Destroy。
func newRemoteConsumer(addr string) polaris.ConsumerAPI {
	cfg, err := config.LoadConfigurationByFile("polaris.yaml")
	if err != nil {
		log.Fatalf("load polaris.yaml fail: %v", err)
	}
	// 用命令行传入的远程地址覆盖配置文件中的服务端地址
	cfg.GetGlobal().GetServerConnector().SetAddresses([]string{addr})
	consumer, err := polaris.NewConsumerAPIByConfig(cfg)
	if err != nil {
		log.Fatalf("create consumerAPI fail: %v", err)
	}
	return consumer
}
