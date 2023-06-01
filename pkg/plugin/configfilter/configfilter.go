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

package configfilter

import (
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/configconnector"
)

// ConfigFileHandleFunc 配置文件处理函数
type ConfigFileHandleFunc func(configFile *configconnector.ConfigFile) (*configconnector.ConfigFileResponse, error)

// Chain 配置过滤链
type Chain []ConfigFilter

// Execute 执行链中的过滤器
func (c Chain) Execute(configFile *configconnector.ConfigFile, next ConfigFileHandleFunc) (*configconnector.ConfigFileResponse, error) {
	log.GetBaseLogger().Infof("[Config] chain execute, chain length:%d\n", len(c))
	for i := len(c) - 1; i >= 0; i-- {
		curFunc := next
		curFilter := c[i]
		next = curFilter.DoFilter(configFile, curFunc)
	}
	return next(configFile)
}

// ConfigFilter 配置过滤器接口
type ConfigFilter interface {
	plugin.Plugin
	DoFilter(configFile *configconnector.ConfigFile, next ConfigFileHandleFunc) ConfigFileHandleFunc
}

func init() {
	plugin.RegisterPluginInterface(common.TypeConfigFilter, new(ConfigFilter))
}
