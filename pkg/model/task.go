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

package model

import "time"

//任务值类型
type TaskValue interface {
	//比较两个元素
	CompareTo(interface{}) int
	//删除前进行检查，返回true才删除，该检查是同步操作
	EnsureDeleted(value interface{}) bool
}

//定时任务处理的数据
type TaskValues interface {
	//获取启动状态
	Started() bool
	//增加数据
	//对于非立即启动的任务，首次增加value时，协程才开始启动
	AddValue(key interface{}, value TaskValue)
	//删除数据
	//当缓存数据列表为空时，对于非长稳运行的任务，则会结束协程
	DeleteValue(key interface{}, value TaskValue)
}

//任务处理结果
type TaskResult int

const (
	// CONTINUE 本次任务处理完毕，更新时间戳并等待下一轮
	CONTINUE TaskResult = iota
	// SKIP 本次任务无需执行，不更新时间戳
	SKIP
	// TERMINATE 后续任务无需再执行
	TERMINATE
)

type TaskEvent int

const (
	// EventStart 任务启动事件
	EventStart TaskEvent = iota
	// EventStop 任务停止事件
	EventStop
)

//回调接口
type PeriodicCallBack interface {
	//任务回调函数
	//参数说明：
	//lastProcessTime：上一次任务处理时间
	//taskKey：任务数据主键
	//taskValue: 任务数据值
	//返回值：
	//TaskResult：后续是否继续执行，本次是否忽略
	Process(taskKey interface{}, taskValue interface{}, lastProcessTime time.Time) TaskResult
	//OnTaskEvent 任务事件回调
	OnTaskEvent(event TaskEvent)
}

//周期执行的任务信息（含高优先级任务)
type PeriodicTask struct {
	//任务标识
	Name string
	//定时任务回调
	CallBack PeriodicCallBack
	//是否需要处理高优先级任务
	TakePriority bool
	//是否长稳运行
	LongRun bool
	//调度周期
	Period time.Duration
	//是否延迟启动，为true的话，则会延迟一个周期再执行第一次任务
	DelayStart bool
}

//调度高优先级任务
type PriorityCallBack interface {
	//处理任务内容
	Process()
}

//任务结构
type PriorityTask struct {
	//任务标识
	Name string
	//任务执行回调函数
	CallBack PriorityCallBack
}
