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
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/client_golang/prometheus/push"
	"go.uber.org/zap"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	statreporter "github.com/polarismesh/polaris-go/pkg/plugin/metrics"
	statcommon "github.com/polarismesh/polaris-go/plugin/metrics/common"
)

const (
	// PluginName is the name of the plugin.
	PluginName      = "prometheus"
	_metricsPull    = "pull"
	_metricsPush    = "push"
	_defaultJobName = "polaris-client"
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
	metricVecCaches map[string]*prometheus.GaugeVec

	clientIP string
	bindIP   string

	initCtx *plugin.InitContext

	// registry metrics registry
	registry *prometheus.Registry

	action ReportAction

	insCollector            *statcommon.StatInfoRevisionCollector
	rateLimitCollector      *statcommon.StatInfoRevisionCollector
	circuitBreakerCollector *statcommon.StatInfoStatefulCollector

	cancel context.CancelFunc
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
	s.initCtx = ctx
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
	s.metricVecCaches = map[string]*prometheus.GaugeVec{}
	s.registry = prometheus.NewRegistry()
	s.insCollector = statcommon.NewStatInfoRevisionCollector()
	s.rateLimitCollector = statcommon.NewStatInfoRevisionCollector()
	s.circuitBreakerCollector = statcommon.NewStatInfoStatefulCollector()
	if err := s.initSampleMapping(statcommon.ServiceCallStrategy, statcommon.ServiceCallLabelOrder); err != nil {
		return err
	}
	if err := s.initSampleMapping(statcommon.RateLimitStrategy, statcommon.RateLimitLabelOrder); err != nil {
		return err
	}
	if err := s.initSampleMapping(statcommon.CircuitBreakerStrategy, statcommon.CircuitBreakerLabelOrder); err != nil {
		return err
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
			if s.insCollector == nil || val == nil {
				return nil
			}
			labels := statcommon.ConvertInsGaugeToLabels(val, s.clientIP)
			s.insCollector.CollectStatInfo(val, labels, statcommon.ServiceCallStrategy,
				statcommon.ServiceCallLabelOrder)
		}
	case model.RateLimitStat:
		val, ok := metricsVal.(*model.RateLimitGauge)
		if ok {
			if s.rateLimitCollector == nil || val == nil {
				return nil
			}
			labels := statcommon.ConvertRateLimitGaugeToLabels(val)
			s.rateLimitCollector.CollectStatInfo(val, labels, statcommon.RateLimitStrategy,
				statcommon.RateLimitLabelOrder)
		}
	case model.CircuitBreakStat:
		val, ok := metricsVal.(*model.CircuitBreakGauge)
		if ok {
			if s.rateLimitCollector == nil || val == nil {
				return nil
			}
			labels := statcommon.ConvertCircuitBreakGaugeToLabels(val)
			s.circuitBreakerCollector.CollectStatInfo(val, labels, statcommon.CircuitBreakerStrategy,
				statcommon.CircuitBreakerLabelOrder)
		}
	}
	return nil
}

func (s *PrometheusReporter) initSampleMapping(strategies []statcommon.MetricValueAggregationStrategy, order []string) error {
	for i := range strategies {
		strategy := strategies[i]
		guageVec := prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: strategy.GetStrategyName(),
			Help: strategy.GetStrategyDescription(),
		}, order)
		s.metricVecCaches[strategy.GetStrategyName()] = guageVec
		if err := s.registry.Register(guageVec); err != nil {
			return err
		}
	}
	return nil
}

func (s *PrometheusReporter) prepare() {
	s.once.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		s.cancel = cancel
		switch s.cfg.Type {
		case _metricsPush:
			s.action = &PushAction{
				initCtx:  s.initCtx,
				reporter: s,
				cfg:      s.cfg,
			}
		default:
			s.action = &PullAction{
				initCtx:  s.initCtx,
				reporter: s,
				cfg:      s.cfg,
			}
		}
		s.action.Init(s.initCtx, s)
		s.action.Run(ctx)
	})
}

// Info 插件信息.
func (s *PrometheusReporter) Info() model.StatInfo {
	if s.action == nil {
		return model.StatInfo{}
	}
	return s.action.Info()
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
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}

type metricsHttpHandler struct {
	handler http.Handler
	lock    sync.RWMutex
}

// ServeHTTP 提供 prometheus http 服务.
func (p *metricsHttpHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	p.lock.RLock()
	defer p.lock.RUnlock()
	p.handler.ServeHTTP(writer, request)
}

type ReportAction interface {
	Init(initCtx *plugin.InitContext, reporter *PrometheusReporter)
	Run(ctx context.Context)
	Info() model.StatInfo
}

type PullAction struct {
	initCtx  *plugin.InitContext
	reporter *PrometheusReporter
	cfg      *Config
	clientIP string
	bindIP   string
	bindPort int32
	ln       net.Listener
}

func (pa *PullAction) Init(initCtx *plugin.InitContext, reporter *PrometheusReporter) {
	pa.clientIP = initCtx.Config.GetGlobal().GetAPI().GetBindIP()
	pa.bindIP = initCtx.Config.GetGlobal().GetAPI().GetBindIP()
	cfgValue := initCtx.Config.GetGlobal().GetStatReporter().GetPluginConfig(PluginName)
	if cfgValue == nil {
		return
	}
	pa.cfg = cfgValue.(*Config)
	if len(pa.cfg.IP) != 0 {
		pa.bindIP = pa.cfg.IP
	}
	pa.bindPort = int32(pa.cfg.port)
}

func (pa *PullAction) doAggregation(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)

	action := func() {
		defer func() {
			if err := recover(); err != nil {
				log.GetBaseLogger().Errorf("stat metrics prometheus panic", zap.Any("error", err))
			}
		}()
		log.GetBaseLogger().Infof("start aggregation stat metrics prometheus")

		statcommon.PutDataFromContainerInOrder(pa.reporter.metricVecCaches, pa.reporter.insCollector,
			pa.reporter.insCollector.GetCurrentRevision())
		statcommon.PutDataFromContainerInOrder(pa.reporter.metricVecCaches, pa.reporter.circuitBreakerCollector, 0)
		statcommon.PutDataFromContainerInOrder(pa.reporter.metricVecCaches, pa.reporter.rateLimitCollector,
			pa.reporter.rateLimitCollector.GetCurrentRevision())
	}

	for {
		select {
		case <-ticker.C:
			action()
		case <-ctx.Done():
			ticker.Stop()
		}
	}
}

func (pa *PullAction) Run(ctx context.Context) {
	if pa.bindPort < 0 {
		return
	}
	go pa.doAggregation(ctx)
	go func() {
		ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", pa.bindIP, pa.bindPort))
		if err != nil {
			log.GetBaseLogger().Errorf("start metrics http-server fail: %v", err)
			pa.bindPort = -1
			return
		}
		pa.ln = ln
		pa.bindPort = int32(ln.Addr().(*net.TCPAddr).Port)
		handler := metricsHttpHandler{
			handler: promhttp.HandlerFor(pa.reporter.registry, promhttp.HandlerOpts{}),
		}

		log.GetBaseLogger().Infof("start metrics http-server address : %s", fmt.Sprintf("%s:%d", pa.bindIP, pa.bindPort))
		if err := http.Serve(ln, &handler); err != nil {
			log.GetBaseLogger().Errorf("start metrics http-server fail : %s", err)
			return
		}
	}()
}

// Info 插件信息.
func (pa *PullAction) Info() model.StatInfo {
	if pa.bindPort <= 0 {
		return model.StatInfo{}
	}
	return model.StatInfo{
		Target:   PluginName,
		Port:     uint32(pa.bindPort),
		Path:     "/metrics",
		Protocol: "http",
	}
}

type PushAction struct {
	initCtx  *plugin.InitContext
	reporter *PrometheusReporter
	cfg      *Config
}

func (pa *PushAction) Init(initCtx *plugin.InitContext, reporter *PrometheusReporter) {
	cfgValue := initCtx.Config.GetGlobal().GetStatReporter().GetPluginConfig(PluginName)
	if cfgValue == nil {
		return
	}
	pa.cfg = cfgValue.(*Config)
}

func (pa *PushAction) Run(ctx context.Context) {
	go func() {
		pushTicker := time.NewTicker(pa.cfg.Interval)

		action := func() {
			defer func() {
				if err := recover(); err != nil {
					log.GetBaseLogger().Errorf("stat metrics prometheus panic", zap.Any("error", err))
				}
			}()

			log.GetBaseLogger().Infof("start push stat metrics prometheus")

			statcommon.PutDataFromContainerInOrder(pa.reporter.metricVecCaches, pa.reporter.insCollector,
				pa.reporter.insCollector.GetCurrentRevision())
			statcommon.PutDataFromContainerInOrder(pa.reporter.metricVecCaches, pa.reporter.circuitBreakerCollector, 0)
			statcommon.PutDataFromContainerInOrder(pa.reporter.metricVecCaches, pa.reporter.rateLimitCollector,
				pa.reporter.rateLimitCollector.GetCurrentRevision())

			if err := push.
				New(pa.cfg.Address, _defaultJobName).
				Gatherer(pa.reporter.registry).
				Push(); err != nil {
				log.GetBaseLogger().Errorf("push metrics to pushgateway fail: %s", err.Error())
			}
		}

		for {
			select {
			case <-pushTicker.C:
				action()
			case <-ctx.Done():
				pushTicker.Stop()
				return
			}
		}
	}()
}

// Info 插件信息.
func (pa *PushAction) Info() model.StatInfo {
	return model.StatInfo{}
}
