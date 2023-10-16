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
	"net/url"
	"sync/atomic"
	"time"

	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	"go.uber.org/zap"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/configconnector"
	"github.com/polarismesh/polaris-go/pkg/plugin/configfilter"
)

const (
	delayMinTime = 1   // 1s
	delayMaxTime = 120 // 120s
)

var (
	_notExistFile = &configconnector.ConfigFile{
		SourceContent: NotExistedFileContent,
		NotExist:      true,
	}
)

// ConfigFileRepo 服务端配置文件代理类，从服务端拉取配置并同步数据
type ConfigFileRepo struct {
	connector     configconnector.ConfigConnector
	chain         configfilter.Chain
	configuration config.Configuration

	configFileMetadata model.ConfigFileMetadata
	// 长轮询通知的版本号
	notifiedVersion uint64
	// 从服务端获取的原始配置对象 *configconnector.ConfigFile
	remoteConfigFileRef *atomic.Value
	retryPolicy         retryPolicy
	listeners           []ConfigFileRepoChangeListener

	persistHandler *CachePersistHandler

	fallbackToLocalCache bool
}

// ConfigFileRepoChangeListener 远程配置文件发布监听器
type ConfigFileRepoChangeListener func(configFileMetadata model.ConfigFileMetadata, newContent string) error

// newConfigFileRepo 创建远程配置文件
func newConfigFileRepo(metadata model.ConfigFileMetadata,
	connector configconnector.ConfigConnector,
	chain configfilter.Chain,
	configuration config.Configuration,
	persistHandler *CachePersistHandler) (*ConfigFileRepo, error) {
	repo := &ConfigFileRepo{
		connector:          connector,
		chain:              chain,
		configuration:      configuration,
		configFileMetadata: metadata,
		notifiedVersion:    initVersion,
		retryPolicy: retryPolicy{
			delayMinTime: delayMinTime,
			delayMaxTime: delayMaxTime,
		},
		remoteConfigFileRef:  &atomic.Value{},
		persistHandler:       persistHandler,
		fallbackToLocalCache: configuration.GetConfigFile().GetLocalCache().IsFallbackToLocalCache(),
	}
	repo.remoteConfigFileRef.Store(&configconnector.ConfigFile{
		Namespace: metadata.GetNamespace(),
		FileGroup: metadata.GetFileGroup(),
		FileName:  metadata.GetFileName(),
		Version:   initVersion,
	})
	// 1. 同步从服务端拉取配置
	if err := repo.pull(); err != nil {
		return nil, err
	}
	return repo, nil
}

func (r *ConfigFileRepo) GetNotifiedVersion() uint64 {
	return r.notifiedVersion
}

func (r *ConfigFileRepo) loadRemoteFile() *configconnector.ConfigFile {
	val := r.remoteConfigFileRef.Load()
	if val == nil {
		return nil
	}
	return val.(*configconnector.ConfigFile)
}

// GetContent 获取配置文件内容
func (r *ConfigFileRepo) GetContent() string {
	remoteFile := r.loadRemoteFile()
	if remoteFile == nil {
		return NotExistedFileContent
	}
	return remoteFile.GetContent()
}

func (r *ConfigFileRepo) getVersion() uint64 {
	remoteConfigFile := r.loadRemoteFile()
	if remoteConfigFile == nil {
		return initVersion
	}
	return remoteConfigFile.GetVersion()
}

func (r *ConfigFileRepo) getDataKey() string {
	remoteConfigFile := r.loadRemoteFile()
	if remoteConfigFile == nil {
		return ""
	}
	return remoteConfigFile.GetDataKey()
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

		response, err := r.chain.Execute(pullConfigFileReq, r.connector.GetConfigFile)

		if err != nil {
			log.GetBaseLogger().Errorf("[Config] failed to pull config file. retry times = %d, err = %v", retryTimes, err)
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
			pulledConfigFile.String(), responseCode, pulledConfigFileVersion, time.Since(startTime).Milliseconds())

		// 拉取成功
		if responseCode == uint32(apimodel.Code_ExecuteSuccess) {
			remoteConfigFile := r.loadRemoteFile()
			// 本地配置文件落后，更新内存缓存
			if remoteConfigFile == nil || pulledConfigFile.Version >= remoteConfigFile.Version {
				// save into local_cache
				r.saveCacheConfigFile(pulledConfigFile)
				r.fireChangeEvent(pulledConfigFile)
			}
			return nil
		}

		// 远端没有此配置文件
		if responseCode == uint32(apimodel.Code_NotFoundResource) {
			log.GetBaseLogger().Warnf("[Config] config file not found, please check whether config file released. %+v", r.configFileMetadata)
			// 删除配置文件
			r.removeCacheConfigFile(&configconnector.ConfigFile{
				Namespace: pullConfigFileReq.Namespace,
				FileGroup: pullConfigFileReq.FileGroup,
				FileName:  pullConfigFileReq.FileName,
			})
			if remoteConfigFile := r.loadRemoteFile(); remoteConfigFile != nil {
				r.fireChangeEvent(_notExistFile)
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
	r.fallbackIfNecessary(retryTimes, pullConfigFileReq)
	return err
}

const (
	PullConfigMaxRetryTimes = 3
)

func (r *ConfigFileRepo) fallbackIfNecessary(retryTimes int, req *configconnector.ConfigFile) {
	if !(retryTimes >= PullConfigMaxRetryTimes && r.fallbackToLocalCache) {
		return
	}
	cacheVal := &configconnector.ConfigFile{}
	fileName := fmt.Sprintf(PatternService, url.QueryEscape(req.Namespace), url.QueryEscape(req.FileGroup),
		url.QueryEscape(req.FileName)) + CacheSuffix
	if err := r.persistHandler.LoadMessageFromFile(fileName, cacheVal); err != nil {
		return
	}

	response, err := r.chain.Execute(req, func(configFile *configconnector.ConfigFile) (*configconnector.ConfigFileResponse, error) {
		return &configconnector.ConfigFileResponse{
			Code: uint32(apimodel.Code_ExecuteSuccess),
			ConfigFile: &configconnector.ConfigFile{
				Namespace:     cacheVal.Namespace,
				FileGroup:     cacheVal.FileGroup,
				FileName:      cacheVal.FileName,
				SourceContent: cacheVal.SourceContent,
				Version:       cacheVal.Version,
				Md5:           cacheVal.Md5,
				Encrypted:     cacheVal.Encrypted,
				Tags:          cacheVal.Tags,
			},
		}, nil
	})
	if err != nil {
		log.GetBaseLogger().Errorf("[Config] fallback to local cache fail. %+v", err)
		return
	}
	log.GetBaseLogger().Errorf("[Config] fallback to local cache success.")
	localFile := response.ConfigFile
	r.fireChangeEvent(localFile)
}

func (r *ConfigFileRepo) saveCacheConfigFile(file *configconnector.ConfigFile) {
	fileName := fmt.Sprintf(PatternService, url.QueryEscape(file.Namespace), url.QueryEscape(file.FileGroup),
		url.QueryEscape(file.FileName)) + CacheSuffix
	r.persistHandler.SaveMessageToFile(fileName, file)
}

func (r *ConfigFileRepo) removeCacheConfigFile(file *configconnector.ConfigFile) {
	fileName := fmt.Sprintf(PatternService, url.QueryEscape(file.Namespace), url.QueryEscape(file.FileGroup),
		url.QueryEscape(file.FileName)) + CacheSuffix
	r.persistHandler.DeleteCacheFromFile(fileName)
}

func deepCloneConfigFile(sourceConfigFile *configconnector.ConfigFile) *configconnector.ConfigFile {
	tags := make([]*configconnector.ConfigFileTag, 0, len(sourceConfigFile.Tags))
	for _, tag := range sourceConfigFile.Tags {
		tags = append(tags, &configconnector.ConfigFileTag{
			Key:   tag.Key,
			Value: tag.Value,
		})
	}
	ret := &configconnector.ConfigFile{
		Namespace:     sourceConfigFile.GetNamespace(),
		FileGroup:     sourceConfigFile.GetFileGroup(),
		FileName:      sourceConfigFile.GetFileName(),
		SourceContent: sourceConfigFile.GetSourceContent(),
		Version:       sourceConfigFile.GetVersion(),
		Md5:           sourceConfigFile.GetMd5(),
		Encrypted:     sourceConfigFile.GetEncrypted(),
		Tags:          tags,
	}
	return ret
}

func (r *ConfigFileRepo) onLongPollingNotified(newVersion uint64) {
	remoteConfigFile := r.loadRemoteFile()
	if remoteConfigFile != nil && remoteConfigFile.GetVersion() >= newVersion {
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

func (r *ConfigFileRepo) fireChangeEvent(f *configconnector.ConfigFile) {
	if f.GetContent() == "" {
		f.SetContent(f.GetSourceContent())
	}
	if f.NotExist {
		r.remoteConfigFileRef = &atomic.Value{}
	} else {
		r.remoteConfigFileRef.Store(f)
	}

	for _, listener := range r.listeners {
		if err := listener(r.configFileMetadata, f.GetContent()); err != nil {
			log.GetBaseLogger().Errorf("[Config] invoke config file repo change listener failed.",
				zap.Any("file", r.configFileMetadata), zap.Error(err))
		}
	}
}
