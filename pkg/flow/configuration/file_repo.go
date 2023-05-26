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
	"time"

	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	"go.uber.org/zap"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/configconnector"
)

const (
	delayMinTime = 1   // 1s
	delayMaxTime = 120 // 120s
)

// ConfigFileRepo 服务端配置文件代理类，从服务端拉取配置并同步数据
type ConfigFileRepo struct {
	connector     configconnector.ConfigConnector
	configuration config.Configuration

	configFileMetadata model.ConfigFileMetadata
	notifiedVersion    uint64                      // 长轮询通知的版本号
	remoteConfigFile   *configconnector.ConfigFile // 从服务端获取的原始配置对象
	retryPolicy        retryPolicy
	listeners          []ConfigFileRepoChangeListener
}

// ConfigFileRepoChangeListener 远程配置文件发布监听器
type ConfigFileRepoChangeListener func(configFileMetadata model.ConfigFileMetadata, newContent string) error

// newConfigFileRepo 创建远程配置文件
func newConfigFileRepo(metadata model.ConfigFileMetadata, connector configconnector.ConfigConnector,
	configuration config.Configuration) (*ConfigFileRepo, error) {
	repo := &ConfigFileRepo{
		connector:          connector,
		configuration:      configuration,
		configFileMetadata: metadata,
		notifiedVersion:    initVersion,
		retryPolicy: retryPolicy{
			delayMinTime: delayMinTime,
			delayMaxTime: delayMaxTime,
		},
		remoteConfigFile: &configconnector.ConfigFile{
			Namespace: metadata.GetNamespace(),
			FileGroup: metadata.GetFileGroup(),
			FileName:  metadata.GetFileName(),
			Version:   initVersion,
		},
	}
	// 1. 同步从服务端拉取配置
	if err := repo.pull(); err != nil {
		return nil, err
	}
	return repo, nil
}

func (r *ConfigFileRepo) GetNotifiedVersion() uint64 {
	return r.notifiedVersion
}

// GetContent 获取配置文件内容
func (r *ConfigFileRepo) GetContent() string {
	if r.remoteConfigFile == nil {
		return NotExistedFileContent
	}
	return r.remoteConfigFile.GetContent()
}

func (r *ConfigFileRepo) getVersion() uint64 {
	if r.remoteConfigFile == nil {
		return initVersion
	}
	return r.remoteConfigFile.GetVersion()
}

func (r *ConfigFileRepo) pull() error {
	pullConfigFileReq := &configconnector.ConfigFile{
		Namespace: r.configFileMetadata.GetNamespace(),
		FileGroup: r.configFileMetadata.GetFileGroup(),
		FileName:  r.configFileMetadata.GetFileName(),
		Version:   r.notifiedVersion,
	}

	log.GetBaseLogger().Infof("[Config] start pull config file. config file = %+v, version = %d",
		r.configFileMetadata, r.notifiedVersion)

	var (
		retryTimes = 0
		err        error
	)
	for retryTimes < 3 {
		startTime := time.Now()

		response, err := r.connector.GetConfigFile(pullConfigFileReq)
		if err != nil {
			log.GetBaseLogger().Errorf("[Config] failed to pull config file. retry times = %d", retryTimes, err)
			r.retryPolicy.fail()
			retryTimes++
			r.retryPolicy.delay()
			continue
		}

		// 拉取配置成功
		pulledConfigFile := response.GetConfigFile()
		responseCode := response.GetCode()

		// 打印请求信息
		pulledConfigFileVersion := int64(-1)
		if pulledConfigFile != nil {
			pulledConfigFileVersion = int64(pulledConfigFile.GetVersion())
		}
		log.GetBaseLogger().Infof("[Config] pull config file finished. config file = %+v, code = %d, version = %d, duration = %d ms",
			pulledConfigFile, responseCode, pulledConfigFileVersion, time.Since(startTime).Milliseconds())

		// 拉取成功
		if responseCode == uint32(apimodel.Code_ExecuteSuccess) {
			// 本地配置文件落后，更新内存缓存
			if r.remoteConfigFile == nil || pulledConfigFile.Version >= r.remoteConfigFile.Version {
				r.remoteConfigFile = deepCloneConfigFile(pulledConfigFile)
				r.fireChangeEvent(pulledConfigFile.GetContent())
			}
			return nil
		}

		// 远端没有此配置文件
		if responseCode == uint32(apimodel.Code_NotFoundResource) {
			log.GetBaseLogger().Warnf("[Config] config file not found, please check whether config file released. %+v", r.configFileMetadata)
			// 删除配置文件
			if r.remoteConfigFile != nil {
				r.remoteConfigFile = nil
				r.fireChangeEvent(NotExistedFileContent)
			}
			return nil
		}

		// 预期之外的状态码，重试
		log.GetBaseLogger().Errorf("[Config] pull response with unexpected code.",
			zap.Int("retry-times", retryTimes), zap.Uint32("code", responseCode))
		err = fmt.Errorf("pull config file with unexpect code. %d", responseCode)
		r.retryPolicy.fail()
		retryTimes++
		r.retryPolicy.delay()
	}
	return err
}

func deepCloneConfigFile(sourceConfigFile *configconnector.ConfigFile) *configconnector.ConfigFile {
	return &configconnector.ConfigFile{
		Namespace: sourceConfigFile.GetNamespace(),
		FileGroup: sourceConfigFile.GetFileGroup(),
		FileName:  sourceConfigFile.GetFileName(),
		Content:   sourceConfigFile.GetContent(),
		Version:   sourceConfigFile.GetVersion(),
		Md5:       sourceConfigFile.GetMd5(),
	}
}

func (r *ConfigFileRepo) onLongPollingNotified(newVersion uint64) {
	if r.remoteConfigFile != nil && r.remoteConfigFile.GetVersion() >= newVersion {
		return
	}
	r.notifiedVersion = newVersion
	if err := r.pull(); err != nil {
		log.GetBaseLogger().Errorf("[Config] pull config file error by check version task.", zap.Error(err))
	}
}

// AddChangeListener 添加配置文件变更监听器
func (r *ConfigFileRepo) AddChangeListener(listener ConfigFileRepoChangeListener) {
	r.listeners = append(r.listeners, listener)
}

func (r *ConfigFileRepo) fireChangeEvent(newContent string) {
	for _, listener := range r.listeners {
		if err := listener(r.configFileMetadata, newContent); err != nil {
			log.GetBaseLogger().Errorf("[Config] invoke config file repo change listener failed.",
				zap.Any("file", r.configFileMetadata), zap.Error(err))
		}
	}
}
