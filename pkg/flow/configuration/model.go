/**
 * Tencent is pleased to support the open source community by making Polaris available.
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

	"github.com/polarismesh/polaris-go/pkg/flow/configuration/remote"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
)

type defaultConfigFile struct {
	model.DefaultConfigFileMetadata

	remoteConfigFileRepo *remote.ConfigFileRepo
	content              string

	lock                *sync.Mutex
	changeListeners     []func(event model.ConfigFileChangeEvent)
	changeListenerChans []chan model.ConfigFileChangeEvent
}

func newDefaultConfigFile(metadata model.ConfigFileMetadata, repo *remote.ConfigFileRepo) *defaultConfigFile {
	configFile := &defaultConfigFile{
		remoteConfigFileRepo: repo,
		content:              repo.GetContent(),
		lock:                 new(sync.Mutex),
	}
	configFile.Namespace = metadata.GetNamespace()
	configFile.FileGroup = metadata.GetFileGroup()
	configFile.FileName = metadata.GetFileName()

	repo.AddChangeListener(configFile.repoChangeListener)

	return configFile
}

// GetContent 获取配置文件内容
func (c *defaultConfigFile) GetContent() string {
	if c.content == remote.NotExistedFileContent {
		return ""
	}
	return c.content
}

// HasContent 是否有配置内容
func (c *defaultConfigFile) HasContent() bool {
	return c.content != "" && c.content != remote.NotExistedFileContent
}

func (c *defaultConfigFile) repoChangeListener(configFileMetadata model.ConfigFileMetadata, newContent string) error {
	oldContent := c.content

	log.GetBaseLogger().Infof("[Config] update content. file = %+v, old content = %s, new content = %s",
		configFileMetadata, oldContent, newContent)

	var changeType model.ChangeType

	if oldContent == remote.NotExistedFileContent && newContent != remote.NotExistedFileContent {
		changeType = model.Added
	} else if oldContent != remote.NotExistedFileContent && newContent == remote.NotExistedFileContent {
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
	}
	c.content = newContent

	c.fireChangeEvent(event)
	return nil
}

// AddChangeListenerWithChannel 增加配置文件变更监听器
func (c *defaultConfigFile) AddChangeListenerWithChannel(changeChan chan model.ConfigFileChangeEvent) {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.changeListenerChans = append(c.changeListenerChans, changeChan)
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
