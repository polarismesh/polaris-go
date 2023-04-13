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

package remote

import (
	"sync"
	"time"

	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"

	"github.com/polarismesh/polaris-go/pkg/flow/configuration/util"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/configconnector"
)

var (
	configConnector configconnector.ConfigConnector

	configFilePool           = new(sync.Map)
	notifiedVersion          = new(sync.Map)
	startLongPollingTaskOnce = new(sync.Once)

	pollingRetryPolicy = retryPolicy{
		delayMinTime: delayMinTime,
		delayMaxTime: delayMaxTime,
	}

	stopPolling bool
)

// InitLongPollingService 初始化长轮询服务
func InitLongPollingService(connector configconnector.ConfigConnector) {
	configConnector = connector
	stopPolling = false
}

func addConfigFileToLongPollingPool(remoteConfigFileRepo *ConfigFileRepo) {
	configFileMetadata := remoteConfigFileRepo.configFileMetadata
	version := remoteConfigFileRepo.getVersion()

	log.GetBaseLogger().Infof("[Config] add long polling config file. file = %+v, version = %d", configFileMetadata, version)

	cacheKey := util.GenConfigFileCacheKeyByMetadata(configFileMetadata)
	configFilePool.Store(cacheKey, remoteConfigFileRepo)
	notifiedVersion.Store(cacheKey, version)

	// 开启长轮询任务
	startLongPollingTaskOnce.Do(func() {
		go startLongPollingTask()
	})
}

// StopLongPollingTask 停止长轮询任务
func StopLongPollingTask() {
	stopPolling = true
}

func startLongPollingTask() {
	time.Sleep(5 * time.Second)

	doLongPolling()
}

func doLongPolling() {
	for {
		if stopPolling {
			return
		}

		// 1. 生成订阅配置列表
		watchConfigFiles := assembleWatchConfigFiles()

		log.GetBaseLogger().Infof("[Config] do long polling. config file size = %d, delay time = %d",
			len(watchConfigFiles), pollingRetryPolicy.currentDelayTime)

		// 2. 调用 connector watch接口
		response, err := configConnector.WatchConfigFiles(watchConfigFiles)
		if err != nil {
			log.GetBaseLogger().Errorf("[Config] long polling failed.", err)
			pollingRetryPolicy.fail()
			pollingRetryPolicy.delay()
			continue
		}

		responseCode := response.GetCode()

		// 3.1 接口调用成功，判断版本号是否有更新，如果有更新则通知 remoteRepo 拉取最新，并触发回调事件
		if responseCode == uint32(apimodel.Code_ExecuteSuccess) && response.GetConfigFile() != nil {
			pollingRetryPolicy.success()

			changedConfigFile := response.GetConfigFile()
			configFileMetadata := &model.DefaultConfigFileMetadata{
				Namespace: changedConfigFile.GetNamespace(),
				FileGroup: changedConfigFile.GetFileGroup(),
				FileName:  changedConfigFile.GetFileName(),
			}

			cacheKey := util.GenConfigFileCacheKeyByMetadata(configFileMetadata)

			newNotifiedVersion := changedConfigFile.GetVersion()
			oldNotifiedVersion := getConfigFileNotifiedVersion(cacheKey)

			maxVersion := oldNotifiedVersion
			if newNotifiedVersion > oldNotifiedVersion {
				maxVersion = newNotifiedVersion
			}

			// 更新版本号
			notifiedVersion.Store(cacheKey, maxVersion)

			log.GetBaseLogger().Infof("[Config] received change event by long polling. file = %+v, new version = %d, old version = %d",
				changedConfigFile, newNotifiedVersion, oldNotifiedVersion)

			// 通知 remoteConfigFileRepo 拉取最新配置
			remoteConfigFileRepo := getRemoteConfigFileRepo(cacheKey)
			remoteConfigFileRepo.onLongPollingNotified(maxVersion)

			continue
		}

		// 3.2 如果没有变更，打印日志
		if responseCode == uint32(apimodel.Code_DataNoChange) {
			pollingRetryPolicy.success()
			log.GetBaseLogger().Infof("[Config] long polling result: data no change")
			continue
		}

		// 3.3 预期之外的状态，退避重试
		log.GetBaseLogger().Errorf("[Config] long polling result with unexpect code. code = {}", responseCode)
		pollingRetryPolicy.fail()
		pollingRetryPolicy.delay()
	}
}

func assembleWatchConfigFiles() []*configconnector.ConfigFile {
	var watchConfigFiles []*configconnector.ConfigFile

	configFilePool.Range(func(key, value interface{}) bool {
		cacheKey := key.(string)
		configFileMetadata := util.ExtractConfigFileMetadataFromKey(cacheKey)

		watchConfigFiles = append(watchConfigFiles, &configconnector.ConfigFile{
			Namespace: configFileMetadata.GetNamespace(),
			FileGroup: configFileMetadata.GetFileGroup(),
			FileName:  configFileMetadata.GetFileName(),
			Version:   getConfigFileNotifiedVersion(cacheKey),
		})

		return true
	})

	return watchConfigFiles
}

func getConfigFileNotifiedVersion(cacheKey string) uint64 {
	var version uint64
	versionObj, ok := notifiedVersion.Load(cacheKey)
	if !ok {
		version = initVersion
	} else {
		version = versionObj.(uint64)
	}
	return version
}

func getRemoteConfigFileRepo(cacheKey string) *ConfigFileRepo {
	configFile, ok := configFilePool.Load(cacheKey)
	if !ok {
		return nil
	}
	return configFile.(*ConfigFileRepo)
}
