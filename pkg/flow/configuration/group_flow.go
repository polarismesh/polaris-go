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
	"github.com/polarismesh/polaris-go/pkg/global"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/configconnector"
)

type ConfigGroupFlow struct {
	cancel context.CancelFunc

	fclock     sync.RWMutex
	groupCache map[string]model.ConfigFileGroup
	repos      map[string]*ConfigGroupRepo

	connector     configconnector.ConfigConnector
	configuration config.Configuration
	logCtx        *log.ContextLogger
	globalCtx     global.ValueContext
}

func newConfigGroupFlow(globalCtx global.ValueContext, connector configconnector.ConfigConnector,
	configuration config.Configuration) (*ConfigGroupFlow, error) {
	ctx, cancel := context.WithCancel(context.Background())

	groupFlow := &ConfigGroupFlow{
		cancel:        cancel,
		connector:     connector,
		configuration: configuration,
		repos:         map[string]*ConfigGroupRepo{},
		groupCache:    map[string]model.ConfigFileGroup{},
		globalCtx:     globalCtx,
		logCtx:        globalCtx.GetContextLogger(),
	}

	go groupFlow.doSync(ctx)
	return groupFlow, nil
}

func (flow *ConfigGroupFlow) GetConfigGroup(namespace, fileGroup string) (model.ConfigFileGroup, error) {
	cacheKey := namespace + "@" + fileGroup

	flow.fclock.RLock()
	configGroup, ok := flow.groupCache[cacheKey]
	flow.fclock.RUnlock()
	if ok {
		if flow.logCtx.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			flow.logCtx.GetBaseLogger().Debugf("[Config][GroupFlow] 命中配置分组缓存. namespace=%s, group=%s",
				namespace, fileGroup)
		}
		return configGroup, nil
	}

	flow.logCtx.GetBaseLogger().Infof("[Config][GroupFlow] 配置分组缓存未命中，开始创建. namespace=%s, group=%s",
		namespace, fileGroup)

	flow.fclock.Lock()
	defer flow.fclock.Unlock()

	// double check
	configGroup, ok = flow.groupCache[cacheKey]
	if ok {
		return configGroup, nil
	}

	groupRepo, err := newConfigGroupRepo(flow.globalCtx, namespace, fileGroup, model.SDKMode, flow.connector,
		flow.configuration)
	if err != nil {
		return nil, err
	}
	flow.repos[cacheKey] = groupRepo

	configGroup = newDefaultConfigGroup(namespace, fileGroup, groupRepo)
	flow.groupCache[cacheKey] = configGroup
	return configGroup, nil
}

func (flow *ConfigGroupFlow) GetConfigGroupWithReq(req *model.GetConfigGroupRequest) (model.ConfigFileGroup, error) {
	cacheKey := req.Namespace + "@" + req.FileGroup

	flow.fclock.RLock()
	configGroup, ok := flow.groupCache[cacheKey]
	flow.fclock.RUnlock()
	if ok {
		if flow.logCtx.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			flow.logCtx.GetBaseLogger().Debugf("[Config][GroupFlow] 命中配置分组缓存(WithReq). namespace=%s, group=%s",
				req.Namespace, req.FileGroup)
		}
		return configGroup, nil
	}

	flow.logCtx.GetBaseLogger().Infof("[Config][GroupFlow] 配置分组缓存未命中，开始创建(WithReq). namespace=%s, group=%s, mode=%v",
		req.Namespace, req.FileGroup, req.Mode)

	flow.fclock.Lock()
	defer flow.fclock.Unlock()

	// double check
	configGroup, ok = flow.groupCache[cacheKey]
	if ok {
		return configGroup, nil
	}

	groupRepo, err := newConfigGroupRepo(flow.globalCtx, req.Namespace, req.FileGroup, req.Mode, flow.connector,
		flow.configuration)
	if err != nil {
		return nil, err
	}
	flow.repos[cacheKey] = groupRepo

	configGroup = newDefaultConfigGroup(req.Namespace, req.FileGroup, groupRepo)
	flow.groupCache[cacheKey] = configGroup
	return configGroup, nil
}

// doSync
func (flow *ConfigGroupFlow) doSync(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	realSync := func() {
		flow.fclock.RLock()
		defer flow.fclock.RUnlock()

		if len(flow.repos) == 0 {
			return
		}
		if flow.logCtx.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			flow.logCtx.GetBaseLogger().Debugf("[Config][GroupFlow] 开始定时同步配置分组. groupCount=%d", len(flow.repos))
		}

		for i := range flow.repos {
			repo := flow.repos[i]
			if flow.logCtx.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
				flow.logCtx.GetBaseLogger().Debugf("[Config][GroupFlow] 同步配置分组. namespace=%s, group=%s, currentRevision=%s",
					repo.namespace, repo.groupName, repo.notifiedVersion)
			}
			if err := repo.pull(); err != nil {
				flow.logCtx.GetBaseLogger().Errorf("[Config] pull config group error. namespace=%s, group=%s, err=%v",
					repo.namespace, repo.groupName, err)
			}
		}
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
