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
	"github.com/polarismesh/polaris-go/pkg/model"
)

var (
	namesapceVal string
	groupVal     string
)

func init() {
	flag.StringVar(&namesapceVal, "namespace", "default", "namespace")
	flag.StringVar(&groupVal, "group", "group", "group")
}

func main() {
	flag.Parse()
	configAPI, err := polaris.NewConfigGroupAPI()

	if err != nil {
		fmt.Println("fail to start example.", err)
		return
	}

	group, err := configAPI.GetConfigGroup(namesapceVal, groupVal)
	if err != nil {
		log.Panic(err)
		return
	}
	group.AddChangeListener(func(event *model.ConfigGroupChangeEvent) {
		before, _ := json.Marshal(event.Before)
		after, _ := json.Marshal(event.After)

		log.Printf("receive config_group change event\nbefore: %s\nafter: %s", string(before), string(after))
	})

	wait := sync.WaitGroup{}
	wait.Add(1)
	wait.Wait()
}
