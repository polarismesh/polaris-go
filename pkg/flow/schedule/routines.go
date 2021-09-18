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

package schedule

import (
	"context"
	"github.com/modern-go/reflect2"
	"github.com/polarismesh/polaris-go/pkg/clock"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"sync"
	"sync/atomic"
	"time"
)

//任务调度协程接口
type TaskRoutine interface {
	//进行任务调度
	Schedule() (chan<- *model.PriorityTask, model.TaskValues)
	//结束协程
	Destroy()
}

//创建任务调度协程
func NewTaskRoutine(periodicTask *model.PeriodicTask) TaskRoutine {
	return &taskRoutine{
		periodicTask:        periodicTask,
		mutableTaskValues:   make(map[interface{}]*TaskItem),
		immutableTaskValues: &atomic.Value{},
		mutex:               &sync.Mutex{}}
}

//任务调度协程
type taskRoutine struct {
	periodicTask *model.PeriodicTask
	ctx          context.Context
	cancel       context.CancelFunc
	//销毁后不能再次启动
	destroyed           bool
	started             bool
	priorityChan        chan *model.PriorityTask
	mutableTaskValues   map[interface{}]*TaskItem
	immutableTaskValues *atomic.Value
	mutex               *sync.Mutex
}

//进行任务调度
func (t *taskRoutine) Schedule() (chan<- *model.PriorityTask, model.TaskValues) {
	if t.periodicTask.TakePriority {
		t.priorityChan = make(chan *model.PriorityTask, prioritySize)
	}
	t.periodicTask.Period = GetDefaultInterval(t.periodicTask.Period)
	return t.priorityChan, t
}

//获取默认的调度时长
func GetDefaultInterval(interval time.Duration) time.Duration {
	if interval < clock.TimeStep() {
		return clock.TimeStep()
	}
	return interval
}

//结束任务调度
func (t *taskRoutine) Destroy() {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	log.GetBaseLogger().Infof("task %s has been destroy", t.periodicTask.Name)
	t.destroyed = true
	t.stop()
}

//任务包裹
type TaskItem struct {
	value           model.TaskValue
	lastProcessTime time.Time
}

//启动协程
func (t *taskRoutine) start() {
	if t.destroyed || t.started {
		return
	}
	t.started = true
	t.periodicTask.CallBack.OnTaskEvent(model.EventStart)
	t.ctx, t.cancel = context.WithCancel(context.Background())
	if t.periodicTask.TakePriority {
		log.GetBaseLogger().Infof("task %s started priority", t.periodicTask.Name)
		go t.runTakePriority()
	} else {
		log.GetBaseLogger().Infof("task %s started period %v", t.periodicTask.Name, t.periodicTask.Period)
		go t.runPeriod()
	}
}

//结束协程
func (t *taskRoutine) stop() {
	if !t.started {
		return
	}
	t.cancel()
	t.started = false
	t.periodicTask.CallBack.OnTaskEvent(model.EventStop)
}

//优先级队列长度，暂定100
const prioritySize = 100

//处理带优先级的任务
func (t *taskRoutine) runTakePriority() {
	ticker := time.NewTicker(t.periodicTask.Period)
	defer ticker.Stop()
	//首次进来先执行一次
	t.iteratePeriodTaskItems(true)
	for {
		select {
		case <-t.ctx.Done():
			log.GetBaseLogger().Infof("task %s has done", t.periodicTask.Name)
			return
		case task := <-t.priorityChan:
			log.GetBaseLogger().Debugf("start to process priority task %s", task.Name)
			task.CallBack.Process()
		case <-ticker.C:
			t.iteratePeriodTaskItems(true)
		}
	}
}

//处理定时任务
func (t *taskRoutine) processPeriodicTask(key interface{}, item *TaskItem) {
	result := t.periodicTask.CallBack.Process(key, item.value, item.lastProcessTime)
	switch result {
	case model.CONTINUE:
		item.lastProcessTime = clock.GetClock().Now()
	case model.TERMINATE:
		log.GetBaseLogger().Infof("item %s in task %s has terminated", key, t.periodicTask.Name)
		item.lastProcessTime = clock.GetClock().Now()
		t.DeleteValue(key, item.value)
	}
}

//遍历并处理定时任务信息
func (t *taskRoutine) iteratePeriodTaskItems(takePriority bool) {
	immutableMap := t.immutableTaskValues.Load().(map[interface{}]*TaskItem)
	if len(immutableMap) == 0 {
		return
	}
	for key, item := range immutableMap {
		if takePriority && len(t.priorityChan) > 0 {
			break
		}
		t.processPeriodicTask(key, item)
	}
}

//处理定时任务
func (t *taskRoutine) runPeriod() {
	ticker := time.NewTicker(t.periodicTask.Period)
	defer ticker.Stop()
	t.iteratePeriodTaskItems(false)
	for {
		select {
		case <-t.ctx.Done():
			log.GetBaseLogger().Infof("task %s has done", t.periodicTask.Name)
			return
		case <-ticker.C:
			t.iteratePeriodTaskItems(false)
		}
	}
}

//获取状态，仅供测试使用
func (t *taskRoutine) Started() bool {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	return t.started
}

//增加数据
//对于非立即启动的任务，首次增加value时，协程才开始启动
func (t *taskRoutine) AddValue(key interface{}, value model.TaskValue) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	var keyExists bool
	var lastValue *TaskItem
	var valueChanged = true
	if lastValue, keyExists = t.mutableTaskValues[key]; keyExists {
		valueChanged = lastValue.value.CompareTo(value) != 0
	}
	taskItem := &TaskItem{
		value: value,
	}
	if t.periodicTask.DelayStart {
		taskItem.lastProcessTime = clock.GetClock().Now()
	}
	t.mutableTaskValues[key] = taskItem
	log.GetBaseLogger().Infof("item %s in task %s has added", key, t.periodicTask.Name)
	if valueChanged {
		t.rebuildImmutableValues()
	}
	if !t.started {
		t.start()
	}
}

//重建只读map
func (t *taskRoutine) rebuildImmutableValues() {
	immutableMap := make(map[interface{}]*TaskItem, len(t.mutableTaskValues))
	for k, v := range t.mutableTaskValues {
		immutableMap[k] = v
	}
	t.immutableTaskValues.Store(immutableMap)
}

//删除数据
//当缓存数据列表为空时，对于非长稳运行的任务，则会结束协程
func (t *taskRoutine) DeleteValue(key interface{}, value model.TaskValue) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	taskItem := t.mutableTaskValues[key]
	value = taskItem.value
	if !reflect2.IsNil(value) && !value.EnsureDeleted(value) {
		//二次校验不通过，不予删除
		return
	}
	log.GetBaseLogger().Infof("item %s in task %s has deleted", key, t.periodicTask.Name)
	delete(t.mutableTaskValues, key)
	t.rebuildImmutableValues()
	if t.periodicTask.LongRun || t.periodicTask.TakePriority {
		return
	}
	if len(t.mutableTaskValues) == 0 {
		t.stop()
	}
}

//通过值来启动任务
func StartTask(taskName string, taskValues model.TaskValues, values map[interface{}]model.TaskValue) {
	for k, v := range values {
		taskValues.AddValue(k, v)
	}
	log.GetBaseLogger().Infof("task %s has been started", taskName)
}
