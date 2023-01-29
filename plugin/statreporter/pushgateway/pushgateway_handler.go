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

package pushgateway

import (
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/plugin/statreporter/prometheus/addons"
)

// PushgatewayHandler handler for prometheus
type PushgatewayHandler struct {
	// prometheus的metrics注册
	registry *prometheus.Registry
	// metrics的 http handler
	handler         http.Handler
	cfg             *Config
	metricVecCaches map[string]prometheus.Collector
	bindIP          string
}

func newHandler(ctx *plugin.InitContext) (*PushgatewayHandler, error) {
	p := &PushgatewayHandler{}
	return p, p.init(ctx)
}

func (p *PushgatewayHandler) init(ctx *plugin.InitContext) error {
	cfgValue := ctx.Config.GetGlobal().GetStatReporter().GetPluginConfig(PluginName)
	if cfgValue != nil {
		p.cfg = cfgValue.(*Config)
	}
	p.bindIP = ctx.Config.GetGlobal().GetAPI().GetBindIP()
	p.metricVecCaches = make(map[string]prometheus.Collector)
	p.registry = prometheus.NewRegistry()
	if err := p.registerMetrics(); err != nil {
		return err
	}

	return nil
}

func (p *PushgatewayHandler) registerMetrics() error {
	for _, desc := range metrcisDesces {
		var collector prometheus.Collector
		switch desc.MetricType {
		case TypeForGaugeVec:
			collector = prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Name: desc.Name,
				Help: desc.Help,
			}, desc.LabelNames)
		case TypeForCounterVec:
			collector = prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: desc.Name,
				Help: desc.Help,
			}, desc.LabelNames)
		case TypeForMaxGaugeVec:
			collector = addons.NewMaxGaugeVec(prometheus.GaugeOpts{
				Name: desc.Name,
				Help: desc.Help,
			}, desc.LabelNames)
		case TypeForHistogramVec:
			collector = prometheus.NewHistogramVec(prometheus.HistogramOpts{
				Name: desc.Name,
				Help: desc.Help,
			}, desc.LabelNames)
		}

		err := p.registry.Register(collector)
		if err != nil {
			log.GetBaseLogger().Errorf("register prometheus collector error, %v", err)
			return err
		}
		if _, ok := p.metricVecCaches[desc.Name]; ok {
			log.GetBaseLogger().Errorf("register prometheus collector duplicate, %s", desc.Name)
			return fmt.Errorf("register prometheus collector duplicate, %s", desc.Name)
		}
		p.metricVecCaches[desc.Name] = collector
	}
	return nil
}

// ReportStat 上报采集指标到 prometheus，这里只针对部分 model.InstanceGauge 的实现做处理
func (p *PushgatewayHandler) ReportStat(metricsType model.MetricType, metricsVal model.InstanceGauge) error {
	switch metricsType {
	case model.ServiceStat:
		val, ok := metricsVal.(*model.ServiceCallResult)
		if ok {
			p.handleServiceGauge(metricsType, val)
		}
	case model.RateLimitStat:
		val, ok := metricsVal.(*model.RateLimitGauge)
		if ok {
			p.handleRateLimitGauge(metricsType, val)
		}
	case model.CircuitBreakStat:
		val, ok := metricsVal.(*model.CircuitBreakGauge)
		if ok {
			p.handleCircuitBreakGauge(metricsType, val)
		}
	}
	return nil
}

func (p *PushgatewayHandler) handleServiceGauge(metricsType model.MetricType, val *model.ServiceCallResult) {
	labels := p.convertInsGaugeToLabels(val)

	total := p.metricVecCaches[MetricsNameUpstreamRequestTotal].(*prometheus.CounterVec)
	total.With(labels).Inc()

	success := p.metricVecCaches[MetricsNameUpstreamRequestSuccess].(*prometheus.CounterVec)
	if val.GetRetStatus() == model.RetSuccess {
		success.With(labels).Inc()
	}

	delay := val.GetDelay()
	if delay != nil {
		data := float64(delay.Milliseconds())

		timeout := p.metricVecCaches[MetricsNameUpstreamRequestTimeout].(*prometheus.GaugeVec)
		timeout.With(labels).Add(data)

		maxTimeout := p.metricVecCaches[MetricsNameUpstreamRequestMaxTimeout].(*addons.MaxGaugeVec)
		maxTimeout.With(labels).Set(data)

		reqDelay := p.metricVecCaches[MetricsNameUpstreamRequestDelay].(*prometheus.HistogramVec)
		reqDelay.With(labels).Observe(data)
	}
}

func (p *PushgatewayHandler) handleRateLimitGauge(metricsType model.MetricType, val *model.RateLimitGauge) {
	labels := p.convertRateLimitGaugeToLabels(val)

	total := p.metricVecCaches[MetricsNameRateLimitRequestTotal].(*prometheus.CounterVec)
	total.With(labels).Inc()

	pass := p.metricVecCaches[MetricsNameRateLimitRequestPass].(*prometheus.CounterVec)
	if val.Result == model.QuotaResultOk {
		pass.With(labels).Inc()
	}

	limit := p.metricVecCaches[MetricsNameRateLimitRequestLimit].(*prometheus.CounterVec)
	if val.Result == model.QuotaResultLimited {
		limit.With(labels).Inc()
	}
}

func (p *PushgatewayHandler) handleCircuitBreakGauge(metricsType model.MetricType, val *model.CircuitBreakGauge) {
	labels := p.convertCircuitBreakGaugeToLabels(val)

	open := p.metricVecCaches[MetricsNameCircuitBreakerOpen].(*prometheus.GaugeVec)

	// 计算完之后的熔断状态
	status := val.GetCircuitBreakerStatus().GetStatus()
	if status == model.Open {
		open.With(labels).Inc()
	} else {
		open.With(labels).Dec()
	}

	halfOpen := p.metricVecCaches[MetricsNameCircuitBreakerHalfOpen].(*prometheus.GaugeVec)

	if status == model.HalfOpen {
		halfOpen.With(labels).Inc()
	} else {
		halfOpen.With(labels).Dec()
	}
}

func (p *PushgatewayHandler) convertInsGaugeToLabels(val *model.ServiceCallResult) map[string]string {
	labels := make(map[string]string)

	for label, supplier := range InstanceGaugeLabelOrder {
		labels[label] = supplier(val)
	}

	labels[CallerIP] = p.bindIP
	return labels
}

func (p *PushgatewayHandler) convertRateLimitGaugeToLabels(val *model.RateLimitGauge) map[string]string {
	labels := make(map[string]string)

	for label, supplier := range RateLimitGaugeLabelOrder {
		labels[label] = supplier(val)
	}
	return labels
}

func (p *PushgatewayHandler) convertCircuitBreakGaugeToLabels(val *model.CircuitBreakGauge) map[string]string {
	labels := make(map[string]string)

	for label, supplier := range CircuitBreakerGaugeLabelOrder {
		labels[label] = supplier(val)
	}
	return labels
}

// Close the prometheus handler
func (p *PushgatewayHandler) Close() error {
	return nil
}
