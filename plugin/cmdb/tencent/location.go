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
	locationProviderTencent string = "qcloud"
	regionKey               string = "region"
	zoneKey                 string = "zone"
	qCloudApi               string = "http://metadata.tencentyun.com/latest/meta-data/placement/%s"
)

// init 注册插件
func init() {
	plugin.RegisterPlugin(&LocationProvider{})
}

// LocationProvider 腾讯云
type LocationProvider struct {
	*plugin.PluginBase
	locCache *model.Location
}

// Init 初始化插件
func (p *LocationProvider) Init(ctx *plugin.InitContext) error {
	log.GetBaseLogger().Infof("start use qcloud location provider")
	p.PluginBase = plugin.NewPluginBase(ctx)

	if ctx.Config.GetGlobal().GetLocation().GetProvider() == p.Name() {
		loc, err := getQCloudLocation()
		if err != nil {
			return err
		}
		p.locCache = loc
	}
	return nil
}

// Type 插件类型
func (p *LocationProvider) Type() common.Type {
	return common.TypeLocationProvider
}

// Name 插件名称
func (p *LocationProvider) Name() string {
	return locationProviderTencent
}

// Destroy 销毁插件，可用于释放资源
func (p *LocationProvider) Destroy() error {
	return p.PluginBase.Destroy()
}

// GetLocation 获取地理位置信息
func (p *LocationProvider) GetLocation() (*model.Location, error) {
	if p.locCache == nil {
		loc, err := p.GetLocation()
		if err != nil {
			return nil, err
		}
		p.locCache = loc
	}

	return p.locCache, nil
}

func getQCloudLocation() (*model.Location, error) {
	log.GetBaseLogger().Infof("start to get location metadata in qcloud")

	locCache := &model.Location{
		Region: "qcloud",
	}

	if region, err := sendRequest(regionKey); err == nil {
		locCache.Zone = region
	} else {
		log.GetBaseLogger().Infof("get region info fail : %s, but not affect", err.Error())
	}
	if zone, err := sendRequest(zoneKey); err == nil {
		locCache.Campus = zone
	} else {
		log.GetBaseLogger().Infof("get zone info fail : %s, but not affect", err.Error())
	}

	log.GetBaseLogger().Infof("get location info from cloud env : %s", locCache.String())
	return locCache, nil
}

func sendRequest(typ string) (string, error) {
	var (
		reqAPI    = fmt.Sprintf(qCloudApi, typ)
		resp, err = http.Get(reqAPI)
		val       []byte
	)

	if err != nil {
		return "", err
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	if val, err = ioutil.ReadAll(resp.Body); err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("curl %s failed", reqAPI)
	}

	return string(val), nil
}
