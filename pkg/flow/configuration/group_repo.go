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

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/configconnector"
	"github.com/polarismesh/polaris-go/pkg/sdk"
)

// ConfigGroupRepo 服务端配置文件代理类，从服务端拉取配置并同步数据
type ConfigGroupRepo struct {
	namespace       string
	groupName       string
	mode            model.GetConfigFileRequestMode
	connector       configconnector.ConfigConnector
	configuration   config.Configuration
	notifiedVersion string
	retryPolicy     retryPolicy
	// 从服务端获取的原始配置对象 *configconnector.ConfigFile
	remoteRef *atomic.Value
	logCtx    *log.ContextLogger

	lock      sync.RWMutex
	listeners []func(oldVal *configconnector.ConfigGroupResponse, newVal *configconnector.ConfigGroupResponse)

	// 用于控制 pull 日志打印频率，每半小时打印一次 Info，避免日志过于频繁
	lastPullLogTime time.Time
	pullCount       uint64
}

func newConfigGroupRepo(globalCtx sdk.ValueContext, namespace, group string, mode model.GetConfigFileRequestMode,
	connector configconnector.ConfigConnector,
	configuration config.Configuration) (*ConfigGroupRepo, error) {
	repo := &ConfigGroupRepo{
		namespace:       namespace,
		groupName:       group,
		mode:            mode,
		connector:       connector,
		configuration:   configuration,
		notifiedVersion: "",
		retryPolicy: retryPolicy{
			delayMinTime: delayMinTime,
			delayMaxTime: delayMaxTime,
		},
		remoteRef:       &atomic.Value{},
		listeners:       make([]func(oldVal *configconnector.ConfigGroupResponse, newVal *configconnector.ConfigGroupResponse), 0, 4),
		logCtx:          globalCtx.GetContextLogger(),
		lastPullLogTime: time.Now(),
	}

	repo.logCtx.GetBaseLogger().Infof("[Config][Group] 创建配置分组仓库. namespace=%s, group=%s, mode=%v",
		namespace, group, mode)

	if err := repo.pull(); err != nil {
		repo.logCtx.GetBaseLogger().Errorf("[Config][Group] 初始拉取配置分组失败. namespace=%s, group=%s, err=%v",
			namespace, group, err)
		return nil, err
	}

	repo.logCtx.GetBaseLogger().Infof("[Config][Group] 初始拉取配置分组完成. namespace=%s, group=%s, revision=%s",
		namespace, group, repo.notifiedVersion)

	return repo, nil
}

func (repo *ConfigGroupRepo) pull() error {
	req := &configconnector.ConfigGroup{
		Namespace: repo.namespace,
		Group:     repo.groupName,
		Revision:  repo.notifiedVersion,
		Mode:      repo.mode,
	}
	groupInfo := fmt.Sprintf("{namespace=%s, group=%s, oldRevision=%s, mode=%v}", req.Namespace, req.Group,
		req.Revision, req.Mode)

	// 每半小时打印一次 Info 日志，避免日志过于频繁
	const pullLogInterval = 30 * time.Minute
	now := time.Now()
	needInfoLog := now.Sub(repo.lastPullLogTime) >= pullLogInterval
	if needInfoLog {
		repo.logCtx.GetBaseLogger().Infof("[Config][Group] start pull. groupInfo:%s (pullCount=%d in last %.0f min)",
			groupInfo, repo.pullCount, now.Sub(repo.lastPullLogTime).Minutes())
		repo.lastPullLogTime = now
		repo.pullCount = 0
	} else {
		repo.logCtx.GetBaseLogger().Debugf("[Config][Group] start pull. groupInfo:%s", groupInfo)
	}
	repo.pullCount++

	var (
		retryTimes = 0
		err        error
	)
	for retryTimes < 3 {
		startTime := time.Now()
		response, err := repo.connector.GetConfigGroup(req)

		if err != nil {
			repo.logCtx.GetBaseLogger().Errorf("[Config][Group] failed to pull. retry times = %d, err = %v, groupInfo:%s",
				retryTimes, err, groupInfo)
			repo.retryPolicy.fail()
			retryTimes++
			repo.retryPolicy.delay()
			continue
		}

		responseCode := response.Code
		if needInfoLog {
			repo.logCtx.GetBaseLogger().Infof("[Config][Group] pull finished. respCode=%d, respRevision=%+v, duration=%dms, "+
				"groupInfo:%s", responseCode, response.Revision, time.Since(startTime).Milliseconds(), groupInfo)
		} else {
			repo.logCtx.GetBaseLogger().Debugf("[Config][Group] pull finished. respCode=%d, respRevision=%+v, duration=%dms, "+
				"groupInfo:%s", responseCode, response.Revision, time.Since(startTime).Milliseconds(), groupInfo)
		}

		// 拉取成功
		if responseCode == uint32(apimodel.Code_ExecuteSuccess) {
			repo.retryPolicy.success()
			remoteConfigFile := repo.loadRemoteGroup()
			oldRevision := "unknown"
			oldFileCount := -1
			if remoteConfigFile != nil {
				oldRevision = remoteConfigFile.Revision
				oldFileCount = len(remoteConfigFile.ReleaseFiles)
			}

			repo.logCtx.GetBaseLogger().Infof("[Config][Group] 拉取成功，比较版本. newRevision=%s, oldRevision=%s, "+
				"newFileCount=%d, oldFileCount=%d, groupInfo:%s, hasCache=%v", response.Revision, oldRevision,
				len(response.ReleaseFiles), oldFileCount, groupInfo, remoteConfigFile != nil)

			// 本地配置文件落后，更新内存缓存
			if remoteConfigFile == nil || response.Revision != remoteConfigFile.Revision {
				if repo.logCtx.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
					for _, f := range response.ReleaseFiles {
						repo.logCtx.GetBaseLogger().Debugf("[Config][Group] 分组文件详情: fileName=%s, version=%d, md5=%s, "+
							"groupInfo:%s", f.FileName, f.Version, f.Md5, groupInfo)
					}
				}
				repo.fireChangeEvent(response, true)
			} else {
				if repo.logCtx.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
					repo.logCtx.GetBaseLogger().Debugf("[Config][Group] 配置分组未变更. namespace=%s, group=%s, revision=%s",
						repo.namespace, repo.groupName, response.Revision)
				}
			}
			return nil
		}

		// 远端没有此配置文件
		if responseCode == uint32(apimodel.Code_NotFoundResource) {
			repo.retryPolicy.success()
			repo.logCtx.GetBaseLogger().Warnf("[Config][Group] not found, check config group exist. groupInfo:%s", groupInfo)
			if remoteConfigFile := repo.loadRemoteGroup(); remoteConfigFile != nil {
				repo.fireChangeEvent(response, false)
			}
			return nil
		}

		// 数据没有变更，正常返回
		if responseCode == uint32(apimodel.Code_DataNoChange) {
			repo.retryPolicy.success()
			if repo.logCtx.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
				repo.logCtx.GetBaseLogger().Debugf("[Config][Group] data no change. groupInfo:%s", groupInfo)
			}
			return nil
		}

		err = fmt.Errorf("pull config group with unexpect code. %d", responseCode)
		// 预期之外的状态码，重试
		repo.logCtx.GetBaseLogger().Errorf("[Config][Group] %v, retry-times=%d, code=%d, groupInfo:%s", err, retryTimes,
			responseCode, groupInfo)

		repo.retryPolicy.fail()
		retryTimes++
		repo.retryPolicy.delay()
	}
	return err
}

func (repo *ConfigGroupRepo) AddChangeListener(listener func(oldVal *configconnector.ConfigGroupResponse,
	newVal *configconnector.ConfigGroupResponse)) {
	repo.lock.Lock()
	defer repo.lock.Unlock()
	repo.listeners = append(repo.listeners, listener)
}

func (repo *ConfigGroupRepo) fireChangeEvent(f *configconnector.ConfigGroupResponse, exist bool) {
	repo.lock.RLock()
	defer repo.lock.RUnlock()

	repo.logCtx.GetBaseLogger().Infof("[Config][Group] 触发分组变更事件. namespace=%s, group=%s, exist=%v, revision=%s, "+
		"fileCount=%d, listenerCount=%d", repo.namespace, repo.groupName, exist, f.Revision, len(f.ReleaseFiles),
		len(repo.listeners))

	// 在存储之前先取出变更前的旧值
	oldVal := repo.loadRemoteGroup()

	// 先存储
	if !exist {
		repo.remoteRef = &atomic.Value{}
		repo.notifiedVersion = ""
	} else {
		repo.remoteRef.Store(f)
		repo.notifiedVersion = f.Revision
	}
	// 后通知，将旧值和新值一起传递给监听器
	for i, listener := range repo.listeners {
		if repo.logCtx.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			repo.logCtx.GetBaseLogger().Debugf("[Config][Group] 通知分组变更监听器[%d]. namespace=%s, group=%s, revision=%s",
				i, repo.namespace, repo.groupName, repo.notifiedVersion)
		}
		listener(oldVal, f)
	}
}

func (r *ConfigGroupRepo) loadRemoteGroup() *configconnector.ConfigGroupResponse {
	val := r.remoteRef.Load()
	if val == nil {
		return nil
	}
	return val.(*configconnector.ConfigGroupResponse)
}
