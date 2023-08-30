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
	"fmt"

	"github.com/polarismesh/polaris-go"
)

func main() {
	configAPI, err := polaris.NewConfigAPI()

	if err != nil {
		fmt.Println("failed to start example.", err)
		return
	}

	// 获取远程的配置文件
	namespace := "default"
	fileGroup := "polaris-config-example"
	fileName := "example.yaml"
	content := "hello world"
	newContent := "bye~"

	err = configAPI.CreateConfigFile(namespace, fileGroup, fileName, content)
	if err != nil {
		fmt.Println("failed to create config file.", err)
		return
	}

	fmt.Println("[Create] Success")

	err = configAPI.UpdateConfigFile(namespace, fileGroup, fileName, newContent)
	if err != nil {
		fmt.Println("failed to update config file.", err)
		return
	}

	fmt.Println("[Update] Success")

	err = configAPI.PublishConfigFile(namespace, fileGroup, fileName)
	if err != nil {
		fmt.Println("failed to publish config file.", err)
		return
	}

	configFile, err := configAPI.GetConfigFile(namespace, fileGroup, fileName)
	if err != nil {
		fmt.Println("failed to get config file.", err)
		return
	}

	fmt.Printf("config file is %#v\n", configFile)
}
