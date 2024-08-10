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
	"fmt"
	"strings"
	"sync"
	"time"

	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/configconnector"
	"github.com/polarismesh/polaris-go/pkg/plugin/configfilter"
)

// ConfigFileFlow 配置中心核心服务门面类
type ConfigFileFlow struct {
	cancel context.CancelFunc

	fclock          sync.RWMutex
	configFileCache map[string]model.ConfigFile
	repos           []*ConfigFileRepo
	configFilePool  map[string]*ConfigFileRepo
	notifiedVersion map[string]uint64

	connector configconnector.ConfigConnector
	chain     configfilter.Chain
	conf      config.Configuration

	persistHandler *CachePersistHandler

	startLongPollingTaskOnce sync.Once
}

// NewConfigFileFlow 创建配置中心服务
func NewConfigFileFlow(connector configconnector.ConfigConnector, chain configfilter.Chain,
	conf config.Configuration) (*ConfigFileFlow, error) {
	persistHandler, err := NewCachePersistHandler(
		conf.GetConfigFile().GetLocalCache().GetPersistDir(),
		conf.GetConfigFile().GetLocalCache().GetPersistMaxWriteRetry(),
		conf.GetConfigFile().GetLocalCache().GetPersistMaxReadRetry(),
		conf.GetConfigFile().GetLocalCache().GetPersistRetryInterval(),
	)
	if err != nil {
		return nil, err
	}

	configFileService := &ConfigFileFlow{
		connector:       connector,
		chain:           chain,
		conf:            conf,
		repos:           make([]*ConfigFileRepo, 0, 8),
		configFileCache: map[string]model.ConfigFile{},
		configFilePool:  map[string]*ConfigFileRepo{},
		notifiedVersion: map[string]uint64{},
		persistHandler:  persistHandler,
	}

	return configFileService, nil
}

// Destroy 销毁服务
func (c *ConfigFileFlow) Destroy() {
	if c.cancel != nil {
		c.cancel()
	}
}

// GetConfigFile 获取配置文件
func (c *ConfigFileFlow) GetConfigFile(req *model.GetConfigFileRequest) (model.ConfigFile, error) {
	configFileMetadata := &model.DefaultConfigFileMetadata{
		Namespace: req.Namespace,
		FileGroup: req.FileGroup,
		FileName:  req.FileName,
	}

	cacheKey := genCacheKeyByMetadata(configFileMetadata)

	c.fclock.RLock()
	configFile, ok := c.configFileCache[cacheKey]
	c.fclock.RUnlock()
	if ok {
		return configFile, nil
	}

	c.fclock.Lock()
	defer c.fclock.Unlock()

	// double check
	configFile, ok = c.configFileCache[cacheKey]
	if ok {
		return configFile, nil
	}

	fileRepo, err := newConfigFileRepo(configFileMetadata, c.connector, c.chain, c.conf, c.persistHandler)
	if err != nil {
		return nil, err
	}
	configFile = newDefaultConfigFile(configFileMetadata, fileRepo)

	if req.Subscribe {
		c.addConfigFileToLongPollingPool(fileRepo)
		c.repos = append(c.repos, fileRepo)
		c.configFileCache[cacheKey] = configFile
	}
	return configFile, nil
}

// CreateConfigFile 创建配置文件
func (c *ConfigFileFlow) CreateConfigFile(namespace, fileGroup, fileName, content string) error {
	// 校验参数
	configFile := &configconnector.ConfigFile{
		Namespace: namespace,
		FileGroup: fileGroup,
		FileName:  fileName,
	}
	configFile.SetContent(content)

	if err := model.CheckConfigFileMetadata(configFile); err != nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, err, "")
	}

	c.fclock.Lock()
	defer c.fclock.Unlock()

	resp, err := c.connector.CreateConfigFile(configFile)
	if err != nil {
		return err
	}

	responseCode := resp.GetCode()

	if responseCode != uint32(apimodel.Code_ExecuteSuccess) {
		log.GetBaseLogger().Infof("[Config] failed to create config file. namespace = %s, fileGroup = %s, fileName = %s, response code = %d",
			namespace, fileGroup, fileName, responseCode)
		errMsg := fmt.Sprintf("failed to create config file. namespace = %s, fileGroup = %s, fileName = %s, response code = %d",
			namespace, fileGroup, fileName, responseCode)
		return model.NewSDKError(model.ErrCodeInternalError, nil, errMsg)
	}

	return nil
}

// UpdateConfigFile 更新配置文件
func (c *ConfigFileFlow) UpdateConfigFile(namespace, fileGroup, fileName, content string) error {
	// 校验参数
	configFile := &configconnector.ConfigFile{
		Namespace: namespace,
		FileGroup: fileGroup,
		FileName:  fileName,
	}
	configFile.SetContent(content)

	if err := model.CheckConfigFileMetadata(configFile); err != nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, err, "")
	}

	c.fclock.Lock()
	defer c.fclock.Unlock()

	resp, err := c.connector.UpdateConfigFile(configFile)
	if err != nil {
		return err
	}

	responseCode := resp.GetCode()

	if responseCode != uint32(apimodel.Code_ExecuteSuccess) {
		log.GetBaseLogger().Infof("[Config] failed to update config file. namespace = %s, fileGroup = %s, fileName = %s, response code = %d",
			namespace, fileGroup, fileName, responseCode)
		errMsg := fmt.Sprintf("failed to update config file. namespace = %s, fileGroup = %s, fileName = %s, response code = %d",
			namespace, fileGroup, fileName, responseCode)
		return model.NewSDKError(model.ErrCodeInternalError, nil, errMsg)
	}

	return nil
}

// PublishConfigFile 发布配置文件
func (c *ConfigFileFlow) PublishConfigFile(namespace, fileGroup, fileName string) error {
	// 检验参数
	configFile := &configconnector.ConfigFile{
		Namespace: namespace,
		FileGroup: fileGroup,
		FileName:  fileName,
	}

	if err := model.CheckConfigFileMetadata(configFile); err != nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, err, "")
	}

	c.fclock.Lock()
	defer c.fclock.Unlock()

	resp, err := c.connector.PublishConfigFile(configFile)
	if err != nil {
		return err
	}

	responseCode := resp.GetCode()

	if responseCode != uint32(apimodel.Code_ExecuteSuccess) {
		log.GetBaseLogger().Infof("[Config] failed to publish config file. namespace = %s, fileGroup = %s, fileName = %s, response code = %d",
			namespace, fileGroup, fileName, responseCode)
		errMsg := fmt.Sprintf("failed to publish config file. namespace = %s, fileGroup = %s, fileName = %s, response code = %d",
			namespace, fileGroup, fileName, responseCode)
		return model.NewSDKError(model.ErrCodeInternalError, nil, errMsg)
	}

	return nil
}

func (c *ConfigFileFlow) addConfigFileToLongPollingPool(fileRepo *ConfigFileRepo) {
	configFileMetadata := fileRepo.configFileMetadata
	version := fileRepo.getVersion()

	log.GetBaseLogger().Infof("[Config] add long polling config file. metadata %#v, version: %+v",
		configFileMetadata, version)

	cacheKey := genCacheKeyByMetadata(configFileMetadata)
	c.configFilePool[cacheKey] = fileRepo
	c.notifiedVersion[cacheKey] = version

	// 开启长轮询任务
	c.startLongPollingTaskOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		c.cancel = cancel
		go func() {
			time.Sleep(5 * time.Second)
			c.mainLoop(ctx)
		}()
	})
}

func (c *ConfigFileFlow) startCheckVersionTask(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	versionCheck := func() {
		c.fclock.RLock()
		defer c.fclock.RUnlock()
		for _, repo := range c.repos {
			// 没有通知版本号
			if repo.GetNotifiedVersion() == initVersion {
				continue
			}

			remoteConfigFile := repo.loadRemoteFile()

			// 从服务端获取的配置文件版本号落后于通知的版本号，重新拉取配置
			if !(remoteConfigFile == nil || repo.GetNotifiedVersion() > remoteConfigFile.GetVersion()) {
				continue
			}

			if remoteConfigFile == nil {
				log.GetBaseLogger().Warnf("[Config] client does not pull the configuration, it will be pulled again."+
					"file = %+v, notified version = %d",
					repo.configFileMetadata, repo.notifiedVersion)
			} else {
				log.GetBaseLogger().Warnf("[Config] notified version greater than pulled version, will pull config file again. "+
					"file = %+v, notified version = %d, pulled version = %d",
					repo.configFileMetadata, repo.notifiedVersion, remoteConfigFile.GetVersion())
			}

			if err := repo.pull(); err != nil {
				log.GetBaseLogger().Errorf("[Config] pull config file error by check version task.", err)
			}
		}
	}

	for {
		select {
		case <-ctx.Done():
			break
		case <-ticker.C:
			versionCheck()
		}
	}
}

func (c *ConfigFileFlow) mainLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		pollingRetryPolicy := retryPolicy{
			delayMinTime: delayMinTime,
			delayMaxTime: delayMaxTime,
		}

		// 1. 生成订阅配置列表
		watchConfigFiles := c.assembleWatchConfigFiles()

		log.GetBaseLogger().Infof("[Config] do long polling. config file size = %d, delay time = %d",
			len(watchConfigFiles), pollingRetryPolicy.currentDelayTime)

		// 2. 调用 connector watch接口
		response, err := c.connector.WatchConfigFiles(watchConfigFiles)
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

			cacheKey := genCacheKey(changedConfigFile.GetNamespace(), changedConfigFile.GetFileGroup(),
				changedConfigFile.GetFileName())

			newNotifiedVersion := changedConfigFile.GetVersion()
			oldNotifiedVersion := c.getConfigFileNotifiedVersion(cacheKey, true)

			maxVersion := oldNotifiedVersion
			if newNotifiedVersion > oldNotifiedVersion {
				maxVersion = newNotifiedVersion
			}

			// 更新版本号
			c.updateNotifiedVersion(cacheKey, maxVersion)

			log.GetBaseLogger().Infof("[Config] received change event by long polling. file = %+v, new version = %d, old version = %d",
				changedConfigFile, newNotifiedVersion, oldNotifiedVersion)

			// 通知 remoteConfigFileRepo 拉取最新配置
			remoteConfigFileRepo := c.getRemoteConfigFileRepo(cacheKey)
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

func (c *ConfigFileFlow) assembleWatchConfigFiles() []*configconnector.ConfigFile {
	c.fclock.RLock()
	defer c.fclock.RUnlock()
	watchConfigFiles := make([]*configconnector.ConfigFile, 0, len(c.configFilePool))

	for cacheKey := range c.configFilePool {
		configFileMetadata := extractConfigFileMetadata(cacheKey)

		watchConfigFiles = append(watchConfigFiles, &configconnector.ConfigFile{
			Namespace: configFileMetadata.GetNamespace(),
			FileGroup: configFileMetadata.GetFileGroup(),
			FileName:  configFileMetadata.GetFileName(),
			Version:   c.getConfigFileNotifiedVersion(cacheKey, false),
		})
	}

	return watchConfigFiles
}

func (c *ConfigFileFlow) updateNotifiedVersion(cacheKey string, version uint64) {
	c.fclock.Lock()
	defer c.fclock.Unlock()
	c.notifiedVersion[cacheKey] = version
}

func (c *ConfigFileFlow) getConfigFileNotifiedVersion(cacheKey string, locking bool) uint64 {
	if locking {
		c.fclock.RLock()
		defer c.fclock.RUnlock()
	}
	version, ok := c.notifiedVersion[cacheKey]
	if !ok {
		version = initVersion
	}
	return version
}

func (c *ConfigFileFlow) getRemoteConfigFileRepo(cacheKey string) *ConfigFileRepo {
	c.fclock.RLock()
	defer c.fclock.RUnlock()
	fileRepo, ok := c.configFilePool[cacheKey]
	if !ok {
		return nil
	}
	return fileRepo
}

const (
	separator = "+"
)

// genCacheKey 生成配置文件缓存的 Key
func genCacheKey(namespace, fileGroup, fileName string) string {
	return namespace + separator + fileGroup + separator + fileName
}

// GenConfigFileCacheKeyByMetadata 生成配置文件缓存的 Key
func genCacheKeyByMetadata(configFileMetadata model.ConfigFileMetadata) string {
	return genCacheKey(configFileMetadata.GetNamespace(), configFileMetadata.GetFileGroup(),
		configFileMetadata.GetFileName())
}

// extractConfigFileMetadata 从配置文件 Key 解析出配置文件元数据
func extractConfigFileMetadata(key string) model.ConfigFileMetadata {
	info := strings.Split(key, separator)
	return &model.DefaultConfigFileMetadata{
		Namespace: info[0],
		FileGroup: info[1],
		FileName:  info[2],
	}
}
