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

package tcp

import (
	"sync"
	"testing"
)

// TestDetector_LastDialErrConcurrent 验证多个 goroutine 并发操作 lastDialErr map 时
// lastDialErrMu 能正确保护并发读写，不会触发 race condition。
func TestDetector_LastDialErrConcurrent(t *testing.T) {
	detector := &Detector{
		lastDialErr: make(map[string]string, 8),
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
				if i%2 == 0 {
					addr = "127.0.0.1:2"
				}
				// 模拟 doTCPDetect 中对 lastDialErr 的并发读写模式
				detector.lastDialErrMu.Lock()
				if detector.lastDialErr == nil {
					detector.lastDialErr = make(map[string]string, 8)
				}
				errMsg := "connection refused"
				if lastErr, ok := detector.lastDialErr[addr]; !ok || lastErr != errMsg {
					detector.lastDialErr[addr] = errMsg
				}
				detector.lastDialErrMu.Unlock()

				// 模拟 delete 路径
				if i%3 == 0 {
					detector.lastDialErrMu.Lock()
					if detector.lastDialErr != nil {
						delete(detector.lastDialErr, addr)
					}
					detector.lastDialErrMu.Unlock()
				}
			}
		}(g)
	}
	wg.Wait()
	t.Logf("concurrent lastDialErr test completed, goroutines=%d, iterations=%d", goroutines, iterations)
}
