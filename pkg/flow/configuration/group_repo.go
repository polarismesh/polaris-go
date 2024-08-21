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
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	"go.uber.org/zap"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/plugin/configconnector"
)

// ConfigGroupRepo 服务端配置文件代理类，从服务端拉取配置并同步数据
type ConfigGroupRepo struct {
	namespace       string
	groupName       string
	connector       configconnector.ConfigConnector
	configuration   config.Configuration
	notifiedVersion string
	retryPolicy     retryPolicy
	// 从服务端获取的原始配置对象 *configconnector.ConfigFile
	remoteRef *atomic.Value

	lock      sync.RWMutex
	listeners []func(*configconnector.ConfigGroupResponse)
}

func newConfigGroupRepo(namespace, group string, connector configconnector.ConfigConnector,
	configuration config.Configuration) (*ConfigGroupRepo, error) {
	repo := &ConfigGroupRepo{
		namespace:       namespace,
		groupName:       group,
		connector:       connector,
		configuration:   configuration,
		notifiedVersion: "",
		retryPolicy: retryPolicy{
			delayMinTime: delayMinTime,
			delayMaxTime: delayMaxTime,
		},
		remoteRef: &atomic.Value{},
		listeners: make([]func(*configconnector.ConfigGroupResponse), 0, 4),
	}
	if err := repo.pull(); err != nil {
		return nil, err
	}
	return repo, nil
}

func (repo *ConfigGroupRepo) pull() error {
	req := &configconnector.ConfigGroup{
		Namespace: repo.namespace,
		Group:     repo.groupName,
		Revision:  repo.notifiedVersion,
	}

	log.GetBaseLogger().Infof("[Config][Group] start pull. namespace=%+v, group=%s, version=%+v",
		repo.namespace, repo.groupName, repo.notifiedVersion)

	var (
		retryTimes = 0
		err        error
	)
	for retryTimes < 3 {
		startTime := time.Now()
		response, err := repo.connector.GetConfigGroup(req)

		if err != nil {
			log.GetBaseLogger().Errorf("[Config][Group] failed to pull. retry times = %d, err = %v", retryTimes, err)
			repo.retryPolicy.fail()
			retryTimes++
			repo.retryPolicy.delay()
			continue
		}

		responseCode := response.Code
		log.GetBaseLogger().Infof("[Config][Group] pull finished. code=%d, revision=%+v, duration=%dms",
			responseCode, responseCode, response.Revision, time.Since(startTime).Milliseconds())

		// 拉取成功
		if responseCode == uint32(apimodel.Code_ExecuteSuccess) {
			remoteConfigFile := repo.loadRemoteGroup()
			// 本地配置文件落后，更新内存缓存
			if remoteConfigFile == nil || response.Revision != remoteConfigFile.Revision {
				repo.fireChangeEvent(response, true)
			}
			return nil
		}

		// 远端没有此配置文件
		if responseCode == uint32(apimodel.Code_NotFoundResource) {
			log.GetBaseLogger().Warnf("[Config][Group] not found, check config group exist.")
			if remoteConfigFile := repo.loadRemoteGroup(); remoteConfigFile != nil {
				repo.fireChangeEvent(response, false)
			}
			return nil
		}

		// 预期之外的状态码，重试
		log.GetBaseLogger().Errorf("[Config][Group] pull response with unexpected code.",
			zap.Int("retry-times", retryTimes), zap.Uint32("code", responseCode))
		err = fmt.Errorf("pull config group with unexpect code. %d", responseCode)
		repo.retryPolicy.fail()
		retryTimes++
		repo.retryPolicy.delay()
	}
	return err
}

func (repo *ConfigGroupRepo) AddChangeListener(listener func(*configconnector.ConfigGroupResponse)) {
	repo.lock.Lock()
	defer repo.lock.Unlock()
	repo.listeners = append(repo.listeners, listener)
}

func (repo *ConfigGroupRepo) fireChangeEvent(f *configconnector.ConfigGroupResponse, exist bool) {
	repo.lock.RLock()
	defer repo.lock.RUnlock()
	for _, listener := range repo.listeners {
		listener(f)
	}

	if !exist {
		repo.remoteRef = &atomic.Value{}
	} else {
		repo.remoteRef.Store(f)
	}
}

func (r *ConfigGroupRepo) loadRemoteGroup() *configconnector.ConfigGroupResponse {
	val := r.remoteRef.Load()
	if val == nil {
		return nil
	}
	return val.(*configconnector.ConfigGroupResponse)
}
