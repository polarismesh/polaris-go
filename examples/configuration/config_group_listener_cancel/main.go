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

// Package main 通过真实 Polaris 配置发布验证可取消监听器和 access 风格的缓存生命周期。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/polarismesh/polaris-go/api"
)

var debug bool

// options 描述测试环境和加速后的缓存生命周期；token 仅从环境变量读取。
type options struct {
	server, discoveryAddr, configAddr, openAPI string
	namespace, listenerMode                    string
	rounds, fetchers                           int
	ttl, stopTimeout, timeout, runTimeout      time.Duration
}

// defaultOptions 读取非敏感环境参数，测试数据始终使用运行时随机生成的分组名。
func defaultOptions() options {
	return options{
		server: os.Getenv("POLARIS_SERVER"), discoveryAddr: os.Getenv("POLARIS_DISCOVERY_ADDR"),
		configAddr: os.Getenv("POLARIS_CONFIG_ADDR"), openAPI: os.Getenv("POLARIS_OPENAPI"),
		namespace: "default", listenerMode: "cancelable", rounds: 20, fetchers: 8,
		ttl: 100 * time.Millisecond, stopTimeout: 100 * time.Millisecond,
		timeout: 20 * time.Second, runTimeout: 5 * time.Minute,
	}
}

// initArgs 注册连接参数、压力参数和 SDK debug 开关。
func initArgs(o *options) {
	flag.StringVar(&o.server, "server", o.server, "Polaris 主机名/IP（POLARIS_SERVER）")
	flag.StringVar(&o.discoveryAddr, "discovery-addr", o.discoveryAddr, "发现 gRPC host:port，默认 server:8091")
	flag.StringVar(&o.configAddr, "config-addr", o.configAddr, "配置 gRPC host:port，默认 server:8093")
	flag.StringVar(&o.openAPI, "openapi", o.openAPI, "管理 HTTP URL，默认 http://server:8090")
	flag.StringVar(&o.namespace, "namespace", o.namespace, "已存在且允许写入的测试命名空间")
	flag.StringVar(&o.listenerMode, "listener-mode", o.listenerMode, "cancelable；legacy 用于复现泄漏，预期 FAIL")
	flag.IntVar(&o.rounds, "rounds", o.rounds, "TTL 过期重建轮数")
	flag.IntVar(&o.fetchers, "fetchers", o.fetchers, "每轮共享同一 SDK 配置组的缓存数量")
	flag.DurationVar(&o.ttl, "ttl", o.ttl, "模拟缓存 TTL（缩短实际 120 秒/6 分钟周期）")
	flag.DurationVar(&o.stopTimeout, "stop-timeout", o.stopTimeout, "模拟 Stop 等待后台协程的上限")
	flag.DurationVar(&o.timeout, "timeout", o.timeout, "单步事件和 GC 验证超时")
	flag.DurationVar(&o.runTimeout, "run-timeout", o.runTimeout, "整套 E2E 超时")
	flag.BoolVar(&debug, "debug", false, "是否开启 Polaris SDK debug 日志")
}

// validate 补齐默认端口并拒绝无效参数；不连接服务端，也不创建测试数据。
func (o *options) validate() error {
	if o.server != "" {
		if strings.Contains(o.server, "/") {
			return fmt.Errorf("server 只接受主机名/IP，自定义端口请使用 discovery-addr/config-addr/openapi")
		}
		if o.discoveryAddr == "" {
			o.discoveryAddr = net.JoinHostPort(o.server, "8091")
		}
		if o.configAddr == "" {
			o.configAddr = net.JoinHostPort(o.server, "8093")
		}
		if o.openAPI == "" {
			o.openAPI = "http://" + net.JoinHostPort(o.server, "8090")
		}
	}
	for _, addr := range []string{o.discoveryAddr, o.configAddr} {
		host, port, err := net.SplitHostPort(addr)
		if err != nil || host == "" || port == "" {
			return fmt.Errorf("请提供 -server/POLARIS_SERVER 或完整的三个服务地址，无效 gRPC 地址 %q", addr)
		}
	}
	u, err := url.Parse(o.openAPI)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("openapi 必须为不含凭据和查询参数的 HTTP(S) URL")
	}
	if o.namespace == "" || o.rounds < 1 || o.fetchers < 1 || o.ttl <= 0 || o.stopTimeout <= 0 ||
		o.timeout <= o.stopTimeout || o.runTimeout <= o.timeout {
		return fmt.Errorf("namespace 不能为空，轮数/并发数/TTL 必须为正，run-timeout > timeout > stop-timeout > 0")
	}
	if o.listenerMode != "cancelable" && o.listenerMode != "legacy" {
		return fmt.Errorf("listener-mode 仅支持 cancelable 或 legacy")
	}
	return nil
}

// main 执行完整 E2E；业务和清理完成后才按结果退出，失败返回非零状态。
func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	o := defaultOptions()
	initArgs(&o)
	flag.Parse()
	if debug {
		if err := api.SetLoggersLevel(api.DebugLog); err != nil {
			log.Printf("[WARN] 设置日志级别为 DEBUG 失败: %v", err)
		} else {
			log.Printf("[INFO] 已设置 Polaris SDK 日志级别为 DEBUG")
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := runE2E(ctx, o)
	stop()
	if err != nil {
		log.Printf("FAIL: %v", err)
		os.Exit(1)
	}
	log.Print("PASS: 配置组监听取消 E2E 全部通过")
}
