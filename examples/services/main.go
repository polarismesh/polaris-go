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
	"github.com/polarismesh/polaris-go/pkg/model"
	"log"
)

func main() {
	consumer, err := polaris.NewConsumerAPI()

	if err != nil {
		fmt.Println("fail to start example.", err)
		return
	}

	// 获取服务列表

	servicesResp, err := consumer.GetServices(&polaris.GetServicesRequest{})
	if err != nil {
		fmt.Println("fail to get services.", err)
		return
	}

	// 打印配置文件内容
	log.Printf("service length is %s", len(servicesResp.GetValue()))
	for _, svc := range servicesResp.GetValue() {
		log.Printf("svc is %s:%s", svc.Namespace, svc.Service)
	}

}

func changeListener(event model.ConfigFileChangeEvent) {
	fmt.Println(fmt.Sprintf("received change event. %+v", event))
}
