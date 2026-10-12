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

package main

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
	"weak"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// fetcherStats 独立保存断言数据，不反向引用 fetcher；回调计数发生在停止标记检查之前。
type fetcherStats struct {
	registrations atomic.Int64
	mu            sync.Mutex
	versions      map[uint64]int
	detached      chan struct{}
}

// record 记录实际进入 SDK 回调的目标文件版本，包括被 MarkStopping 拒绝的回调。
func (s *fetcherStats) record(event *model.ConfigGroupChangeEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, file := range event.After {
		if file.FileName == fixtureFileName {
			s.versions[file.Version]++
		}
	}
}

// count 返回某次发布对应的回调次数，可以与 SDK 分发并发调用。
func (s *fetcherStats) count(version uint64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.versions[version]
}

// configGroupFetcher 保留 access 的方法值回调、配置组引用和停止标记；不实现其业务内容转换。
type configGroupFetcher struct {
	mu           sync.Mutex
	group        model.ConfigFileGroup
	load         func() (model.ConfigFileGroup, error)
	registerGate <-chan struct{}
	mode         string
	stats        *fetcherStats
	stopped      atomic.Bool
	cleaned      atomic.Bool
	// 保留非微小对象，便于用弱引用验证方法值是否仍使旧 fetcher 存活。
	payload [4096]byte
}

// Fetch 首次获取真实 SDK 分组并注册监听；ctx 必须为缓存生命周期，而非 HTTP 请求 context。
// registerGate 非 nil 时模拟远端返回后仍阻塞的 WarmUp，故刻意不响应 ctx 取消。
func (f *configGroupFetcher) Fetch(ctx context.Context) error {
	f.mu.Lock()
	initialized := f.group != nil
	f.mu.Unlock()
	if initialized {
		return nil
	}
	group, err := f.load()
	if err != nil {
		return err
	}
	if f.registerGate != nil {
		<-f.registerGate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.group != nil {
		return nil
	}
	if f.mode == "legacy" {
		group.AddChangeListener(f.onConfigGroupChange)
		close(f.stats.detached) // 对照组无取消能力；继续运行以检查真实的回调/对象泄漏。
	} else {
		cg, ok := group.(model.CancelableConfigFileGroup)
		if !ok {
			return fmt.Errorf("SDK 返回 %T，不支持 CancelableConfigFileGroup", group)
		}
		cancel := cg.AddChangeListenerWithCancel(f.onConfigGroupChange)
		detached := f.stats.detached
		context.AfterFunc(ctx, func() {
			cancel()
			close(detached)
		}) // 只捕获 SDK 取消函数和完成信号，不捕获 f。
	}
	f.stats.registrations.Add(1)
	f.group = group
	return nil
}

// onConfigGroupChange 记录原始回调入口，避免停止标记掩盖监听器未摘除的问题。
func (f *configGroupFetcher) onConfigGroupChange(event *model.ConfigGroupChangeEvent) {
	f.stats.record(event)
	if f.stopped.Load() {
		return
	}
	// 真实 access 在这里更新索引并唤醒 long polling；本测试以版本计数验证通知抵达。
}

// MarkStopping 模拟 Stop 超时路径，仅拒绝后续业务处理，不清理引用。
func (f *configGroupFetcher) MarkStopping() { f.stopped.Store(true) }

// Cleanup 模拟正常停止路径，释放配置组引用；允许阻塞中的 WarmUp 在此后才注册监听。
func (f *configGroupFetcher) Cleanup() {
	f.MarkStopping()
	f.mu.Lock()
	f.group = nil
	f.mu.Unlock()
	f.cleaned.Store(true)
}

// resourceCache 模拟 Run/WarmUp/Stop；工作门用于确定性构造 Stop 超时。
type resourceCache struct {
	fetcher     *configGroupFetcher
	stopTimeout time.Duration
	workerGate  <-chan struct{}
	mu          sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
	workerDone  chan struct{}
	started     chan struct{}
	ready       chan struct{}
	stopped     chan struct{}
	runDone     chan struct{}
	runErr      error
	stopOnce    sync.Once
	normalStop  bool
}

// newResourceCache 构造并异步 Run；调用方通过 started/ready 等待 context 创建及 WarmUp 完成。
func newResourceCache(load func() (model.ConfigFileGroup, error), mode string, timeout time.Duration,
	registerGate, workerGate <-chan struct{}) *resourceCache {
	c := &resourceCache{
		fetcher: &configGroupFetcher{
			load: load, mode: mode, registerGate: registerGate,
			stats: &fetcherStats{versions: make(map[uint64]int), detached: make(chan struct{})},
		},
		stopTimeout: timeout, workerGate: workerGate,
		started: make(chan struct{}), ready: make(chan struct{}),
		stopped: make(chan struct{}), runDone: make(chan struct{}),
	}
	go c.Run()
	return c
}

// Run 创建缓存独立 context 并执行 WarmUp；WarmUp 完成后才发布后台工作状态。
func (c *resourceCache) Run() {
	defer close(c.runDone)
	c.mu.Lock()
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.mu.Unlock()
	close(c.started)
	c.runErr = c.fetcher.Fetch(c.ctx)
	c.mu.Lock()
	if c.runErr == nil && c.ctx.Err() == nil {
		c.workerDone = make(chan struct{})
	}
	workerDone := c.workerDone
	c.mu.Unlock()
	close(c.ready)
	if workerDone != nil {
		defer close(workerDone)
		<-c.ctx.Done()
		if c.workerGate != nil {
			<-c.workerGate
		}
	}
}

// Stop 先取消生命周期 context，再限时等待工作结束；WarmUp 尚未完成时允许先 Cleanup。
// 返回 true 表示正常清理，false 表示仅 MarkStopping；重复或并发调用结果一致。
func (c *resourceCache) Stop() bool {
	c.stopOnce.Do(func() {
		defer close(c.stopped)
		<-c.started
		c.mu.Lock()
		c.cancel()
		workerDone := c.workerDone
		c.mu.Unlock()
		c.normalStop = true
		if workerDone != nil {
			timer := time.NewTimer(c.stopTimeout)
			defer timer.Stop()
			select {
			case <-workerDone:
			case <-timer.C:
				c.normalStop = false
			}
		}
		if c.normalStop {
			c.fetcher.Cleanup()
		} else {
			c.fetcher.MarkStopping()
		}
	})
	return c.normalStop
}

// retiredFetcher 仅持有统计信息和弱引用，不延长旧 fetcher 的生命周期。
type retiredFetcher struct {
	stats *fetcherStats
	ref   weak.Pointer[configGroupFetcher]
}

// retire 建立回收验证记录；调用方需同时释放 resourceCache 的强引用。
func (c *resourceCache) retire() retiredFetcher {
	return retiredFetcher{stats: c.fetcher.stats, ref: weak.Make(c.fetcher)}
}
