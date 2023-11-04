/*
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
 *  under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
 * CONDITIONS OF ANY KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations under the License.
 *
 */

package configconnector

import (
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

// ConfigConnector interface of config connector plugin
type ConfigConnector interface {
	plugin.Plugin
	// GetConfigFile Get config file
	GetConfigFile(configFile *ConfigFile) (*ConfigFileResponse, error)
	// WatchConfigFiles Watch config files
	WatchConfigFiles(configFileList []*ConfigFile) (*ConfigFileResponse, error)
	// CreateConfigFile Create config file
	CreateConfigFile(configFile *ConfigFile) (*ConfigFileResponse, error)
	// UpdateConfigFile Update config file
	UpdateConfigFile(configFile *ConfigFile) (*ConfigFileResponse, error)
	// PublishConfigFile Publish config file
	PublishConfigFile(configFile *ConfigFile) (*ConfigFileResponse, error)
	// GetConfigGroup query config_group release file list
	GetConfigGroup(req *ConfigGroup) (*ConfigGroupResponse, error)
}

// init
func init() {
	plugin.RegisterPluginInterface(common.TypeConfigConnector, new(ConfigConnector))
}
