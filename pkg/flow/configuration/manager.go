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

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/flow/configuration/util"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/configconnector"
)

type configFileManager struct {
	configFileCache          *sync.Map
	lock                     *sync.Mutex
	connector                configconnector.ConfigConnector
	configuration            config.Configuration
	configFileFactoryManager *configFileFactoryManager
}

func newConfigFileManager(connector configconnector.ConfigConnector,
	configuration config.Configuration) *configFileManager {
	return &configFileManager{
		configFileCache:          new(sync.Map),
		lock:                     new(sync.Mutex),
		connector:                connector,
		configuration:            configuration,
		configFileFactoryManager: newConfigFileFactoryManager(connector, configuration),
	}
}

func (c *configFileManager) getConfigFile(configFileMetadata model.ConfigFileMetadata) (model.ConfigFile, error) {
	cacheKey := util.GenConfigFileCacheKeyByMetadata(configFileMetadata)

	configFileObj, ok := c.configFileCache.Load(cacheKey)

	if ok {
		return configFileObj.(model.ConfigFile), nil
	}

	c.lock.Lock()
	defer c.lock.Unlock()

	// double check
	configFileObj, ok = c.configFileCache.Load(cacheKey)

	if ok {
		return configFileObj.(model.ConfigFile), nil
	}

	factory := c.configFileFactoryManager.getFactory(configFileMetadata)

	configFile, err := factory.createConfigFile(configFileMetadata)
	if err != nil {
		return nil, err
	}

	c.configFileCache.Store(cacheKey, configFile)

	return configFile, nil
}
