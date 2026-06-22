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

package composite

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/polarismesh/polaris-go/pkg/log"
)

// newTestLogCtx 构造一个已初始化的 ContextLogger，供 newTaskExecutor 的 recovery 使用。
func newTestLogCtx() *log.ContextLogger {
	logCtx := &log.ContextLogger{}
	logCtx.Init()
	return logCtx
}

// TestTaskExecutor_IntervalExecute_Repeats 验证 IntervalExecute 注册的任务会按周期
// 反复执行，而不是只执行一次。
// 测试场景：以 50ms 周期注册一个计数回调，运行约 350ms 后计数应明显大于 1。
// 前置条件：newTaskExecutor 正常启动 worker 主循环。
// 预期结果：回调执行次数 >= 3（周期任务未退化为一次性任务）。
func TestTaskExecutor_IntervalExecute_Repeats(t *testing.T) {
	executor := newTaskExecutor(2, newTestLogCtx())
	defer executor.Stop()

	var count int32
	executor.IntervalExecute(50*time.Millisecond, func() {
		atomic.AddInt32(&count, 1)
	})

	// 350ms / 50ms ≈ 7 次，放宽下限到 3 次以容忍调度抖动（100ms 巡检粒度）。
	time.Sleep(350 * time.Millisecond)
	got := atomic.LoadInt32(&count)
	assert.GreaterOrEqual(t, got, int32(3),
		"IntervalExecute 应周期重复执行，实际执行次数=%d（=1 说明退化为一次性任务）", got)
}

// TestTaskExecutor_DelayExecute_Once 验证 DelayExecute 注册的任务只执行一次。
// 测试场景：以 50ms 延迟注册一个计数回调，运行约 300ms 后计数应恒为 1。
// 前置条件：newTaskExecutor 正常启动 worker 主循环。
// 预期结果：回调执行次数 == 1（一次性任务不会被重复触发）。
func TestTaskExecutor_DelayExecute_Once(t *testing.T) {
	executor := newTaskExecutor(2, newTestLogCtx())
	defer executor.Stop()

	var count int32
	executor.DelayExecute(50*time.Millisecond, func() {
		atomic.AddInt32(&count, 1)
	})

	time.Sleep(300 * time.Millisecond)
	got := atomic.LoadInt32(&count)
	assert.Equal(t, int32(1), got,
		"DelayExecute 应只执行一次，实际执行次数=%d", got)
}

// TestTaskExecutor_Stop_HaltsInterval 验证 executor Stop 后周期任务停止执行。
// 测试场景：注册 50ms 周期任务，运行一段时间后 Stop，记录 Stop 时计数，再等待，
// 计数不应继续显著增长。
// 前置条件：newTaskExecutor 正常启动。
// 预期结果：Stop 后计数基本不再增长（最多容忍 1 次在途回调）。
func TestTaskExecutor_Stop_HaltsInterval(t *testing.T) {
	executor := newTaskExecutor(2, newTestLogCtx())

	var count int32
	executor.IntervalExecute(50*time.Millisecond, func() {
		atomic.AddInt32(&count, 1)
	})

	time.Sleep(200 * time.Millisecond)
	executor.Stop()
	afterStop := atomic.LoadInt32(&count)

	time.Sleep(200 * time.Millisecond)
	final := atomic.LoadInt32(&count)

	assert.LessOrEqual(t, final-afterStop, int32(1),
		"Stop 后周期任务应停止，stop时=%d 最终=%d", afterStop, final)
}
