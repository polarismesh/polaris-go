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

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
)

var debug bool

func initArgs() {
	flag.BoolVar(&debug, "debug", true, "是否开启调试模式")
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
	configAPI, err := polaris.NewConfigAPI()
	if err != nil {
		log.Printf("创建配置API失败: %v", err)
		return
	}
	log.Println("配置API创建成功")

	// 获取远程的配置文件
	namespace := "default"
	fileGroup := "polaris-config-example"
	fileName := "example.yaml"

	log.Printf("准备获取配置文件 - namespace: %s, fileGroup: %s, fileName: %s", namespace, fileGroup, fileName)

	configFile, err := configAPI.GetConfigFile(namespace, fileGroup, fileName)
	if err != nil {
		log.Printf("获取配置文件失败: %v", err)
		return
	}
	log.Println("配置文件获取成功")

	// 打印配置文件内容
	content := configFile.GetContent()
	log.Printf("配置文件内容:\n%s", content)
	log.Printf("配置文件内容长度: %d 字符", len(content))
	log.Printf("fetched config file:\n %s\n", getString(configFile))

	// 方式一：添加监听器
	log.Println("添加配置变更监听器（回调函数方式）...")
	configFile.AddChangeListener(changeListener)

	// 方式二：添加监听器
	log.Println("添加配置变更监听器（通道方式）...")
	changeChan := configFile.AddChangeListenerWithChannel()

	log.Println("开始监听配置变更事件...")
	for {
		select {
		case event := <-changeChan:
			log.Printf("通过通道接收到配置变更事件: %+v", event)
			log.Printf("updated config file:\n %s\n", getString(configFile))
		}
	}
}

func changeListener(event model.ConfigFileChangeEvent) {
	log.Printf("通过回调函数接收到配置变更事件:")
	log.Printf("  - 变更类型: %v", event.ChangeType)
	log.Printf("  - 命名空间: %s", event.ConfigFileMetadata.GetNamespace())
	log.Printf("  - 文件组: %s", event.ConfigFileMetadata.GetFileGroup())
	log.Printf("  - 文件名: %s", event.ConfigFileMetadata.GetFileName())
	log.Printf("  - 新内容: %s", event.NewValue)
	log.Printf("  - 旧内容: %s", event.OldValue)
	log.Printf("完整事件信息: %+v", jsonMarshal(event))
}

func jsonMarshal(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(data)
}

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
