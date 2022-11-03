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
	"github.com/polarismesh/polaris-go/pkg/flow/configuration/remote"
	"github.com/polarismesh/polaris-go/pkg/flow/configuration/util"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/configconnector"
)

type configFileFactory struct {
	connector     configconnector.ConfigConnector
	configuration config.Configuration
}

func newConfigFileFactory(connector configconnector.ConfigConnector, configuration config.Configuration) *configFileFactory {
	return &configFileFactory{
		connector:     connector,
		configuration: configuration,
	}
}

func (c *configFileFactory) createConfigFile(configFileMetadata model.ConfigFileMetadata) (model.ConfigFile, error) {
	configFileRemoteRepo, err := remote.NewConfigFileRepo(configFileMetadata, c.connector, c.configuration)
	if err != nil {
		return nil, err
	}

	return newDefaultConfigFile(configFileMetadata, configFileRemoteRepo), err
}

type configFileFactoryManager struct {
	factories     *sync.Map
	connector     configconnector.ConfigConnector
	configuration config.Configuration
}

func newConfigFileFactoryManager(connector configconnector.ConfigConnector, configuration config.Configuration) *configFileFactoryManager {
	return &configFileFactoryManager{
		factories:     new(sync.Map),
		connector:     connector,
		configuration: configuration,
	}
}

func (c *configFileFactoryManager) getFactory(configFileMetadata model.ConfigFileMetadata) *configFileFactory {
	cacheKey := util.GenConfigFileCacheKeyByMetadata(configFileMetadata)

	factoryObj, ok := c.factories.Load(cacheKey)
	if ok {
		return factoryObj.(*configFileFactory)
	}

	factory := newConfigFileFactory(c.connector, c.configuration)

	c.factories.Store(cacheKey, factory)

	return factory
}
