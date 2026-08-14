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
	"sort"
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
	"github.com/polarismesh/polaris-go/pkg/sdk"
)

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
	globalCtx sdk.ValueContext
	logCtx    *log.ContextLogger

	persistHandler *CachePersistHandler

	startLongPollingTaskOnce sync.Once

	eventReporterChain []events.EventReporter

	// onWatchChanged 配置文件监听列表变化时的回调，由 Engine 注入用于触发即时 ReportClient
	onWatchChanged func()
}

// NewConfigFileFlow 创建配置中心服务
func NewConfigFileFlow(globalCtx sdk.ValueContext, connector configconnector.ConfigConnector,
	chain configfilter.Chain,
	conf config.Configuration, eventReporterChain []events.EventReporter) (*ConfigFileFlow, error) {
	persistHandler, err := NewCachePersistHandler(
		conf.GetConfigFile().GetLocalCache().GetPersistDir(),
		conf.GetConfigFile().GetLocalCache().GetPersistMaxWriteRetry(),
		conf.GetConfigFile().GetLocalCache().GetPersistMaxReadRetry(),
		conf.GetConfigFile().GetLocalCache().GetPersistRetryInterval(),
		globalCtx.GetContextLogger(),
	)
	if err != nil {
		return nil, err
	}

	configFileService := &ConfigFileFlow{
		connector:          connector,
		chain:              chain,
		conf:               conf,
		globalCtx:          globalCtx,
		logCtx:             globalCtx.GetContextLogger(),
		repos:              make([]*ConfigFileRepo, 0, 8),
		configFileCache:    sync.Map{},
		configFilePool:     map[string]*ConfigFileRepo{},
		notifiedVersion:    map[string]uint64{},
		persistHandler:     persistHandler,
		eventReporterChain: eventReporterChain,
		shardLockCount:     16, // 使用16个分段锁
		shardLocks:         make([]sync.RWMutex, 16),
		fclock:             sync.RWMutex{}, // 初始化全局锁
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
		if c.logCtx.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			c.logCtx.GetBaseLogger().Debugf("[ConfigFileFlow] 命中配置文件缓存. file=%s/%s/%s",
				req.Namespace, req.FileGroup, req.FileName)
		}
		return configFile.(model.ConfigFile), nil
	}

	c.logCtx.GetBaseLogger().Infof("[ConfigFileFlow] 配置文件缓存未命中，开始创建. file=%s/%s/%s, subscribe=%v",
		req.Namespace, req.FileGroup, req.FileName, req.Subscribe)

	// 使用分段写锁进行双重检查
	c.getShardLock(cacheKey)
	defer c.getShardUnlock(cacheKey)

	// double check
	if configFile, ok := c.configFileCache.Load(cacheKey); ok {
		return configFile.(model.ConfigFile), nil
	}

	fileRepo, err := newConfigFileRepo(c.globalCtx, configFileMetadata, c.connector, c.chain, c.conf, c.persistHandler,
		c.eventReporterChain)
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
		c.logCtx.GetBaseLogger().Infof("[ConfigFileFlow] 配置文件已订阅并加入长轮询池. file=%s/%s/%s, version=%d",
			req.Namespace, req.FileGroup, req.FileName, fileRepo.getVersion())
		// 通知监听列表变化以触发即时 ReportClient 上报。
		// 回调（TriggerNow）本身非阻塞（CAS + 内部起协程），直接同步调用即可，无需再包一层 go。
		c.fclock.RLock()
		onWatchChanged := c.onWatchChanged
		c.fclock.RUnlock()
		if onWatchChanged != nil {
			onWatchChanged()
		}
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
		c.logCtx.GetBaseLogger().Infof("[ConfigFileFlow] failed to create config file. namespace = %s, fileGroup = %s, fileName = %s, response code = %d",
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
		c.logCtx.GetBaseLogger().Infof("[ConfigFileFlow] failed to update config file. namespace = %s, fileGroup = %s, fileName = %s, response code = %d",
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
		c.logCtx.GetBaseLogger().Infof("[ConfigFileFlow] failed to publish config file. namespace = %s, fileGroup = %s, "+
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
		c.logCtx.GetBaseLogger().Infof("[ConfigFileFlow] failed to UpsertAndPublishConfigFile. namespace = %s, "+
			"fileGroup = %s, fileName = %s, err:%+v", namespace, fileGroup, fileName, err)
		return err
	}

	responseCode := resp.GetCode()

	if responseCode != uint32(apimodel.Code_ExecuteSuccess) {
		c.logCtx.GetBaseLogger().Infof("[ConfigFileFlow] failed to upsert and publish config file. namespace = %s, "+
			"fileGroup = %s, fileName = %s, response code = %d",
			namespace, fileGroup, fileName, responseCode)
		errMsg := fmt.Sprintf("failed to upsert and publish config file. namespace = %s, fileGroup = %s, "+
			"fileName = %s, response code = %d",
			namespace, fileGroup, fileName, responseCode)
		return model.NewSDKError(model.ErrCodeInternalError, nil, errMsg)
	}

	return nil
}

func (c *ConfigFileFlow) addConfigFileToLongPollingPool(fileRepo *ConfigFileRepo) {
	configFileMetadata := fileRepo.configFileMetadata
	version := fileRepo.getVersion()

	c.logCtx.GetBaseLogger().Infof("[ConfigFileFlow] add long polling config file. metadata %#v, version: %+v, notifiedVersion: %d",
		configFileMetadata, version, fileRepo.GetNotifiedVersion())

	cacheKey := genCacheKeyByMetadata(configFileMetadata)
	// 使用全局锁保护对 configFilePool 和 notifiedVersion 的写操作
	c.fclock.Lock()
	c.configFilePool[cacheKey] = fileRepo
	c.notifiedVersion[cacheKey] = version
	c.fclock.Unlock()

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

func (c *ConfigFileFlow) mainLoop(ctx context.Context) {
	// 每半小时打印一次 Info 日志，确认长轮询在正常工作
	const infoLogInterval = 30 * time.Minute
	lastInfoLogTime := time.Now()
	noChangeCount := uint64(0)

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

		if c.logCtx.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			c.logCtx.GetBaseLogger().Debugf("[ConfigFileFlow][LongPolling] 开始长轮询. configFileSize=%d, delayTime=%d",
				len(watchConfigFiles), pollingRetryPolicy.currentDelayTime)
			for _, wf := range watchConfigFiles {
				c.logCtx.GetBaseLogger().Debugf("[ConfigFileFlow][LongPolling] watch文件详情: file=%s/%s/%s, version=%d",
					wf.GetNamespace(), wf.GetFileGroup(), wf.GetFileName(), wf.GetVersion())
			}
		}

		// 每半小时打印一次 Info 级别日志，确认长轮询在正常工作
		now := time.Now()
		if now.Sub(lastInfoLogTime) >= infoLogInterval {
			c.logCtx.GetBaseLogger().Infof("[ConfigFileFlow][LongPolling] 长轮询运行中. watchFileCount=%d, noChangeCount=%d (最近%.0f分钟)",
				len(watchConfigFiles), noChangeCount, now.Sub(lastInfoLogTime).Minutes())
			lastInfoLogTime = now
			noChangeCount = 0
		}
		c.logCtx.GetBaseLogger().Debugf("[ConfigFileFlow][LongPolling] do long polling. config file size = %d, delay time = %d",
			len(watchConfigFiles), pollingRetryPolicy.currentDelayTime)

		// 2. 调用 connector watch接口
		response, err := c.connector.WatchConfigFiles(watchConfigFiles)
		if err != nil {
			c.logCtx.GetBaseLogger().Errorf("[ConfigFileFlow][LongPolling] long polling failed. err:%v", err)
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

			c.logCtx.GetBaseLogger().Infof("[ConfigFileFlow][LongPolling] received change event by long polling. file = %+v, new "+
				"version = %d, old version = %d, maxVersion = %d", changedConfigFile, newNotifiedVersion,
				oldNotifiedVersion, maxVersion)

			// 通知 remoteConfigFileRepo 拉取最新配置
			remoteConfigFileRepo := c.getRemoteConfigFileRepo(cacheKey)
			if remoteConfigFileRepo == nil {
				c.logCtx.GetBaseLogger().Errorf("[ConfigFileFlow][LongPolling] 未找到配置文件Repo. cacheKey=%s", cacheKey)
				continue
			}
			remoteConfigFileRepo.onLongPollingNotified(maxVersion)

			continue
		}

		// 3.2 如果没有变更，打印日志
		if responseCode == uint32(apimodel.Code_DataNoChange) {
			pollingRetryPolicy.success()
			noChangeCount++
			c.logCtx.GetBaseLogger().Debugf("[ConfigFileFlow][LongPolling] long polling result: data no change. "+
				"watchFileCount=%d", len(watchConfigFiles))
			continue
		}

		// 3.3 预期之外的状态，退避重试
		c.logCtx.GetBaseLogger().Errorf("[ConfigFileFlow][LongPolling] long polling result with unexpect code. code = %d",
			responseCode)
		pollingRetryPolicy.fail()
		pollingRetryPolicy.delay()
	}
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

// ConfigFileMetadataItem 配置文件元数据项，对应 ReportClient 上报 config_metadata 中
// config_watch 数组的单个元素，字段采用 snake_case JSON 命名与服务端配置中心三级树一致。
type ConfigFileMetadataItem struct {
	Namespace string `json:"namespace"`
	Group     string `json:"group"`
	FileName  string `json:"file_name"`
	Version   uint64 `json:"version"`
	Md5       string `json:"md5"`
}

// GetWatchedConfigFileMetadata 返回当前监听的配置文件元数据列表快照，供 ReportClient 上报 config_metadata。
// 内部持有 fclock 读锁遍历 configFilePool，调用期间配置文件列表不会被并发修改；
// 返回值始终非 nil（池为空时返回空切片），调用方可直接序列化为 JSON。
// receiver 为 nil 时（配置中心未启用，指针被装入接口）返回空切片，避免解引用 panic。
// 返回前按 (namespace, group, file_name) 排序：configFilePool 是 map，遍历序随机，若不排序，
// 每次序列化出的 config_metadata 字符串顺序都不同，会把「同一份监听列表」误判为「订阅变化」，
// 导致 client_info.json 每个上报周期都被重写一遍（多余的磁盘 I/O 与日志）；排序后字符串稳定，
// 变化检测才真实反映监听集合的增删。
func (c *ConfigFileFlow) GetWatchedConfigFileMetadata() []ConfigFileMetadataItem {
	if c == nil {
		return []ConfigFileMetadataItem{}
	}
	c.fclock.RLock()
	defer c.fclock.RUnlock()
	items := make([]ConfigFileMetadataItem, 0, len(c.configFilePool))
	for cacheKey, repo := range c.configFilePool {
		metadata := repo.configFileMetadata
		item := ConfigFileMetadataItem{
			Namespace: metadata.GetNamespace(),
			Group:     metadata.GetFileGroup(),
			FileName:  metadata.GetFileName(),
		}
		// version 与 md5 统一取自同一次 loadRemoteFile 快照（即本地实际生效的配置），保证自一致——
		// 若 version 取 notifiedVersion 而 md5 取 remoteConfigFileRef，长轮询并发更新瞬间会拼出
		// "version 旧、md5 新" 的撕裂组合。尚未拉取到文件时 version 回退 notifiedVersion、md5 留空。
		if cf := repo.loadRemoteFile(); cf != nil {
			item.Version = cf.GetVersion()
			item.Md5 = cf.GetMd5()
		} else {
			item.Version = c.getConfigFileNotifiedVersion(cacheKey, false)
		}
		items = append(items, item)
	}
	// 排序保证序列化结果确定，与监听集合的内容一一对应（与遍历顺序无关）
	sort.Slice(items, func(i, j int) bool {
		if items[i].Namespace != items[j].Namespace {
			return items[i].Namespace < items[j].Namespace
		}
		if items[i].Group != items[j].Group {
			return items[i].Group < items[j].Group
		}
		return items[i].FileName < items[j].FileName
	})
	return items
}

// ConfigFileContentItem 配置文件元数据 + 内容，供配置生效查询 ACK 应答使用。
// 相比 ConfigFileMetadataItem 多 Content 字段，仅在单点查询命中时返回，
// 不进入 ReportClient 的全量 config_metadata 上报（避免 config_metadata 携带大体积内容膨胀）。
type ConfigFileContentItem struct {
	Namespace string `json:"namespace"`
	Group     string `json:"group"`
	FileName  string `json:"file_name"`
	Version   uint64 `json:"version"`
	Md5       string `json:"md5"`
	// Content 为配置文件的源内容（SourceContent）：非加密配置即应用生效内容；加密配置为密文。
	// 取源内容而非 GetContent() 的原因有二：
	//  1. Md5 是服务端对源内容的摘要，回传源内容才能保证 md5(content) 自洽，可供服务端校验；
	//  2. 加密配置的 GetContent() 是解密后的明文，回传明文会把本仓库刻意保护的敏感内容（配置
	//     变更日志已对加密 tag 打码）经 ACK 明文回传，扩大暴露面。密文回传不泄露明文，且服务端
	//     作为配置来源本就可据此校验版本与摘要。
	Content string `json:"content"`
	// EffectiveTime 配置在客户端本地的实际生效时刻（int64 毫秒时间戳），
	// 取自 ConfigFileRepo 在 fireChangeEvent 时记录的 time.Now().UnixMilli()。
	// 未拉取到远端文件时为零值（omitempty 省略）。
	EffectiveTime int64 `json:"effective_time,omitempty"`
	// Pulled 标记是否已拉取到远端文件（仅内部使用，不进入 ACK JSON）。
	// 已订阅但尚未拉取成功（首次拉取失败/重试中）时为 false，供调用方区分「未生效」与「已生效」。
	Pulled bool `json:"-"`
}

// GetWatchedConfigFileContent 按 (namespace, group, fileName) 查询单个监听配置文件的元数据与内容。
// 未监听返回 (zero, false)；已监听但尚未拉取到远端文件返回 (item, true) 且 item.Pulled=false；
// 已拉取返回 (item, true) 且 item.Pulled=true，version/md5/content/effectiveTime 齐全。
// version/md5/content 三者统一取自同一次 loadRemoteFile 快照，保证自一致——
// 若分别从 notifiedVersion 与 remoteConfigFileRef 取，长轮询并发更新时会返回
// "version 旧、content 新" 的撕裂组合，导致服务端误判配置是否生效。
// content 取 SourceContent（加密配置为密文），与 md5 自洽且不回传解密明文，详见字段注释。
// 内部持有 fclock 读锁，并发安全；receiver 为 nil 时返回 (zero, false) 避免解引用 panic。
func (c *ConfigFileFlow) GetWatchedConfigFileContent(namespace, fileGroup, fileName string) (ConfigFileContentItem, bool) {
	if c == nil {
		return ConfigFileContentItem{}, false
	}
	cacheKey := genCacheKey(namespace, fileGroup, fileName)
	c.fclock.RLock()
	defer c.fclock.RUnlock()
	repo, ok := c.configFilePool[cacheKey]
	if !ok || repo == nil {
		return ConfigFileContentItem{}, false
	}
	item := ConfigFileContentItem{
		Namespace: namespace,
		Group:     fileGroup,
		FileName:  fileName,
	}
	// 单次快照取值，保证 version/md5/content 自一致
	cf := repo.loadRemoteFile()
	if cf == nil {
		// 已订阅但尚未拉取到远端文件（首次拉取失败/重试中）：仅回退 notifiedVersion，
		// Pulled=false 供调用方按「未生效」处理
		item.Version = c.getConfigFileNotifiedVersion(cacheKey, false)
		return item, true
	}
	item.Version = cf.GetVersion()
	item.Md5 = cf.GetMd5()
	item.Content = cf.GetSourceContent()
	item.EffectiveTime = repo.getEffectiveTime()
	item.Pulled = true
	return item, true
}

// SetWatchChangedCallback 注入配置文件监听列表变化时的回调。
// 由 Engine 在创建 ReportClientCallBack 后注入，cb 通常为 callback.TriggerNow；
// cb 为 nil 时清除回调。回调在新配置 Subscribe 时被异步调用。
func (c *ConfigFileFlow) SetWatchChangedCallback(cb func()) {
	c.fclock.Lock()
	defer c.fclock.Unlock()
	c.onWatchChanged = cb
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
