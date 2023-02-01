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

package prometheus

import (
	"net/http"
	"sync"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	statreporter "github.com/polarismesh/polaris-go/pkg/plugin/metrics"
)

const (
	// PluginName is the name of the plugin.
	PluginName string = "prometheus"
)

var _ statreporter.StatReporter = (*PrometheusReporter)(nil)

// init 注册插件.
func init() {
	plugin.RegisterPlugin(&PrometheusReporter{})
}

// PrometheusReporter is a prometheus reporter.
type PrometheusReporter struct {
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
	handler *PrometheusHandler
}

// Type 插件类型.
func (s *PrometheusReporter) Type() common.Type {
	return common.TypeStatReporter
}

// Name 插件名，一个类型下插件名唯一.
func (s *PrometheusReporter) Name() string {
	return PluginName
}

// Init 初始化插件.
func (s *PrometheusReporter) Init(ctx *plugin.InitContext) error {
	s.RunContext = common.NewRunContext()
	s.globalCtx = ctx.ValueCtx
	s.plugins = ctx.Plugins
	s.PluginBase = plugin.NewPluginBase(ctx)
	handler, err := newHandler(ctx)
	if err != nil {
		return err
	}
	s.handler = handler
	return nil
}

// ReportStat 报告统计数据.
func (s *PrometheusReporter) ReportStat(metricType model.MetricType, metricsVal model.InstanceGauge) error {
	return s.handler.ReportStat(metricType, metricsVal)
}

// Info 插件信息.
func (s *PrometheusReporter) Info() model.StatInfo {
	if !s.handler.exportSuccess() {
		return model.StatInfo{}
	}
	return model.StatInfo{
		Target:   s.Name(),
		Port:     uint32(s.handler.port),
		Path:     "/metrics",
		Protocol: "http",
	}
}

// Destroy .销毁插件.
func (s *PrometheusReporter) Destroy() error {
	if s.PluginBase != nil {
		if err := s.PluginBase.Destroy(); err != nil {
			return err
		}
	}
	if s.RunContext != nil {
		if err := s.RunContext.Destroy(); err != nil {
			return err
		}
	}

	if s.handler != nil {
		if err := s.handler.Close(); err != nil {
			return err
		}
	}

	return nil
}

type metricsHttpHandler struct {
	promeHttpHandler http.Handler
	lock             *sync.RWMutex
}

// ServeHTTP 提供 prometheus http 服务.
func (p *metricsHttpHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	p.lock.RLock()
	defer p.lock.RUnlock()
	p.promeHttpHandler.ServeHTTP(writer, request)
}
