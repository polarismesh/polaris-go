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

package location

import (
	"sort"

	"github.com/pkg/errors"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/plugin/location/local"
	"github.com/polarismesh/polaris-go/plugin/location/remotehttp"
	"github.com/polarismesh/polaris-go/plugin/location/remoteservice"
)

// LocationPlugin location provider plugin
type LocationPlugin interface {
	// Init init plugin
	Init(ctx *plugin.InitContext) error
	// GetLocation get location
	GetLocation() (*model.Location, error)
	// Name plugin name
	Name() string
}

const (
	_ = iota
	PriorityLocal
	PriorityRemoteHttp
	PriorityRemoteService
)

type ProviderType = string

const (
	Local         ProviderType = "local"
	RemoteHttp    ProviderType = "remoteHttp"
	RemoteService ProviderType = "remoteService"
)

// 定义类型的优先级
var priority = map[ProviderType]int{
	Local:         PriorityLocal,
	RemoteHttp:    PriorityRemoteHttp,
	RemoteService: PriorityRemoteService,
}

// GetPriority 获取Provider的优先级
func GetPriority(typ ProviderType) int {
	return priority[typ]
}

const (
	ProviderName string = "chain"
)

// init 注册插件
func init() {
	plugin.RegisterPlugin(&Provider{})
}

// Provider 从环境变量获取地域信息
type Provider struct {
	*plugin.PluginBase
	pluginChains []LocationPlugin
}

// Init 初始化插件
func (p *Provider) Init(ctx *plugin.InitContext) error {
	p.PluginBase = plugin.NewPluginBase(ctx)
	providers := ctx.Config.GetGlobal().GetLocation().GetProviders()
	p.pluginChains = make([]LocationPlugin, 0, len(providers))
	for _, provider := range providers {
		switch provider.Type {
		case Local:
			localProvider, err := local.New(ctx)
			if err != nil {
				log.GetBaseLogger().Errorf("create local location plugin error: %v", err)
				return err
			}
			p.pluginChains = append(p.pluginChains, localProvider)
		case RemoteHttp:
			remoteHttpProvider, err := remotehttp.New(ctx)
			if err != nil {
				log.GetBaseLogger().Errorf("create remoteHttp location plugin error: %v", err)
				return err
			}
			p.pluginChains = append(p.pluginChains, remoteHttpProvider)
		case RemoteService:
			remoteServiceProvider, err := remoteservice.New(ctx)
			if err != nil {
				log.GetBaseLogger().Errorf("create remoteService location plugin error: %v", err)
				return err
			}
			p.pluginChains = append(p.pluginChains, remoteServiceProvider)
		default:
			log.GetBaseLogger().Errorf("unknown location provider type: %s", provider.Type)
			return errors.New("unknown location provider type")
		}
	}
	// 根据优先级对插件进行排序
	sort.Slice(p.pluginChains, func(i, j int) bool {
		return priority[p.pluginChains[i].Name()] < priority[p.pluginChains[j].Name()]
	})

	activeProviders := []string{}
	for i := range p.pluginChains {
		activeProviders = append(activeProviders, p.pluginChains[i].Name())
	}
	log.GetBaseLogger().Infof("active location provider: %+v", activeProviders)
	return nil
}

// Destroy 销毁插件，可用于释放资源
func (p *Provider) Destroy() error {
	p.pluginChains = []LocationPlugin{}
	return p.PluginBase.Destroy()
}

// Type 插件类型
func (p *Provider) Type() common.Type {
	return common.TypeLocationProvider
}

// Name 插件名称
func (p *Provider) Name() string {
	return ProviderName
}

// GetLocation 获取地理位置信息
func (p *Provider) GetLocation() (*model.Location, error) {
	location := &model.Location{}

	for _, item := range p.pluginChains {
		tmp, err := item.GetLocation()
		if err != nil {
			log.GetBaseLogger().Errorf("get location from plugin %s error: %v", item.Name(), err)
			continue
		}
		location = tmp
	}
	return location, nil
}
