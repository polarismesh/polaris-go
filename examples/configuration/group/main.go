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
	"log"
	"sync"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/pkg/model"
)

var (
	namesapceVal string
	groupVal     string
)

func init() {
	flag.StringVar(&namesapceVal, "namespace", "default", "namespace")
	flag.StringVar(&groupVal, "group", "polaris-config-example", "group")
}

// isConfigGroupEqual 比较两个配置组是否相等（忽略数组顺序）
func isConfigGroupEqual(before, after []*model.SimpleConfigFile) bool {
	if len(before) != len(after) {
		return false
	}

	// 创建 map 用于快速查找和比较
	beforeMap := make(map[string]*model.SimpleConfigFile)
	for _, file := range before {
		key := file.Namespace + "/" + file.FileGroup + "/" + file.FileName
		beforeMap[key] = file
	}

	// 检查 after 中的每个文件是否在 before 中存在且内容相同
	for _, file := range after {
		key := file.Namespace + "/" + file.FileGroup + "/" + file.FileName
		beforeFile, exists := beforeMap[key]
		if !exists {
			return false
		}
		// 比较版本号和 MD5
		if beforeFile.Version != file.Version || beforeFile.Md5 != file.Md5 {
			return false
		}
	}

	return true
}

func main() {
	flag.Parse()
	log.Printf("Starting config group example with namespace: %s, group: %s", namesapceVal, groupVal)

	configAPI, err := polaris.NewConfigGroupAPI()

	if err != nil {
		log.Printf("fail to create ConfigGroupAPI: %v", err)
		return
	}
	log.Println("ConfigGroupAPI created successfully")

	log.Printf("Fetching config group: namespace=%s, group=%s", namesapceVal, groupVal)
	group, err := configAPI.GetConfigGroup(namesapceVal, groupVal)
	if err != nil {
		log.Panicf("fail to get config group: %v", err)
		return
	}

	// 获取配置组中的所有文件
	files, revision, success := group.GetFiles()
	if !success {
		log.Println("Warning: Failed to get files from config group or group is empty")
	} else {
		log.Printf("Config group fetched successfully, revision: %s, file count: %d", revision, len(files))
	}

	// 打印配置组中的所有文件名
	if len(files) > 0 {
		log.Println("Config files in group:")
		for _, file := range files {
			log.Printf("  - %s (version: %d, md5: %s)", file.FileName, file.Version, file.Md5)
		}
	} else {
		log.Println("No config files found in group")
	}

	log.Println("Adding change listener for config group...")
	group.AddChangeListener(func(event *model.ConfigGroupChangeEvent) {
		// 判断配置是否真的发生了变化（忽略数组顺序）
		if isConfigGroupEqual(event.Before, event.After) {
			//log.Println("Config group polled, but no changes detected (content unchanged)")
			return
		}

		before, _ := json.Marshal(event.Before)
		after, _ := json.Marshal(event.After)
		log.Printf("receive config_group change event\nbefore: %s\nafter: %s", string(before), string(after))
	})
	log.Println("Change listener added successfully")

	log.Println("Listening for config group changes... Press Ctrl+C to exit")
	wait := sync.WaitGroup{}
	wait.Add(1)
	wait.Wait()
}
