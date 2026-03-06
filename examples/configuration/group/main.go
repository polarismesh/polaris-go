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
	"sync"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

var (
	debug         bool
	namespace     string
	fileGroup     string
	configPath    string
	subscribeFile bool
)

// configCache 统一的配置文件缓存，key 为 namespace/fileGroup/fileName
type configCacheEntry struct {
	Version    uint64
	ConfigFile model.ConfigFile
}

var configCache sync.Map // key: namespace/fileGroup/fileName, value: *configCacheEntry

func initArgs() {
	flag.BoolVar(&debug, "debug", false, "是否开启调试模式")
	flag.StringVar(&namespace, "namespace", "default", "命名空间")
	flag.StringVar(&fileGroup, "group", "polaris-config-example", "配置文件组")
	flag.StringVar(&configPath, "config", "./polaris.yaml", "path for config file")
	flag.BoolVar(&subscribeFile, "subscribeFile", true, "是否订阅配置文件")
}

func main() {
	initArgs()
	flag.Parse()
	// 设置日志输出格式：日期 + 时间 + 文件名:行号
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	log.Println("开始启动配置分组监听示例...")
	if debug {
		// 设置日志级别为DEBUG
		if err := api.SetLoggersLevel(api.DebugLog); err != nil {
			log.Printf("fail to set log level to DEBUG, err is %v", err)
		} else {
			log.Printf("successfully set log level to DEBUG")
		}
	}

	cfg, err := config.LoadConfigurationByFile(configPath)
	if err != nil {
		log.Fatalf("load configuration by file %s failed: %v", configPath, err)
	}
	sdkCtx, err := polaris.NewSDKContextByConfig(cfg)
	if err != nil {
		log.Fatalf("fail to create sdkContext, err is %v", err)
	}
	// 步骤1: 创建配置分组 API 和 配置文件 API
	configGroupAPI := polaris.NewConfigGroupAPIByContext(sdkCtx)
	log.Println("配置分组API创建成功")

	configFileAPI := polaris.NewConfigAPIByContext(sdkCtx)
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

	// 打印配置组中的所有文件名，并初始化本地缓存
	if len(files) > 0 {
		log.Println("配置分组中的文件列表:")
		for _, file := range files {
			log.Printf("  - %s (version: %d)", file.FileName, file.Version)
			// 初始化本地缓存
			key := getConfigFileKey(file.Namespace, file.FileGroup, file.FileName)
			configCache.Store(key, &configCacheEntry{Version: file.Version})
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
		fetchConfigFile(configFileAPI, file.Namespace, file.FileGroup, file.FileName)
	}

	// 使用 WaitGroup 保持程序运行
	wait := sync.WaitGroup{}
	wait.Add(1)
	wait.Wait()
}

// handleConfigGroupChange 处理配置分组变更事件
// 使用本地缓存 localConfigCache 与 event.After 进行比较，而非 event.Before 与 event.After
func handleConfigGroupChange(event *model.ConfigGroupChangeEvent, configFileAPI polaris.ConfigAPI) {
	log.Printf("收到配置分组变更事件, %s", event.GetString())
	// 遍历 after，构建 afterMap 并与本地缓存比较，找出新增和变更的配置文件
	afterMap := make(map[string]*model.SimpleConfigFile)
	for _, file := range event.After {
		key := getConfigFileKey(file.Namespace, file.FileGroup, file.FileName)
		afterMap[key] = file
		cached, exists := configCache.Load(key)
		if !exists {
			// 缓存中不存在，说明是新增的配置文件
			log.Printf("检测到新增配置文件: %s (version: %d)", key, file.Version)
			fetchConfigFile(configFileAPI, file.Namespace, file.FileGroup, file.FileName)
		} else {
			// 缓存中存在，检查版本变化
			entry := cached.(*configCacheEntry)
			if entry.Version != file.Version {
				log.Printf("检测到配置文件变更: %s (version: %d->%d), file:%v",
					key, entry.Version, file.Version, entry.ConfigFile.GetContent())
				fetchConfigFile(configFileAPI, file.Namespace, file.FileGroup, file.FileName)
			}
		}
	}

	// 遍历缓存，找出在 after 中不存在的（即被删除的）配置文件
	configCache.Range(func(k, v interface{}) bool {
		key := k.(string)
		if _, exists := afterMap[key]; !exists {
			log.Printf("检测到配置文件被删除: %s", key)
			configCache.Delete(key)
		}
		return true
	})
}

// fetchConfigFile 拉取并监听单个配置文件
func fetchConfigFile(configFileAPI polaris.ConfigAPI, ns, group, fileName string) {
	key := getConfigFileKey(ns, group, fileName)

	log.Printf("正在拉取配置文件: %s", key)
	configFile, err := configFileAPI.FetchConfigFile(&polaris.GetConfigFileRequest{
		GetConfigFileRequest: &model.GetConfigFileRequest{
			Namespace: ns,
			FileGroup: group,
			FileName:  fileName,
			Subscribe: subscribeFile,
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

	// 存储配置文件到缓存
	configCache.Store(key, &configCacheEntry{
		Version:    configFile.GetVersion(),
		ConfigFile: configFile,
	})
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
