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

package control

import (
	"context"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/modern-go/reflect2"
	"sync/atomic"
	"time"
)

//带有优先级任务打断能力的协程结构
type PriorityRoutine interface {
	//添加周期性任务
	AddPeriodicTask(task *model.PeriodicTask)
	//新增优先级任务，假如队列满则阻塞
	AddPriorityTask(task *model.PriorityTask) error
	//尝试新增优先级任务，新增成功返回true，否则返回false
	TryAddPriorityTask(task *model.PriorityTask) bool
	//开启协程执行
	Start()
	//结束协程执行
	Stop()
}

//PriorityRoutine的实现
type priorityRoutine struct {
	//协程名称
	name string
	//控制上下文
	ctxControl context.Context
	//取消函数
	cancel context.CancelFunc
	//任务队列
	taskChan chan model.PriorityTask
	//定时的轮询间隔
	interval time.Duration
	//周期轮询任务
	periodicTasks atomic.Value
}

//创建优先级协程
func NewPriorityRoutine(name string, interval time.Duration, queueSize int) PriorityRoutine {
	routine := &priorityRoutine{
		name:     name,
		taskChan: make(chan model.PriorityTask, queueSize),
		interval: interval}
	routine.ctxControl, routine.cancel = context.WithCancel(context.Background())
	return routine
}

//新增优先级任务
func (p *priorityRoutine) AddPriorityTask(task *model.PriorityTask) error {
	select {
	case p.taskChan <- *task:
	case <-p.ctxControl.Done():
		return model.NewSDKError(model.ErrCodeInvalidStateError, nil, "routine %s has been terminated", p.name)
	}
	return nil
}

//尝试新增优先级任务，新增成功返回true，否则返回false
func (p *priorityRoutine) TryAddPriorityTask(task *model.PriorityTask) bool {
	select {
	case p.taskChan <- *task:
		return true
	default:
		return false
	}
}

//开启协程执行
func (p *priorityRoutine) Start() {
	go p.doStart()
}

//执行定期任务
func (p *priorityRoutine) doStart() {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	var err error
	for {
		select {
		case <-p.ctxControl.Done():
			log.GetBaseLogger().Infof("routine %s has been terminated", p.name)
			return
		case task := <-p.taskChan:
			if err = task.Process(task.TaskData); nil != err {
				log.GetBaseLogger().Errorf("fail to process task %s, error is %v", task.Name, err)
			}
		case <-ticker.C:
			tasksValue := p.periodicTasks.Load()
			if reflect2.IsNil(tasksValue) {
				continue
			}
			tasks := tasksValue.([]*model.PeriodicTask)
			for _, periodicTask := range tasks {
				if periodicTask.Terminated {
					continue
				}
				p.doPeriodicTask(periodicTask)
			}
		}
	}
}

//是否有高优先级任务
func (p *priorityRoutine) hasPriorityTask() bool {
	return len(p.taskChan) > 0
}

//处理定时任务
func (p *priorityRoutine) doPeriodicTask(task *model.PeriodicTask) {
	var next bool
	var err error
	if next, err = task.Process(task.Values, p.hasPriorityTask); nil != err {
		log.GetBaseLogger().Errorf("fail to process periodic task %s, error is %v", task.Name, err)
	}
	task.Terminated = !next
}

//添加周期性任务
func (p *priorityRoutine) AddPeriodicTask(task *model.PeriodicTask) {
	tasksValue := p.periodicTasks.Load()
	if reflect2.IsNil(tasksValue) {
		p.periodicTasks.Store([]*model.PeriodicTask{task})
	} else {
		tasks := tasksValue.([]*model.PeriodicTask)
		tasks = append(tasks, task)
		p.periodicTasks.Store(tasks)
	}
}

//结束协程执行
func (p *priorityRoutine) Stop() {
	log.GetBaseLogger().Infof("start to terminate routine %s", p.name)
	p.cancel()
}
