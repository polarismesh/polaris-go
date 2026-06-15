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

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestHalfOpenStatus_AcquirePermission_ConcurrentNoOverGrant 测试场景：高并发下半开配额发放不超额
// 前置条件：maxRequest=N，并发 G=1000 个 goroutine 抢占 AcquirePermission
// 预期结果：恰好 N 次返回 true、其余返回 false；最终 allocated == N
func TestHalfOpenStatus_AcquirePermission_ConcurrentNoOverGrant(t *testing.T) {
	const maxRequest = 7
	const goroutines = 1000

	hs := NewHalfOpenStatus("rule-A", time.Now(), maxRequest).(*HalfOpenStatus)

	var success int64
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if hs.AcquirePermission() {
				atomic.AddInt64(&success, 1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(maxRequest), atomic.LoadInt64(&success),
		"applies should equal maxRequest")
	assert.Equal(t, int64(maxRequest), hs.AllocatedCount(),
		"allocated should equal maxRequest after concurrent acquire")
}

// TestHalfOpenStatus_AcquirePermission_RejectsWhenMaxRequestZero 测试场景：maxRequest=0 时直接拒绝放行
// 前置条件：使用 maxRequest=0 构造 HalfOpenStatus
// 预期结果：AcquirePermission 始终返回 false，避免半开态下无限放行
func TestHalfOpenStatus_AcquirePermission_RejectsWhenMaxRequestZero(t *testing.T) {
	hs := NewHalfOpenStatus("rule-zero", time.Now(), 0).(*HalfOpenStatus)
	assert.False(t, hs.AcquirePermission())
	assert.Equal(t, int64(0), hs.AllocatedCount())
}

// TestHalfOpenStatus_Release_AllSuccessTransitsToClose 测试场景：归集结果全部成功且达阈值时返回 Close
// 前置条件：maxRequest=3，依次 Release(true) 三次
// 预期结果：第三次 Release 返回 true（触发判定）；CalNextStatus 返回 Close
func TestHalfOpenStatus_Release_AllSuccessTransitsToClose(t *testing.T) {
	hs := NewHalfOpenStatus("rule-close", time.Now(), 3).(*HalfOpenStatus)

	assert.False(t, hs.Release(true))
	assert.False(t, hs.Release(true))
	assert.True(t, hs.Release(true))

	assert.Equal(t, int64(3), hs.FinishedCount())
	assert.Equal(t, Close, hs.CalNextStatus())
}

// TestHalfOpenStatus_Release_AnyFailureTransitsToOpen 测试场景：归集结果任一失败立即返回 Open
// 前置条件：maxRequest=3，先 Release(true) 一次再 Release(false) 一次
// 预期结果：失败那次 Release 返回 true；CalNextStatus 返回 Open
func TestHalfOpenStatus_Release_AnyFailureTransitsToOpen(t *testing.T) {
	hs := NewHalfOpenStatus("rule-open", time.Now(), 3).(*HalfOpenStatus)

	assert.False(t, hs.Release(true))
	assert.True(t, hs.Release(false))

	assert.Equal(t, Open, hs.CalNextStatus())
}

// TestHalfOpenStatus_Release_PartialResultsKeepHalfOpen 测试场景：归集未达阈值且全部成功时保持 HalfOpen
// 前置条件：maxRequest=5，仅 Release(true) 两次
// 预期结果：CalNextStatus 返回 HalfOpen，未触发切换
func TestHalfOpenStatus_Release_PartialResultsKeepHalfOpen(t *testing.T) {
	hs := NewHalfOpenStatus("rule-half", time.Now(), 5).(*HalfOpenStatus)

	assert.False(t, hs.Release(true))
	assert.False(t, hs.Release(true))

	assert.Equal(t, HalfOpen, hs.CalNextStatus())
}
