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

// Package main 演示 polaris-go 客户端参与「配置生效查询」。
//
// 本示例配合配置生效查询端到端验证脚本 (config-effect-test.sh) 使用：
//   - setup 模式：创建并发布 3 份全量基线配置（由 base name 派生 -1/-2/-3.yaml），供验证流程初始化
//   - run   模式：作为配置客户端常驻运行，订阅 3 个配置文件并暴露 HTTP 观察接口：
//     GET /health    健康检查，初始拉取完成后返回 200
//     GET /config    返回当前生效配置快照 (含 clientId 与 files 数组，每个文件含 version/md5/content)
//     GET /clientid  返回 SDK 的 clientID (供验证脚本调用服务端 /maintain/v1/clients/event)
//
// 验证脚本会通过服务端 maintain 接口向本客户端 PUSH 配置生效查询，
// 客户端通过 WatchClientEvents 长连接回 ACK (含 version/md5/applied)，
// 脚本解析服务端返回的 ACK content 并与客户端 /config 快照比对，验证端到端生效查询。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
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
	// configFileCount 单次验证创建/订阅的配置文件数量，由 base name 派生 -1/-2/-3.yaml。
	configFileCount = 3
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
	flag.StringVar(&fileName, "file", "config-effect-example", "配置文件 base name，派生 -1/-2/-3.yaml")
	flag.StringVar(&content, "content", "effect-content-v", "setup 模式写入的配置内容 base，派生 1/2/3 后缀")
	flag.StringVar(&port, "port", ":18091", "run 模式下 HTTP 监听地址")
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

// baseName 去掉 -file 传入值尾部的 .yaml/.yml 后缀，避免派生出 base.yaml-1.yaml 这种文件名。
// raw 传入的 -file 值，可能带也可能不带后缀。
func baseName(raw string) string {
	s := strings.TrimSuffix(raw, ".yaml")
	s = strings.TrimSuffix(s, ".yml")
	return s
}

// derivedFileName 由 base name 与序号派生配置文件名: <base>-<idx>.yaml。
func derivedFileName(base string, idx int) string {
	return fmt.Sprintf("%s-%d.yaml", base, idx)
}

// derivedContent 由 base content 与序号派生配置内容: <base><idx>。
func derivedContent(base string, idx int) string {
	return fmt.Sprintf("%s%d", base, idx)
}

// runSetup 准备 configFileCount 份全量基线配置，供验证流程初始化使用。
// 每个文件：已存在且内容一致则跳过；已存在但内容不一致则更新；不存在则创建。最后发布全量版本。
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
	base := baseName(fileName)
	log.Printf("[Setup] 准备 %d 份配置文件: base=%q, 内容base=%q", configFileCount, base, content)

	for i := 1; i <= configFileCount; i++ {
		fname := derivedFileName(base, i)
		fcontent := derivedContent(content, i)
		if err := setupOne(configAPI, namespace, fileGroup, fname, fcontent); err != nil {
			log.Fatalf("[Setup] 准备配置文件 %s 失败: %v", fname, err)
		}
	}
	log.Printf("[Setup] 成功准备 %d 份配置文件", configFileCount)
}

// setupOne 准备单个配置文件：已存在且内容一致则跳过；内容不一致则更新；不存在则创建。最后发布全量版本。
// configAPI 配置 API；ns/group/fname 配置三元组；fcontent 期望写入的内容。
// 返回 error 表示创建/更新/发布失败，调用方据此中止整个 setup。
func setupOne(configAPI polaris.ConfigAPI, ns, group, fname, fcontent string) error {
	log.Printf("[Setup] 准备配置文件: %s/%s/%s, 期望内容=%q", ns, group, fname, fcontent)

	existing, fetchErr := configAPI.FetchConfigFile(&polaris.GetConfigFileRequest{
		GetConfigFileRequest: &model.GetConfigFileRequest{
			Namespace: ns,
			FileGroup: group,
			FileName:  fname,
		},
	})
	if fetchErr == nil && existing.HasContent() {
		if existing.GetContent() == fcontent {
			log.Printf("[Setup] 配置文件已存在且内容一致(content=%q)，跳过创建与发布", fcontent)
			return nil
		}
		log.Printf("[Setup] 配置文件已存在(当前内容=%q)，更新为 %q", existing.GetContent(), fcontent)
		if err := configAPI.UpdateConfigFile(ns, group, fname, fcontent); err != nil {
			return fmt.Errorf("更新配置文件失败: %w", err)
		}
	} else {
		log.Printf("[Setup] 配置文件不存在，创建: content=%q", fcontent)
		if err := configAPI.CreateConfigFile(ns, group, fname, fcontent); err != nil {
			log.Printf("[Setup] Create 失败(%v)，尝试 Update", err)
			if err := configAPI.UpdateConfigFile(ns, group, fname, fcontent); err != nil {
				return fmt.Errorf("更新配置文件失败: %w", err)
			}
		}
	}

	if err := configAPI.PublishConfigFile(ns, group, fname); err != nil {
		return fmt.Errorf("发布配置文件失败: %w", err)
	}
	log.Printf("[Setup] 成功发布全量配置文件: %s, content=%q", fname, fcontent)
	return nil
}

// configFileState 是 /config 接口返回的单个配置文件生效快照。
type configFileState struct {
	Namespace string `json:"namespace"`
	FileGroup string `json:"fileGroup"`
	FileName  string `json:"fileName"`
	Version   uint64 `json:"version"`
	Md5       string `json:"md5"`
	Content   string `json:"content"`
	Ready     bool   `json:"ready"`
	FetchErr  string `json:"fetchErr,omitempty"`
}

// configSnapshot 是 /config 接口返回的整体快照，含 clientID 与全部监听文件的生效状态。
type configSnapshot struct {
	ClientID string            `json:"clientId"`
	Files    []configFileState `json:"files"`
}

var (
	snapshot configSnapshot
	ready    atomic.Bool
)

// runClient 作为配置客户端常驻运行：拉取并订阅 configFileCount 个配置文件，暴露 HTTP 观察接口。
// 单个文件拉取失败仅记录到对应快照的 FetchErr，不中止其余文件订阅与客户端运行。
func runClient() {
	cfg, err := config.LoadConfigurationByFile(configPath)
	if err != nil {
		log.Fatalf("[Client] 加载配置 %s 失败: %v", configPath, err)
	}

	base := baseName(fileName)
	snapshot.Files = make([]configFileState, configFileCount)
	for i := 1; i <= configFileCount; i++ {
		fname := derivedFileName(base, i)
		snapshot.Files[i-1] = configFileState{
			Namespace: namespace,
			FileGroup: fileGroup,
			FileName:  fname,
		}
	}

	sdkCtx, err := polaris.NewSDKContextByConfig(cfg)
	if err != nil {
		log.Fatalf("[Client] 创建 SDKContext 失败: %v", err)
	}
	defer sdkCtx.Destroy()

	// 暴露 clientID 供验证脚本调用服务端 /maintain/v1/clients/event
	snapshot.ClientID = sdkCtx.GetValueContext().GetClientId()
	log.Printf("[Client] clientID: %s", snapshot.ClientID)
	log.Printf("[Client] 拉取 %d 个配置文件(base=%q)", configFileCount, base)

	go serveHTTP()

	configAPI := polaris.NewConfigAPIByContext(sdkCtx)
	for i := 1; i <= configFileCount; i++ {
		idx := i - 1
		fname := snapshot.Files[idx].FileName
		cf, fetchErr := configAPI.FetchConfigFile(&polaris.GetConfigFileRequest{
			GetConfigFileRequest: &model.GetConfigFileRequest{
				Namespace: namespace,
				FileGroup: fileGroup,
				FileName:  fname,
				Subscribe: true,
			},
		})
		if fetchErr != nil {
			snapshot.Files[idx].FetchErr = fetchErr.Error()
			log.Printf("[Client] 拉取配置文件 %s 失败: %v", fname, fetchErr)
			continue
		}
		refreshFileState(idx, cf)
		log.Printf("[Client] 配置文件 %s 获取成功: version=%d, md5=%s, content=%q",
			fname, cf.GetVersion(), cf.GetMd5(), cf.GetContent())
		// 闭包捕获 idx 与 cf，变更时仅刷新对应文件快照
		cf.AddChangeListener(func(event model.ConfigFileChangeEvent) {
			refreshFileState(idx, cf)
			log.Printf("[Change] 文件=%s, 变更类型=%v, 旧内容=%q, 新内容=%q, version=%d, md5=%s",
				fname, event.ChangeType, event.OldValue, event.NewValue, cf.GetVersion(), cf.GetMd5())
		})
	}
	ready.Store(true)

	waitSignal()
}

// refreshFileState 用指定 ConfigFile 的最新内容刷新对应 idx 的快照。
// cf 为 nil 时直接返回，避免空指针。
func refreshFileState(idx int, cf model.ConfigFile) {
	if cf == nil {
		return
	}
	snapshot.Files[idx].Version = cf.GetVersion()
	snapshot.Files[idx].Md5 = cf.GetMd5()
	snapshot.Files[idx].Content = cf.GetContent()
	snapshot.Files[idx].Ready = true
	snapshot.Files[idx].FetchErr = ""
}

// serveHTTP 启动 HTTP 观察接口。
func serveHTTP() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", helpHandler)
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/config", configHandler)
	mux.HandleFunc("/clientid", clientIDHandler)
	log.Printf("[Client] HTTP 观察服务监听: %s", port)
	if err := http.ListenAndServe(port, mux); err != nil {
		log.Fatalf("[Client] HTTP 服务异常: %v", err)
	}
}

// helpHandler 返回接口说明。
func helpHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, `Polaris 配置生效查询验证客户端

接口:
  GET /health    - 健康检查，初始拉取完成后返回 200
  GET /config    - 返回当前生效配置快照(JSON): {clientId, files:[{namespace,fileGroup,fileName,version,md5,content,ready}]}
  GET /clientid  - 返回 SDK clientID (供验证脚本调用服务端 /maintain/v1/clients/event)

验证脚本通过服务端 maintain 接口向本客户端 PUSH 配置生效查询，
客户端通过 WatchClientEvents 长连接回 ACK (含 version/md5/applied)。
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

// configHandler 返回当前生效配置快照，含 clientId 与全部监听文件的状态数组。
func configHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	data, err := json.Marshal(snapshot)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(data)
}

// clientIDHandler 返回 SDK clientID，供验证脚本拼接服务端 maintain 查询 URL。
func clientIDHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, snapshot.ClientID)
}

// waitSignal 阻塞等待退出信号。
func waitSignal() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	sig := <-ch
	log.Printf("[Client] 收到信号 %v，准备退出", sig)
}
