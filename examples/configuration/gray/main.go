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

// Package main 演示 polaris-go 客户端参与配置灰度发布。
//
// 灰度规则的匹配与灰度内容的下发完全由服务端承担，客户端仅需在
// global.client.labels 中上报标签，SDK 会在拉取配置时将标签携带到
// GetConfigFile 请求中。本程序提供两种运行模式：
//   - setup: 创建并发布一份全量基线配置，供验证流程初始化使用
//   - run  : 作为配置客户端常驻运行，拉取配置并订阅变更，同时暴露
//     HTTP 接口供验证脚本观察当前生效内容（灰度版本或全量版本）
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
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

const (
	// ActionSetup 创建并发布全量基线配置后退出。
	ActionSetup = "setup"
	// ActionRun 作为配置客户端常驻运行。
	ActionRun = "run"
)

var (
	debug      bool
	action     string
	configPath string
	namespace  string
	fileGroup  string
	fileName   string
	content    string
	port       string
)

// initArgs 注册命令行参数。
func initArgs() {
	flag.BoolVar(&debug, "debug", false, "是否开启 Polaris SDK debug 日志")
	flag.StringVar(&action, "action", ActionRun, "执行模式: setup(创建并发布基线配置) | run(作为客户端运行)")
	flag.StringVar(&configPath, "config", "./polaris.yaml", "polaris.yaml 配置文件路径")
	flag.StringVar(&namespace, "namespace", "default", "命名空间")
	flag.StringVar(&fileGroup, "group", "polaris-config-example", "配置文件组")
	flag.StringVar(&fileName, "file", "gray-example.yaml", "配置文件名")
	flag.StringVar(&content, "content", "normal-content-v1", "setup 模式下写入的配置内容")
	flag.StringVar(&port, "port", ":18081", "run 模式下 HTTP 监听地址")
}

func main() {
	// 设置日志输出格式：日期 + 时间 + 微秒 + 文件名:行号
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
	initArgs()
	flag.Parse()

	if debug {
		if err := api.SetLoggersLevel(api.DebugLog); err != nil {
			log.Printf("[WARN] 设置日志级别为 DEBUG 失败: %v", err)
		} else {
			log.Printf("[INFO] 已设置 Polaris SDK 日志级别为 DEBUG")
		}
	}

	switch action {
	case ActionSetup:
		runSetup()
	case ActionRun:
		runClient()
	default:
		log.Fatalf("未知 action: %s (支持: setup, run)", action)
	}
}

// runSetup 准备一份全量基线配置，供验证流程初始化使用。
// 若配置文件已存在且内容与期望一致则跳过(不重复创建)；已存在但内容不一致则更新；
// 不存在则创建。最后发布全量版本。这样可重复执行而不会因文件已存在触发服务端冲突。
func runSetup() {
	cfg, err := config.LoadConfigurationByFile(configPath)
	if err != nil {
		log.Fatalf("[Setup] 加载配置 %s 失败: %v", configPath, err)
	}
	sdkCtx, err := polaris.NewSDKContextByConfig(cfg)
	if err != nil {
		log.Fatalf("[Setup] 创建 SDKContext 失败: %v", err)
	}
	defer sdkCtx.Destroy()

	configAPI := polaris.NewConfigAPIByContext(sdkCtx)
	log.Printf("[Setup] 准备配置文件: %s/%s/%s, 期望内容=%q", namespace, fileGroup, fileName, content)

	// 先探测配置文件是否已存在，已存在则不重复创建。
	existing, fetchErr := configAPI.FetchConfigFile(&polaris.GetConfigFileRequest{
		GetConfigFileRequest: &model.GetConfigFileRequest{
			Namespace: namespace,
			FileGroup: fileGroup,
			FileName:  fileName,
		},
	})
	if fetchErr == nil && existing.HasContent() {
		// 配置文件已存在且有内容
		if existing.GetContent() == content {
			log.Printf("[Setup] 配置文件已存在且内容一致(content=%q)，跳过创建与发布", content)
			return
		}
		log.Printf("[Setup] 配置文件已存在(当前内容=%q)，更新为 %q", existing.GetContent(), content)
		if err := configAPI.UpdateConfigFile(namespace, fileGroup, fileName, content); err != nil {
			log.Fatalf("[Setup] 更新配置文件失败: %v", err)
		}
	} else {
		// 配置文件不存在，创建
		log.Printf("[Setup] 配置文件不存在，创建: content=%q", content)
		if err := configAPI.CreateConfigFile(namespace, fileGroup, fileName, content); err != nil {
			// Create 失败可能是并发导致文件已存在，回退到 Update。
			log.Printf("[Setup] Create 失败(%v)，尝试 Update", err)
			if err := configAPI.UpdateConfigFile(namespace, fileGroup, fileName, content); err != nil {
				log.Fatalf("[Setup] 更新配置文件失败: %v", err)
			}
		}
	}

	if err := configAPI.PublishConfigFile(namespace, fileGroup, fileName); err != nil {
		if strings.Contains(err.Error(), "conflict") {
			log.Fatalf("[Setup] 发布全量配置文件失败(409 conflict): %v\n"+
				"  服务端存在活跃灰度发布，禁止发布全量(灰度存在时不能直接发全量)。\n"+
				"  请先在北极星控制台停止 %s/%s/%s 的灰度发布，再重跑脚本。",
				err, namespace, fileGroup, fileName)
		}
		log.Fatalf("[Setup] 发布全量配置文件失败: %v", err)
	}
	log.Printf("[Setup] 成功发布全量配置文件: content=%q", content)
}

// configFileState 是 /config 接口返回的客户端当前生效配置快照。
type configFileState struct {
	Namespace string            `json:"namespace"`
	FileGroup string            `json:"fileGroup"`
	FileName  string            `json:"fileName"`
	Version   uint64            `json:"version"`
	Md5       string            `json:"md5"`
	Content   string            `json:"content"`
	Labels    map[string]string `json:"labels"`
	LocalIP   string            `json:"localIP"`
	Ready     bool              `json:"ready"`
	FetchErr  string            `json:"fetchErr,omitempty"`
}

var (
	state     configFileState
	ready     atomic.Bool
	configRef model.ConfigFile
)

// runClient 作为配置客户端常驻运行：拉取配置、订阅变更并暴露 HTTP 观察接口。
func runClient() {
	cfg, err := config.LoadConfigurationByFile(configPath)
	if err != nil {
		log.Fatalf("[Client] 加载配置 %s 失败: %v", configPath, err)
	}
	clientLabels := cfg.GetGlobal().GetClient().GetLabels()
	localIPAddr := getLocalIP()

	state.Namespace = namespace
	state.FileGroup = fileGroup
	state.FileName = fileName
	state.Labels = clientLabels
	state.LocalIP = localIPAddr

	sdkCtx, err := polaris.NewSDKContextByConfig(cfg)
	if err != nil {
		log.Fatalf("[Client] 创建 SDKContext 失败: %v", err)
	}
	defer sdkCtx.Destroy()

	configAPI := polaris.NewConfigAPIByContext(sdkCtx)
	log.Printf("[Client] 客户端标签: %v, 本地IP: %s", clientLabels, localIPAddr)
	log.Printf("[Client] 拉取配置文件: %s/%s/%s", namespace, fileGroup, fileName)

	// 先启动 HTTP 服务，便于验证脚本在初始拉取期间轮询就绪状态。
	go serveHTTP()

	configRef, err = configAPI.FetchConfigFile(&polaris.GetConfigFileRequest{
		GetConfigFileRequest: &model.GetConfigFileRequest{
			Namespace: namespace,
			FileGroup: fileGroup,
			FileName:  fileName,
			Subscribe: true,
		},
	})
	if err != nil {
		state.FetchErr = err.Error()
		log.Printf("[Client] 拉取配置文件失败: %v", err)
	} else {
		refreshState()
		log.Printf("[Client] 配置文件获取成功: version=%d, md5=%s, content=%q",
			configRef.GetVersion(), configRef.GetMd5(), configRef.GetContent())
		// 订阅配置变更，变更事件由长轮询 watch 通道驱动。
		configRef.AddChangeListener(changeListener)
	}
	ready.Store(true)

	waitSignal()
}

// changeListener 处理配置变更事件，记录新旧内容与变更类型。
func changeListener(event model.ConfigFileChangeEvent) {
	refreshState()
	log.Printf("[Change] 变更类型=%v, 旧内容=%q, 新内容=%q, version=%d, md5=%s",
		event.ChangeType, event.OldValue, event.NewValue, configRef.GetVersion(), configRef.GetMd5())
}

// refreshState 用当前 ConfigFile 的最新内容刷新 /config 接口返回快照。
func refreshState() {
	if configRef == nil {
		return
	}
	state.Version = configRef.GetVersion()
	state.Md5 = configRef.GetMd5()
	state.Content = configRef.GetContent()
	state.Ready = true
}

// serveHTTP 启动 HTTP 观察接口。
func serveHTTP() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", helpHandler)
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/config", configHandler)
	log.Printf("[Client] HTTP 观察服务监听: %s", port)
	if err := http.ListenAndServe(port, mux); err != nil {
		log.Fatalf("[Client] HTTP 服务异常: %v", err)
	}
}

// helpHandler 返回接口说明。
func helpHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, `Polaris 配置灰度验证客户端

接口:
  GET /health  - 健康检查，初始拉取完成后返回 200
  GET /config  - 返回当前生效配置快照(JSON): content/version/md5/labels/localIP

当前客户端标签会在 /config 的 labels 字段中体现，服务端据此判定是否命中灰度规则。
`)
}

// healthHandler 在初始拉取完成后返回 200，否则返回 503。
func healthHandler(w http.ResponseWriter, r *http.Request) {
	if ready.Load() {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "OK")
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	fmt.Fprintln(w, "initializing")
}

// configHandler 返回当前生效配置快照。
func configHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// 同步就绪标志，保证 JSON 中 ready 字段与 /health 一致。
	state.Ready = ready.Load()
	data, err := json.Marshal(state)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(data)
}

// waitSignal 阻塞等待退出信号。
func waitSignal() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	sig := <-ch
	log.Printf("[Client] 收到信号 %v，准备退出", sig)
}

// getLocalIP 获取本机首个非回环 IPv4 地址，用于 IP 维度灰度规则配置参考。
func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return ""
}
