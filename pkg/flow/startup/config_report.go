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

package startup

import (
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"time"
)

//创建配置上报回调
func NewConfigReportCallBack(engine model.Engine, globalCtx model.ValueContext) *ConfigReportCallBack {
	return &ConfigReportCallBack{
		engine:    engine,
		globalCtx: globalCtx,
		interval:  config.DefaultReportSDKConfigurationInterval,
	}
}

//自身配置上报任务回调
type ConfigReportCallBack struct {
	engine    model.Engine
	globalCtx model.ValueContext
	interval  time.Duration
}

//执行任务
func (c *ConfigReportCallBack) Process(
	taskKey interface{}, taskValue interface{}, lastProcessTime time.Time) model.TaskResult {
	if !lastProcessTime.IsZero() && time.Since(lastProcessTime) < c.interval {
		return model.SKIP
	}
	err := c.engine.SyncReportStat(model.SDKCfgStat, nil)
	t, _ := c.globalCtx.GetValue(model.ContextKeyToken)
	token := t.(model.SDKToken)
	if nil != err {
		log.GetBaseLogger().Errorf("report sdk config info, IP: %s, PID: %d, UID: %s, error:%s",
			token.IP, token.PID, token.UID, err)
		//发生错误则进行重试，直到上报成功为止
		return model.SKIP
	}
	return model.CONTINUE
}

//OnTaskEvent 任务事件回调
func (c *ConfigReportCallBack) OnTaskEvent(event model.TaskEvent) {

}
