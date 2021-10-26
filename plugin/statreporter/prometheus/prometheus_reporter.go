// Tencent is pleased to support the open source community by making polaris-go available.
//
// Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
//
// Licensed under the BSD 3-Clause License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// https://opensource.org/licenses/BSD-3-Clause
//
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissionsr and limitations under the License.
//
//@Author: springliao
//@Description:
//@Time: 2021/10/19 12:48

package prometheus

import (
	"context"
	"sync/atomic"

	sysconfig "github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/network"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/localregistry"
)

const (
	opReportStat = "ReportStat"
)

//Stat2FileReporter 打印统计日志到本地文件中
type PrometheusReporter struct {
	*plugin.PluginBase
	*common.RunContext
	cfg               *Config
	hasReportConfig   uint32
	connectionManager network.ConnectionManager
	// handler
	handler *PrometheusHandler
	//
	cancelFunc context.CancelFunc
	//全局上下文
	globalCtx model.ValueContext
	//全局配置marshal的字符串
	globalCfgStr atomic.Value
	//sdk加载的插件
	sdkPlugins string
	//插件工厂
	plugins plugin.Supplier
	//本地缓存插件
	registry localregistry.LocalRegistry
}

//Type 插件类型
func (s *PrometheusReporter) Type() common.Type {
	return common.TypeStatReporter
}

//Name 插件名，一个类型下插件名唯一
func (s *PrometheusReporter) Name() string {
	return "Prometheus"
}

//Init 初始化插件
func (s *PrometheusReporter) Init(ctx *plugin.InitContext) error {
	s.RunContext = common.NewRunContext()
	s.globalCtx = ctx.ValueCtx
	s.plugins = ctx.Plugins
	s.PluginBase = plugin.NewPluginBase(ctx)
	cfgValue := ctx.Config.GetGlobal().GetStatReporter().GetPluginConfig(s.Name())
	if cfgValue != nil {
		s.cfg = cfgValue.(*Config)
	}
	s.connectionManager = ctx.ConnManager
	s.initPrometheusHandler(ctx)
	return nil
}

func (s *PrometheusReporter) initPrometheusHandler(ctx *plugin.InitContext) {
	monitorSvrCfg := ctx.Config.GetGlobal().GetSystem().GetMonitorCluster()
	engine := ctx.ValueCtx.GetEngine()

	provider := NewProvider(monitorSvrCfg, engine)
	handler := NewHandler(engine.GetContext().GetClientId(), provider, *s.cfg)

	s.handler = handler
}

//start 启动定时协程
func (g *PrometheusReporter) Start() error {
	return nil
}

// enable
func (g *PrometheusReporter) IsEnable(cfg sysconfig.Configuration) bool {
	if cfg.GetGlobal().GetSystem().GetMode() == model.ModeWithAgent {
		return false
	} else {
		for _, name := range cfg.GetGlobal().GetStatReporter().GetChain() {
			if name == g.Name() {
				return true
			}
		}
	}
	return false
}

// Destroy
func (g *PrometheusReporter) Destroy() error {
	err := g.PluginBase.Destroy()
	if err != nil {
		return err
	}
	err = g.RunContext.Destroy()
	if err != nil {
		return err
	}
	return nil
}

// ReportStat
func (s *PrometheusReporter) ReportStat(statInfo *model.StatInfo) error {
	err := s.handler.handleStat(statInfo)
	if err != nil {
		return err
	}
	return nil
}

//init 注册插件
func init() {
	plugin.RegisterConfigurablePlugin(&PrometheusReporter{}, &Config{})
	//plugin.RegisterPlugin(&PrometheusReporter{})
}
