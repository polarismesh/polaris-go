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

package subscribe

import (
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

var (
	SubScribedServices []model.ServiceKey
)

type Subscribe interface {
	plugin.Plugin

	//执行消息处理逻辑
	DoSubScribe(event *common.PluginEvent) error

	//订阅service消息
	WatchService(key model.ServiceKey) (interface{}, error)
}

//初始化
func init() {
	plugin.RegisterPluginInterface(common.TypeSubScribe, new(Subscribe))
}
