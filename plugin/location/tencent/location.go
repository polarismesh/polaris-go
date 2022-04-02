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

package tencent

import (
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

const (
	locationProviderTencent string = "tencent"
	regionKey               string = "region"
	zoneKey                 string = "zone"
	qCloudApi               string = "http://metadata.tencentyun.com/latest/meta-data/placement/%s"
)

// LocationProvider 腾讯云
type LocationProvider struct {
	*plugin.PluginBase
	locCache *model.Location
}

// Type 插件类型
func (p *LocationProvider) Type() common.Type {
	return common.TypeLocationProvider
}

// Name 插件名称
func (p *LocationProvider) Name() string {
	return locationProviderTencent
}

// GetLocation 获取地理位置信息
func (p *LocationProvider) GetLocation() (*model.Location, error) {
	if p.locCache == nil {
		log.GetBaseLogger().Infof("start to get location metadata in cloud env")

		p.locCache = &model.Location{
			Region: "qcloud",
		}

		if region, err := sendRequest(regionKey); err == nil {
			p.locCache.Zone = region
		} else {
			log.GetBaseLogger().Infof("get region info fail : %s, but not affect", err.Error())
		}
		if zone, err := sendRequest(zoneKey); err == nil {
			p.locCache.Campus = zone
		} else {
			log.GetBaseLogger().Infof("get zone info fail : %s, but not affect", err.Error())
		}

		log.GetBaseLogger().Infof("get location info from cloud env : %s", p.locCache.String())
	}

	return p.locCache, nil
}

func sendRequest(typ string) (string, error) {
	reqApi := fmt.Sprintf(qCloudApi, typ)
	resp, err := http.Get(reqApi)
	if err != nil {
		return "", err
	}

	defer resp.Body.Close()

	val, err := ioutil.ReadAll(resp.Body)

	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("curl %s failed", reqApi)
	}

	return string(val), nil
}
