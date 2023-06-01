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
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/configconnector"
)

// Proxy is a config connector proxy
type Proxy struct {
	ConfigFilter
	engine model.Engine
}

// SetRealPlugin set real plugin
func (p *Proxy) SetRealPlugin(pg plugin.Plugin, engine model.Engine) {
	p.ConfigFilter = pg.(ConfigFilter)
	p.engine = engine
}

// DoFilter do filter
func (p *Proxy) DoFilter(configFile *configconnector.ConfigFile, next ConfigFileHandleFunc) ConfigFileHandleFunc {
	return p.ConfigFilter.DoFilter(configFile, next)
}

func init() {
	plugin.RegisterPluginProxy(common.TypeConfigFilter, &Proxy{})
}
