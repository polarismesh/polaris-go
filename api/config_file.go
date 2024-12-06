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

package api

import "github.com/polarismesh/polaris-go/pkg/model"

type GetConfigFileRequest struct {
	*model.GetConfigFileRequest
}

type GetConfigGroupRequest struct {
	*model.GetConfigGroupRequest
}

// ConfigFileAPI 配置文件的 API
type ConfigFileAPI interface {
	SDKOwner
	// Deprecated: please use FetchConfigFile
	// GetConfigFile 获取配置文件
	GetConfigFile(namespace, fileGroup, fileName string) (model.ConfigFile, error)
	// FetchConfigFile 获取配置文件
	FetchConfigFile(*GetConfigFileRequest) (model.ConfigFile, error)
	// CreateConfigFile 创建配置文件
	CreateConfigFile(namespace, fileGroup, fileName, content string) error
	// UpdateConfigFile 更新配置文件
	UpdateConfigFile(namespace, fileGroup, fileName, content string) error
	// PublishConfigFile 发布配置文件
	PublishConfigFile(namespace, fileGroup, fileName string) error
}

type ConfigGroupAPI interface {
	SDKOwner
	// GetConfigGroup 获取配置分组
	GetConfigGroup(namespace, group string) (model.ConfigFileGroup, error)
	// FetchConfigGroup 获取配置文件
	FetchConfigGroup(*GetConfigGroupRequest) (model.ConfigFileGroup, error)
}

var (
	// NewConfigFileAPIBySDKContext 通过 SDKContext 创建 ConfigFileAPI
	NewConfigFileAPIBySDKContext = newConfigFileAPIBySDKContext
	// NewConfigFileAPI 通过 polaris.yaml 创建 ConfigFileAPI
	NewConfigFileAPI = newConfigFileAPI
	// NewConfigFileAPIByConfig 通过 Configuration 创建 ConfigFileAPI
	NewConfigFileAPIByConfig = newConfigFileAPIByConfig
	// NewConfigFileAPIByFile 通过配置文件创建 ConfigFileAPI
	NewConfigFileAPIByFile = newConfigFileAPIByFile

	// NewConfigGroupAPIBySDKContext 通过 SDKContext 创建 ConfigGroupAPI
	NewConfigGroupAPIBySDKContext = newConfigGroupAPIBySDKContext
	// NewConfigGroupAPI 通过 polaris.yaml 创建 ConfigGroupAPI
	NewConfigGroupAPI = newConfigGroupAPI
	// NewConfigGroupAPIByConfig 通过 Configuration 创建 ConfigGroupAPI
	NewConfigGroupAPIByConfig = newConfigGroupAPIByConfig
	// NewConfigGroupAPIByFile 通过配置文件创建 ConfigGroupAPI
	NewConfigGroupAPIByFile = newConfigGroupAPIByFile
)
