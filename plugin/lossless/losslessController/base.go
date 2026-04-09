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

// Package losslesscontroller provides lossless controller implementation for polaris-go.
package losslesscontroller

import (
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/event"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/events"
	"github.com/polarismesh/polaris-go/pkg/sdk"
)

const (
	// PluginName 插件名称
	PluginName = "losslessController"
)

// init 注册插件
func init() {
	plugin.RegisterPlugin(&LosslessController{})
}

// LosslessController 无损上下线控制器
type LosslessController struct {
	*plugin.PluginBase
	// pluginCtx
	pluginCtx *plugin.InitContext
	engine    sdk.Engine
	// losslessInfo 无损上下线信息
	losslessInfo model.LosslessInfo
	log          log.Logger
}

// Type 插件类型
func (p *LosslessController) Type() common.Type {
	return common.TypeLossless
}

// Name 插件名称
func (p *LosslessController) Name() string {
	return PluginName
}

// Init 初始化插件
func (p *LosslessController) Init(ctx *plugin.InitContext) error {
	p.PluginBase = plugin.NewPluginBase(ctx)
	p.pluginCtx = ctx
	p.losslessInfo = model.LosslessInfo{}
	p.log = ctx.ValueCtx.GetContextLogger().GetLosslessLogger()
	p.log.Infof("[LosslessController] plugin initialized")
	return nil
}

func (p *LosslessController) reportEvent(eventInfo event.BaseEventImpl) {
	eventReporters, err := p.pluginCtx.Plugins.GetPlugins(common.TypeEventReporter)
	if err != nil {
		p.log.Errorf("[LosslessController] GetPlugins(%s) err: %+v", common.TypeEventReporter, err)
		return
	}
	for _, eventReporterPlugin := range eventReporters {
		eventReporter, ok := eventReporterPlugin.(events.EventReporter)
		if !ok {
			p.log.Errorf("[LosslessController] GetEventReportChain type assertion failed")
			return
		}
		if err = eventReporter.ReportEvent(&eventInfo); err != nil {
			p.log.Errorf("[LosslessController] report event(%s) err: %+v", model.JSONString(eventInfo), err)
			continue
		}
	}
}
