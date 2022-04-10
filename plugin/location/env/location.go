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

package env

import (
	"os"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

const (
	locationProviderEnv string = "env"

	envKeyRegion string = "POLARIS_INSTANCE_REGION"
	envKeyZone   string = "POLARIS_INSTANCE_ZONE"
	envKeyCampus string = "POLARIS_INSTANCE_CAMPUS"
)

// init 注册插件
func init() {
	plugin.RegisterPlugin(&LocationProvider{})
}

// LocationProvider 从环境变量获取地域信息
type LocationProvider struct {
	*plugin.PluginBase
	locCache *model.Location
}

// Init 初始化插件
func (p *LocationProvider) Init(ctx *plugin.InitContext) error {
	p.PluginBase = plugin.NewPluginBase(ctx)
	return nil
}

// Destroy 销毁插件，可用于释放资源
func (p *LocationProvider) Destroy() error {
	return nil
}

// Type 插件类型
func (p *LocationProvider) Type() common.Type {
	return common.TypeLocationProvider
}

// Name 插件名称
func (p *LocationProvider) Name() string {
	return locationProviderEnv
}

// GetLocation 获取地理位置信息
func (p *LocationProvider) GetLocation() (*model.Location, error) {
	if p.locCache == nil {
		p.locCache = &model.Location{
			Region: os.Getenv(envKeyRegion),
			Zone:   os.Getenv(envKeyZone),
			Campus: os.Getenv(envKeyCampus),
		}
	}

	return p.locCache, nil
}
