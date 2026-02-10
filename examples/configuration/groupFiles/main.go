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
	"time"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
)

var (
	debug     bool
	namespace string
	fileGroup string
	printData bool
)

func initArgs() {
	flag.BoolVar(&debug, "debug", false, "是否开启调试模式")
	flag.StringVar(&namespace, "namespace", "default", "命名空间")
	flag.StringVar(&fileGroup, "group", "polaris-config-example", "配置文件组")
	flag.BoolVar(&printData, "printData", false, "是否打印配置内容")
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

	// 步骤3: 获取分组下的所有配置文件列表
	files, revision, success := group.GetFiles()
	if !success {
		log.Println("警告: 获取配置分组文件列表失败或分组为空")
	} else {
		log.Printf("配置分组获取成功, revision: %s, 文件数量: %d", revision, len(files))
	}

	// 步骤4: 拉取分组下的所有配置文件（并发方式）
	log.Println("开始并发拉取分组下的所有配置文件...")
	totalStartTime := time.Now()

	// 使用channel收集耗时数据
	durationChan := make(chan time.Duration, len(files))
	var wg sync.WaitGroup

	for _, file := range files {
		wg.Add(1)
		go func(f *model.SimpleConfigFile) {
			defer wg.Done()
			d := fetchConfigFile(configFileAPI, f.Namespace, f.FileGroup, f.FileName)
			if d > 0 {
				durationChan <- d
			}
		}(file)
	}

	// 等待所有goroutine完成
	wg.Wait()
	close(durationChan)

	// 收集所有耗时数据
	var durations []time.Duration
	for d := range durationChan {
		durations = append(durations, d)
	}
	totalDuration := time.Since(totalStartTime)

	// 计算并打印耗时统计
	if len(durations) > 0 {
		var minDuration, maxDuration, sumDuration time.Duration
		minDuration = durations[0]
		maxDuration = durations[0]
		for _, d := range durations {
			sumDuration += d
			if d < minDuration {
				minDuration = d
			}
			if d > maxDuration {
				maxDuration = d
			}
		}
		avgDuration := sumDuration / time.Duration(len(durations))
		log.Printf("单个文件耗时统计 - 最小: %v, 最大: %v, 平均: %v", minDuration, maxDuration, avgDuration)
		log.Printf("并发效率 - 串行理论耗时: %v, 并发实际耗时: %v, 节省时间: %v", sumDuration, totalDuration, sumDuration-totalDuration)
	}
	log.Printf("整个配置分组文件获取完成，共有%d个, 并发总耗时: %v", len(files), totalDuration)

	log.Println("结束...")
}

// fetchConfigFile 拉取单个配置文件，返回耗时
func fetchConfigFile(configFileAPI polaris.ConfigAPI, ns, group, fileName string) time.Duration {
	key := getConfigFileKey(ns, group, fileName)

	log.Printf("正在拉取配置文件: %s", key)
	startTime := time.Now()
	configFile, err := configFileAPI.FetchConfigFile(&polaris.GetConfigFileRequest{
		GetConfigFileRequest: &model.GetConfigFileRequest{
			Namespace: ns,
			FileGroup: group,
			FileName:  fileName,
			Subscribe: true, // 必须设置为 true，才能将配置文件加入长轮询池进行监听
		},
	})
	duration := time.Since(startTime)
	if err != nil {
		log.Printf("获取配置文件 %s 失败: %v, 耗时: %v", key, err, duration)
		return 0
	}

	log.Printf("配置文件 %s 获取成功，耗时: %v\n", key, duration)
	if printData {
		log.Printf("配置文件 %s 内容: %s", key, getString(configFile))
	}
	return duration
}

// getConfigFileKey 生成配置文件的唯一标识
func getConfigFileKey(namespace, fileGroup, fileName string) string {
	return fmt.Sprintf("%s/%s/%s", namespace, fileGroup, fileName)
}

// getString 获取配置文件的字符串表示
func getString(c model.ConfigFile) string {
	if !c.HasContent() {
		return fmt.Sprintf("ConfigFile{Namespace=%s, FileGroup=%s, FileName=%s, Version=%d, VersionName=%s, Md5=%s, "+
			"Labels=%v, HasContent=%t}", c.GetNamespace(), c.GetFileGroup(), c.GetFileName(), c.GetVersion(),
			c.GetVersionName(), c.GetMd5(), c.GetLabels(), c.HasContent())
	} else {
		if len(c.GetContent()) < 100 {
			return fmt.Sprintf("ConfigFile{Namespace=%s, FileGroup=%s, FileName=%s, Version=%d, VersionName=%s, "+
				"Md5=%s, Labels=%v, HasContent=%t, ContentLength=%d, Content=%s}", c.GetNamespace(), c.GetFileGroup(),
				c.GetFileName(), c.GetVersion(), c.GetVersionName(), c.GetMd5(), c.GetLabels(), c.HasContent(),
				len(c.GetContent()), c.GetContent())
		} else {
			return fmt.Sprintf("ConfigFile{Namespace=%s, FileGroup=%s, FileName=%s, Version=%d, VersionName=%s, "+
				"Md5=%s, Labels=%v, HasContent=%t, ContentLength=%d, Content=...}", c.GetNamespace(), c.GetFileGroup(),
				c.GetFileName(), c.GetVersion(), c.GetVersionName(), c.GetMd5(), c.GetLabels(), c.HasContent(),
				len(c.GetContent()))
		}
	}
}
