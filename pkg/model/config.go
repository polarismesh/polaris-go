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

package model

import "time"

// ChangeType 配置文件变更类型
type ChangeType int

const (
	// Modified 修改类型
	Modified ChangeType = iota
	// Deleted 删除类型
	Deleted
	// Added 新增类型
	Added
	// NotChanged 没有变更
	NotChanged
)

type (
	// OnConfigFileChange 配置文件变更回调监听器
	OnConfigFileChange func(event ConfigFileChangeEvent)
	// OnConfigGroupChange .
	OnConfigGroupChange func(event *ConfigGroupChangeEvent)
)

// ConfigFileChangeEvent 配置文件变更事件
type ConfigFileChangeEvent struct {
	ConfigFileMetadata ConfigFileMetadata
	// OldValue 变更之前的值
	OldValue string
	// NewValue 变更之后的值
	NewValue string
	// ChangeType 变更类型
	ChangeType ChangeType
}

type SimpleConfigFile struct {
	Namespace   string
	FileGroup   string
	FileName    string
	Version     uint64
	Md5         string
	ReleaseTime time.Time
}

// ConfigGroupChangeEvent 配置文件变更事件
type ConfigGroupChangeEvent struct {
	Before []*SimpleConfigFile
	After  []*SimpleConfigFile
}

// ConfigFileMetadata 配置文件元信息
type ConfigFileMetadata interface {
	// GetNamespace 获取 Namespace 信息
	GetNamespace() string
	// GetFileGroup 获取配置文件组
	GetFileGroup() string
	// GetFileName 获取配置文件值
	GetFileName() string
}

// ConfigFile 文本类型配置文件对象
type ConfigFile interface {
	ConfigFileMetadata
	// GetContent 获取配置文件内容
	GetContent() string
	// HasContent 是否有配置内容
	HasContent() bool
	// AddChangeListenerWithChannel 增加配置文件变更监听器
	AddChangeListenerWithChannel() <-chan ConfigFileChangeEvent
	// AddChangeListener 增加配置文件变更监听器
	AddChangeListener(cb OnConfigFileChange)
}

// DefaultConfigFileMetadata 默认 ConfigFileMetadata 实现类
type DefaultConfigFileMetadata struct {
	Namespace string
	FileGroup string
	FileName  string
}

// GetNamespace 获取 Namespace
func (m *DefaultConfigFileMetadata) GetNamespace() string {
	return m.Namespace
}

// GetFileGroup 获取配置文件组
func (m *DefaultConfigFileMetadata) GetFileGroup() string {
	return m.FileGroup
}

// GetFileName 获取配置文件值
func (m *DefaultConfigFileMetadata) GetFileName() string {
	return m.FileName
}

type ConfigFileGroup interface {
	// GetFiles
	GetFiles() ([]*SimpleConfigFile, string, bool)
	// AddChangeListener 增加配置文件变更监听器
	AddChangeListener(cb OnConfigGroupChange)
}
