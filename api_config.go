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

package polaris

import (
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

type configAPI struct {
	rawAPI api.ConfigFileAPI
}

// NewConfigAPI 获取配置中心 API
func NewConfigAPI() (ConfigAPI, error) {
	rawAPI, err := api.NewConfigFileAPI()
	if err != nil {
		return nil, err
	}
	return &configAPI{
		rawAPI: rawAPI,
	}, nil
}

// NewConfigAPIByConfig 通过配置对象获取配置中心 API
func NewConfigAPIByConfig(cfg config.Configuration) (ConfigAPI, error) {
	rawAPI, err := api.NewConfigFileAPIByConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &configAPI{
		rawAPI: rawAPI,
	}, nil
}

// NewConfigAPIByFile 通过配置文件获取配置中心 API
func NewConfigAPIByFile(path string) (ConfigAPI, error) {
	rawAPI, err := api.NewConfigFileAPIByFile(path)
	if err != nil {
		return nil, err
	}
	return &configAPI{
		rawAPI: rawAPI,
	}, nil
}

// NewConfigAPIByContext 通过上下文对象获取配置中心 API
func NewConfigAPIByContext(context api.SDKContext) ConfigAPI {
	rawAPI := api.NewConfigFileAPIBySDKContext(context)
	return &configAPI{
		rawAPI: rawAPI,
	}
}

// GetConfigFile 获取配置文件
func (c *configAPI) GetConfigFile(namespace, fileGroup, fileName string) (ConfigFile, error) {
	return c.rawAPI.GetConfigFile(namespace, fileGroup, fileName)
}

// CreateConfigFile 创建配置文件
func (c *configAPI) CreateConfigFile(namespace, fileGroup, fileName, content string) error {
	return c.rawAPI.CreateConfigFile(namespace, fileGroup, fileName, content)
}

// UpdateConfigFile 更新配置文件
func (c *configAPI) UpdateConfigFile(namespace, fileGroup, fileName, content string) error {
	return c.rawAPI.UpdateConfigFile(namespace, fileGroup, fileName, content)
}

// PublishConfigFile 发布配置文件
func (c *configAPI) PublishConfigFile(namespace, fileGroup, fileName string) error {
	return c.rawAPI.PublishConfigFile(namespace, fileGroup, fileName)
}

// SDKContext 获取SDK上下文
func (c *configAPI) SDKContext() api.SDKContext {
	return c.rawAPI.SDKContext()
}

type configGroupAPI struct {
	rawAPI api.ConfigGroupAPI
}

// NewConfigGroupAPI 获取配置中心 API
func NewConfigGroupAPI() (ConfigGroupAPI, error) {
	rawAPI, err := api.NewConfigGroupAPI()
	if err != nil {
		return nil, err
	}
	return &configGroupAPI{
		rawAPI: rawAPI,
	}, nil
}

// NewConfigAPIByConfig 通过配置对象获取配置中心 API
func NewConfigGroupAPIByConfig(cfg config.Configuration) (ConfigGroupAPI, error) {
	rawAPI, err := api.NewConfigGroupAPIByConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &configGroupAPI{
		rawAPI: rawAPI,
	}, nil
}

// NewConfigAPIByFile 通过配置文件获取配置中心 API
func NewConfigGroupAPIByFile(path string) (ConfigGroupAPI, error) {
	rawAPI, err := api.NewConfigGroupAPIByFile(path)
	if err != nil {
		return nil, err
	}
	return &configGroupAPI{
		rawAPI: rawAPI,
	}, nil
}

// NewConfigAPIByContext 通过上下文对象获取配置中心 API
func NewConfigGroupAPIByContext(context api.SDKContext) ConfigGroupAPI {
	rawAPI := api.NewConfigGroupAPIBySDKContext(context)
	return &configGroupAPI{
		rawAPI: rawAPI,
	}
}

func (c *configGroupAPI) GetConfigGroup(namesapce, group string) (model.ConfigFileGroup, error) {
	return c.rawAPI.GetConfigGroup(namesapce, group)
}

// SDKContext 获取SDK上下文
func (c *configGroupAPI) SDKContext() api.SDKContext {
	return c.rawAPI.SDKContext()
}
