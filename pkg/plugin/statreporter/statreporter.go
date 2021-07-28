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

package statreporter

import (
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

//StatReporter 【扩展点接口】上报统计结果
type StatReporter interface {
	plugin.Plugin
	//上报回调健康检查结果
	//model.MetricType, 统计信息的类型，
	//目前有SDKAPIStat和ServiceStat两种，分别对应sdk内部方法统计和外部服务调用统计
	//model.InstanceGauge，具体的一次统计数据
	ReportStat(model.MetricType, model.InstanceGauge) error
}

//初始化
func init() {
	plugin.RegisterPluginInterface(common.TypeStatReporter, new(StatReporter))
}
