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
)

type ConfigGroupFlow struct {
	cancel context.CancelFunc

	fclock     sync.RWMutex
	groupCache map[string]model.ConfigFileGroup
	repos      map[string]*ConfigGroupRepo

	connector     configconnector.ConfigConnector
	configuration config.Configuration
}

func newConfigGroupFlow(connector configconnector.ConfigConnector, configuration config.Configuration) (*ConfigGroupFlow, error) {
	ctx, cancel := context.WithCancel(context.Background())

	groupFlow := &ConfigGroupFlow{
		cancel:        cancel,
		connector:     connector,
		configuration: configuration,
		repos:         map[string]*ConfigGroupRepo{},
		groupCache:    map[string]model.ConfigFileGroup{},
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
		return configGroup, nil
	}

	flow.fclock.Lock()
	defer flow.fclock.Unlock()

	// double check
	configGroup, ok = flow.groupCache[cacheKey]
	if ok {
		return configGroup, nil
	}

	groupRepo, err := newConfigGroupRepo(namespace, fileGroup, model.SDKMode, flow.connector, flow.configuration)
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
		return configGroup, nil
	}

	flow.fclock.Lock()
	defer flow.fclock.Unlock()

	// double check
	configGroup, ok = flow.groupCache[cacheKey]
	if ok {
		return configGroup, nil
	}

	groupRepo, err := newConfigGroupRepo(req.Namespace, req.FileGroup, req.Mode, flow.connector, flow.configuration)
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

		for i := range flow.repos {
			repo := flow.repos[i]
			if err := repo.pull(); err != nil {
				log.GetBaseLogger().Errorf("[Config] pull config grup error.", err)
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
