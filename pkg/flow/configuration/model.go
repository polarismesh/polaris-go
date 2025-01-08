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

	lock                sync.RWMutex
	changeListeners     []func(event model.ConfigFileChangeEvent)
	changeListenerChans []chan model.ConfigFileChangeEvent
}

func newDefaultConfigFile(metadata model.ConfigFileMetadata, repo *ConfigFileRepo) *defaultConfigFile {
	configFile := &defaultConfigFile{
		fileRepo:   repo,
		content:    repo.GetContent(),
		persistent: repo.GetPersistent(),
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

// HasContent 是否有配置内容
func (c *defaultConfigFile) HasContent() bool {
	return c.content != "" && c.content != NotExistedFileContent
}

func (c *defaultConfigFile) repoChangeListener(configFileMetadata model.ConfigFileMetadata, newContent string, persistent model.Persistent) error {
	oldContent := c.content

	log.GetBaseLogger().Infof("[Config] update content. file = %+v, old content = %s, new content = %s",
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
	for _, listenerChan := range c.changeListenerChans {
		listenerChan <- event
	}

	for _, changeListener := range c.changeListeners {
		changeListener(event)
	}
}

type defaultConfigGroup struct {
	namespace       string
	group           string
	repo            *ConfigGroupRepo
	lock            sync.RWMutex
	changeListeners []model.OnConfigGroupChange
}

func newDefaultConfigGroup(ns, group string, repo *ConfigGroupRepo) *defaultConfigGroup {
	configGroup := &defaultConfigGroup{
		namespace:       ns,
		group:           group,
		repo:            repo,
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

func (c *defaultConfigGroup) repoChangeListener(val *configconnector.ConfigGroupResponse) {
	oldVal := c.repo.loadRemoteGroup()

	event := &model.ConfigGroupChangeEvent{
		After: val.ReleaseFiles,
	}
	if oldVal != nil {
		event.Before = oldVal.ReleaseFiles
	}

	c.lock.RLock()
	defer c.lock.RUnlock()

	for i := range c.changeListeners {
		c.changeListeners[i](event)
	}
}
