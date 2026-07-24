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

// Package main 是 callAuditLog 远程验证的被调服务：注册实例到远程 Polaris 服务端，
// 并对外提供 HTTP echo 服务，供主调（consumer）发现并调用，从而触发审计日志写盘。
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

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
)

const (
	// defaultInstanceWeight 注册实例默认权重，范围 0-10000；权重为 0 的实例会被负载均衡过滤，导致主调无法发现
	defaultInstanceWeight = 100
)

var (
	// server 远程 Polaris 服务端地址，格式 <host>:<port>，覆盖 polaris.yaml 中的 serverConnector.addresses
	server string
	// configPath polaris.yaml 配置文件路径
	configPath string
	// namespace 被调服务所在命名空间
	namespace string
	// service 被调服务名，注册到远程服务端供主调发现
	service string
	// token 服务访问 Token，服务端开鉴权时必填
	token string
	// port 被调 HTTP echo 服务监听端口，0 表示由系统分配随机端口
	port int
	// debug 是否开启 Polaris SDK debug 日志
	debug bool
)

// initArgs 初始化命令行参数。
func initArgs() {
	flag.StringVar(&server, "server", "", "远程 Polaris 服务端地址 <host>:<port>（必填），覆盖 polaris.yaml 中的服务端地址")
	flag.StringVar(&configPath, "config", "./provider/polaris.yaml", "polaris.yaml 配置文件路径")
	flag.StringVar(&namespace, "namespace", "default", "被调服务所在命名空间")
	flag.StringVar(&service, "service", "LogAuditCallee", "被调服务名（注册到远程服务端供主调发现）")
	flag.StringVar(&token, "token", "", "服务访问 Token（服务端开鉴权时必填）")
	flag.IntVar(&port, "port", 0, "被调 HTTP echo 服务监听端口，0 表示由系统分配随机端口")
	flag.BoolVar(&debug, "debug", false, "是否开启 Polaris SDK debug 日志")
}

// PolarisProvider 被调服务：注册实例到 Polaris 并对外提供 HTTP echo 服务。
type PolarisProvider struct {
	provider  polaris.ProviderAPI
	namespace string
	service   string
	host      string
	port      int
	token     string
	webSvr    *http.Server
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

	// 2. 创建连接远程服务端的 ProviderAPI（用 -server 覆盖 polaris.yaml 中的服务端地址）
	provider, err := newProvider(configPath, server)
	if err != nil {
		log.Fatalf("create providerAPI fail: %v", err)
	}
	defer provider.Destroy()

	// 3. 获取本机出口 IP，作为注册实例的 Host（主调需通过该地址访问被调）
	host, err := getLocalHost(server)
	if err != nil {
		log.Fatalf("get local host fail: %v", err)
	}

	svr := &PolarisProvider{
		provider:  provider,
		namespace: namespace,
		service:   service,
		host:      host,
		token:     token,
	}

	// 4. 启动 HTTP echo 服务（被调业务进程）
	if err := svr.runEchoServer(port); err != nil {
		log.Fatalf("run echo server fail: %v", err)
	}

	// 5. 注册被调实例到远程服务端（RegisterInstance 自动开启后台心跳保活）
	if err := svr.register(); err != nil {
		log.Fatalf("register instance fail: %v", err)
	}
	log.Printf("[INFO] provider registered: namespace=%s service=%s host=%s port=%d", namespace, service, svr.host, svr.port)
	// ready 标记，供 verify.sh 检测注册完成（注册成功即可被发现，无需等待心跳）
	fmt.Printf("[PROVIDER_READY] host=%s port=%d\n", svr.host, svr.port)

	// 6. 长驻等待退出信号，收到后 deregister 并关闭 HTTP 服务
	svr.waitSignal()
}

// newProvider 加载 polaris.yaml 并用 -server 覆盖服务端地址后创建 ProviderAPI。
// configPath 配置文件路径；addr 远程服务端地址，覆盖配置文件中的 serverConnector.addresses。
// 返回可用的 polaris.ProviderAPI，创建失败返回错误。
func newProvider(configPath, addr string) (polaris.ProviderAPI, error) {
	cfg, err := config.LoadConfigurationByFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", configPath, err)
	}
	cfg.GetGlobal().GetServerConnector().SetAddresses([]string{addr})
	return polaris.NewProviderAPIByConfig(cfg)
}

// runEchoServer 启动 HTTP echo 服务（被调业务进程），监听 listenPort（0 表示随机端口）。
// 监听成功后回填实际端口到 svr.port；监听与对外服务失败返回错误。
func (svr *PolarisProvider) runEchoServer(listenPort int) error {
	http.HandleFunc("/echo", func(rw http.ResponseWriter, r *http.Request) {
		msg := fmt.Sprintf("Hello, I'm %s Provider, host=%s:%d", svr.service, svr.host, svr.port)
		log.Printf("get echo request from %s, response:%s", r.RemoteAddr, msg)
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte(msg))
	})

	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", listenPort))
	if err != nil {
		return fmt.Errorf("listen tcp: %w", err)
	}
	svr.port = ln.Addr().(*net.TCPAddr).Port

	svr.webSvr = &http.Server{}
	go func() {
		log.Printf("[INFO] echo server listening on %d", svr.port)
		if err := svr.webSvr.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Fatalf("echo server serve fail: %v", err)
		}
	}()
	return nil
}

// register 注册被调实例到远程 Polaris 服务端。
// RegisterInstance 自动开启后台心跳保活（AutoHeartbeat），无需手动上报心跳。
// 注册失败返回错误。
func (svr *PolarisProvider) register() error {
	req := &polaris.InstanceRegisterRequest{}
	req.Service = svr.service
	req.Namespace = svr.namespace
	req.Host = svr.host
	req.Port = svr.port
	req.ServiceToken = svr.token
	req.SetTTL(5)
	// 显式声明实例健康与权重，确保主调 GetOneInstance 可发现
	// （避免依赖服务端默认值，部分服务端默认权重 0 会被负载均衡过滤）
	req.SetHealthy(true)
	weight := defaultInstanceWeight
	req.Weight = &weight
	resp, err := svr.provider.RegisterInstance(req)
	if err != nil {
		return fmt.Errorf("register: %w", err)
	}
	log.Printf("[INFO] register response: instanceId=%s", resp.InstanceID)
	return nil
}

// deregister 反注册被调实例，进程退出前调用以释放服务端实例。
func (svr *PolarisProvider) deregister() {
	req := &polaris.InstanceDeRegisterRequest{}
	req.Service = svr.service
	req.Namespace = svr.namespace
	req.Host = svr.host
	req.Port = svr.port
	req.ServiceToken = svr.token
	if err := svr.provider.Deregister(req); err != nil {
		log.Printf("[WARN] deregister fail: %v", err)
		return
	}
	log.Printf("[INFO] deregister successfully")
}

// waitSignal 阻塞等待 SIGINT/SIGTERM，收到后 deregister 并关闭 HTTP 服务。
func (svr *PolarisProvider) waitSignal() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	s := <-ch
	log.Printf("catch signal(%v), stop provider", s)
	svr.deregister()
	_ = svr.webSvr.Close()
}

// getLocalHost 通过 dial 远程服务端获取本机出口 IP，作为注册实例的 Host。
// serverAddr 远程服务端地址 <host>:<port>；返回出口 IP，dial 失败返回错误。
func getLocalHost(serverAddr string) (string, error) {
	conn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	localAddr := conn.LocalAddr().String()
	if idx := strings.LastIndex(localAddr, ":"); idx > 0 {
		return localAddr[:idx], nil
	}
	return localAddr, nil
}
