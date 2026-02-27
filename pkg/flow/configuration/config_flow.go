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
	"hash/fnv"
	"strings"
	"sync"
	"time"

	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/configconnector"
	"github.com/polarismesh/polaris-go/pkg/plugin/configfilter"
	"github.com/polarismesh/polaris-go/pkg/plugin/events"
)

// 新增：动态长轮询管理器结构
type dynamicPollingManager struct {
	mu               sync.RWMutex
	watchConfigFiles map[string]*configconnector.ConfigFile
	pollingInProgress bool
	pollingCancel     context.CancelFunc
	pollingVersion    uint64
}

// 新增：创建动态长轮询管理器
func newDynamicPollingManager() *dynamicPollingManager {
	return &dynamicPollingManager{
		watchConfigFiles: make(map[string]*configconnector.ConfigFile),
		pollingVersion:   1,
	}
}

// 新增：动态添加监控文件
func (d *dynamicPollingManager) addWatchFile(cacheKey string, configFile *configconnector.ConfigFile) {
	d.mu.Lock()
	defer d.mu.Unlock()
	
	d.watchConfigFiles[cacheKey] = configFile
	
	// 如果当前正在轮询，需要触发重新组装监控列表
	if d.pollingInProgress && d.pollingCancel != nil {
		d.pollingCancel()
	}
}

// 新增：获取当前监控列表的快照
func (d *dynamicPollingManager) getWatchFilesSnapshot() []*configconnector.ConfigFile {
	d.mu.RLock()
	defer d.mu.RUnlock()
	
	snapshot := make([]*configconnector.ConfigFile, 0, len(d.watchConfigFiles))
	for _, file := range d.watchConfigFiles {
		snapshot = append(snapshot, file)
	}
	return snapshot
}

// 新增：动态管理器的完整实现
func (d *dynamicPollingManager) setPollingStatus(inProgress bool, cancel context.CancelFunc) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pollingInProgress = inProgress
	d.pollingCancel = cancel
}

func (d *dynamicPollingManager) removeWatchFile(cacheKey string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.watchConfigFiles, cacheKey)
}

func (d *dynamicPollingManager) updateFileVersion(cacheKey string, version uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if file, exists := d.watchConfigFiles[cacheKey]; exists {
		file.Version = version
	}
}

// 新增：版本连续性检查器
type versionContinuityChecker struct {
	maxVersionJump uint64 // 允许的最大版本跳跃值
}

func newVersionContinuityChecker() *versionContinuityChecker {
	return &versionContinuityChecker{
		maxVersionJump: 10, // 允许最多跳跃10个版本
	}
}

// 检查版本连续性
func (v *versionContinuityChecker) checkContinuity(oldVersion, newVersion uint64) (bool, string) {
	if oldVersion == initVersion || newVersion <= oldVersion {
		return true, ""
	}
	
	jump := newVersion - oldVersion
	if jump > v.maxVersionJump {
		reason := fmt.Sprintf("版本跳跃过大: 从%d到%d, 跳跃了%d个版本", oldVersion, newVersion, jump)
		return false, reason
	}
	
	return true, ""
}

// 新增：配置变更历史记录器
type configChangeHistory struct {
	mu      sync.RWMutex
	history map[string][]uint64 // cacheKey -> version history
}

func newConfigChangeHistory() *configChangeHistory {
	return &configChangeHistory{
		history: make(map[string][]uint64),
	}
}

// 记录版本变更历史
func (h *configChangeHistory) recordVersionChange(cacheKey string, version uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	if _, exists := h.history[cacheKey]; !exists {
		h.history[cacheKey] = make([]uint64, 0, 10)
	}
	
	// 只保留最近10个版本记录
	if len(h.history[cacheKey]) >= 10 {
		h.history[cacheKey] = h.history[cacheKey][1:]
	}
	
	h.history[cacheKey] = append(h.history[cacheKey], version)
}

// 获取版本变更历史
func (h *configChangeHistory) getVersionHistory(cacheKey string) []uint64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	
	return h.history[cacheKey]
}

// ConfigFileFlow 配置中心核心服务门面类
type ConfigFileFlow struct {
	cancel context.CancelFunc

	// 分段锁，支持并发获取不同文件的配置
	shardLocks     []sync.RWMutex
	shardLockCount int
	// 全局锁，用于保护需要全局遍历的操作（如assembleWatchConfigFiles）
	fclock          sync.RWMutex
	configFileCache sync.Map // 使用sync.Map确保并发安全
	repos           []*ConfigFileRepo
	configFilePool  map[string]*ConfigFileRepo
	notifiedVersion map[string]uint64

	connector configconnector.ConfigConnector
	chain     configfilter.Chain
	conf      config.Configuration

	persistHandler *CachePersistHandler

	startLongPollingTaskOnce sync.Once

	eventReporterChain []events.EventReporter
	
	// 新增：动态长轮询管理器
	pollingManager *dynamicPollingManager
	
	// 新增：版本连续性检查器
	versionChecker *versionContinuityChecker
	
	// 新增：配置变更历史记录器
	changeHistory *configChangeHistory
}

// NewConfigFileFlow 创建配置中心服务
func NewConfigFileFlow(connector configconnector.ConfigConnector, chain configfilter.Chain,
	conf config.Configuration, eventReporterChain []events.EventReporter) (*ConfigFileFlow, error) {
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
		connector:          connector,
		chain:              chain,
		conf:               conf,
		repos:              make([]*ConfigFileRepo, 0, 8),
		configFileCache:    sync.Map{},
		configFilePool:     map[string]*ConfigFileRepo{},
		notifiedVersion:    map[string]uint64{},
		persistHandler:     persistHandler,
		eventReporterChain: eventReporterChain,
		shardLockCount:     16, // 使用16个分段锁
		shardLocks:         make([]sync.RWMutex, 16),
		fclock:             sync.RWMutex{}, // 初始化全局锁
		
		// 新增：初始化动态长轮询管理器
		pollingManager: newDynamicPollingManager(),
		
		// 新增：初始化版本连续性检查器
		versionChecker: newVersionContinuityChecker(),
		
		// 新增：初始化配置变更历史记录器
		changeHistory: newConfigChangeHistory(),
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
		Mode:      req.Mode,
	}

	cacheKey := genCacheKeyByMetadata(configFileMetadata)

	// 使用sync.Map的Load方法检查缓存
	if configFile, ok := c.configFileCache.Load(cacheKey); ok {
		if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			log.GetBaseLogger().Debugf("[Config][Flow] 命中配置文件缓存. file=%s/%s/%s",
				req.Namespace, req.FileGroup, req.FileName)
		}
		return configFile.(model.ConfigFile), nil
	}

	log.GetBaseLogger().Infof("[Config][Flow] 配置文件缓存未命中，开始创建. file=%s/%s/%s, subscribe=%v",
		req.Namespace, req.FileGroup, req.FileName, req.Subscribe)

	// 使用分段写锁进行双重检查
	c.getShardLock(cacheKey)
	defer c.getShardUnlock(cacheKey)

	// double check
	if configFile, ok := c.configFileCache.Load(cacheKey); ok {
		return configFile.(model.ConfigFile), nil
	}

	fileRepo, err := newConfigFileRepo(configFileMetadata, c.connector, c.chain, c.conf, c.persistHandler, c.eventReporterChain)
	if err != nil {
		return nil, err
	}
	configFile := newDefaultConfigFile(configFileMetadata, fileRepo)

	if req.Subscribe {
		c.addConfigFileToLongPollingPool(fileRepo)
		// 使用全局锁保护repos切片的操作
		c.fclock.Lock()
		c.repos = append(c.repos, fileRepo)
		c.fclock.Unlock()
		c.configFileCache.Store(cacheKey, configFile)
		log.GetBaseLogger().Infof("[Config][Flow] 配置文件已订阅并加入长轮询池. file=%s/%s/%s, version=%d",
			req.Namespace, req.FileGroup, req.FileName, fileRepo.getVersion())
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

	cacheKey := genCacheKey(namespace, fileGroup, fileName)
	c.getShardLock(cacheKey)
	defer c.getShardUnlock(cacheKey)

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

	cacheKey := genCacheKey(namespace, fileGroup, fileName)
	c.getShardLock(cacheKey)
	defer c.getShardUnlock(cacheKey)

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

	cacheKey := genCacheKey(namespace, fileGroup, fileName)
	c.getShardLock(cacheKey)
	defer c.getShardUnlock(cacheKey)

	resp, err := c.connector.PublishConfigFile(configFile)
	if err != nil {
		return err
	}

	responseCode := resp.GetCode()
	responseMessage := resp.GetMessage()

	if responseCode != uint32(apimodel.Code_ExecuteSuccess) {
		log.GetBaseLogger().Infof("[Config] failed to publish config file. namespace = %s, fileGroup = %s, "+
			"fileName = %s, response code = %d, msg:%v", namespace, fileGroup, fileName, responseCode, responseMessage)
		errMsg := fmt.Sprintf("failed to publish config file. namespace = %s, fileGroup = %s, fileName = %s, "+
			"response code = %d, msg:%v", namespace, fileGroup, fileName, responseCode, responseMessage)
		return model.NewSDKError(model.ErrCodeInternalError, nil, errMsg)
	}

	return nil
}

// UpsertAndPublishConfigFile 创建配置文件并发布
func (c *ConfigFileFlow) UpsertAndPublishConfigFile(namespace, fileGroup, fileName, content string) error {
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

	cacheKey := genCacheKey(namespace, fileGroup, fileName)
	c.getShardLock(cacheKey)
	defer c.getShardUnlock(cacheKey)

	resp, err := c.connector.UpsertAndPublishConfigFile(configFile)
	if err != nil {
		log.GetBaseLogger().Infof("[Config] failed to UpsertAndPublishConfigFile. namespace = %s, "+
			"fileGroup = %s, fileName = %s, err:%+v", namespace, fileGroup, fileName, err)
		return err
	}

	responseCode := resp.GetCode()

	if responseCode != uint32(apimodel.Code_ExecuteSuccess) {
		log.GetBaseLogger().Infof("[Config] failed to upsert and publish config file. namespace = %s, "+
			"fileGroup = %s, fileName = %s, response code = %d",
			namespace, fileGroup, fileName, responseCode)
		errMsg := fmt.Sprintf("failed to upsert and publish config file. namespace = %s, fileGroup = %s, "+
			"fileName = %s, response code = %d",
			namespace, fileGroup, fileName, responseCode)
		return model.NewSDKError(model.ErrCodeInternalError, nil, errMsg)
	}

	return nil
}

// 修改addConfigFileToLongPollingPool函数，使用动态管理器
func (c *ConfigFileFlow) addConfigFileToLongPollingPool(fileRepo *ConfigFileRepo) {
	configFileMetadata := fileRepo.configFileMetadata
	version := fileRepo.getVersion()

	log.GetBaseLogger().Infof("[Config] add long polling config file. metadata %#v, version: %+v, notifiedVersion: %d",
		configFileMetadata, version, fileRepo.GetNotifiedVersion())

	cacheKey := genCacheKeyByMetadata(configFileMetadata)
	
	// 使用全局锁保护对 configFilePool 和 notifiedVersion 的写操作
	c.fclock.Lock()
	c.configFilePool[cacheKey] = fileRepo
	c.notifiedVersion[cacheKey] = version
	c.fclock.Unlock()
	
	// 新增：动态添加到长轮询管理器
	watchFile := &configconnector.ConfigFile{
		Namespace: configFileMetadata.GetNamespace(),
		FileGroup: configFileMetadata.GetFileGroup(),
		FileName:  configFileMetadata.GetFileName(),
		Version:   version,
	}
	c.pollingManager.addWatchFile(cacheKey, watchFile)

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

// TODO delete
func (c *ConfigFileFlow) startCheckVersionTask(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	versionCheck := func() {
		// 对每个repo分别获取对应的分段锁进行检查
		for _, repo := range c.repos {
			// 没有通知版本号
			if repo.GetNotifiedVersion() == initVersion {
				if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
					log.GetBaseLogger().Debugf("[Config][CheckVersion] 跳过未通知的配置文件. file=%s/%s/%s, notifiedVersion=%d",
						repo.configFileMetadata.GetNamespace(), repo.configFileMetadata.GetFileGroup(),
						repo.configFileMetadata.GetFileName(), repo.GetNotifiedVersion())
				}
				continue
			}

			cacheKey := genCacheKeyByMetadata(repo.configFileMetadata)
			c.getShardRLock(cacheKey)

			remoteConfigFile := repo.loadRemoteFile()

			// 从服务端获取的配置文件版本号落后于通知的版本号，重新拉取配置
			if !(remoteConfigFile == nil || repo.GetNotifiedVersion() > remoteConfigFile.GetVersion()) {
				if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
					log.GetBaseLogger().Debugf("[Config][CheckVersion] 版本一致，无需重新拉取. file=%s/%s/%s, notifiedVersion=%d, remoteVersion=%d",
						repo.configFileMetadata.GetNamespace(), repo.configFileMetadata.GetFileGroup(),
						repo.configFileMetadata.GetFileName(), repo.GetNotifiedVersion(), remoteConfigFile.GetVersion())
				}
				c.getShardRUnlock(cacheKey)
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

			c.getShardRUnlock(cacheKey)

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

// 修改mainLoop函数，实现非阻塞长轮询
func (c *ConfigFileFlow) mainLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second) // 每5秒检查一次
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.doNonBlockingPolling(ctx)
		}
	}
}

// 修改版本更新逻辑，添加连续性检查
func (c *ConfigFileFlow) updateConfigFileVersionWithContinuityCheck(cacheKey string, oldVersion, newVersion uint64) error {
	// 检查版本连续性
	isContinuous, reason := c.versionChecker.checkContinuity(oldVersion, newVersion)
	
	if !isContinuous {
		log.GetBaseLogger().Warnf("[Config][VersionCheck] %s, 触发全量拉取", reason)
		
		// 触发全量拉取，确保版本连续性
		c.triggerFullPull(cacheKey, newVersion)
		return nil
	}
	
	// 正常版本更新
	c.updateNotifiedVersion(cacheKey, newVersion)
	
	// 通知对应的repo拉取最新配置
	remoteConfigFileRepo := c.getRemoteConfigFileRepo(cacheKey)
	if remoteConfigFileRepo == nil {
		return fmt.Errorf("未找到配置文件Repo. cacheKey=%s", cacheKey)
	}
	
	remoteConfigFileRepo.onLongPollingNotified(newVersion)
	
	log.GetBaseLogger().Infof("[Config][VersionCheck] 版本正常更新. cacheKey=%s, oldVersion=%d, newVersion=%d",
		cacheKey, oldVersion, newVersion)
	
	return nil
}

// 修改doNonBlockingPolling函数中的版本处理逻辑
func (c *ConfigFileFlow) doNonBlockingPolling(ctx context.Context) {
	// 1. 获取当前监控文件的快照
	watchConfigFiles := c.pollingManager.getWatchFilesSnapshot()

	if len(watchConfigFiles) == 0 {
		if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			log.GetBaseLogger().Debugf("[Config][NonBlockingPolling] 没有需要监控的配置文件")
		}
		return
	}

	if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		log.GetBaseLogger().Debugf("[Config][NonBlockingPolling] 开始非阻塞长轮询. configFileSize=%d",
			len(watchConfigFiles))
		for _, wf := range watchConfigFiles {
			log.GetBaseLogger().Debugf("[Config][NonBlockingPolling] watch文件详情: file=%s/%s/%s, version=%d",
				wf.GetNamespace(), wf.GetFileGroup(), wf.GetFileName(), wf.GetVersion())
		}
	}

	log.GetBaseLogger().Infof("[Config] do non-blocking polling. config file size = %d", len(watchConfigFiles))

	// 2. 使用带超时的WatchConfigFiles调用
	_, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	response, err := c.connector.WatchConfigFiles(watchConfigFiles)
	if err != nil {
		log.GetBaseLogger().Errorf("[Config] non-blocking polling failed.", err)
		return
	}

	responseCode := response.GetCode()

	// 3.1 接口调用成功，处理配置变更
	if responseCode == uint32(apimodel.Code_ExecuteSuccess) && response.GetConfigFile() != nil {
		changedConfigFile := response.GetConfigFile()

		cacheKey := genCacheKey(changedConfigFile.GetNamespace(), changedConfigFile.GetFileGroup(),
			changedConfigFile.GetFileName())

		newNotifiedVersion := changedConfigFile.GetVersion()
		oldNotifiedVersion := c.getConfigFileNotifiedVersion(cacheKey, true)

		// 使用版本连续性检查器处理版本更新
		if err := c.updateConfigFileVersionWithContinuityCheck(cacheKey, oldNotifiedVersion, newNotifiedVersion); err != nil {
			log.GetBaseLogger().Errorf("[Config][NonBlockingPolling] 版本更新失败. cacheKey=%s, error=%v", cacheKey, err)
			return
		}

		log.GetBaseLogger().Infof("[Config] received change event by non-blocking polling. file = %+v, new version = %d, old version = %d",
			changedConfigFile, newNotifiedVersion, oldNotifiedVersion)
		
		return
	}

	// 3.2 如果没有变更，记录日志
	if responseCode == uint32(apimodel.Code_DataNoChange) {
		if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			log.GetBaseLogger().Debugf("[Config] non-blocking polling result: data no change. watchFileCount=%d", len(watchConfigFiles))
		} else {
			log.GetBaseLogger().Infof("[Config] non-blocking polling result: data no change")
		}
		return
	}

	// 3.3 预期之外的状态，记录错误
	log.GetBaseLogger().Errorf("[Config] non-blocking polling result with unexpected code. code = %d", responseCode)
}

// 新增：触发全量拉取，确保版本连续性
func (c *ConfigFileFlow) triggerFullPull(cacheKey string, targetVersion uint64) {
	c.getShardLock(cacheKey)
	defer c.getShardUnlock(cacheKey)

	remoteConfigFileRepo := c.getRemoteConfigFileRepo(cacheKey)
	if remoteConfigFileRepo == nil {
		log.GetBaseLogger().Errorf("[Config][FullPull] 未找到配置文件Repo. cacheKey=%s", cacheKey)
		return
	}

	// 强制拉取最新配置
	if err := remoteConfigFileRepo.pull(); err != nil {
		log.GetBaseLogger().Errorf("[Config][FullPull] 全量拉取配置失败. cacheKey=%s, error=%v", cacheKey, err)
		return
	}

	// 更新版本号
	c.updateNotifiedVersion(cacheKey, targetVersion)
	
	log.GetBaseLogger().Infof("[Config][FullPull] 全量拉取完成. cacheKey=%s, targetVersion=%d", cacheKey, targetVersion)
}

func (c *ConfigFileFlow) assembleWatchConfigFiles() []*configconnector.ConfigFile {
	// 使用全局锁保护configFilePool的遍历操作
	// 由于需要遍历整个pool，这里仍然使用原来的fclock锁机制
	// 但实际的长轮询操作频率较低，对性能影响有限
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
		return initVersion
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

// getShardIndex 根据cacheKey获取对应的分段锁索引
func (c *ConfigFileFlow) getShardIndex(cacheKey string) int {
	hash := fnv.New32a()
	hash.Write([]byte(cacheKey))
	return int(hash.Sum32()) % c.shardLockCount
}

// getShardRLock 获取指定cacheKey的读锁
func (c *ConfigFileFlow) getShardRLock(cacheKey string) {
	index := c.getShardIndex(cacheKey)
	c.shardLocks[index].RLock()
}

// getShardRUnlock 释放指定cacheKey的读锁
func (c *ConfigFileFlow) getShardRUnlock(cacheKey string) {
	index := c.getShardIndex(cacheKey)
	c.shardLocks[index].RUnlock()
}

// getShardLock 获取指定cacheKey的写锁
func (c *ConfigFileFlow) getShardLock(cacheKey string) {
	index := c.getShardIndex(cacheKey)
	c.shardLocks[index].Lock()
}

// getShardUnlock 释放指定cacheKey的写锁
func (c *ConfigFileFlow) getShardUnlock(cacheKey string) {
	index := c.getShardIndex(cacheKey)
	c.shardLocks[index].Unlock()
}