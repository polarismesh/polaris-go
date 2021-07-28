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

package file

import (
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

//Alarm2FileReporter 默认告警处理器，打印本地告警日志
type Alarm2FileReporter struct {
	*plugin.PluginBase
}

//Type 插件类型
func (g *Alarm2FileReporter) Type() common.Type {
	return common.TypeAlarmReporter
}

//Name 插件名，一个类型下插件名唯一
func (g *Alarm2FileReporter) Name() string {
	return "alarm2file"
}

//Init 初始化插件
func (g *Alarm2FileReporter) Init(ctx *plugin.InitContext) error {
	g.PluginBase = plugin.NewPluginBase(ctx)
	return nil
}

//Destroy 销毁插件，可用于释放资源
func (g *Alarm2FileReporter) Destroy() error {
	return nil
}

//ReportAlarm 上报告警
func (g *Alarm2FileReporter) ReportAlarm(level int, message string) error {
	return nil
}

//init 注册插件
func init() {
	plugin.RegisterPlugin(&Alarm2FileReporter{})
}
