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
	"github.com/polarismesh/polaris-go/pkg/plugin/events"
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
	connector configconnector.ConfigConnector
	chain     configfilter.Chain
	conf      config.Configuration

	configFileMetadata model.ConfigFileMetadata
	// 长轮询通知的版本号
	notifiedVersion uint64
	// 从服务端获取的原始配置对象 *configconnector.ConfigFile
	remoteConfigFileRef *atomic.Value
	retryPolicy         retryPolicy
	listeners           []ConfigFileRepoChangeListener

	persistHandler *CachePersistHandler

	fallbackToLocalCache bool

	eventReporterChain []events.EventReporter
}

// ConfigFileRepoChangeListener 远程配置文件发布监听器
type ConfigFileRepoChangeListener func(configFileMetadata model.ConfigFileMetadata, newContent string, persistent model.Persistent) error

// newConfigFileRepo 创建远程配置文件
func newConfigFileRepo(metadata model.ConfigFileMetadata,
	connector configconnector.ConfigConnector,
	chain configfilter.Chain,
	conf config.Configuration,
	persistHandler *CachePersistHandler,
	eventChain []events.EventReporter) (*ConfigFileRepo, error) {
	repo := &ConfigFileRepo{
		connector:          connector,
		chain:              chain,
		conf:               conf,
		configFileMetadata: metadata,
		notifiedVersion:    initVersion,
		retryPolicy: retryPolicy{
			delayMinTime: delayMinTime,
			delayMaxTime: delayMaxTime,
		},
		remoteConfigFileRef:  &atomic.Value{},
		persistHandler:       persistHandler,
		fallbackToLocalCache: conf.GetConfigFile().GetLocalCache().IsFallbackToLocalCache(),
		eventReporterChain:   eventChain,
	}
	repo.remoteConfigFileRef.Store(&configconnector.ConfigFile{
		Namespace: metadata.GetNamespace(),
		FileGroup: metadata.GetFileGroup(),
		FileName:  metadata.GetFileName(),
		Version:   initVersion,
		Mode:      metadata.GetFileMode(),
	})

	log.GetBaseLogger().Infof("[Config][Repo] 创建配置文件仓库. file=%s/%s/%s, mode=%v, initVersion=%d",
		metadata.GetNamespace(), metadata.GetFileGroup(), metadata.GetFileName(),
		metadata.GetFileMode(), initVersion)

	// 1. 同步从服务端拉取配置
	if err := repo.pull(); err != nil {
		log.GetBaseLogger().Errorf("[Config][Repo] 初始拉取配置失败. file=%s/%s/%s, err=%v",
			metadata.GetNamespace(), metadata.GetFileGroup(), metadata.GetFileName(), err)
		return nil, err
	}

	log.GetBaseLogger().Infof("[Config][Repo] 初始拉取配置完成. file=%s/%s/%s, version=%d, notifiedVersion=%d",
		metadata.GetNamespace(), metadata.GetFileGroup(), metadata.GetFileName(),
		repo.getVersion(), repo.notifiedVersion)

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

// GetPersistent 获取配置文件持久化配置
func (r *ConfigFileRepo) GetPersistent() model.Persistent {
	remoteFile := r.loadRemoteFile()
	if remoteFile == nil {
		return model.Persistent{}
	}
	return remoteFile.GetPersistent()
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
	pullStartTime := time.Now()

	if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		log.GetBaseLogger().Debugf("[Config][Repo] 开始拉取配置文件. file=%s/%s/%s, notifiedVersion=%d, currentVersion=%d",
			r.configFileMetadata.GetNamespace(), r.configFileMetadata.GetFileGroup(),
			r.configFileMetadata.GetFileName(), r.notifiedVersion, r.getVersion())
	}

	pullConfigFileReq := &configconnector.ConfigFile{
		Namespace: r.configFileMetadata.GetNamespace(),
		FileGroup: r.configFileMetadata.GetFileGroup(),
		FileName:  r.configFileMetadata.GetFileName(),
		Version:   r.notifiedVersion,
		Mode:      r.configFileMetadata.GetFileMode(),
		Tags:      make([]*configconnector.ConfigFileTag, 0, len(r.conf.GetGlobal().GetClient().GetLabels())),
	}
	for k, v := range r.conf.GetGlobal().GetClient().GetLabels() {
		pullConfigFileReq.Tags = append(pullConfigFileReq.Tags, &configconnector.ConfigFileTag{
			Key:   k,
			Value: v,
		})
	}
	prepareReqDuration := time.Since(pullStartTime)

	log.GetBaseLogger().Infof("[Config] start pull config file. config file = %+v, version = %d",
		r.configFileMetadata, r.notifiedVersion)

	var (
		retryTimes = 0
		err        error
	)
	for retryTimes < 3 {
		startTime := time.Now()

		// 执行过滤器链和网络请求
		chainStartTime := time.Now()
		response, err := r.chain.Execute(pullConfigFileReq, r.connector.GetConfigFile)
		chainDuration := time.Since(chainStartTime)

		if err != nil {
			log.GetBaseLogger().Errorf("[Config] failed to pull config file. retry times = %d, err = %v, chain耗时 = %dms", retryTimes, err, chainDuration.Milliseconds())
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
		log.GetBaseLogger().Infof("[Config] pull config file finished. config file = %+v, code = %d, version = %d, duration = %d ms, chain耗时 = %d ms",
			pulledConfigFile.String(), responseCode, pulledConfigFileVersion, time.Since(startTime).Milliseconds(), chainDuration.Milliseconds())

		// 拉取成功
		if responseCode == uint32(apimodel.Code_ExecuteSuccess) {
			remoteConfigFile := r.loadRemoteFile()
			oldVersion := uint64(0)
			if remoteConfigFile != nil {
				oldVersion = remoteConfigFile.GetVersion()
			}

			if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
				log.GetBaseLogger().Debugf("[Config][Repo] 拉取成功，比较版本. file=%s/%s/%s, pulledVersion=%d, localVersion=%d, remoteConfigFileNil=%v",
					pullConfigFileReq.Namespace, pullConfigFileReq.FileGroup, pullConfigFileReq.FileName,
					pulledConfigFile.Version, oldVersion, remoteConfigFile == nil)
			}

			// 本地配置文件落后，更新内存缓存
			if remoteConfigFile == nil || pulledConfigFile.Version >= remoteConfigFile.Version {
				// 修复bug: pull成功后必须更新notifiedVersion，否则startCheckVersionTask会因为
				// notifiedVersion==initVersion(0)而跳过版本检查，导致配置变更无法被备份机制检测到
				oldNotifiedVersion := r.notifiedVersion
				r.notifiedVersion = pulledConfigFile.Version

				log.GetBaseLogger().Infof("[Config][Repo] 更新notifiedVersion. file=%s/%s/%s, oldNotifiedVersion=%d, newNotifiedVersion=%d, pulledVersion=%d",
					pullConfigFileReq.Namespace, pullConfigFileReq.FileGroup, pullConfigFileReq.FileName,
					oldNotifiedVersion, r.notifiedVersion, pulledConfigFile.Version)

				// save into local_cache
				saveCacheStart := time.Now()
				r.saveCacheConfigFile(pulledConfigFile)
				saveCacheDuration := time.Since(saveCacheStart)

				fireEventStart := time.Now()
				r.fireChangeEvent(pulledConfigFile)
				fireEventDuration := time.Since(fireEventStart)

				totalPullDuration := time.Since(pullStartTime)
				log.GetBaseLogger().Infof("[Config][Repo] pull耗时统计 - file=%s/%s/%s, 总耗时=%dms, 准备请求=%dms, chain执行=%dms, 保存缓存=%dms, 触发事件=%dms",
					pullConfigFileReq.Namespace, pullConfigFileReq.FileGroup, pullConfigFileReq.FileName,
					totalPullDuration.Milliseconds(), prepareReqDuration.Milliseconds(), chainDuration.Milliseconds(),
					saveCacheDuration.Milliseconds(), fireEventDuration.Milliseconds())
			} else {
				if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
					log.GetBaseLogger().Debugf("[Config][Repo] 拉取的版本不比本地新，跳过更新. file=%s/%s/%s, pulledVersion=%d, localVersion=%d",
						pullConfigFileReq.Namespace, pullConfigFileReq.FileGroup, pullConfigFileReq.FileName,
						pulledConfigFile.Version, oldVersion)
				}
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
				log.GetBaseLogger().Infof("[Config][Repo] 配置文件已删除，触发变更事件. file=%s/%s/%s",
					pullConfigFileReq.Namespace, pullConfigFileReq.FileGroup, pullConfigFileReq.FileName)
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
	log.GetBaseLogger().Infof("[Config] fallback to local cache success.")
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

func (r *ConfigFileRepo) handleEventReporterChain(f *configconnector.ConfigFile) {
	e := &model.BaseEventImpl{
		BaseType: model.ConfigBaseEvent,
		ConfigEvent: &model.ConfigEventImpl{
			EventName:         model.ConfigUpdated,
			EventTime:         time.Now().Format("2006-01-02 15:04:05"),
			Namespace:         r.configFileMetadata.GetNamespace(),
			ConfigGroup:       r.configFileMetadata.GetFileGroup(),
			ConfigFileName:    r.configFileMetadata.GetFileName(),
			ConfigFileVersion: f.GetVersionName(),
			ClientType:        model.ConfigFileRequestMode2Str[r.configFileMetadata.GetFileMode()],
		},
	}
	for _, chain := range r.eventReporterChain {
		if err := chain.ReportEvent(e); err != nil {
			log.GetBaseLogger().Errorf("[Config] report event(%+v) err: %+v", e, err)
			continue
		}
	}
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
	currentVersion := uint64(0)
	if remoteConfigFile != nil {
		currentVersion = remoteConfigFile.GetVersion()
	}

	if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		log.GetBaseLogger().Debugf("[Config][Repo] 收到长轮询通知. file=%s/%s/%s, newVersion=%d, currentVersion=%d, notifiedVersion=%d",
			r.configFileMetadata.GetNamespace(), r.configFileMetadata.GetFileGroup(),
			r.configFileMetadata.GetFileName(), newVersion, currentVersion, r.notifiedVersion)
	}

	if remoteConfigFile != nil && remoteConfigFile.GetVersion() >= newVersion {
		if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			log.GetBaseLogger().Debugf("[Config][Repo] 本地版本不落后，跳过拉取. file=%s/%s/%s, localVersion=%d, notifiedNewVersion=%d",
				r.configFileMetadata.GetNamespace(), r.configFileMetadata.GetFileGroup(),
				r.configFileMetadata.GetFileName(), remoteConfigFile.GetVersion(), newVersion)
		}
		return
	}

	log.GetBaseLogger().Infof("[Config][Repo] 长轮询通知版本更新，开始拉取. file=%s/%s/%s, newVersion=%d, currentVersion=%d",
		r.configFileMetadata.GetNamespace(), r.configFileMetadata.GetFileGroup(),
		r.configFileMetadata.GetFileName(), newVersion, currentVersion)

	r.notifiedVersion = newVersion
	if err := r.pull(); err != nil {
		log.GetBaseLogger().Errorf("[Config] pull config file error by long polling notification. file=%s/%s/%s, err=%v",
			r.configFileMetadata.GetNamespace(), r.configFileMetadata.GetFileGroup(),
			r.configFileMetadata.GetFileName(), err)
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

	log.GetBaseLogger().Infof("[Config][Repo] 触发配置变更事件. file=%s/%s/%s, version=%d, notExist=%v, listenerCount=%d",
		r.configFileMetadata.GetNamespace(), r.configFileMetadata.GetFileGroup(),
		r.configFileMetadata.GetFileName(), f.GetVersion(), f.NotExist, len(r.listeners))

	if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		log.GetBaseLogger().Debugf("[Config][Repo] 配置变更事件详情. file=%s/%s/%s, version=%d, md5=%s, contentLen=%d",
			r.configFileMetadata.GetNamespace(), r.configFileMetadata.GetFileGroup(),
			r.configFileMetadata.GetFileName(), f.GetVersion(), f.GetMd5(), len(f.GetContent()))
	}

	if f.NotExist {
		r.remoteConfigFileRef = &atomic.Value{}
	} else {
		r.remoteConfigFileRef.Store(f)
	}

	for i, listener := range r.listeners {
		if err := listener(r.configFileMetadata, f.GetContent(), f.Persistent); err != nil {
			log.GetBaseLogger().Errorf("[Config] invoke config file repo change listener[%d] failed. file=%s/%s/%s, err=%v",
				i, r.configFileMetadata.GetNamespace(), r.configFileMetadata.GetFileGroup(),
				r.configFileMetadata.GetFileName(), err)
		}
	}

	// 处理文件配置变更事件上报
	r.handleEventReporterChain(f)
}
