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
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	lmTestNamespace = "ns-test"
	lmTestService   = "svc-test"
)

// findMetadataByKV 在返回列表中查找包含 key=val 的 metadata，便于断言"任一命中"
func findMetadataByKV(list []map[string]string, key, val string) bool {
	for _, m := range list {
		if v, ok := m[key]; ok && v == val {
			return true
		}
	}
	return false
}

// TestLocalMetadataStore_RegisterAndGet 注册后能取回，且返回值为副本
func TestLocalMetadataStore_RegisterAndGet(t *testing.T) {
	s := newLocalMetadataStore()
	src := map[string]string{"env": "prod", "az": "az1"}
	s.Register(lmTestNamespace, lmTestService, 1, "id-1", "1.1.1.1", 80, src)

	got := s.Get(lmTestNamespace, lmTestService)
	assert.Len(t, got, 1)
	assert.Equal(t, "prod", got[0]["env"])
	assert.Equal(t, "az1", got[0]["az"])

	// 修改返回的 map 不影响内部
	got[0]["env"] = "tampered"
	got2 := s.Get(lmTestNamespace, lmTestService)
	assert.Equal(t, "prod", got2[0]["env"])

	// 修改原始 src 不影响内部（浅拷贝隔离）
	src["env"] = "changed"
	got3 := s.Get(lmTestNamespace, lmTestService)
	assert.Equal(t, "prod", got3[0]["env"])
}

// TestLocalMetadataStore_GetEmpty 未注册时 Get 返回 nil
func TestLocalMetadataStore_GetEmpty(t *testing.T) {
	s := newLocalMetadataStore()
	assert.Nil(t, s.Get(lmTestNamespace, lmTestService))
}

// TestLocalMetadataStore_RegisterEmptyInstanceID 无 instanceID 时丢弃
func TestLocalMetadataStore_RegisterEmptyInstanceID(t *testing.T) {
	s := newLocalMetadataStore()
	s.Register(lmTestNamespace, lmTestService, 1, "", "1.1.1.1", 80, map[string]string{"k": "v"})
	assert.Nil(t, s.Get(lmTestNamespace, lmTestService))
}

// TestLocalMetadataStore_RegisterOverwrite 同 (ns, svc, pid, instanceID) 覆盖
func TestLocalMetadataStore_RegisterOverwrite(t *testing.T) {
	s := newLocalMetadataStore()
	s.Register(lmTestNamespace, lmTestService, 1, "id-1", "1.1.1.1", 80, map[string]string{"v": "old"})
	s.Register(lmTestNamespace, lmTestService, 1, "id-1", "1.1.1.1", 80, map[string]string{"v": "new"})

	got := s.Get(lmTestNamespace, lmTestService)
	assert.Len(t, got, 1)
	assert.Equal(t, "new", got[0]["v"])
}

// TestLocalMetadataStore_DeregisterByID 指定 instanceID 删除
func TestLocalMetadataStore_DeregisterByID(t *testing.T) {
	s := newLocalMetadataStore()
	s.Register(lmTestNamespace, lmTestService, 1, "id-1", "1.1.1.1", 80, map[string]string{"k": "1"})
	s.Register(lmTestNamespace, lmTestService, 1, "id-2", "2.2.2.2", 80, map[string]string{"k": "2"})

	s.Deregister(lmTestNamespace, lmTestService, 1, "id-1", "", 0)

	got := s.Get(lmTestNamespace, lmTestService)
	assert.Len(t, got, 1)
	assert.True(t, findMetadataByKV(got, "k", "2"))
	assert.False(t, findMetadataByKV(got, "k", "1"))
}

// TestLocalMetadataStore_DeregisterByHostPort 无 instanceID 时按 (pid, host, port) 精确删
func TestLocalMetadataStore_DeregisterByHostPort(t *testing.T) {
	s := newLocalMetadataStore()
	s.Register(lmTestNamespace, lmTestService, 1, "id-1", "1.1.1.1", 80, map[string]string{"k": "1"})
	s.Register(lmTestNamespace, lmTestService, 1, "id-2", "2.2.2.2", 80, map[string]string{"k": "2"})

	s.Deregister(lmTestNamespace, lmTestService, 1, "", "1.1.1.1", 80)

	got := s.Get(lmTestNamespace, lmTestService)
	assert.Len(t, got, 1)
	assert.True(t, findMetadataByKV(got, "k", "2"))
}

// TestLocalMetadataStore_DeregisterByHostPort_PIDIsolation 不同 PID 的同 host:port 不会互相删除
func TestLocalMetadataStore_DeregisterByHostPort_PIDIsolation(t *testing.T) {
	s := newLocalMetadataStore()
	s.Register(lmTestNamespace, lmTestService, 1, "id-1", "1.1.1.1", 80, map[string]string{"k": "from-pid-1"})
	s.Register(lmTestNamespace, lmTestService, 2, "id-2", "1.1.1.1", 80, map[string]string{"k": "from-pid-2"})

	// 删 PID=1 的 host:port，PID=2 的同 host:port 必须保留
	s.Deregister(lmTestNamespace, lmTestService, 1, "", "1.1.1.1", 80)

	got := s.Get(lmTestNamespace, lmTestService)
	assert.Len(t, got, 1)
	assert.True(t, findMetadataByKV(got, "k", "from-pid-2"))
}

// TestLocalMetadataStore_DeregisterRemovesEmptyBucket 桶清空后顶层 key 也应清理
func TestLocalMetadataStore_DeregisterRemovesEmptyBucket(t *testing.T) {
	s := newLocalMetadataStore()
	s.Register(lmTestNamespace, lmTestService, 1, "id-1", "1.1.1.1", 80, nil)
	s.Deregister(lmTestNamespace, lmTestService, 1, "id-1", "", 0)

	s.mu.RLock()
	_, exist := s.store[buildServiceKey(lmTestNamespace, lmTestService)]
	s.mu.RUnlock()
	assert.False(t, exist, "empty bucket should be removed from top-level store")
}

// TestLocalMetadataStore_MultiInstanceSameService 同 ns+svc 多实例都被返回
func TestLocalMetadataStore_MultiInstanceSameService(t *testing.T) {
	s := newLocalMetadataStore()
	s.Register(lmTestNamespace, lmTestService, 1, "id-1", "1.1.1.1", 80, map[string]string{"role": "primary"})
	s.Register(lmTestNamespace, lmTestService, 1, "id-2", "1.1.1.2", 80, map[string]string{"role": "secondary"})

	got := s.Get(lmTestNamespace, lmTestService)
	assert.Len(t, got, 2)
	assert.True(t, findMetadataByKV(got, "role", "primary"))
	assert.True(t, findMetadataByKV(got, "role", "secondary"))
}

// TestLocalMetadataStore_MultiPID 不同 PID 的同 (ns, svc) 记录共存且都可取
func TestLocalMetadataStore_MultiPID(t *testing.T) {
	s := newLocalMetadataStore()
	s.Register(lmTestNamespace, lmTestService, 1, "id-1", "1.1.1.1", 80, map[string]string{"pid_label": "p1"})
	s.Register(lmTestNamespace, lmTestService, 2, "id-2", "1.1.1.1", 80, map[string]string{"pid_label": "p2"})

	got := s.Get(lmTestNamespace, lmTestService)
	assert.Len(t, got, 2)
	assert.True(t, findMetadataByKV(got, "pid_label", "p1"))
	assert.True(t, findMetadataByKV(got, "pid_label", "p2"))
}

// TestLocalMetadataStore_Concurrent 并发 Register/Deregister/Get 不应数据竞争
func TestLocalMetadataStore_Concurrent(t *testing.T) {
	s := newLocalMetadataStore()
	const workers = 32
	const iters = 200

	var wg sync.WaitGroup
	wg.Add(workers * 3)

	for i := 0; i < workers; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				s.Register(lmTestNamespace, lmTestService, int32(id), "id-"+strconv.Itoa(id)+"-"+strconv.Itoa(j),
					"1.1.1.1", 80, map[string]string{"k": "v"})
			}
		}(i)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				s.Deregister(lmTestNamespace, lmTestService, int32(id),
					"id-"+strconv.Itoa(id)+"-"+strconv.Itoa(j), "", 0)
			}
		}(i)
		go func() {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				_ = s.Get(lmTestNamespace, lmTestService)
			}
		}()
	}
	wg.Wait()
}
