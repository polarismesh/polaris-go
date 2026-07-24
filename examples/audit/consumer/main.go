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

// Package main 是 callAuditLog 远程验证的主调服务：从远程 Polaris 服务端发现被调实例，
// 真实调用被调 HTTP echo 接口，上报服务调用结果触发 callAuditLog 写审计日志，并校验审计日志已生成。
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

const (
	// discoverRetryMax 发现实例最大重试次数（被调注册到主调发现存在服务端传播延迟，需重试兜底）
	discoverRetryMax = 15
	// discoverRetryInterval 每次发现重试间隔
	discoverRetryInterval = 1 * time.Second
	// auditFlushWait 等待异步审计日志刷盘的时间（callAuditLog 后台 goroutine 写盘）
	auditFlushWait = 2 * time.Second
	// callCalleeTimeout 调用被调 HTTP echo 的超时时间
	callCalleeTimeout = 3 * time.Second
	// defaultRequestDuration 持续请求默认总时长
	defaultRequestDuration = 60 * time.Second
	// defaultRequestInterval 持续请求默认间隔（每隔该时长发起一次调用并上报）
	defaultRequestInterval = 5 * time.Second
)

var (
	// server 远程 Polaris 服务端地址，格式 <host>:<port>，覆盖 polaris.yaml 中的 serverConnector.addresses
	server string
	// configPath polaris.yaml 配置文件路径（需启用 callAuditLog 插件）
	configPath string
	// namespace 被调服务所在命名空间
	namespace string
	// serviceName 被调服务名，远程服务端须已注册该服务且存在健康实例
	serviceName string
	// callerNamespace 主调服务命名空间，写入审计日志的主调方信息；留空则与被调命名空间（-namespace）一致
	callerNamespace string
	// callerService 主调服务名，写入审计日志的主调方信息
	callerService string
	// callerIP 主调方 IP，写入审计日志的主调方 IP；留空则审计日志自动使用 SDK 本机 IP（GetBindIP，与 client_info.json 一致）
	callerIP string
	// method 本次调用的接口方法，写入审计日志
	method string
	// auditLogPath 审计日志文件路径，需与 polaris.yaml 中 callAuditLog.rotateOutputPath 一致
	auditLogPath string
	// requestDuration 持续请求总时长；<=0 表示只请求一次
	requestDuration time.Duration
	// requestInterval 持续请求间隔（每隔该时长发起一次调用并上报）
	requestInterval time.Duration
	// debug 是否开启 Polaris SDK debug 日志
	debug bool
)

// initArgs 初始化命令行参数。
func initArgs() {
	flag.StringVar(&server, "server", "", "远程 Polaris 服务端地址 <host>:<port>（必填），覆盖 polaris.yaml 中的服务端地址")
	flag.StringVar(&configPath, "config", "./consumer/polaris.yaml", "polaris.yaml 配置文件路径（需启用 callAuditLog）")
	flag.StringVar(&namespace, "namespace", "default", "被调服务所在命名空间")
	flag.StringVar(&serviceName, "service", "LogAuditCallee", "被调服务名（远程服务端须已注册并有健康实例）")
	flag.StringVar(&callerNamespace, "caller-namespace", "",
		"主调服务命名空间（写入审计日志主调方信息）；留空则与被调命名空间 -namespace 一致")
	flag.StringVar(&callerService, "caller-service", "caller-service", "主调服务名（写入审计日志主调方信息）")
	flag.StringVar(&callerIP, "caller-ip", "",
		"主调方 IP（写入审计日志）；留空则审计自动使用 SDK 本机 IP GetBindIP()（与 client_info.json 的 Host 一致）")
	flag.StringVar(&method, "method", "/api/demo/get", "本次调用的接口方法（写入审计日志）")
	flag.StringVar(&auditLogPath, "audit-log", "./polaris/log/audit/polaris-audit.log",
		"审计日志文件路径，需与 polaris.yaml 中 callAuditLog.rotateOutputPath 一致")
	flag.DurationVar(&requestDuration, "duration", defaultRequestDuration, "持续请求总时长（如 60s；<=0 表示只请求一次）")
	flag.DurationVar(&requestInterval, "interval", defaultRequestInterval, "持续请求间隔（如 5s）")
	flag.BoolVar(&debug, "debug", false, "是否开启 Polaris SDK debug 日志")
}

// httpClient 调用被调 echo 服务的 HTTP 客户端，带超时避免被调不可达时长时间阻塞。
var httpClient = &http.Client{Timeout: callCalleeTimeout}

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
	consumer, err := newConsumer(configPath, server)
	if err != nil {
		log.Fatalf("create consumerAPI fail: %v", err)
	}
	defer consumer.Destroy()
	log.Printf("[INFO] connected to remote polaris server %s, namespace=%s service=%s", server, namespace, serviceName)

	// 打印 SDK 本机客户端身份（IP/ID）：审计日志的 caller_ip（未显式设置 -caller-ip 时）与 caller_id
	// 即取自这里。其中 client IP 与 client_info.json 的 host 一致；client ID 为 SDK 客户端唯一标识，
	// 标识的是与 client_info.json 同一个 SDK 客户端（该响应缓存文件本身不回显 id）。
	sdkCtx := consumer.SDKContext()
	clientIP := sdkCtx.GetConfig().GetGlobal().GetAPI().GetBindIP()
	clientID := sdkCtx.GetValueContext().GetClientId()
	log.Printf("[INFO] SDK client IP=%s ID=%s（IP 与 client_info.json 的 host 一致；ID 为 SDK 客户端唯一标识；审计日志 caller_ip/caller_id 取自此二者）",
		clientIP, clientID)

	// 3. 先发现被调实例（带重试，容忍注册到发现的服务端传播延迟），确保被调已就绪再进入持续请求
	inst, err := discoverInstance(consumer, namespace, serviceName)
	if err != nil {
		log.Fatalf("discover instance fail: %v", err)
	}
	log.Printf("[INFO] got instance %s:%d id=%s", inst.GetHost(), inst.GetPort(), inst.GetId())

	// 4. 持续请求：复用同一个 ConsumerAPI，按 interval 周期发起「发现→调用→上报」，持续 duration。
	//    每次上报触发 callAuditLog 写一条审计日志。
	reported := runReportLoop(consumer)
	log.Printf("[INFO] 持续请求结束：duration=%v interval=%v，成功上报 %d 次，审计日志预期 %s",
		requestDuration, requestInterval, reported, auditLogPath)

	// 5. 等待异步审计日志刷盘（callAuditLog 后台 goroutine 写盘）
	time.Sleep(auditFlushWait)

	// 6. 读取并校验审计日志：至少有一条记录；并对比行数与上报次数
	data, err := os.ReadFile(auditLogPath)
	if err != nil {
		log.Fatalf("[FAIL] 读取审计日志失败: %v（审计日志未生成，验证失败）", err)
	}
	lines := countNonEmptyLines(data)
	if lines == 0 {
		log.Fatalf("[FAIL] 审计日志为空，验证失败")
	}
	fmt.Println("=== 审计日志内容 ===")
	fmt.Print(string(data))
	fmt.Printf("=== 审计日志共 %d 行，本次成功上报 %d 次 ===\n", lines, reported)
	if lines < reported {
		// callAuditLog 为 best-effort（队列满会丢弃）；正常情况下行数应等于上报次数，
		// 少于时打印告警而非失败，保持 best-effort 语义
		log.Printf("[WARN] 审计日志行数(%d) < 成功上报次数(%d)：可能刷盘延迟或队列满丢弃（best-effort，不保证不丢）",
			lines, reported)
	}
	fmt.Printf("=== 验证通过：审计日志已生成于 %s ===\n", auditLogPath)
}

// newConsumer 加载 polaris.yaml（含 callAuditLog 配置）并用 -server 覆盖服务端地址后创建 ConsumerAPI。
// configPath 配置文件路径；addr 远程服务端地址，覆盖配置文件中的 serverConnector.addresses。
// 返回可用的 polaris.ConsumerAPI，创建失败返回错误。
func newConsumer(configPath, addr string) (polaris.ConsumerAPI, error) {
	cfg, err := config.LoadConfigurationByFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", configPath, err)
	}
	cfg.GetGlobal().GetServerConnector().SetAddresses([]string{addr})
	return polaris.NewConsumerAPIByConfig(cfg)
}

// tryGetInstance 单次发现一个被调实例（不重试）。
// consumer 主调 API；ns 命名空间；svc 服务名。
// 返回发现的实例；发现失败或无可用实例时返回错误。
func tryGetInstance(consumer polaris.ConsumerAPI, ns, svc string) (model.Instance, error) {
	req := &polaris.GetOneInstanceRequest{}
	req.Namespace = ns
	req.Service = svc
	resp, err := consumer.GetOneInstance(req)
	if err != nil {
		return nil, err
	}
	insts := resp.GetInstances()
	if len(insts) == 0 {
		return nil, fmt.Errorf("no instance returned for %s/%s", ns, svc)
	}
	return insts[0], nil
}

// discoverInstance 从远程服务端发现一个被调实例，带重试以容忍被调注册到主调发现的服务端传播延迟。
// consumer 主调 API；ns 命名空间；svc 服务名。
// 返回发现的实例；重试耗尽仍无实例则返回错误。
func discoverInstance(consumer polaris.ConsumerAPI, ns, svc string) (model.Instance, error) {
	var lastErr error
	for i := 0; i < discoverRetryMax; i++ {
		inst, err := tryGetInstance(consumer, ns, svc)
		if err == nil {
			return inst, nil
		}
		lastErr = err
		log.Printf("[WARN] discover %s/%s attempt %d/%d fail: %v", ns, svc, i+1, discoverRetryMax, lastErr)
		time.Sleep(discoverRetryInterval)
	}
	return nil, fmt.Errorf("discover %s/%s after %d attempts: %w", ns, svc, discoverRetryMax, lastErr)
}

// reportOnce 执行一次「发现→调用→上报」：发现被调实例、真实调用其 HTTP echo、上报服务调用结果触发审计。
// consumer 主调 API；seq 本次请求序号（用于日志）。
// 返回本次是否成功上报（true 表示 UpdateServiceCallResult 成功，审计日志预期新增一行）。
func reportOnce(consumer polaris.ConsumerAPI, seq int) bool {
	inst, err := tryGetInstance(consumer, namespace, serviceName)
	if err != nil {
		log.Printf("[WARN] seq=%d 发现实例失败: %v，跳过本次上报", seq, err)
		return false
	}
	delay, retCode, retStatus := callCallee(inst)
	callResult := &polaris.ServiceCallResult{}
	callResult.SetCalledInstance(inst)
	callResult.SetRetStatus(retStatus)
	callResult.SetRetCode(retCode)
	callResult.SetDelay(delay)
	callResult.SetMethod(method)
	// 主调命名空间：显式 -caller-namespace 优先，留空则回退到被调命名空间（-namespace）
	callerNs := callerNamespace
	if callerNs == "" {
		callerNs = namespace
	}
	callResult.SetCallerService(&model.ServiceInfo{Namespace: callerNs, Service: callerService})
	// 仅当用户显式指定 -caller-ip 时才覆盖；留空则审计插件回退到 SDK 本机 IP（GetBindIP，与 client_info.json 一致）
	if callerIP != "" {
		callResult.SetCalledIP(callerIP)
	}
	callResult.SetTimestamp(time.Now())
	if err := consumer.UpdateServiceCallResult(callResult); err != nil {
		log.Printf("[WARN] seq=%d UpdateServiceCallResult 失败: %v", seq, err)
		return false
	}
	log.Printf("[INFO] seq=%d 上报成功 (ret_code=%d ret_status=%v delay=%v)", seq, retCode, retStatus, delay)
	return true
}

// runReportLoop 持续请求主循环：t=0 立即发起一次，之后每 requestInterval 发起一次，直到累计达到
// requestDuration。requestDuration<=0 或 requestInterval<=0 时只请求一次。
// consumer 主调 API（全程复用同一实例，避免频繁创建销毁 SDKContext）。
// 返回成功上报的次数。
func runReportLoop(consumer polaris.ConsumerAPI) int {
	seq := 1
	reported := 0
	if reportOnce(consumer, seq) {
		reported++
	}
	if requestDuration <= 0 || requestInterval <= 0 {
		return reported
	}

	ticker := time.NewTicker(requestInterval)
	defer ticker.Stop()
	timeout := time.After(requestDuration)
	for {
		select {
		case <-timeout:
			return reported
		case <-ticker.C:
			seq++
			if reportOnce(consumer, seq) {
				reported++
			}
		}
	}
}

// countNonEmptyLines 统计字节内容中的非空行数（每条审计记录占一行）。
// data 待统计的字节内容；返回非空行数量。
func countNonEmptyLines(data []byte) int {
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

// callCallee 真实调用被调实例的 HTTP echo 接口，返回调用延迟、返回码与结果状态。
// inst 被调实例；调用失败时降级为构造的失败结果，不阻断审计验证流程（审计只依赖 UpdateServiceCallResult）。
func callCallee(inst model.Instance) (time.Duration, int32, model.RetStatus) {
	url := fmt.Sprintf("http://%s:%d/echo", inst.GetHost(), inst.GetPort())
	start := time.Now()
	resp, err := httpClient.Get(url)
	delay := time.Since(start)
	if err != nil {
		log.Printf("[WARN] call callee %s fail: %v（降级为构造的调用结果，不影响审计验证）", url, err)
		return delay, http.StatusInternalServerError, model.RetFail
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	log.Printf("[INFO] call callee %s ok, status=%d delay=%v", url, resp.StatusCode, delay)
	retStatus := model.RetSuccess
	if resp.StatusCode == http.StatusTooManyRequests {
		retStatus = model.RetFlowControl
	} else if resp.StatusCode >= http.StatusInternalServerError {
		retStatus = model.RetFail
	}
	return delay, int32(resp.StatusCode), retStatus
}
