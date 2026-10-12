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
	"errors"
	"fmt"
	"log"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

// suite 共享同一个 SDK 配置组，保证缓存换代始终发生在同一 listener 容器上。
type suite struct {
	opts        options
	fixture     *fixture
	files       polaris.ConfigAPI
	groups      polaris.ConfigGroupAPI
	group       model.ConfigFileGroup
	observer    *fetcherStats
	sequence    int
	lastVersion uint64
}

// runE2E 创建数据和真实 SDK，顺序执行场景；所有错误（含清理错误）向上传播。
func runE2E(parent context.Context, o options) (result error) {
	if err := o.validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, o.runTimeout)
	defer cancel()
	fixture, err := newFixture(o, os.Getenv("POLARIS_TOKEN"))
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, fixture.cleanup()) }()
	if err := fixture.prepare(ctx); err != nil {
		return err
	}
	cfg := config.NewDefaultConfiguration([]string{o.discoveryAddr})
	cfg.GetGlobal().GetServerConnector().SetToken(fixture.token)
	cfg.GetConfigFile().GetConfigConnectorConfig().SetAddresses([]string{o.configAddr})
	cfg.GetConfigFile().GetConfigConnectorConfig().SetToken(fixture.token)
	cfg.GetConfigFile().GetConfigConnectorConfig().SetConnectTimeout(3 * time.Second)
	cfg.GetConfigFile().GetConfigConnectorConfig().SetMessageTimeout(3 * time.Second)
	sdkCtx, err := polaris.NewSDKContextByConfig(cfg)
	if err != nil {
		return fmt.Errorf("create SDK: %w", err)
	}
	defer sdkCtx.Destroy()
	s := &suite{
		opts: o, fixture: fixture, files: polaris.NewConfigAPIByContext(sdkCtx),
		groups:   polaris.NewConfigGroupAPIByContext(sdkCtx),
		observer: &fetcherStats{versions: make(map[uint64]int)},
	}
	if err := s.initialize(ctx); err != nil {
		return err
	}
	scenarios := []struct {
		name string
		run  func(context.Context) error
	}{
		{"request-context-and-normal-stop", s.normalStop},
		{"stop-timeout-mark-stopping", s.stopTimeout},
		{"late-registration-after-cleanup", s.lateRegistration},
		{"ttl-expiration-overlapping-generations", s.churn},
		{"callback-self-cancellation", s.selfCancellation},
	}
	for _, scenario := range scenarios {
		log.Printf("[RUN] %s", scenario.name)
		if err := scenario.run(ctx); err != nil {
			return fmt.Errorf("%s: %w", scenario.name, err)
		}
		log.Printf("[PASS] %s", scenario.name)
	}
	runtime.KeepAlive(sdkCtx)
	return nil
}

// initialize 发布基线，并检查 Fetch/Get 两个公开入口均返回同一可取消对象。
func (s *suite) initialize(ctx context.Context) error {
	version, err := s.publish(ctx)
	if err != nil {
		return err
	}
	group, err := s.loadGroup()
	if err != nil {
		return err
	}
	s.group = group
	if _, ok := group.(model.CancelableConfigGroupListener); !ok {
		return fmt.Errorf("FetchConfigGroup returned %T without cancelable capability", group)
	}
	getGroup, err := s.groups.GetConfigGroup(s.fixture.namespace, s.fixture.group)
	if err != nil {
		return err
	}
	if getGroup != group {
		return fmt.Errorf("GetConfigGroup and FetchConfigGroup returned different objects")
	}
	// 旧接口观察器始终存活，用于证明取消后确有新事件到达同一个 SDK 组。
	group.AddChangeListener(s.observer.record)
	step, cancel := context.WithTimeout(ctx, s.opts.timeout)
	defer cancel()
	return waitUntil(step, "initial published file visible", func() bool {
		files, _, _ := group.GetFiles()
		for _, file := range files {
			if file.FileName == fixtureFileName && file.Version == version {
				return true
			}
		}
		return false
	})
}

// loadGroup 每次走公开 FetchConfigGroup，校验缓存换代没有创建新的 SDK 分组。
func (s *suite) loadGroup() (model.ConfigFileGroup, error) {
	group, err := s.groups.FetchConfigGroup(&polaris.GetConfigGroupRequest{
		GetConfigGroupRequest: &model.GetConfigGroupRequest{
			Namespace: s.fixture.namespace, FileGroup: s.fixture.group, Subscribe: true, Mode: model.SDKMode,
		},
	})
	if err != nil {
		return nil, err
	}
	if s.group != nil && s.group != group {
		return nil, fmt.Errorf("SDK did not reuse the original config group")
	}
	return group, nil
}

// publish 通过真实 SDK 更新并发布唯一内容，再独立拉取文件确认内容并获取服务端版本号。
// 分组元数据可能不携带 MD5，不能用摘要识别事件，也不能假定版本号从 1 连续增长。
func (s *suite) publish(ctx context.Context) (uint64, error) {
	s.sequence++
	content := fmt.Sprintf("run: %s\ngeneration: %d\n", s.fixture.group, s.sequence)
	if err := s.files.UpdateConfigFile(s.fixture.namespace, s.fixture.group, fixtureFileName, content); err != nil {
		return 0, fmt.Errorf("update test config: %w", err)
	}
	if err := s.files.PublishConfigFile(s.fixture.namespace, s.fixture.group, fixtureFileName); err != nil {
		return 0, fmt.Errorf("publish test config: %w", err)
	}
	step, cancel := context.WithTimeout(ctx, s.opts.timeout)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		if err := step.Err(); err != nil {
			return 0, errors.Join(fmt.Errorf("confirm published config content and version: %w", err), lastErr)
		}
		// 不订阅文件，避免长期缓存命中；确认的是独立文件拉取结果，随后仍需检查分组回调。
		file, err := s.files.FetchConfigFile(&polaris.GetConfigFileRequest{
			GetConfigFileRequest: &model.GetConfigFileRequest{
				Namespace: s.fixture.namespace, FileGroup: s.fixture.group, FileName: fixtureFileName,
				Subscribe: false, Mode: model.SDKMode,
			},
		})
		lastErr = err
		if err == nil && file.GetContent() == content && file.GetVersion() > s.lastVersion {
			s.lastVersion = file.GetVersion()
			log.Printf("[PUBLISH] generation=%d version=%d", s.sequence, s.lastVersion)
			return s.lastVersion, nil
		}
		select {
		case <-step.Done():
		case <-ticker.C:
		}
	}
}

// pulse 等待新发布抵达仍存活的旧接口观察器和当前缓存，核对每个缓存恰好收到一次。
func (s *suite) pulse(ctx context.Context, active []*resourceCache, retired []retiredFetcher) error {
	version, err := s.publish(ctx)
	if err != nil {
		return err
	}
	step, cancel := context.WithTimeout(ctx, s.opts.timeout)
	defer cancel()
	err = waitUntil(step, "published event reaches active listeners", func() bool {
		if s.observer.count(version) == 0 {
			return false
		}
		for _, cache := range active {
			if cache.fetcher.stats.count(version) == 0 {
				return false
			}
		}
		return true
	})
	if err != nil {
		return err
	}
	// 等待完整分发释放其临时回调快照；旧 fetcher 的弱引用在这里提供额外的泄漏断言。
	gcErr := waitUntil(step, "retired fetchers reclaimed while SDK group remains cached", func() bool {
		runtime.GC()
		for _, old := range retired {
			if old.ref.Value() != nil {
				return false
			}
		}
		return true
	})
	for _, cache := range active {
		if count := cache.fetcher.stats.count(version); count != 1 {
			return fmt.Errorf("active fetcher got %d callbacks for one publication, want 1", count)
		}
	}
	retained, staleCalls := 0, 0
	for _, old := range retired {
		staleCalls += old.stats.count(version)
		if old.ref.Value() != nil {
			retained++
		}
	}
	log.Printf("[CHECK] active=%d retired=%d retained=%d staleCallbacks=%d", len(active), len(retired), retained, staleCalls)
	if staleCalls != 0 || retained != 0 {
		return errors.Join(fmt.Errorf("listener leak: retired=%d retained=%d staleCallbacks=%d",
			len(retired), retained, staleCalls), gcErr)
	}
	return gcErr
}

// start 创建一代缓存并等待 WarmUp；出错时停止已创建的全部缓存。
func (s *suite) start(ctx context.Context, count int) ([]*resourceCache, error) {
	caches := make([]*resourceCache, count)
	for i := range caches {
		caches[i] = newResourceCache(s.loadGroup, s.opts.listenerMode, s.opts.stopTimeout, nil, nil)
	}
	for _, cache := range caches {
		if err := s.awaitReady(ctx, cache); err != nil {
			for _, created := range caches {
				created.Stop()
			}
			return nil, err
		}
	}
	return caches, nil
}

// awaitReady 等待 WarmUp 完成，并返回其原始错误。
func (s *suite) awaitReady(ctx context.Context, cache *resourceCache) error {
	step, cancel := context.WithTimeout(ctx, s.opts.timeout)
	defer cancel()
	if err := waitSignal(step, cache.ready, "cache WarmUp"); err != nil {
		return err
	}
	return cache.runErr
}

// awaitStopped 等待取消闭包和后台协程都退出，不把 Cleanup/MarkStopping 标记当作取消成功。
func (s *suite) awaitStopped(ctx context.Context, cache *resourceCache) error {
	step, cancel := context.WithTimeout(ctx, s.opts.timeout)
	defer cancel()
	if err := waitSignal(step, cache.fetcher.stats.detached, "SDK listener cancellation"); err != nil {
		return err
	}
	return waitSignal(step, cache.runDone, "cache worker exit")
}

// retireNormally 多次 Stop 后等待取消完成，生成仅包含弱引用的记录并清空传入缓存切片。
func (s *suite) retireNormally(ctx context.Context, caches []*resourceCache) ([]retiredFetcher, error) {
	retired := make([]retiredFetcher, 0, len(caches))
	for i, cache := range caches {
		if !cache.Stop() || !cache.Stop() || !cache.fetcher.cleaned.Load() {
			return nil, fmt.Errorf("normal Stop did not perform Cleanup")
		}
		if err := s.awaitStopped(ctx, cache); err != nil {
			return nil, err
		}
		retired = append(retired, cache.retire())
		caches[i] = nil
	}
	return retired, nil
}

// normalStop 验证 HTTP 请求取消不摘掉缓存监听，多次 Fetch 不重复注册，缓存 Stop 后旧对象可回收。
func (s *suite) normalStop(ctx context.Context) error {
	caches, err := s.start(ctx, 1)
	if err != nil {
		return err
	}
	defer func() {
		if caches[0] != nil {
			caches[0].Stop()
		}
	}()
	requestCtx, requestCancel := context.WithCancel(ctx)
	for i := 0; i < 5; i++ {
		if err := caches[0].fetcher.Fetch(requestCtx); err != nil {
			requestCancel()
			return err
		}
	}
	requestCancel()
	if count := caches[0].fetcher.stats.registrations.Load(); count != 1 {
		return fmt.Errorf("repeated Fetch registered %d listeners", count)
	}
	if err := s.pulse(ctx, caches, nil); err != nil {
		return err
	}
	retired, err := s.retireNormally(ctx, caches)
	if err != nil {
		return err
	}
	return s.pulse(ctx, nil, retired)
}

// stopTimeout 验证后台工作不退出时 Stop 仅 MarkStopping，但生命周期取消仍会摘除 SDK 监听。
func (s *suite) stopTimeout(ctx context.Context) error {
	retired, err := s.stopBlockedCache(ctx, false)
	if err != nil {
		return err
	}
	return s.pulse(ctx, nil, []retiredFetcher{retired})
}

// lateRegistration 验证 Cleanup 先执行，阻塞的 WarmUp 随后注册时，已取消 context 仍能摘除监听。
func (s *suite) lateRegistration(ctx context.Context) error {
	retired, err := s.stopBlockedCache(ctx, true)
	if err != nil {
		return err
	}
	return s.pulse(ctx, nil, []retiredFetcher{retired})
}

// stopBlockedCache 通过门信号构造超时或晚注册，返回前释放所有强引用和后台工作。
func (s *suite) stopBlockedCache(ctx context.Context, late bool) (retiredFetcher, error) {
	gate := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gate) }) }
	var registerGate, workerGate <-chan struct{}
	if late {
		registerGate = gate
	} else {
		workerGate = gate
	}
	cache := newResourceCache(s.loadGroup, s.opts.listenerMode, s.opts.stopTimeout, registerGate, workerGate)
	defer func() { release(); cache.Stop() }()
	step, cancel := context.WithTimeout(ctx, s.opts.timeout)
	defer cancel()
	if err := waitSignal(step, cache.started, "Run context created"); err != nil {
		return retiredFetcher{}, err
	}
	if !late {
		if err := s.awaitReady(ctx, cache); err != nil {
			return retiredFetcher{}, err
		}
		if err := s.pulse(ctx, []*resourceCache{cache}, nil); err != nil {
			return retiredFetcher{}, err
		}
	}
	if normal := cache.Stop(); normal != late || cache.fetcher.cleaned.Load() != late {
		return retiredFetcher{}, fmt.Errorf("unexpected stop path: normal=%v cleaned=%v late=%v",
			normal, cache.fetcher.cleaned.Load(), late)
	}
	if !late {
		// 工作仍堵塞时就要完成 listener 摘除，不能等 workerGate 释放再算成功。
		if err := waitSignal(step, cache.fetcher.stats.detached, "cancel during Stop timeout"); err != nil {
			return retiredFetcher{}, err
		}
	}
	release()
	if err := s.awaitReady(ctx, cache); err != nil {
		return retiredFetcher{}, err
	}
	if err := s.awaitStopped(ctx, cache); err != nil {
		return retiredFetcher{}, err
	}
	return cache.retire(), nil
}

// churn 用实际定时器触发过期，并让新旧缓存代际短暂重叠；仅保留弱引用验证历史对象不累积。
func (s *suite) churn(ctx context.Context) error {
	current, err := s.start(ctx, s.opts.fetchers)
	if err != nil {
		return err
	}
	defer func() {
		for _, cache := range current {
			if cache != nil {
				cache.Stop()
			}
		}
	}()
	if err := s.pulse(ctx, current, nil); err != nil {
		return err
	}
	var retired []retiredFetcher
	for round := 0; round < s.opts.rounds; round++ {
		next, err := s.start(ctx, s.opts.fetchers)
		if err != nil {
			return err
		}
		if round == 0 {
			// 在首次交叠期间发布一次，确认新旧两代各有一个独立的活监听器。
			both := append(append([]*resourceCache{}, current...), next...)
			if err := s.pulse(ctx, both, nil); err != nil {
				for _, cache := range next {
					cache.Stop()
				}
				return err
			}
		}
		expired := make(chan struct{})
		old := current
		timer := time.AfterFunc(s.opts.ttl, func() {
			for _, cache := range old {
				cache.Stop()
			}
			close(expired)
		})
		if err := waitSignal(ctx, expired, "cache TTL expiration"); err != nil {
			timer.Stop()
			for _, cache := range next {
				cache.Stop()
			}
			return err
		}
		gone, retireErr := s.retireNormally(ctx, old)
		current = next
		if retireErr != nil {
			return retireErr
		}
		retired = append(retired, gone...)
		log.Printf("[TTL] round=%d/%d active=%d retired=%d", round+1, s.opts.rounds, len(current), len(retired))
	}
	if err := s.pulse(ctx, current, retired); err != nil {
		return err
	}
	last, err := s.retireNormally(ctx, current)
	if err != nil {
		return err
	}
	return s.pulse(ctx, nil, append(retired, last...))
}

// selfCancellation 验证真实仓库分发中自取消不会死锁，后续发布仍能抵达旧接口观察器。
func (s *suite) selfCancellation(ctx context.Context) error {
	listener := s.group.(model.CancelableConfigGroupListener)
	stats := &fetcherStats{versions: make(map[uint64]int)}
	canceled := make(chan struct{})
	ready := make(chan struct{})
	var cancel func()
	var once sync.Once
	cancel = listener.AddChangeListenerWithCancel(func(event *model.ConfigGroupChangeEvent) {
		<-ready
		stats.record(event)
		once.Do(func() { cancel(); close(canceled) })
	})
	close(ready)
	defer cancel()
	first, err := s.publish(ctx)
	if err != nil {
		return err
	}
	step, stop := context.WithTimeout(ctx, s.opts.timeout)
	defer stop()
	if err := waitSignal(step, canceled, "callback self cancellation"); err != nil {
		return err
	}
	if stats.count(first) != 1 {
		return fmt.Errorf("self-canceling listener did not receive its publication")
	}
	second, err := s.publish(ctx)
	if err != nil {
		return err
	}
	if err := waitUntil(step, "publication after self cancellation", func() bool {
		return s.observer.count(second) > 0
	}); err != nil {
		return err
	}
	if stats.count(second) != 0 {
		return fmt.Errorf("self-canceled listener received a later publication")
	}
	return nil
}
