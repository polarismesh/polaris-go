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
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/client_golang/prometheus/push"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	statreporter "github.com/polarismesh/polaris-go/pkg/plugin/metrics"
	"github.com/polarismesh/polaris-go/plugin/metrics/prometheus/addons"
)

const (
	// PluginName is the name of the plugin.
	PluginName      string = "prometheus"
	_metricsPull    string = "pull"
	_metricsPush    string = "push"
	_defaultJobName        = "polaris-client"
)

var _ statreporter.StatReporter = (*PrometheusReporter)(nil)

// init 注册插件.
func init() {
	plugin.RegisterPlugin(&PrometheusReporter{})
}

type metricsHandler interface {
	io.Closer
	init(ctx *plugin.InitContext) error
	reportStat(metricsType model.MetricType, metricsVal model.InstanceGauge) error
	info() model.StatInfo
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
	plugins         plugin.Supplier
	once            sync.Once
	metricVecCaches map[string]prometheus.Collector

	clientIP string
	bindIP   string

	// registry metrics registry
	registry *prometheus.Registry

	// metrics pull resource
	ln       net.Listener
	pullPort int32

	// metrics push resource
	pushTicker *time.Ticker
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
	s.clientIP = ctx.Config.GetGlobal().GetAPI().GetBindIP()
	s.bindIP = ctx.Config.GetGlobal().GetAPI().GetBindIP()
	cfgValue := ctx.Config.GetGlobal().GetStatReporter().GetPluginConfig(PluginName)
	if cfgValue != nil {
		s.cfg = cfgValue.(*Config)
	}
	return nil
}

// ReportStat 报告统计数据.
func (s *PrometheusReporter) ReportStat(metricsType model.MetricType, metricsVal model.InstanceGauge) error {
	s.prepare()
	switch metricsType {
	case model.ServiceStat:
		val, ok := metricsVal.(*model.ServiceCallResult)
		if ok {
			s.handleServiceGauge(metricsType, val)
		}
	case model.RateLimitStat:
		val, ok := metricsVal.(*model.RateLimitGauge)
		if ok {
			s.handleRateLimitGauge(metricsType, val)
		}
	case model.CircuitBreakStat:
		val, ok := metricsVal.(*model.CircuitBreakGauge)
		if ok {
			s.handleCircuitBreakGauge(metricsType, val)
		}
	}
	return nil
}

func (s *PrometheusReporter) handleServiceGauge(metricsType model.MetricType, val *model.ServiceCallResult) {
	labels := convertInsGaugeToLabels(val, s.clientIP)
	total := s.metricVecCaches[MetricsNameUpstreamRequestTotal].(*prometheus.CounterVec)
	total.With(labels).Inc()

	success := s.metricVecCaches[MetricsNameUpstreamRequestSuccess].(*prometheus.CounterVec)
	if val.GetRetStatus() == model.RetSuccess {
		success.With(labels).Inc()
	}

	delay := val.GetDelay()
	if delay != nil {
		data := float64(delay.Milliseconds())

		timeout := s.metricVecCaches[MetricsNameUpstreamRequestTimeout].(*prometheus.GaugeVec)
		timeout.With(labels).Add(data)

		maxTimeout := s.metricVecCaches[MetricsNameUpstreamRequestMaxTimeout].(*addons.MaxGaugeVec)
		maxTimeout.With(labels).Set(data)

		reqDelay := s.metricVecCaches[MetricsNameUpstreamRequestDelay].(*prometheus.HistogramVec)
		reqDelay.With(labels).Observe(data)
	}
}

func (s *PrometheusReporter) handleRateLimitGauge(metricsType model.MetricType, val *model.RateLimitGauge) {
	labels := convertRateLimitGaugeToLabels(val)

	total := s.metricVecCaches[MetricsNameRateLimitRequestTotal].(*prometheus.CounterVec)
	total.With(labels).Inc()

	pass := s.metricVecCaches[MetricsNameRateLimitRequestPass].(*prometheus.CounterVec)
	if val.Result == model.QuotaResultOk {
		pass.With(labels).Inc()
	}

	limit := s.metricVecCaches[MetricsNameRateLimitRequestLimit].(*prometheus.CounterVec)
	if val.Result == model.QuotaResultLimited {
		limit.With(labels).Inc()
	}
}

func (s *PrometheusReporter) handleCircuitBreakGauge(metricsType model.MetricType, val *model.CircuitBreakGauge) {
	labels := convertCircuitBreakGaugeToLabels(val)

	open := s.metricVecCaches[MetricsNameCircuitBreakerOpen].(*prometheus.GaugeVec)

	// 计算完之后的熔断状态
	status := val.GetCircuitBreakerStatus().GetStatus()
	if status == model.Open {
		open.With(labels).Inc()
	} else {
		open.With(labels).Dec()
	}

	halfOpen := s.metricVecCaches[MetricsNameCircuitBreakerHalfOpen].(*prometheus.GaugeVec)

	if status == model.HalfOpen {
		halfOpen.With(labels).Inc()
	} else {
		halfOpen.With(labels).Dec()
	}
}

func (s *PrometheusReporter) prepare() {
	s.once.Do(func() {
		switch s.cfg.Type {
		case _metricsPush:
			s.initPush()
		default:
			s.initPull()
		}
	})
}

func (s *PrometheusReporter) initPush() {
	s.pushTicker = time.NewTicker(s.cfg.Interval)
	go func() {
		for range s.pushTicker.C {
			if err := push.
				New(s.cfg.Address, _defaultJobName).
				Gatherer(s.registry).
				Push(); err != nil {
				log.GetBaseLogger().Errorf("push metrics to pushgateway fail: %s", err.Error())
			}
		}
	}()
}

func (s *PrometheusReporter) initPull() {
	if s.cfg.IP != "" {
		s.bindIP = s.cfg.IP
	}

	s.pullPort = int32(s.cfg.port)
	if s.pullPort < 0 {
		return
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", s.bindIP, s.pullPort))
	if err != nil {
		log.GetBaseLogger().Errorf("start metrics http-server fail: %v", err)
		s.pullPort = -1
		return
	}

	s.ln = ln
	s.pullPort = int32(ln.Addr().(*net.TCPAddr).Port)

	go func() {
		handler := metricsHttpHandler{
			promeHttpHandler: promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{}),
			lock:             &sync.RWMutex{},
		}

		log.GetBaseLogger().Infof("start metrics http-server address : %s", fmt.Sprintf("%s:%d", s.bindIP, s.pullPort))
		if err := http.Serve(ln, &handler); err != nil {
			log.GetBaseLogger().Errorf("start metrics http-server fail : %s", err)
			return
		}
	}()
}

// Info 插件信息.
func (s *PrometheusReporter) Info() model.StatInfo {
	if s.cfg.Type != _metricsPull {
		return model.StatInfo{}
	}
	if s.pullPort <= 0 {
		return model.StatInfo{}
	}
	return model.StatInfo{
		Target:   PluginName,
		Port:     uint32(s.pullPort),
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
	switch s.cfg.Type {
	case _metricsPush:
		if s.pushTicker != nil {
			s.pushTicker.Stop()
		}
	default:
		if s.ln != nil {
			_ = s.ln.Close()
		}
	}
	return nil
}
