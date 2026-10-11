// Tencent is pleased to support the open source community by making polaris-go available.
//
// Copyright (C) 2021 Tencent. All rights reserved.
//
// Licensed under the BSD 3-Clause License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// https://opensource.org/licenses/BSD-3-Clause
//
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package flow

import (
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/flow/schedule"
	"github.com/polarismesh/polaris-go/pkg/flow/startup"
	"github.com/polarismesh/polaris-go/pkg/model"
)

const (
	taskCircuitBreak  = "circuitBreakTask"
	taskConfigReport  = "sdkConfigReportTask"
	taskClientReport  = "clientReportTask"
	taskServerService = "syncGetServerService"
	taskHealthCheck   = "healthCheckTask"
)

// ScheduleTask 调度任务
func (e *Engine) ScheduleTask(task *model.PeriodicTask) (chan<- *model.PriorityTask, model.TaskValues) {
	routine := schedule.NewTaskRoutine(task, e.logCtx)
	e.taskRoutines = append(e.taskRoutines, routine)
	return routine.Schedule()
}

// addClientReportTask 添加客户端定期上报任务
func (e *Engine) addClientReportTask() (model.TaskValues, error) {
	configFileFlow := e.getConfigFileFlow()
	callback, err := startup.NewReportClientCallBack(e.configuration, e.plugins, e.globalCtx, configFileFlow)
	if err != nil {
		return nil, err
	}
	// 注入即时上报回调：配置文件监听列表变化时异步触发一次 ReportClient
	if configFileFlow != nil {
		configFileFlow.SetWatchChangedCallback(callback.TriggerNow)
	}
	_, taskValues := e.ScheduleTask(&model.PeriodicTask{
		Name:         taskClientReport,
		CallBack:     callback,
		TakePriority: false,
		LongRun:      true,
		Period:       e.configuration.GetGlobal().GetAPI().GetReportInterval() / 2,
	})
	return taskValues, nil
}

// addClientEventWatcher 创建并启动 WatchClientEvents 长连接监听器。
// 非致命：watcher 启动失败仅记日志、不影响 SDK 核心功能；配置中心未启用时 provider 为真 nil 接口，
// watcher 仍建流应答（config 查询回 applied=false）。
func (e *Engine) addClientEventWatcher() {
	// 显式声明接口变量并仅在非 nil 时赋值：直接传 e.getConfigFileFlow() 会把 typed-nil 指针
	// 装入接口，导致 watcher 内 `configFlow != nil` 判断失效并解引用 nil receiver 而 panic。
	var provider startup.WatchedConfigFileProvider
	if configFileFlow := e.getConfigFileFlow(); configFileFlow != nil {
		provider = configFileFlow
	}
	watcher := startup.NewClientEventWatcher(e.connector, e.globalCtx.GetClientId(), provider, e.logCtx)
	e.clientEventWatcher = watcher
	watcher.Start()
	e.logCtx.GetBaseLogger().Infof("client event watcher started, clientID %s", e.globalCtx.GetClientId())
}

// addSDKConfigReportTask 添加定期上报sdk配置任务
func (e *Engine) addSDKConfigReportTask() model.TaskValues {
	callback := startup.NewConfigReportCallBack(e, e.globalCtx, e.logCtx)
	_, taskValues := e.ScheduleTask(&model.PeriodicTask{
		Name:         taskConfigReport,
		CallBack:     callback,
		TakePriority: false,
		LongRun:      true,
		Period:       config.DefaultReportSDKConfigurationInterval / 2,
	})
	return taskValues
}

const keyDiscoverService = "discoverService"

// addLoadServerServiceTask 添加获取系统服务信息任务（包括路由和实例）
func (e *Engine) addLoadServerServiceTask() (model.TaskValues, error) {
	callback, err := startup.NewServerServiceCallBack(e.configuration, e.plugins, e)
	if err != nil {
		return nil, err
	}
	_, taskValues := e.ScheduleTask(&model.PeriodicTask{
		Name:         taskServerService,
		CallBack:     callback,
		TakePriority: false,
		LongRun:      false,
		Period:       config.DefaultDiscoverServiceRetryInterval / 2,
	})
	return taskValues, nil
}
