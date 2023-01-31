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
	"context"
	"time"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/location"
)

var (
	ReportHandlerForLocation = "locationReport"
)

// init 注册插件
func init() {
	plugin.RegisterPlugin(&ReportHandler{})
}

type ReportHandler struct {
	*plugin.PluginBase
	globalCtx        model.ValueContext
	locationProvider location.Provider
}

// Type 插件类型
func (h *ReportHandler) Type() common.Type {
	return common.TypeReportHandler
}

// Name 插件名，一个类型下插件名唯一
func (h *ReportHandler) Name() string {
	return ReportHandlerForLocation
}

// Init 初始化插件
func (h *ReportHandler) Init(ctx *plugin.InitContext) error {
	h.PluginBase = plugin.NewPluginBase(ctx)
	h.globalCtx = ctx.ValueCtx

	providerName := ctx.Config.GetGlobal().GetLocation().GetProviders()
	if len(providerName) != 0 {
		locProvider, err := ctx.Plugins.GetPlugin(common.TypeLocationProvider, location.ProviderName)
		if err != nil {
			return err
		}
		h.locationProvider = locProvider.(location.Provider)
	}

	ctx.Plugins.RegisterEventSubscriber(common.OnContextStarted,
		common.PluginEventHandler{Callback: h.waitLocationInfo})
	return nil
}

// Destroy 销毁插件，可用于释放资源
func (h *ReportHandler) Destroy() error {
	return nil
}

func (h *ReportHandler) InitLocal(client *namingpb.Client) {
	location := client.GetLocation()
	h.updateLocation(&model.Location{
		Region: location.GetRegion().GetValue(),
		Zone:   location.GetZone().GetValue(),
		Campus: location.GetCampus().GetValue(),
	}, nil)
}

// HandleRequest Handling Request body for Report
func (h *ReportHandler) HandleRequest(req *model.ReportClientRequest) {

}

// HandleResponse Handling Report Responsive Body
func (h *ReportHandler) HandleResponse(resp *model.ReportClientResponse, err error) {
	if err != nil {
		h.updateLocation(nil, err.(model.SDKError))
		return
	}

	loc := &model.Location{
		Region: resp.Region,
		Zone:   resp.Zone,
		Campus: resp.Campus,
	}
	if h.locationProvider != nil {
		_loc, err := h.locationProvider.GetLocation()
		if err != nil {
			log.GetBaseLogger().Errorf("location provider get location fail, error:%v", err)
		} else {
			loc = _loc
		}

		resp.Region = loc.Region
		resp.Zone = loc.Zone
		resp.Campus = loc.Campus
	}
	h.updateLocation(loc, nil)
}

// updateLocation 更新区域属性
func (h *ReportHandler) updateLocation(location *model.Location, lastErr model.SDKError) {
	if nil != location {
		// 已获取到客户端的地域信息，更新到全局上下文
		log.GetBaseLogger().Infof("current client area info is {Region:%s, Zone:%s, Campus:%s}",
			location.Region, location.Zone, location.Campus)
	}
	if h.globalCtx.SetCurrentLocation(location, lastErr) {
		log.GetBaseLogger().Infof("client area info is ready")
	}
}

// 等待地域信息就绪
func (h *ReportHandler) waitLocationInfo(event *common.PluginEvent) error {
	if h.locationProvider == nil {
		return nil
	}

	// 这里做一个等待，等待地理位置信息获取成功，如果超过一定时间还没有获取到，则认为是获取不到地理位置信息，自动跳过忽略
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ready := h.globalCtx.WaitLocationInfo(ctx, model.LocationReady)
	loc := h.globalCtx.GetCurrentLocation().GetLocation()
	if !ready || loc.IsEmpty() {
		log.GetBaseLogger().Warnf("[ReportHandler][Location] auto inject location info not ready")
	}

	return nil
}
