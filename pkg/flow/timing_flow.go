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
	routine := schedule.NewTaskRoutine(task)
	e.taskRoutines = append(e.taskRoutines, routine)
	return routine.Schedule()
}

// addClientReportTask 添加客户端定期上报任务
func (e *Engine) addClientReportTask() (model.TaskValues, error) {
	callback, err := startup.NewReportClientCallBack(e.configuration, e.plugins, e.globalCtx)
	if err != nil {
		return nil, err
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

// addSDKConfigReportTask 添加定期上报sdk配置任务
func (e *Engine) addSDKConfigReportTask() model.TaskValues {
	callback := startup.NewConfigReportCallBack(e, e.globalCtx)
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
