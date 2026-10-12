// Tencent is pleased to support the open source community by making polaris-go available.
//
// Copyright (C) 2021 Tencent. All rights reserved.
//
// Licensed under the BSD 3-Clause License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// https://opensource.org/licenses/BSD-3-Clause
//
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package configuration 提供配置文件和配置分组的拉取、缓存与变更监听。
package configuration

import (
	"fmt"
	"runtime/debug"
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

	// 无论是否加密，配置变更日志均不打印配置内容明文，仅打印不可逆的 Md5、version 及加密标识用于判断变更。
	//
	// 此处依赖两个前提：(1) 同一 repo 的 fireChangeEvent 串行触发（初始 pull 仅构造时一次，
	// 其后均来自 mainLoop 单 goroutine 的长轮询通知）；(2) fireChangeEvent 先 Store 新文件到
	// remoteConfigFileRef 再回调本 listener。故 loadRemoteFile() 取到的即为本次变更的新文件。
	// 若未来同一文件的 pull 被并发化，此处 md5/version 可能与本次 event 错配（仅影响日志，不影响功能）。
	// 删除(NotExist)场景 remoteConfigFileRef 被重置为空，loadRemoteFile() 返回 nil，按零值打印即可。
	var (
		encrypted  bool
		newMd5     string
		newVersion uint64
	)
	if rf := c.fileRepo.loadRemoteFile(); rf != nil {
		encrypted = rf.GetEncrypted()
		newMd5 = rf.GetMd5()
		newVersion = rf.GetVersion()
	}

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

	c.logCtx.GetBaseLogger().Infof("[Config] 配置文件变更事件. file=%s/%s/%s, changeType=%v, encrypted=%v, "+
		"newMd5=%s, newVersion=%d, listenerCount=%d, chanListenerCount=%d",
		configFileMetadata.GetNamespace(), configFileMetadata.GetFileGroup(), configFileMetadata.GetFileName(),
		changeType, encrypted, newMd5, newVersion, len(c.changeListeners), len(c.changeListenerChans))

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
		c.invokeChangeListener(i, changeListener, event)
	}
}

// invokeChangeListener 调用单个用户回调；回调 panic 时记录 ERROR 日志并返回，不影响后续监听器和宿主进程。
func (c *defaultConfigFile) invokeChangeListener(index int, listener model.OnConfigFileChange,
	event model.ConfigFileChangeEvent) {
	returned := false
	defer func() {
		if returned {
			return
		}
		// 旧版 panic(nil) 的 recover 返回 nil，必须通过正常返回标记识别异常。
		r := recover()
		// panic 值可能包含配置明文，只记录类型，不能格式化其内容。
		c.logCtx.GetBaseLogger().Errorf("[Config] 配置文件变更监听器[%d] panic recovered. file=%s/%s/%s, "+
			"changeType=%v, panicType=%T\nstack: %s", index, event.ConfigFileMetadata.GetNamespace(),
			event.ConfigFileMetadata.GetFileGroup(), event.ConfigFileMetadata.GetFileName(), event.ChangeType,
			r, string(debug.Stack()))
	}()
	listener(event)
	returned = true
}

type defaultConfigGroup struct {
	namespace       string
	group           string
	repo            *ConfigGroupRepo
	logCtx          *log.ContextLogger
	lock            sync.RWMutex
	changeListeners []model.OnConfigGroupChange
	// cancelableListeners 和 nextListenerID 均由 lock 保护。
	cancelableListeners map[uint64]model.OnConfigGroupChange
	nextListenerID      uint64
}

var _ model.CancelableConfigFileGroup = (*defaultConfigGroup)(nil)

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

// AddChangeListenerWithCancel 注册独立的配置分组回调；nil 回调返回空操作取消函数。
// 返回的取消函数可并发、重复调用，不等待已进入分发快照的回调，可在回调内取消自身。
func (c *defaultConfigGroup) AddChangeListenerWithCancel(cb model.OnConfigGroupChange) (cancel func()) {
	if cb == nil {
		return func() {}
	}

	c.lock.Lock()
	if c.cancelableListeners == nil {
		c.cancelableListeners = make(map[uint64]model.OnConfigGroupChange)
	}
	c.nextListenerID++
	id := c.nextListenerID
	c.cancelableListeners[id] = cb
	cancelableListenerCount := len(c.cancelableListeners)
	listenerCount := len(c.changeListeners) + cancelableListenerCount
	c.lock.Unlock()

	// 数量在注册时持锁取快照，日志在锁外输出；通过 ID 与取消记录关联。
	c.logCtx.GetBaseLogger().Infof("[Config][Group] 已注册配置分组监听器. namespace=%s, group=%s, "+
		"listenerID=%d, cancelableListenerCount=%d, listenerCount=%d",
		c.namespace, c.group, id, cancelableListenerCount, listenerCount)

	// 仅捕获分组对象和 ID，取消函数本身不持有回调及其关联的调用方对象。
	return func() {
		c.lock.Lock()
		if _, exists := c.cancelableListeners[id]; !exists {
			c.lock.Unlock()
			return
		}
		delete(c.cancelableListeners, id)
		remainingCancelableListeners := len(c.cancelableListeners)
		remainingListeners := len(c.changeListeners) + remainingCancelableListeners
		c.lock.Unlock()

		// 只记录实际摘除，数量在锁内取快照；日志写入在锁外，避免阻塞其他监听器操作。
		c.logCtx.GetBaseLogger().Infof("[Config][Group] 已取消配置分组监听器. namespace=%s, group=%s, "+
			"listenerID=%d, remainingCancelableListeners=%d, remainingListeners=%d",
			c.namespace, c.group, id, remainingCancelableListeners, remainingListeners)
	}
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

// repoChangeListener 将仓库变更同步分发给监听器；oldVal 可为 nil，newVal 必须非 nil。
// 在锁内复制监听器、锁外调用，允许回调注册或取消监听器；本轮快照不受后续取消影响。
func (c *defaultConfigGroup) repoChangeListener(oldVal *configconnector.ConfigGroupResponse,
	newVal *configconnector.ConfigGroupResponse) {
	event := &model.ConfigGroupChangeEvent{
		After: newVal.ReleaseFiles,
	}
	if oldVal != nil {
		event.Before = oldVal.ReleaseFiles
	}

	c.lock.RLock()
	listeners := make([]model.OnConfigGroupChange, 0, len(c.changeListeners)+len(c.cancelableListeners))
	listeners = append(listeners, c.changeListeners...)
	for _, listener := range c.cancelableListeners {
		listeners = append(listeners, listener)
	}
	c.lock.RUnlock()

	oldFileCount := 0
	if oldVal != nil {
		oldFileCount = len(oldVal.ReleaseFiles)
	}
	info := fmt.Sprintf("namespace=%s, group=%s, revision:%s, beforeFileCount=%d, afterFileCount=%d",
		c.namespace, c.group, newVal.Revision, oldFileCount, len(newVal.ReleaseFiles))
	c.logCtx.GetBaseLogger().Infof("[Config][Group] 配置分组变更监听触发. listenerCount=%d, %s", len(listeners), info)
	if c.logCtx.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		c.logCtx.GetBaseLogger().Debugf("[Config][Group] 配置分组变更详情. %s, event=%s", info, event.GetString())
	}

	for i, listener := range listeners {
		if c.logCtx.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
			c.logCtx.GetBaseLogger().Debugf("[Config][Group] 调用分组变更监听器[%d]. %s", i, info)
		}
		c.invokeChangeListener(i, listener, event, info)
	}
}

// invokeChangeListener 调用单个用户回调；回调 panic 时记录 ERROR 日志并返回，不影响后续监听器和宿主进程。
func (c *defaultConfigGroup) invokeChangeListener(index int, listener model.OnConfigGroupChange,
	event *model.ConfigGroupChangeEvent, info string) {
	returned := false
	defer func() {
		if returned {
			return
		}
		// 旧版 panic(nil) 的 recover 返回 nil，必须通过正常返回标记识别异常。
		r := recover()
		// panic 值可能包含配置明文，只记录类型，不能格式化其内容。
		c.logCtx.GetBaseLogger().Errorf("[Config][Group] 分组变更监听器[%d] panic recovered. %s, panicType=%T\nstack: %s",
			index, info, r, string(debug.Stack()))
	}()
	listener(event)
	returned = true
}
