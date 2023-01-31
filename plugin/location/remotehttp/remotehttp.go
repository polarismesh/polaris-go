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

package remotehttp

import (
	location2 "github.com/polarismesh/polaris-go/plugin/location"
	"io"
	"net/http"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
)

const (
	locationProviderName string = "remoteHttp"
)

func New(ctx *plugin.InitContext) (location2.LocationPlugin, error) {
	impl := &LocationProviderImpl{}
	return impl, impl.Init(ctx)
}

// LocationProviderImpl 通过http服务获取地理位置信息
type LocationProviderImpl struct {
	address *model.Location
}

// Init 初始化插件
func (p *LocationProviderImpl) Init(ctx *plugin.InitContext) error {
	log.GetBaseLogger().Infof("start remoteHttp location provider")

	provider := ctx.Config.GetGlobal().GetLocation().GetProvider(locationProviderName)

	p.address = &model.Location{
		Region: provider.Region,
		Zone:   provider.Zone,
		Campus: provider.Campus,
	}

	return nil
}

// Name 插件名称
func (p *LocationProviderImpl) Name() string {
	return locationProviderName
}

// GetLocation 获取地理位置信息
func (p *LocationProviderImpl) GetLocation() (*model.Location, error) {

	region := getResponse(p.address.Region)
	campus := getResponse(p.address.Campus)
	zone := getResponse(p.address.Zone)

	if region == "" && campus == "" && zone == "" {
		log.GetBaseLogger().Errorf("get location from remote http error: %v", "all location is empty")
	}

	log.GetBaseLogger().Infof("get location from remote http: region=%s, campus=%s, zone=%s", region, campus, zone)
	loc := &model.Location{
		Region: region,
		Campus: campus,
		Zone:   zone,
	}

	return loc, nil
}

func getResponse(url string) string {
	res, err := http.Get(url)
	if err != nil {
		log.GetBaseLogger().Errorf("get location from remote http error: %v", err)
		return ""
	}
	defer res.Body.Close()
	resBody, err := io.ReadAll(res.Body)
	if err != nil {
		log.GetBaseLogger().Errorf("read location from remote http error: %v", err)
		return ""
	}

	return string(resBody)
}

// GetPriority 获取优先级
func (p *LocationProviderImpl) GetPriority() int {
	return location2.PriorityRemoteHttp
}
