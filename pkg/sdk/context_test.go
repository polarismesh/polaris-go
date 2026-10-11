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

package sdk

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestInitUID_MultiContextUnique 验证同一进程内多次调用 InitUID（模拟多 SDKContext）
// 得到的 UID 互不相同：首个 context 不带 seq 后缀（向后兼容历史格式），
// 从第二个起追加 "-<seq>" 后缀。
// 注：clientIDSeq 是包级全局状态，本测试运行前可能已被其他测试消耗，
// 故只断言唯一性与前缀，不断言具体 seq 数字。
func TestInitUID_MultiContextUnique(t *testing.T) {
	// PodName 档：多次 InitUID 得到互不相同的 UID，均以 "pod-xyz" 开头
	uids := make([]string, 3)
	for i := range uids {
		tok := &SDKToken{PodName: "pod-xyz", PID: 100}
		tok.InitUID()
		uids[i] = tok.UID
		assert.True(t, strings.HasPrefix(tok.UID, "pod-xyz"), "PodName 档 UID 前缀错误: %s", tok.UID)
	}
	assert.Len(t, uniqueSet(uids), 3, "PodName 档多 context UID 应互不相同")

	// HostName 档：均以 "host-abc-200" 开头
	for i := 0; i < 2; i++ {
		tok := &SDKToken{HostName: "host-abc", PID: 200}
		tok.InitUID()
		assert.True(t, strings.HasPrefix(tok.UID, "host-abc-200"), "HostName 档 UID 前缀错误: %s", tok.UID)
		uids = append(uids, tok.UID)
	}

	// IP 档：均以 "10.0.0.1-300" 开头
	for i := 0; i < 2; i++ {
		tok := &SDKToken{IP: "10.0.0.1", PID: 300}
		tok.InitUID()
		assert.True(t, strings.HasPrefix(tok.UID, "10.0.0.1-300"), "IP 档 UID 前缀错误: %s", tok.UID)
		uids = append(uids, tok.UID)
	}
	// 全部 7 个 UID 互不相同
	assert.Len(t, uniqueSet(uids), len(uids), "多 context 生成的 UID 应全部互不相同")
}

// TestInitUID_FirstContextBackwardCompatible 验证首个 context（seq==0）不追加后缀，
// UID 与历史版本格式完全一致，避免破坏既有按 clientID 匹配的运维脚本/监控/告警。
func TestInitUID_FirstContextBackwardCompatible(t *testing.T) {
	// 重置包级序列，模拟"进程内首个 context"
	resetClientIDSeqForTest()

	tok := &SDKToken{PodName: "pod-a", PID: 1}
	tok.InitUID()
	assert.Equal(t, "pod-a", tok.UID, "首个 context 的 PodName 档不应带 seq 后缀")

	resetClientIDSeqForTest()
	tok = &SDKToken{HostName: "host-a", PID: 1234}
	tok.InitUID()
	assert.Equal(t, "host-a-1234", tok.UID, "首个 context 的 HostName 档应与历史格式一致")

	resetClientIDSeqForTest()
	tok = &SDKToken{IP: "1.2.3.4", PID: 5678}
	tok.InitUID()
	assert.Equal(t, "1.2.3.4-5678", tok.UID, "首个 context 的 IP 档应与历史格式一致")

	// 第二个 context 才追加 -1
	tok2 := &SDKToken{IP: "1.2.3.4", PID: 5678}
	tok2.InitUID()
	assert.Equal(t, "1.2.3.4-5678-1", tok2.UID, "第二个 context 应追加 -1 后缀")
}

// TestInitUID_UUIDFallbackUnique 验证三字段全空时回退随机 UUID，全大写且不重复。
func TestInitUID_UUIDFallbackUnique(t *testing.T) {
	seen := make(map[string]struct{}, 10)
	for i := 0; i < 10; i++ {
		tok := &SDKToken{}
		tok.InitUID()
		assert.NotEmpty(t, tok.UID)
		assert.Equal(t, tok.UID, strings.ToUpper(tok.UID))
		_, dup := seen[tok.UID]
		assert.False(t, dup, "UUID 兜底档不应重复: %s", tok.UID)
		seen[tok.UID] = struct{}{}
	}
}

// TestInitUID_ConcurrentUnique 验证并发调用 InitUID 也能保证 UID 唯一（atomic 序列号线程安全）。
func TestInitUID_ConcurrentUnique(t *testing.T) {
	const n = 100
	var wg sync.WaitGroup
	uids := make([]string, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			tok := &SDKToken{HostName: "h", PID: 1}
			tok.InitUID()
			uids[idx] = tok.UID
		}(i)
	}
	wg.Wait()
	assert.Len(t, uniqueSet(uids), n, "并发生成的 UID 应全部互不相同")
}

// TestInitUID_Priority 验证优先级：PodName > HostName > IP。
func TestInitUID_Priority(t *testing.T) {
	tok := &SDKToken{PodName: "p", HostName: "h", IP: "1.1.1.1", PID: 9}
	tok.InitUID()
	assert.True(t, strings.HasPrefix(tok.UID, "p"), "PodName 优先级最高, got %s", tok.UID)
	assert.NotContains(t, tok.UID, "h", "命中 PodName 档不应含 HostName")

	tok = &SDKToken{HostName: "h", IP: "1.1.1.1", PID: 9}
	tok.InitUID()
	assert.True(t, strings.HasPrefix(tok.UID, "h-9"), "HostName 次优先, got %s", tok.UID)

	tok = &SDKToken{IP: "1.1.1.1", PID: 9}
	tok.InitUID()
	assert.True(t, strings.HasPrefix(tok.UID, "1.1.1.1-9"), "IP 再次之, got %s", tok.UID)
}

// uniqueSet 返回切片中唯一元素集合。
func uniqueSet(items []string) map[string]struct{} {
	set := make(map[string]struct{}, len(items))
	for _, v := range items {
		set[v] = struct{}{}
	}
	return set
}

// resetClientIDSeqForTest 重置包级 clientID 序列，仅供测试模拟"进程内首个 context"。
// 注意：会影响全局状态，使用它的测试不可与其他 InitUID 测试并行（本包测试默认串行）。
func resetClientIDSeqForTest() {
	atomic.StoreUint64(&clientIDSeq, 0)
}
