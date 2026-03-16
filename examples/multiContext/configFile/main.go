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
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

var (
	debug      bool
	namespace  string
	fileGroup  string
	fileName   string
	subscribe  bool
	clientsVal string
)

func init() {
	flag.BoolVar(&debug, "debug", false, "是否开启调试模式")
	flag.StringVar(&namespace, "namespace", "default", "命名空间")
	flag.StringVar(&fileGroup, "group", "polaris-config-example", "配置文件组")
	flag.StringVar(&fileName, "file", "example.yaml", "配置文件名")
	flag.BoolVar(&subscribe, "subscribe", true, "是否订阅配置文件变更")
	flag.StringVar(&clientsVal, "clients", "", "clientId与server地址映射，格式: clientId=serverAddr，多个用逗号分隔，例如: ctxA=114.132.192.60,ctxB=114.132.29.62")
}

// parseClients 解析 -clients 参数，返回 clientId -> serverAddr 的映射
func parseClients(clientsStr string) (map[string]string, error) {
	result := make(map[string]string)
	if clientsStr == "" {
		return nil, fmt.Errorf("-clients 参数不能为空，格式: clientId=serverAddr，多个用逗号分隔")
	}
	pairs := strings.Split(clientsStr, ",")
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return nil, fmt.Errorf("无效的 client 配置: %q，格式应为 clientId=serverAddr", pair)
		}
		clientId := strings.TrimSpace(parts[0])
		serverAddr := strings.TrimSpace(parts[1])
		if _, exists := result[clientId]; exists {
			return nil, fmt.Errorf("重复的 clientId: %s", clientId)
		}
		result[clientId] = serverAddr
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("-clients 参数解析后为空，格式: clientId=serverAddr，多个用逗号分隔")
	}
	return result, nil
}

func main() {
	flag.Parse()
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	// 解析 -clients 参数
	clientMap, err := parseClients(clientsVal)
	if err != nil {
		log.Fatalf("解析 -clients 参数失败: %v", err)
	}

	log.Printf("启动多 Context 配置文件监听示例...")
	log.Printf("clients: %v, namespace: %s, group: %s, file: %s",
		clientMap, namespace, fileGroup, fileName)

	if debug {
		if err := api.SetLoggersLevel(api.DebugLog); err != nil {
			log.Printf("设置日志级别为 DEBUG 失败: %v", err)
		} else {
			log.Printf("已设置日志级别为 DEBUG")
		}
	}

	// errCh 用于收集 serverProcess 的错误，当所有 serverProcess 都异常退出时主动结束
	errCh := make(chan error, len(clientMap))

	var wg sync.WaitGroup
	wg.Add(len(clientMap))

	for clientId, serverAddr := range clientMap {
		go func(cid, addr string) {
			defer wg.Done()
			if err := serverProcess(addr, cid); err != nil {
				errCh <- fmt.Errorf("%s 异常退出: %v", cid, err)
			}
		}(clientId, serverAddr)
	}

	// 监听系统信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// 当所有 serverProcess 都结束时通知 doneCh
	doneCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneCh)
	}()

	select {
	case sig := <-sigCh:
		log.Printf("收到退出信号: %v，正在退出...", sig)
	case <-doneCh:
		// 所有 serverProcess 都已结束（可能全部出错），收集错误信息
		close(errCh)
		for err := range errCh {
			log.Println(err)
		}
		log.Println("所有 serverProcess 已结束，程序退出")
	}
}

func serverProcess(serverAddr, clientId string) error {
	log.Printf("%s client start, serverAddr=%s", clientId, serverAddr)

	conf := config.NewDefaultConfiguration([]string{serverAddr + ":8091"})
	conf.GetConfigFile().GetConfigConnectorConfig().SetAddresses([]string{serverAddr + ":8093"})
	conf.GetConsumer().GetLocalCache().SetPersistDir("./cache/" + clientId + "/backup")
	conf.GetConfigFile().GetLocalCache().SetPersistDir("./cache/" + clientId + "/config")
	conf.GetGlobal().GetClient().(*config.ClientConfigImpl).AddLabels(map[string]string{
		"uin": clientId,
	})

	sdkContext, err := polaris.NewSDKContextByConfig(conf)
	if err != nil {
		log.Printf("%s 创建 SDK Context 失败, serverAddr=%s: %v", clientId, serverAddr, err)
		return err
	}

	idInfoMap := sdkContext.GetConfig().GetGlobal().GetClient().GetLabels()
	idInfoMapStr := printMap(idInfoMap)

	configFileAPI := polaris.NewConfigAPIByContext(sdkContext)
	log.Printf("%s 准备获取配置文件 - namespace: %s, group: %s, file: %s, subscribe: %v, serverAddr: %s",
		idInfoMapStr, namespace, fileGroup, fileName, subscribe, serverAddr)

	configFile, err := configFileAPI.FetchConfigFile(&polaris.GetConfigFileRequest{
		GetConfigFileRequest: &model.GetConfigFileRequest{
			Namespace: namespace,
			FileGroup: fileGroup,
			FileName:  fileName,
			Subscribe: subscribe,
		},
	})
	if err != nil {
		log.Printf("%s 获取配置文件失败, serverAddr=%s: %v", idInfoMapStr, serverAddr, err)
		return err
	}
	log.Printf("%s 配置文件获取成功, serverAddr=%s", idInfoMapStr, serverAddr)

	// 打印配置文件内容
	content := configFile.GetContent()
	log.Printf("%s 配置文件内容 (serverAddr=%s):\n%s", idInfoMapStr, serverAddr, content)
	log.Printf("%s 配置文件内容长度: %d 字符", idInfoMapStr, len(content))

	// 方式一：添加监听器（回调函数方式）
	log.Printf("%s 添加配置变更监听器（回调函数方式）, serverAddr=%s...", idInfoMapStr, serverAddr)
	configFile.AddChangeListener(func(event model.ConfigFileChangeEvent) {
		log.Printf("%s 通过回调函数接收到配置变更事件 (serverAddr=%s):", idInfoMapStr, serverAddr)
		log.Printf("%s   - 变更类型: %v", idInfoMapStr, event.ChangeType)
		log.Printf("%s   - 命名空间: %s", idInfoMapStr, event.ConfigFileMetadata.GetNamespace())
		log.Printf("%s   - 文件组: %s", idInfoMapStr, event.ConfigFileMetadata.GetFileGroup())
		log.Printf("%s   - 文件名: %s", idInfoMapStr, event.ConfigFileMetadata.GetFileName())
		log.Printf("%s   - 新内容: %s", idInfoMapStr, event.NewValue)
		log.Printf("%s   - 旧内容: %s", idInfoMapStr, event.OldValue)
		log.Printf("%s 完整事件信息: %s", idInfoMapStr, jsonMarshal(event))
	})

	// 方式二：添加监听器（通道方式）
	log.Printf("%s 添加配置变更监听器（通道方式）, serverAddr=%s...", idInfoMapStr, serverAddr)
	changeChan := configFile.AddChangeListenerWithChannel()

	log.Printf("%s 开始监听配置变更事件 (serverAddr=%s)... Press Ctrl+C to exit", idInfoMapStr, serverAddr)
	// 正常情况下阻塞在此，不会返回；由主函数通过信号控制退出
	for {
		select {
		case event := <-changeChan:
			log.Printf("%s 通过通道接收到配置变更事件 (serverAddr=%s): %+v, content: %v",
				idInfoMapStr, serverAddr, event, configFile.GetContent())
		}
	}
}

// 需要过滤的 SDK 系统标签
var systemLabels = map[string]bool{
	"CLIENT_ID":       true,
	"CLIENT_VERSION":  true,
	"CLIENT_LANGUAGE": true,
	"CLIENT_IP":       true,
}

func printMap(m map[string]string) string {
	if len(m) == 0 {
		return "empty"
	}
	parts := make([]string, 0, len(m))
	for k, v := range m {
		if systemLabels[k] {
			continue
		}
		parts = append(parts, fmt.Sprintf("{%q:%q}", k, v))
	}
	if len(parts) == 0 {
		return "empty"
	}
	return strings.Join(parts, ", ")
}

func jsonMarshal(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(data)
}
