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

package configuration

import (
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/configconnector"
)

// newTestConfigGroup 创建无需远端服务的配置分组并捕获日志。
// 测试结束后恢复全局 logger，调用方不能并行运行测试且需等待后台分发退出。
func newTestConfigGroup(t *testing.T) (*defaultConfigGroup, *captureLogger) {
	t.Helper()
	original := log.GetBaseLogger()
	logger := &captureLogger{}
	log.SetBaseLogger(logger)
	t.Cleanup(func() { log.SetBaseLogger(original) })
	repo := &ConfigGroupRepo{remoteRef: &atomic.Value{}}
	return newDefaultConfigGroup("default", "test-group", repo), logger
}

// waitForConfigGroupListener 等待并发操作完成；超时则报告对应的监听器操作失败。
func waitForConfigGroupListener(t *testing.T, done <-chan struct{}, operation string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", operation)
	}
}

// TestDefaultConfigGroup_AddChangeListenerWithCancel 验证事件内容、空回调和幂等取消语义。
func TestDefaultConfigGroup_AddChangeListenerWithCancel(t *testing.T) {
	tests := []struct {
		name         string
		nilCallback  bool
		cancelBefore int
		cancelAfter  int
		wantCalls    int
	}{
		{name: "receive then cancel", cancelAfter: 1, wantCalls: 1},
		{name: "cancel before event", cancelBefore: 1, cancelAfter: 1},
		{name: "repeated cancel", cancelAfter: 3, wantCalls: 1},
		{name: "repeated early cancel", cancelBefore: 3, cancelAfter: 3},
		{name: "nil callback", nilCallback: true, cancelAfter: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			group, logger := newTestConfigGroup(t)
			oldVal := &configconnector.ConfigGroupResponse{
				ReleaseFiles: []*model.SimpleConfigFile{{FileName: "old.yaml", Version: 1}},
			}
			newVal := &configconnector.ConfigGroupResponse{
				ReleaseFiles: []*model.SimpleConfigFile{{FileName: "new.yaml", Version: 2}},
			}
			calls := 0
			var got *model.ConfigGroupChangeEvent
			var cb model.OnConfigGroupChange
			if !tt.nilCallback {
				cb = func(event *model.ConfigGroupChangeEvent) {
					calls++
					got = event
				}
			}
			cancel := group.AddChangeListenerWithCancel(cb)
			if cancel == nil {
				t.Fatal("registration returned a nil cancel function")
			}
			for i := 0; i < tt.cancelBefore; i++ {
				cancel()
			}
			group.repoChangeListener(oldVal, newVal)
			if calls != tt.wantCalls {
				t.Errorf("callback calls = %d, want %d", calls, tt.wantCalls)
			}
			if tt.wantCalls > 0 {
				want := &model.ConfigGroupChangeEvent{Before: oldVal.ReleaseFiles, After: newVal.ReleaseFiles}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("event = %+v, want %+v", got, want)
				}
			}
			for i := 0; i < tt.cancelAfter; i++ {
				cancel()
			}
			group.repoChangeListener(newVal, &configconnector.ConfigGroupResponse{})
			if calls != tt.wantCalls {
				t.Errorf("callback called after cancel: calls = %d, want %d", calls, tt.wantCalls)
			}
			if count := len(group.cancelableListeners); count != 0 {
				t.Errorf("retained cancelable listeners = %d, want 0", count)
			}
			wantListenerLogs := 1
			if tt.nilCallback {
				wantListenerLogs = 0
			}
			for _, action := range []string{"已注册配置分组监听器.", "已取消配置分组监听器."} {
				if count := strings.Count(logger.dump(), action); count != wantListenerLogs {
					t.Errorf("%s log count = %d, want %d", action, count, wantListenerLogs)
				}
			}
		})
	}
}

// TestDefaultConfigGroup_IndependentRegistrations 验证相同回调的多次注册和旧取消函数互不影响。
func TestDefaultConfigGroup_IndependentRegistrations(t *testing.T) {
	group, _ := newTestConfigGroup(t)
	calls := 0
	cb := func(*model.ConfigGroupChangeEvent) { calls++ }
	cancelFirst := group.AddChangeListenerWithCancel(cb)
	cancelSecond := group.AddChangeListenerWithCancel(cb)
	cancelFirst()
	group.repoChangeListener(nil, &configconnector.ConfigGroupResponse{})
	if calls != 1 {
		t.Fatalf("canceling first registration affected second: calls = %d, want 1", calls)
	}
	cancelSecond()
	cancelThird := group.AddChangeListenerWithCancel(cb)
	cancelFirst()
	cancelSecond()
	group.repoChangeListener(nil, &configconnector.ConfigGroupResponse{})
	if calls != 2 {
		t.Fatalf("old cancel functions affected new registration: calls = %d, want 2", calls)
	}
	cancelThird()
	group.repoChangeListener(nil, &configconnector.ConfigGroupResponse{})
	if calls != 2 {
		t.Errorf("canceled registrations still receive events: calls = %d, want 2", calls)
	}
}

// configGroupListenerOwner 模拟以方法值注册回调的缓存对象。
// 使用非微小对象，避免 runtime 对微小分配的合并影响回收断言。
type configGroupListenerOwner struct {
	calls   *int64
	payload [4096]byte
}

func (o *configGroupListenerOwner) onChange(*model.ConfigGroupChangeEvent) {
	atomic.AddInt64(o.calls, 1)
}

// registerCollectibleGroupListener 将 owner 的强引用限制在此函数和注册后的方法值内。
func registerCollectibleGroupListener(group *defaultConfigGroup, calls *int64, collected chan struct{}) func() {
	owner := &configGroupListenerOwner{calls: calls}
	runtime.SetFinalizer(owner, func(*configGroupListenerOwner) { close(collected) })
	return group.AddChangeListenerWithCancel(owner.onChange)
}

// TestDefaultConfigGroup_CancelReleasesCallback 验证存活的分组和取消函数不会保留已取消的回调对象。
func TestDefaultConfigGroup_CancelReleasesCallback(t *testing.T) {
	group, _ := newTestConfigGroup(t)
	var calls int64
	collected := make(chan struct{})
	cancel := registerCollectibleGroupListener(group, &calls, collected)
	defer func() {
		runtime.KeepAlive(group)
		runtime.KeepAlive(cancel)
	}()

	runtime.GC()
	group.repoChangeListener(nil, &configconnector.ConfigGroupResponse{})
	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Fatalf("active callback calls = %d, want 1", got)
	}
	select {
	case <-collected:
		t.Fatal("active listener owner was collected before cancellation")
	default:
	}

	cancel()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		runtime.GC()
		select {
		case <-collected:
			cancel()
			group.repoChangeListener(nil, &configconnector.ConfigGroupResponse{})
			if got := atomic.LoadInt64(&calls); got != 1 {
				t.Errorf("canceled callback calls = %d, want 1", got)
			}
			return
		case <-deadline.C:
			t.Fatal("canceled callback owner retained by the live group or cancel function")
		case <-ticker.C:
		}
	}
}

// TestDefaultConfigGroup_AddChangeListener 验证旧接口保留注册顺序和重复追加行为，日志包含两类监听器。
func TestDefaultConfigGroup_AddChangeListener(t *testing.T) {
	group, logger := newTestConfigGroup(t)
	var calls []string
	first := func(*model.ConfigGroupChangeEvent) { calls = append(calls, "first") }
	group.AddChangeListener(first)
	group.AddChangeListener(func(*model.ConfigGroupChangeEvent) { calls = append(calls, "second") })
	group.AddChangeListener(first)
	cancel := group.AddChangeListenerWithCancel(func(*model.ConfigGroupChangeEvent) {
		calls = append(calls, "cancelable")
	})
	if !strings.Contains(logger.dump(), "已注册配置分组监听器. namespace=default, group=test-group, "+
		"listenerID=1, cancelableListenerCount=1, listenerCount=4") {
		t.Errorf("registration log missing identity or total including legacy listeners: %s", logger.dump())
	}
	group.repoChangeListener(nil, &configconnector.ConfigGroupResponse{})
	want := []string{"first", "second", "first", "cancelable"}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("listener calls = %v, want %v", calls, want)
	}
	if !strings.Contains(logger.dump(), "listenerCount=4,") {
		t.Errorf("listener count omitted cancelable listeners: %s", logger.dump())
	}
	cancel()
	if !strings.Contains(logger.dump(), "已取消配置分组监听器. namespace=default, group=test-group, "+
		"listenerID=1, remainingCancelableListeners=0, remainingListeners=3") {
		t.Errorf("cancellation log missing registration identity or remaining legacy listeners: %s", logger.dump())
	}
	calls = nil
	group.repoChangeListener(nil, &configconnector.ConfigGroupResponse{})
	want = []string{"first", "second", "first"}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("legacy listener calls after cancel = %v, want %v", calls, want)
	}
	if !strings.Contains(logger.dump(), "listenerCount=3,") {
		t.Errorf("listener count did not reflect cancellation: %s", logger.dump())
	}
}

// TestDefaultConfigGroup_EventLogLevels 验证 INFO 保留分发摘要，DEBUG 只输出一次完整事件。
func TestDefaultConfigGroup_EventLogLevels(t *testing.T) {
	for _, debug := range []bool{false, true} {
		name := "INFO"
		if debug {
			name = "DEBUG"
		}
		t.Run(name, func(t *testing.T) {
			group, logger := newTestConfigGroup(t)
			logger.debug = debug
			calls := 0
			for i := 0; i < 3; i++ {
				group.AddChangeListener(func(*model.ConfigGroupChangeEvent) { calls++ })
			}
			group.repoChangeListener(nil, &configconnector.ConfigGroupResponse{
				Revision: "r1", ReleaseFiles: []*model.SimpleConfigFile{{FileName: "config.yaml", Version: 1}},
			})
			logs := logger.dump()
			if !strings.Contains(logs, "配置分组变更监听触发. listenerCount=3,") ||
				!strings.Contains(logs, "beforeFileCount=0, afterFileCount=1") {
				t.Errorf("missing event summary: %s", logs)
			}
			wantDetails := 0
			if debug {
				wantDetails = 1
			}
			if got := strings.Count(logs, "event="); got != wantDetails {
				t.Errorf("full event log count = %d, want %d: %s", got, wantDetails, logs)
			}
			if calls != 3 {
				t.Errorf("callback calls = %d, want 3", calls)
			}
		})
	}
}

// TestDefaultConfigGroup_SelfCancellation 验证回调内取消自身可完成且后续事件不再触发该回调。
func TestDefaultConfigGroup_SelfCancellation(t *testing.T) {
	group, _ := newTestConfigGroup(t)
	calls := 0
	var cancel func()
	cancel = group.AddChangeListenerWithCancel(func(*model.ConfigGroupChangeEvent) {
		calls++
		cancel()
	})
	done := make(chan struct{})
	go func() {
		group.repoChangeListener(nil, &configconnector.ConfigGroupResponse{})
		close(done)
	}()
	waitForConfigGroupListener(t, done, "self cancellation")
	group.repoChangeListener(nil, &configconnector.ConfigGroupResponse{})
	if calls != 1 {
		t.Errorf("self-canceling callback calls = %d, want 1", calls)
	}
}

// TestDefaultConfigGroup_ListenerSnapshot 验证慢回调不阻塞注册/取消，且快照后的变更从下一轮生效。
func TestDefaultConfigGroup_ListenerSnapshot(t *testing.T) {
	tests := []struct {
		name       string
		cancelable bool
	}{
		{name: "legacy slow callback"},
		{name: "cancelable slow callback", cancelable: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			group, _ := newTestConfigGroup(t)
			started := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			var blockOnce sync.Once
			blocker := func(*model.ConfigGroupChangeEvent) {
				blockOnce.Do(func() {
					close(started)
					<-release
				})
			}
			stopBlocker := func() {}
			if tt.cancelable {
				stopBlocker = group.AddChangeListenerWithCancel(blocker)
			} else {
				group.AddChangeListener(blocker)
			}
			targetCalls := 0
			cancelTarget := group.AddChangeListenerWithCancel(func(*model.ConfigGroupChangeEvent) {
				targetCalls++
			})
			dispatched := make(chan struct{})
			go func() {
				group.repoChangeListener(nil, &configconnector.ConfigGroupResponse{})
				close(dispatched)
			}()
			waitForConfigGroupListener(t, started, "slow callback to start")

			lateLegacyCalls, lateCancelableCalls := 0, 0
			var cancelLate func()
			updated := make(chan struct{})
			go func() {
				cancelTarget()
				cancelTarget()
				stopBlocker()
				group.AddChangeListener(func(*model.ConfigGroupChangeEvent) { lateLegacyCalls++ })
				cancelLate = group.AddChangeListenerWithCancel(func(*model.ConfigGroupChangeEvent) {
					lateCancelableCalls++
				})
				close(updated)
			}()
			waitForConfigGroupListener(t, updated, "registration and cancellation during slow callback")
			unblock()
			waitForConfigGroupListener(t, dispatched, "snapshot dispatch")
			if targetCalls != 1 || lateLegacyCalls != 0 || lateCancelableCalls != 0 {
				t.Fatalf("snapshot calls = (%d, %d, %d), want (1, 0, 0)",
					targetCalls, lateLegacyCalls, lateCancelableCalls)
			}
			group.repoChangeListener(nil, &configconnector.ConfigGroupResponse{})
			if targetCalls != 1 || lateLegacyCalls != 1 || lateCancelableCalls != 1 {
				t.Errorf("next dispatch calls = (%d, %d, %d), want (1, 1, 1)",
					targetCalls, lateLegacyCalls, lateCancelableCalls)
			}
			cancelLate()
		})
	}
}

// TestDefaultConfigGroup_ConcurrentListeners 验证并发注册、重复取消和分发后无残留回调，供 race 检查。
func TestDefaultConfigGroup_ConcurrentListeners(t *testing.T) {
	group, logger := newTestConfigGroup(t)
	const (
		registrars  = 8
		dispatchers = 4
		iterations  = 100
	)
	var cancelableCalls, legacyCalls int64
	start := make(chan struct{})
	cancels := make(chan func(), registrars)
	var registerWG, dispatchWG, cancelWG sync.WaitGroup
	cancelWG.Add(1)
	go func() {
		defer cancelWG.Done()
		for cancel := range cancels {
			cancel()
		}
	}()
	registerWG.Add(registrars)
	for i := 0; i < registrars; i++ {
		go func() {
			defer registerWG.Done()
			<-start
			group.AddChangeListener(func(*model.ConfigGroupChangeEvent) {
				atomic.AddInt64(&legacyCalls, 1)
			})
			for j := 0; j < iterations; j++ {
				cancel := group.AddChangeListenerWithCancel(func(*model.ConfigGroupChangeEvent) {
					atomic.AddInt64(&cancelableCalls, 1)
				})
				cancels <- cancel
				cancel()
			}
		}()
	}
	dispatchWG.Add(dispatchers)
	for i := 0; i < dispatchers; i++ {
		go func() {
			defer dispatchWG.Done()
			<-start
			for j := 0; j < iterations; j++ {
				group.repoChangeListener(nil, &configconnector.ConfigGroupResponse{})
			}
		}()
	}
	close(start)
	done := make(chan struct{})
	go func() {
		registerWG.Wait()
		close(cancels)
		cancelWG.Wait()
		dispatchWG.Wait()
		close(done)
	}()
	waitForConfigGroupListener(t, done, "concurrent listener operations")
	beforeCancelable := atomic.LoadInt64(&cancelableCalls)
	beforeLegacy := atomic.LoadInt64(&legacyCalls)
	group.repoChangeListener(nil, &configconnector.ConfigGroupResponse{})
	if got := atomic.LoadInt64(&cancelableCalls); got != beforeCancelable {
		t.Errorf("canceled callbacks received another event: calls = %d, previously %d", got, beforeCancelable)
	}
	if got := atomic.LoadInt64(&legacyCalls) - beforeLegacy; got != registrars {
		t.Errorf("legacy callback calls = %d, want %d", got, registrars)
	}
	if count := len(group.cancelableListeners); count != 0 {
		t.Errorf("retained cancelable listeners after churn = %d, want 0", count)
	}
	for _, action := range []string{"已注册配置分组监听器.", "已取消配置分组监听器."} {
		if count := strings.Count(logger.dump(), action); count != registrars*iterations {
			t.Errorf("concurrent %s log count = %d, want %d", action, count, registrars*iterations)
		}
	}
}
