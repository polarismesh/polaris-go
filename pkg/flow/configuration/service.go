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

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/flow/configuration/remote"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/configconnector"
)

// ConfigFileService 配置中心核心服务门面类
type ConfigFileService struct {
	connector         configconnector.ConfigConnector
	configuration     config.Configuration
	configFileManager *configFileManager
}

var configFileService *ConfigFileService

var once = new(sync.Once)

// NewConfigFileService 创建配置中心服务
func NewConfigFileService(connector configconnector.ConfigConnector,
	configuration config.Configuration) *ConfigFileService {

	once.Do(func() {
		configFileService = &ConfigFileService{
			connector:         connector,
			configuration:     configuration,
			configFileManager: newConfigFileManager(connector, configuration),
		}

		remote.InitLongPollingService(connector)
	})
	return configFileService
}

// GetConfigFile 获取配置文件
func (c *ConfigFileService) GetConfigFile(namespace, fileGroup, fileName string) (model.ConfigFile, error) {
	return c.configFileManager.getConfigFile(&model.DefaultConfigFileMetadata{
		Namespace: namespace,
		FileGroup: fileGroup,
		FileName:  fileName,
	})
}

// Destroy 销毁服务
func (c *ConfigFileService) Destroy() {
	remote.StopLongPollingTask()
	remote.StopCheckVersionTask()
}
