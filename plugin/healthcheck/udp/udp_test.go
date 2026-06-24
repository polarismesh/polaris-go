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

package udp

import (
	"sync"
	"testing"
)

// TestDetector_LastErrConcurrent 验证多个 goroutine 并发操作 lastErr map 时
// lastErrMu 能正确保护并发读写，不会触发 race condition。
func TestDetector_LastErrConcurrent(t *testing.T) {
	detector := &Detector{
		lastErr: make(map[string]string, 8),
	}

	var wg sync.WaitGroup
	const goroutines = 10
	const iterations = 100

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				addr := "127.0.0.1:1"
				stage := "dial"
				if i%2 == 0 {
					addr = "127.0.0.1:2"
				}
				if i%3 == 0 {
					stage = "read"
				}
				// 模拟 logConvergedErr 中对 lastErr 的并发读写模式
				detector.lastErrMu.Lock()
				if detector.lastErr == nil {
					detector.lastErr = make(map[string]string, 8)
				}
				errKey := addr + "|" + stage
				errMsg := "connection refused"
				if lastErr, ok := detector.lastErr[errKey]; !ok || lastErr != errMsg {
					detector.lastErr[errKey] = errMsg
				}
				detector.lastErrMu.Unlock()
			}
		}(g)
	}
	wg.Wait()
	t.Logf("concurrent lastErr test completed, goroutines=%d, iterations=%d", goroutines, iterations)
}

// TestDetector_ClearErrConcurrent 验证 clearErr 与 lastErr 的并发 upsert 交叉执行时
// 持锁保护正确，不触发 concurrent map writes。
//
// 这是对历史 P1 缺陷的回归：doUDPDetect 早返回/成功分支曾直接裸 delete(g.lastErr,...)
// 未持 lastErrMu，而 logConvergedErr 持锁写同一 map；detector 为全局单例被多 worker
// 并发调度时构成数据竞争。必须以 go test -race 运行才能暴露。
func TestDetector_ClearErrConcurrent(t *testing.T) {
	detector := &Detector{
		lastErr: make(map[string]string, 8),
	}

	// 模拟 logConvergedErr 的持锁写（不经过 logCtx，避免依赖全局 logger）
	upsertErr := func(addr, stage, errMsg string) {
		detector.lastErrMu.Lock()
		defer detector.lastErrMu.Unlock()
		if detector.lastErr == nil {
			detector.lastErr = make(map[string]string, 8)
		}
		errKey := addr + "|" + stage
		if lastErr, ok := detector.lastErr[errKey]; !ok || lastErr != errMsg {
			detector.lastErr[errKey] = errMsg
		}
	}

	var wg sync.WaitGroup
	const goroutines = 10
	const iterations = 100
	addrs := []string{"127.0.0.1:1", "127.0.0.1:2", "127.0.0.1:3"}

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				addr := addrs[i%len(addrs)]
				if i%2 == 0 {
					// 写入 err 记录（模拟探测失败上报）
					upsertErr(addr, "read", "i/o timeout")
				} else {
					// 清除 err 记录（模拟探测恢复/成功分支，走修复后的持锁 clearErr）
					detector.clearErr(addr)
				}
			}
		}(g)
	}
	wg.Wait()
	t.Logf("concurrent clearErr test completed, goroutines=%d, iterations=%d", goroutines, iterations)
}

// TestDetector_LogConvergedErr 验证 lastErr map 在不同 stage 和 err 内容变化时的收敛行为。
// 直接测试 map 操作逻辑，不经过 logConvergedErr（避免依赖 logCtx）。
func TestDetector_LogConvergedErr(t *testing.T) {
	detector := &Detector{
		lastErr: make(map[string]string, 8),
	}

	// 模拟 logConvergedErr 的核心收敛逻辑：首次写入、相同 err 不更新、不同 err 更新
	upsertErr := func(addr, stage, errMsg string) {
		detector.lastErrMu.Lock()
		defer detector.lastErrMu.Unlock()
		errKey := addr + "|" + stage
		if lastErr, ok := detector.lastErr[errKey]; !ok || lastErr != errMsg {
			detector.lastErr[errKey] = errMsg
		}
	}

	// 首次错误：应记录
	upsertErr("10.0.0.1:80", "dial", "connection refused")
	assertLastErr(t, detector, "10.0.0.1:80|dial", "connection refused")

	// 相同错误：不应更新记录
	upsertErr("10.0.0.1:80", "dial", "connection refused")
	assertLastErr(t, detector, "10.0.0.1:80|dial", "connection refused")

	// 不同 stage 的相同地址：独立记录
	upsertErr("10.0.0.1:80", "read", "i/o timeout")
	assertLastErr(t, detector, "10.0.0.1:80|dial", "connection refused")
	assertLastErr(t, detector, "10.0.0.1:80|read", "i/o timeout")

	// err 内容变化：应更新
	upsertErr("10.0.0.1:80", "dial", "no route to host")
	assertLastErr(t, detector, "10.0.0.1:80|dial", "no route to host")

	// 不同地址：独立记录
	upsertErr("10.0.0.2:80", "dial", "connection refused")
	assertLastErr(t, detector, "10.0.0.2:80|dial", "connection refused")
	assertLastErr(t, detector, "10.0.0.1:80|dial", "no route to host")
}

func assertLastErr(t *testing.T, d *Detector, key, expected string) {
	t.Helper()
	d.lastErrMu.Lock()
	defer d.lastErrMu.Unlock()
	actual, ok := d.lastErr[key]
	if !ok {
		t.Errorf("lastErr[%s] not found, expected=%q", key, expected)
		return
	}
	if actual != expected {
		t.Errorf("lastErr[%s] = %q, want %q", key, actual, expected)
	}
}
