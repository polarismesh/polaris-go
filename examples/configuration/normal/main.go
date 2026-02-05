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
	"sync"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
)

var (
	debug     bool
	namespace string
	fileGroup string
)

// configFileMap 存储已监听的配置文件，key 为 namespace/fileGroup/fileName
var configFileMap sync.Map

func initArgs() {
	flag.BoolVar(&debug, "debug", true, "是否开启调试模式")
	flag.StringVar(&namespace, "namespace", "default", "命名空间")
	flag.StringVar(&fileGroup, "group", "polaris-config-example", "配置文件组")
}

func main() {
	initArgs()
	flag.Parse()
	log.Println("开始启动配置文件监听示例...")

	if debug {
		// 设置日志级别为DEBUG
		if err := api.SetLoggersLevel(api.DebugLog); err != nil {
			log.Printf("fail to set log level to DEBUG, err is %v", err)
		} else {
			log.Printf("successfully set log level to DEBUG")
		}
	}

	// 步骤1: 创建配置分组 API 和 配置文件 API
	configGroupAPI, err := polaris.NewConfigGroupAPI()
	if err != nil {
		log.Printf("创建配置分组API失败: %v", err)
		return
	}
	log.Println("配置分组API创建成功")

	configFileAPI, err := polaris.NewConfigAPI()
	if err != nil {
		log.Printf("创建配置文件API失败: %v", err)
		return
	}
	log.Println("配置文件API创建成功")

	// 步骤2: 拉取配置分组
	log.Printf("准备获取配置分组 - namespace: %s, fileGroup: %s", namespace, fileGroup)
	group, err := configGroupAPI.GetConfigGroup(namespace, fileGroup)
	if err != nil {
		log.Printf("获取配置分组失败: %v", err)
		return
	}
	log.Println("配置分组获取成功")

	// 获取分组下的所有配置文件列表
	files, revision, success := group.GetFiles()
	if !success {
		log.Println("警告: 获取配置分组文件列表失败或分组为空")
	} else {
		log.Printf("配置分组获取成功, revision: %s, 文件数量: %d", revision, len(files))
	}

	// 打印配置组中的所有文件名
	if len(files) > 0 {
		log.Println("配置分组中的文件列表:")
		for _, file := range files {
			log.Printf("  - %s (version: %d, md5: %s)", file.FileName, file.Version, file.Md5)
		}
	} else {
		log.Println("配置分组中没有配置文件")
	}

	// 步骤3: 监听配置分组变化
	log.Println("添加配置分组变更监听器...")
	group.AddChangeListener(func(event *model.ConfigGroupChangeEvent) {
		handleConfigGroupChange(event, configFileAPI)
	})
	log.Println("配置分组变更监听器添加成功")

	// 步骤4: 拉取分组下的所有配置文件并监听
	log.Println("开始拉取分组下的所有配置文件...")
	for _, file := range files {
		fetchAndWatchConfigFile(configFileAPI, file.Namespace, file.FileGroup, file.FileName)
	}

	log.Println("所有配置文件监听已启动，等待变更事件...")

	// 使用 WaitGroup 保持程序运行
	wait := sync.WaitGroup{}
	wait.Add(1)
	wait.Wait()
}

// handleConfigGroupChange 处理配置分组变更事件
func handleConfigGroupChange(event *model.ConfigGroupChangeEvent, configFileAPI polaris.ConfigAPI) {
	// 判断配置是否真的发生了变化
	if isConfigGroupEqual(event.Before, event.After) {
		return
	}

	before, _ := json.Marshal(event.Before)
	after, _ := json.Marshal(event.After)
	log.Printf("收到配置分组变更事件\n变更前: %s\n变更后: %s", string(before), string(after))

	// 找出新增的配置文件并添加监听
	beforeMap := make(map[string]*model.SimpleConfigFile)
	for _, file := range event.Before {
		key := getConfigFileKey(file.Namespace, file.FileGroup, file.FileName)
		beforeMap[key] = file
	}

	for _, file := range event.After {
		key := getConfigFileKey(file.Namespace, file.FileGroup, file.FileName)
		if _, exists := beforeMap[key]; !exists {
			// 新增的配置文件，拉取并监听
			log.Printf("检测到新增配置文件: %s", key)
			fetchAndWatchConfigFile(configFileAPI, file.Namespace, file.FileGroup, file.FileName)
		}
	}

	// 找出删除的配置文件
	afterMap := make(map[string]*model.SimpleConfigFile)
	for _, file := range event.After {
		key := getConfigFileKey(file.Namespace, file.FileGroup, file.FileName)
		afterMap[key] = file
	}

	for _, file := range event.Before {
		key := getConfigFileKey(file.Namespace, file.FileGroup, file.FileName)
		if _, exists := afterMap[key]; !exists {
			log.Printf("检测到配置文件被删除: %s", key)
			configFileMap.Delete(key)
		}
	}
}

// fetchAndWatchConfigFile 拉取并监听单个配置文件
func fetchAndWatchConfigFile(configFileAPI polaris.ConfigAPI, ns, group, fileName string) {
	key := getConfigFileKey(ns, group, fileName)

	// 检查是否已经监听
	if _, exists := configFileMap.Load(key); exists {
		log.Printf("配置文件 %s 已在监听中，跳过", key)
		return
	}

	log.Printf("正在拉取配置文件: %s", key)
	configFile, err := configFileAPI.FetchConfigFile(&polaris.GetConfigFileRequest{
		GetConfigFileRequest: &model.GetConfigFileRequest{
			Namespace: ns,
			FileGroup: group,
			FileName:  fileName,
			Subscribe: true, // 必须设置为 true，才能将配置文件加入长轮询池进行监听
		},
	})
	if err != nil {
		log.Printf("获取配置文件 %s 失败: %v", key, err)
		return
	}

	log.Printf("配置文件 %s 获取成功:\n%s", key, getString(configFile))

	// 打印配置文件内容
	content := configFile.GetContent()
	if len(content) > 200 {
		log.Printf("配置文件 %s 内容 (前200字符):\n%s...", key, content[:200])
	} else {
		log.Printf("配置文件 %s 内容:\n%s", key, content)
	}

	// 存储配置文件引用
	configFileMap.Store(key, configFile)

	// 方式一: 使用回调函数监听
	configFile.AddChangeListener(func(event model.ConfigFileChangeEvent) {
		handleConfigFileChange(key, event)
	})

	// 方式二: 使用通道监听 (启动 goroutine 处理)
	changeChan := configFile.AddChangeListenerWithChannel()
	go watchConfigFileChanges(key, changeChan)

	log.Printf("配置文件 %s 监听已添加", key)
}

// handleConfigFileChange 处理配置文件变更事件（回调方式）
func handleConfigFileChange(key string, event model.ConfigFileChangeEvent) {
	log.Printf("通过回调函数收到配置文件 %s 变更事件:", key)
	log.Printf("  - 变更类型: %v", event.ChangeType)
	log.Printf("  - 命名空间: %s", event.ConfigFileMetadata.GetNamespace())
	log.Printf("  - 文件组: %s", event.ConfigFileMetadata.GetFileGroup())
	log.Printf("  - 文件名: %s", event.ConfigFileMetadata.GetFileName())

	if event.ChangeType == model.Deleted {
		log.Printf("配置文件 %s 已被删除", key)
		configFileMap.Delete(key)
	}
}

// watchConfigFileChanges 监听配置文件变更（通道方式）
func watchConfigFileChanges(key string, changeChan <-chan model.ConfigFileChangeEvent) {
	for event := range changeChan {
		log.Printf("通过通道收到配置文件 %s 变更事件: %+v", key, event)

		if event.ChangeType == model.Deleted {
			log.Printf("配置文件 %s 已被删除，停止监听", key)
			configFileMap.Delete(key)
			return
		}
	}
}

// isConfigGroupEqual 比较两个配置组是否相等（忽略数组顺序）
func isConfigGroupEqual(before, after []*model.SimpleConfigFile) bool {
	if len(before) != len(after) {
		return false
	}

	beforeMap := make(map[string]*model.SimpleConfigFile)
	for _, file := range before {
		key := getConfigFileKey(file.Namespace, file.FileGroup, file.FileName)
		beforeMap[key] = file
	}

	for _, file := range after {
		key := getConfigFileKey(file.Namespace, file.FileGroup, file.FileName)
		beforeFile, exists := beforeMap[key]
		if !exists {
			return false
		}
		if beforeFile.Version != file.Version || beforeFile.Md5 != file.Md5 {
			return false
		}
	}

	return true
}

// getConfigFileKey 生成配置文件的唯一标识
func getConfigFileKey(namespace, fileGroup, fileName string) string {
	return fmt.Sprintf("%s/%s/%s", namespace, fileGroup, fileName)
}

// getString 获取配置文件的字符串表示
func getString(c model.ConfigFile) string {
	return fmt.Sprintf("ConfigFile{Namespace=%s, FileGroup=%s, FileName=%s, Version=%d, VersionName=%s, Md5=%s, "+
		"Labels=%v, HasContent=%t, ContentLength=%d}",
		c.GetNamespace(),
		c.GetFileGroup(),
		c.GetFileName(),
		c.GetVersion(),
		c.GetVersionName(),
		c.GetMd5(),
		c.GetLabels(),
		c.HasContent(),
		len(c.GetContent()),
	)
}
