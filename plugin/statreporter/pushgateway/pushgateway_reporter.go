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

package pushgateway

import (
	"time"

	"github.com/prometheus/client_golang/prometheus/push"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/statreporter"
)

const (
	// PluginName is the name of the plugin.
	PluginName      = "pushgateway"
	_defaultJobName = "polaris-client"
)

var _ statreporter.StatReporter = (*PushgatewayReporter)(nil)

// init 注册插件.
func init() {
	plugin.RegisterPlugin(&PushgatewayReporter{})
}

// PushgatewayReporter is a prometheus reporter.
type PushgatewayReporter struct {
	*plugin.PluginBase
	*common.RunContext
	// 本插件的配置
	cfg *Config
	// 全局上下文
	globalCtx model.ValueContext
	// sdk加载的插件
	sdkPlugins string
	// 插件工厂
	plugins plugin.Supplier
	// prometheus的metrics注册
	handler *PushgatewayHandler
	ticker  *time.Ticker
}

// Type 插件类型.
func (s *PushgatewayReporter) Type() common.Type {
	return common.TypeStatReporter
}

// Name 插件名，一个类型下插件名唯一.
func (s *PushgatewayReporter) Name() string {
	return PluginName
}

// Init 初始化插件.
func (s *PushgatewayReporter) Init(ctx *plugin.InitContext) error {
	s.RunContext = common.NewRunContext()
	s.globalCtx = ctx.ValueCtx
	s.plugins = ctx.Plugins
	s.PluginBase = plugin.NewPluginBase(ctx)
	handler, err := newHandler(ctx)
	if err != nil {
		return err
	}
	s.handler = handler
	cfgValue := ctx.Config.GetGlobal().GetStatReporter().GetPluginConfig(PluginName)
	s.cfg = cfgValue.(*Config)
	if cfgValue != nil {
		s.runPushMetrics(cfgValue.(*Config).PushInterval)
	}
	return nil
}

// ReportStat 报告统计数据.
func (s *PushgatewayReporter) ReportStat(metricType model.MetricType, metricsVal model.InstanceGauge) error {
	return s.handler.ReportStat(metricType, metricsVal)
}

// Info 插件信息.
func (s *PushgatewayReporter) Info() model.StatInfo {
	return model.StatInfo{}
}

// Destroy .销毁插件.
func (s *PushgatewayReporter) Destroy() error {
	if err := s.PluginBase.Destroy(); err != nil {
		return err
	}
	if err := s.RunContext.Destroy(); err != nil {
		return err
	}
	if s.ticker != nil {
		s.ticker.Stop()
	}

	if s.handler != nil {
		if err := s.handler.Close(); err != nil {
			return err
		}
	}

	return nil
}

// runPushMetrics 指标推送
func (s *PushgatewayReporter) runPushMetrics(interval time.Duration) {
	s.ticker = time.NewTicker(interval)
	go func() {
		for range s.ticker.C {
			if err := push.
				New(s.cfg.Address, _defaultJobName).
				Gatherer(s.handler.registry).
				Push(); err != nil {
				log.GetBaseLogger().Errorf("push metrics to pushgateway fail: %s", err.Error())
			}
		}
	}()
}
