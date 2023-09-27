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

import (
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

type configFileAPI struct {
	context SDKContext
}

func newConfigFileAPI() (ConfigFileAPI, error) {
	return newConfigFileAPIByConfig(config.NewDefaultConfigurationWithDomain())
}

func newConfigFileAPIByConfig(cfg config.Configuration) (ConfigFileAPI, error) {
	context, err := InitContextByConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &configFileAPI{context}, nil
}

func newConfigFileAPIByFile(path string) (ConfigFileAPI, error) {
	context, err := InitContextByFile(path)
	if err != nil {
		return nil, err
	}
	return &configFileAPI{context}, nil
}

func newConfigFileAPIBySDKContext(context SDKContext) ConfigFileAPI {
	return &configFileAPI{
		context: context,
	}
}

// GetConfigFile 获取配置文件
func (c *configFileAPI) GetConfigFile(namespace, fileGroup, fileName string) (model.ConfigFile, error) {
	return c.context.GetEngine().SyncGetConfigFile(namespace, fileGroup, fileName)
}

// CreateConfigFile 创建配置文件
func (c *configFileAPI) CreateConfigFile(namespace, fileGroup, fileName, content string) error {
	return c.context.GetEngine().SyncCreateConfigFile(namespace, fileGroup, fileName, content)
}

// UpdateConfigFile 更新配置文件
func (c *configFileAPI) UpdateConfigFile(namespace, fileGroup, fileName, content string) error {
	return c.context.GetEngine().SyncUpdateConfigFile(namespace, fileGroup, fileName, content)
}

// PublishConfigFile 发布配置文件
func (c *configFileAPI) PublishConfigFile(namespace, fileGroup, fileName string) error {
	return c.context.GetEngine().SyncPublishConfigFile(namespace, fileGroup, fileName)
}

// SDKContext 获取SDK上下文
func (c *configFileAPI) SDKContext() SDKContext {
	return c.context
}
