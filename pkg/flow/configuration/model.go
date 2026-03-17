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

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/configconnector"
)

type defaultConfigFile struct {
	model.DefaultConfigFileMetadata

	fileRepo   *ConfigFileRepo
	content    string
	persistent model.Persistent
	logCtx     *log.ContextLogger

	lock                sync.RWMutex
	changeListeners     []func(event model.ConfigFileChangeEvent)
	changeListenerChans []chan model.ConfigFileChangeEvent
}

func newDefaultConfigFile(metadata model.ConfigFileMetadata, repo *ConfigFileRepo) *defaultConfigFile {
	configFile := &defaultConfigFile{
		fileRepo:   repo,
		content:    repo.GetContent(),
		persistent: repo.GetPersistent(),
		logCtx:     repo.logCtx,
	}
	configFile.Namespace = metadata.GetNamespace()
	configFile.FileGroup = metadata.GetFileGroup()
	configFile.FileName = metadata.GetFileName()

	repo.AddChangeListener(configFile.repoChangeListener)
	return configFile
}

// GetLabels 获取标签
func (c *defaultConfigFile) GetLabels() map[string]string {
	remote := c.fileRepo.loadRemoteFile()
	if remote == nil {
		return map[string]string{}
	}
	return remote.GetLabels()
}

// GetContent 获取配置文件内容
func (c *defaultConfigFile) GetContent() string {
	if c.content == NotExistedFileContent {
		return ""
	}
	return c.content
}

// GetPersistent 获取配置文件内容
func (c *defaultConfigFile) GetPersistent() model.Persistent {
	return c.persistent
}

// GetVersionName 获取配置文件版本名称
func (c *defaultConfigFile) GetVersionName() string {
	if c.fileRepo == nil || c.fileRepo.loadRemoteFile() == nil {
		return ""
	}
	return c.fileRepo.loadRemoteFile().GetVersionName()
}

// GetVersion 获取配置文件版本号
func (c *defaultConfigFile) GetVersion() uint64 {
	if c.fileRepo == nil || c.fileRepo.loadRemoteFile() == nil {
		return 0
	}
	return c.fileRepo.loadRemoteFile().GetVersion()
}

// GetMd5 获取配置文件MD5值
func (c *defaultConfigFile) GetMd5() string {
	if c.fileRepo == nil || c.fileRepo.loadRemoteFile() == nil {
		return ""
	}
	return c.fileRepo.loadRemoteFile().GetMd5()
}

// HasContent 是否有配置内容
func (c *defaultConfigFile) HasContent() bool {
	return c.content != "" && c.content != NotExistedFileContent
}

func (c *defaultConfigFile) repoChangeListener(configFileMetadata model.ConfigFileMetadata, newContent string, persistent model.Persistent) error {
	oldContent := c.content

	c.logCtx.GetBaseLogger().Infof("[Config] update content. file = %+v, old content = %s, new content = %s",
		configFileMetadata, oldContent, newContent)

	var changeType model.ChangeType

	if oldContent == NotExistedFileContent && newContent != NotExistedFileContent {
		changeType = model.Added
	} else if oldContent != NotExistedFileContent && newContent == NotExistedFileContent {
		changeType = model.Deleted
		// NotExistedFileContent 只用于内部删除标记，不应该透露给用户
		newContent = ""
	} else if oldContent != newContent {
		changeType = model.Modified
	} else {
		changeType = model.NotChanged
	}

	event := model.ConfigFileChangeEvent{
		ConfigFileMetadata: configFileMetadata,
		OldValue:           c.content,
		NewValue:           newContent,
		ChangeType:         changeType,
		Persistent:         persistent,
	}
	c.content = newContent

	c.logCtx.GetBaseLogger().Infof("[Config] 配置文件变更事件. file=%s/%s/%s, changeType=%v, listenerCount=%d, "+
		"chanListenerCount=%d", configFileMetadata.GetNamespace(), configFileMetadata.GetFileGroup(),
		configFileMetadata.GetFileName(), changeType, len(c.changeListeners), len(c.changeListenerChans))

	c.fireChangeEvent(event)
	return nil
}

// AddChangeListenerWithChannel 增加配置文件变更监听器
func (c *defaultConfigFile) AddChangeListenerWithChannel() <-chan model.ConfigFileChangeEvent {
	c.lock.Lock()
	defer c.lock.Unlock()
	changeChan := make(chan model.ConfigFileChangeEvent, 64)
	c.changeListenerChans = append(c.changeListenerChans, changeChan)
	return changeChan
}

// AddChangeListener 增加配置文件变更监听器
func (c *defaultConfigFile) AddChangeListener(cb model.OnConfigFileChange) {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.changeListeners = append(c.changeListeners, cb)
}

func (c *defaultConfigFile) fireChangeEvent(event model.ConfigFileChangeEvent) {
	if c.logCtx.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		c.logCtx.GetBaseLogger().Debugf("[Config] 开始分发配置变更事件. file=%s/%s/%s, changeType=%v, chanCount=%d, listenerCount=%d",
			event.ConfigFileMetadata.GetNamespace(), event.ConfigFileMetadata.GetFileGroup(),
			event.ConfigFileMetadata.GetFileName(), event.ChangeType,
			len(c.changeListenerChans), len(c.changeListeners))
	}

	for i, listenerChan := range c.changeListenerChans {
		if c.logCtx.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			c.logCtx.GetBaseLogger().Debugf("[Config] 发送变更事件到channel[%d]. file=%s/%s/%s",
				i, event.ConfigFileMetadata.GetNamespace(), event.ConfigFileMetadata.GetFileGroup(),
				event.ConfigFileMetadata.GetFileName())
		}
		listenerChan <- event
	}

	for i, changeListener := range c.changeListeners {
		if c.logCtx.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			c.logCtx.GetBaseLogger().Debugf("[Config] 调用变更监听器[%d]. file=%s/%s/%s",
				i, event.ConfigFileMetadata.GetNamespace(), event.ConfigFileMetadata.GetFileGroup(),
				event.ConfigFileMetadata.GetFileName())
		}
		changeListener(event)
	}
}

type defaultConfigGroup struct {
	namespace       string
	group           string
	repo            *ConfigGroupRepo
	logCtx          *log.ContextLogger
	lock            sync.RWMutex
	changeListeners []model.OnConfigGroupChange
}

func newDefaultConfigGroup(ns, group string, repo *ConfigGroupRepo) *defaultConfigGroup {
	configGroup := &defaultConfigGroup{
		namespace:       ns,
		group:           group,
		repo:            repo,
		logCtx:          repo.logCtx,
		changeListeners: []model.OnConfigGroupChange{},
	}
	repo.AddChangeListener(configGroup.repoChangeListener)
	return configGroup
}

// AddChangeListener 增加配置文件变更监听器
func (c *defaultConfigGroup) AddChangeListener(cb model.OnConfigGroupChange) {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.changeListeners = append(c.changeListeners, cb)
}

func (c *defaultConfigGroup) GetFiles() ([]*model.SimpleConfigFile, string, bool) {
	val := c.repo.loadRemoteGroup()
	if val == nil {
		return nil, "", false
	}
	files := val.ReleaseFiles
	if len(files) == 0 {
		return nil, "", false
	}
	return files, val.Revision, true
}

func (c *defaultConfigGroup) repoChangeListener(oldVal *configconnector.ConfigGroupResponse,
	newVal *configconnector.ConfigGroupResponse) {
	event := &model.ConfigGroupChangeEvent{
		After: newVal.ReleaseFiles,
	}
	if oldVal != nil {
		event.Before = oldVal.ReleaseFiles
	}

	oldFileCount := 0
	if oldVal != nil {
		oldFileCount = len(oldVal.ReleaseFiles)
	}
	info := fmt.Sprintf("namespace=%s, group=%s, revision:%s, beforeFileCount=%d, afterFileCount=%d, event=%v",
		c.namespace, c.group, newVal.Revision, oldFileCount, len(newVal.ReleaseFiles), event.GetString())
	c.logCtx.GetBaseLogger().Infof("[Config][Group] 配置分组变更监听触发. listenerCount=%d, %s", len(c.changeListeners), info)

	c.lock.RLock()
	defer c.lock.RUnlock()

	for i := range c.changeListeners {
		if c.logCtx.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			c.logCtx.GetBaseLogger().Debugf("[Config][Group] 调用分组变更监听器[%d]. %s", i, info)
		}
		c.changeListeners[i](event)
	}
}
