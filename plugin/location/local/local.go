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

package local

import (
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
)

const (
	locationProviderLocal string = "local"
)

// LocationProviderImpl 从环境变量获取地域信息
type LocationProviderImpl struct {
	ctx      *plugin.InitContext
	locCache *model.Location
}

func New(ctx *plugin.InitContext) (*LocationProviderImpl, error) {
	impl := &LocationProviderImpl{}
	return impl, impl.Init(ctx)
}

// Init 初始化插件
func (p *LocationProviderImpl) Init(ctx *plugin.InitContext) error {
	p.ctx = ctx
	return nil
}

// Name 插件名称
func (p *LocationProviderImpl) Name() string {
	return locationProviderLocal
}

// GetLocation 获取地理位置信息
func (p *LocationProviderImpl) GetLocation() (*model.Location, error) {
	if p.locCache != nil {
		return p.locCache, nil
	}

	log.GetBaseLogger().Infof("start use env location provider")

	provider := p.ctx.Config.GetGlobal().GetLocation().GetProvider(locationProviderLocal)
	options := provider.GetOptions()

	region, _ := options["region"].(string)
	zone, _ := options["zone"].(string)
	campus, _ := options["campus"].(string)

	p.locCache = &model.Location{
		Region: region,
		Zone:   zone,
		Campus: campus,
	}
	return p.locCache, nil
}
