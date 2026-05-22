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

package quota

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestShouldLogRemoteErr_FirstCallAlwaysLogs 验证：首次调用必打（避免静默丢失第一条错误）.
func TestShouldLogRemoteErr_FirstCallAlwaysLogs(t *testing.T) {
	w := &RateLimitWindow{}
	shouldLog, suppressed := w.shouldLogRemoteErr(time.Now().UnixNano())
	assert.True(t, shouldLog, "首次调用必须打")
	assert.Equal(t, int64(0), suppressed, "首次没有被压制的累计")
}

// TestShouldLogRemoteErr_WithinIntervalSuppresses 验证：限频窗口内的连续调用被压制并累计.
func TestShouldLogRemoteErr_WithinIntervalSuppresses(t *testing.T) {
	w := &RateLimitWindow{}
	now := time.Now().UnixNano()
	// 第一次必打
	w.shouldLogRemoteErr(now)
	// 接下来 99 次都在 1ms 内 → 全部压制
	for i := 0; i < 99; i++ {
		shouldLog, _ := w.shouldLogRemoteErr(now + int64(i*int(time.Millisecond)))
		assert.False(t, shouldLog, "限频窗口内调用 #%d 应被压制", i+2)
	}
	assert.Equal(t, int64(99), atomic.LoadInt64(&w.remoteErrLogSuppressedCount),
		"99 次调用应全部累计")
}

// TestShouldLogRemoteErr_AfterIntervalLogsAgain 验证：跨过限频间隔后再次允许打，并报告累计被压制次数.
func TestShouldLogRemoteErr_AfterIntervalLogsAgain(t *testing.T) {
	w := &RateLimitWindow{}
	t0 := int64(1_000_000_000) // 任意起点（1s in ns）
	w.shouldLogRemoteErr(t0)
	// 模拟 50 次被压制
	for i := 0; i < 50; i++ {
		w.shouldLogRemoteErr(t0 + int64(i)*int64(time.Millisecond))
	}
	assert.Equal(t, int64(50), atomic.LoadInt64(&w.remoteErrLogSuppressedCount))

	// 跨过 5s 间隔后再调用
	shouldLog, suppressed := w.shouldLogRemoteErr(t0 + remoteErrLogIntervalNano + 1)
	assert.True(t, shouldLog, "跨过限频间隔后应允许打印")
	assert.Equal(t, int64(50), suppressed, "应携带累计的被压制次数")
	assert.Equal(t, int64(0), atomic.LoadInt64(&w.remoteErrLogSuppressedCount),
		"打印时累计计数应被重置")
}
