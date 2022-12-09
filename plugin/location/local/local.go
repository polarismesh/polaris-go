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
	"bytes"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	location2 "github.com/polarismesh/polaris-go/plugin/location"
	"os"
	"regexp"
)

const (
	locationProviderLocal string = "local"
)

// LocationProviderImpl 从环境变量获取地域信息
type LocationProviderImpl struct {
	locCache *model.Location
}

func New(ctx *plugin.InitContext) (location2.LocationPlugin, error) {
	impl := &LocationProviderImpl{}
	return impl, impl.Init(ctx)
}

// Init 初始化插件
func (p *LocationProviderImpl) Init(ctx *plugin.InitContext) error {
	log.GetBaseLogger().Infof("start use env location provider")

	provider := ctx.Config.GetGlobal().GetLocation().GetProvider(locationProviderLocal)

	var sdkErr model.SDKError
	p.locCache = &model.Location{
		Region: getValue(provider.Region),
		Zone:   getValue(provider.Zone),
		Campus: getValue(provider.Campus),
	}

	return sdkErr
}

// Name 插件名称
func (p *LocationProviderImpl) Name() string {
	return locationProviderLocal
}

// GetLocation 获取地理位置信息
func (p *LocationProviderImpl) GetLocation() (*model.Location, error) {
	return p.locCache, nil
}

// GetPriority 获取优先级
func (p *LocationProviderImpl) GetPriority() int {
	return location2.PriorityLocal
}

func getValue(str string) string {
	r, _ := regexp.Compile("\\${(.+)}")

	data := []byte(str)
	for _, match := range r.FindAllSubmatch(data, -1) {
		key := os.Getenv(string(match[1]))
		if key != "" {
			data = bytes.Replace(data, match[0], []byte(key), 1)
		}
	}
	return string(data)
}
