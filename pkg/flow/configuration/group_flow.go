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
	"context"
	"sync"
	"time"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/configconnector"
	"github.com/polarismesh/polaris-go/pkg/sdk"
)

// inFlightEntry 记录正在进行中的分组仓库创建，确保相同 key 的并发请求
// 不会重复发起 gRPC 调用。
type inFlightEntry struct {
	done chan struct{}
	cg   model.ConfigFileGroup
	err  error
}

type ConfigGroupFlow struct {
	cancel context.CancelFunc

	fclock     sync.RWMutex
	groupCache map[string]model.ConfigFileGroup
	repos      map[string]*ConfigGroupRepo

	// inFlight 记录当前正在创建中的 key（gRPC 进行中）。
	// 该 map 由 fclock 写锁保护，gRPC 调用本身在锁外执行，
	// 其他等待者通过 entry.done channel 等待完成。
	inFlight map[string]*inFlightEntry

	connector     configconnector.ConfigConnector
	configuration config.Configuration
	logCtx        *log.ContextLogger
	globalCtx     sdk.ValueContext
}

func newConfigGroupFlow(globalCtx sdk.ValueContext, connector configconnector.ConfigConnector,
	configuration config.Configuration) (*ConfigGroupFlow, error) {
	ctx, cancel := context.WithCancel(context.Background())

	groupFlow := &ConfigGroupFlow{
		cancel:        cancel,
		connector:     connector,
		configuration: configuration,
		repos:         map[string]*ConfigGroupRepo{},
		groupCache:    map[string]model.ConfigFileGroup{},
		inFlight:      map[string]*inFlightEntry{},
		globalCtx:     globalCtx,
		logCtx:        globalCtx.GetContextLogger(),
	}

	go groupFlow.doSync(ctx)
	return groupFlow, nil
}

func (flow *ConfigGroupFlow) GetConfigGroup(namespace, fileGroup string) (model.ConfigFileGroup, error) {
	return flow.getOrCreateGroup(namespace, fileGroup, model.SDKMode)
}

func (flow *ConfigGroupFlow) GetConfigGroupWithReq(req *model.GetConfigGroupRequest) (model.ConfigFileGroup, error) {
	return flow.getOrCreateGroup(req.Namespace, req.FileGroup, req.Mode)
}

// getOrCreateGroup 是 GetConfigGroup 和 GetConfigGroupWithReq 的统一实现。
// 设计保证：
//   - 不同 key 可完全并发创建，无全局串行化。
//   - 相同 key 只发起一次 gRPC 调用（通过 inFlight map 实现 singleflight 语义）。
//   - fclock 写锁仅用于 map 读写（纳秒级），gRPC 网络调用在锁外执行。
func (flow *ConfigGroupFlow) getOrCreateGroup(namespace, fileGroup string,
	mode model.GetConfigFileRequestMode) (model.ConfigFileGroup, error) {
	cacheKey := namespace + "@" + fileGroup

	// 快速路径：缓存命中（仅读锁）。
	flow.fclock.RLock()
	cg, ok := flow.groupCache[cacheKey]
	flow.fclock.RUnlock()
	if ok {
		if flow.logCtx.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			flow.logCtx.GetBaseLogger().Debugf("[Config][GroupFlow] 命中配置分组缓存. namespace=%s, group=%s",
				namespace, fileGroup)
		}
		return cg, nil
	}

	flow.logCtx.GetBaseLogger().Infof("[Config][GroupFlow] 配置分组缓存未命中，开始创建(WithReq). namespace=%s, group=%s, mode=%v",
		namespace, fileGroup, mode)

	// 慢路径：需要创建。使用写锁检查/注册 inFlight。
	flow.fclock.Lock()
	// 获取写锁后二次检查缓存。
	if cg, ok = flow.groupCache[cacheKey]; ok {
		flow.fclock.Unlock()
		return cg, nil
	}
	// 检查是否有其他 goroutine 正在创建同一个 key。
	if entry, exists := flow.inFlight[cacheKey]; exists {
		flow.fclock.Unlock()
		// 等待进行中的创建完成。
		<-entry.done
		return entry.cg, entry.err
	}
	// 将自己注册为该 key 的创建者。
	entry := &inFlightEntry{done: make(chan struct{})}
	flow.inFlight[cacheKey] = entry
	flow.fclock.Unlock()

	// 确保即使 newConfigGroupRepo 发生 panic，等待者也能被唤醒，
	// 且 inFlight 条目被清理。
	defer func() {
		close(entry.done)
		flow.fclock.Lock()
		delete(flow.inFlight, cacheKey)
		flow.fclock.Unlock()
	}()

	// === gRPC 调用在锁外执行 ===
	groupRepo, err := newConfigGroupRepo(flow.globalCtx, namespace, fileGroup, mode, flow.connector,
		flow.configuration)

	if err != nil {
		entry.err = err
	} else {
		cg = newDefaultConfigGroup(namespace, fileGroup, groupRepo)
		entry.cg = cg

		// 将结果写入缓存（短暂写锁，纳秒级）。
		flow.fclock.Lock()
		flow.repos[cacheKey] = groupRepo
		flow.groupCache[cacheKey] = cg
		flow.fclock.Unlock()
	}

	return entry.cg, entry.err
}

// doSync 定时同步所有已知的配置分组仓库。
// 修复：在短暂 RLock 下取 repos 快照，随后在锁外并发拉取每个 repo。
// 这避免了 doSync 在整个遍历期间阻塞新的 GetConfigGroup 调用。
func (flow *ConfigGroupFlow) doSync(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	realSync := func() {
		// 1. 在短暂读锁下快照 repos 切片。
		flow.fclock.RLock()
		if len(flow.repos) == 0 {
			flow.fclock.RUnlock()
			return
		}
		repos := make([]*ConfigGroupRepo, 0, len(flow.repos))
		for _, r := range flow.repos {
			repos = append(repos, r)
		}
		flow.fclock.RUnlock()

		if flow.logCtx.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			flow.logCtx.GetBaseLogger().Debugf("[Config][GroupFlow] 开始定时同步配置分组. groupCount=%d", len(repos))
		}

		// 2. 在锁外并发拉取每个 repo，使用有界并发池避免打爆服务端。
		const maxConcurrency = 16
		sem := make(chan struct{}, maxConcurrency)
		var wg sync.WaitGroup

		for _, repo := range repos {
			sem <- struct{}{}
			wg.Add(1)
			go func(r *ConfigGroupRepo) {
				defer wg.Done()
				defer func() { <-sem }()
				if flow.logCtx.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
					flow.logCtx.GetBaseLogger().Debugf("[Config][GroupFlow] 同步配置分组. namespace=%s, group=%s, currentRevision=%s",
						r.namespace, r.groupName, r.notifiedVersion)
				}
				if err := r.pull(); err != nil {
					flow.logCtx.GetBaseLogger().Errorf("[Config] pull config group error. namespace=%s, group=%s, err=%v",
						r.namespace, r.groupName, err)
				}
			}(repo)
		}
		wg.Wait()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			realSync()
		}
	}
}
