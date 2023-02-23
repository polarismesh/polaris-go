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

package prometheus

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/plugin/metrics/prometheus/addons"
)

// MetricsType 指标类型，对应 Prometheus 提供的 Collector 类型.
type MetricsType int

const (
	// TypeForCounterVec metric type.
	TypeForCounterVec MetricsType = iota
	TypeForGaugeVec
	TypeForGauge
	TypeForHistogramVec
	TypeForMaxGaugeVec
)

// metricDesc 指标描述.
type metricDesc struct {
	Name       string
	Help       string
	MetricType MetricsType
	LabelNames []string
}

const (
	// CalleeNamespace SystemMetricName.
	CalleeNamespace = "callee_namespace"
	CalleeService   = "callee_service"
	CalleeSubset    = "callee_subset"
	CalleeInstance  = "callee_instance"
	CalleeRetCode   = "callee_result_code"
	CalleeMethod    = "callee_method"
	CallerNamespace = "caller_namespace"
	CallerService   = "caller_service"
	CallerIP        = "caller_ip"
	CallerLabels    = "caller_labels"
	MetricNameLabel = "metric_name"

	// MetricsNameUpstreamRequestTotal 与路由、请求相关的指标信息.
	MetricsNameUpstreamRequestTotal      = "upstream_rq_total"
	MetricsNameUpstreamRequestSuccess    = "upstream_rq_success"
	MetricsNameUpstreamRequestTimeout    = "upstream_rq_timeout"
	MetricsNameUpstreamRequestMaxTimeout = "upstream_rq_max_timeout"
	MetricsNameUpstreamRequestDelay      = "upstream_rq_delay"

	// 限流相关指标信息.
	MetricsNameRateLimitRequestTotal = "ratelimit_rq_total"
	MetricsNameRateLimitRequestPass  = "ratelimit_rq_pass"
	MetricsNameRateLimitRequestLimit = "ratelimit_rq_limit"

	// 熔断相关指标信息.
	MetricsNameCircuitBreakerOpen     = "circuitbreaker_open"
	MetricsNameCircuitBreakerHalfOpen = "circuitbreaker_halfopen"

	// SystemMetricValue.
	NilValue = "__NULL__"
)

// 服务路由相关指标.
var (
	RouterGaugeNames []string = []string{
		MetricsNameUpstreamRequestTotal,
		MetricsNameUpstreamRequestSuccess,
		MetricsNameUpstreamRequestTimeout,
		MetricsNameUpstreamRequestMaxTimeout,
	}

	UpstreamRequestTotal = metricDesc{
		Name:       MetricsNameUpstreamRequestTotal,
		Help:       "total of request per period",
		MetricType: TypeForCounterVec,
		LabelNames: GetLabels(InstanceGaugeLabelOrder),
	}

	UpstreamRequestSuccess = metricDesc{
		Name:       MetricsNameUpstreamRequestSuccess,
		Help:       "total of success request per period",
		MetricType: TypeForCounterVec,
		LabelNames: GetLabels(InstanceGaugeLabelOrder),
	}

	UpstreamRequestTimeout = metricDesc{
		Name:       MetricsNameUpstreamRequestTimeout,
		Help:       "total of request delay per period",
		MetricType: TypeForGaugeVec,
		LabelNames: GetLabels(InstanceGaugeLabelOrder),
	}

	UpstreamRequestMaxTimeout = metricDesc{
		Name:       MetricsNameUpstreamRequestMaxTimeout,
		Help:       "maximum request delay per period",
		MetricType: TypeForMaxGaugeVec,
		LabelNames: GetLabels(InstanceGaugeLabelOrder),
	}

	UpstreamRequestDelay = metricDesc{
		Name:       MetricsNameUpstreamRequestDelay,
		Help:       "per request delay per period",
		MetricType: TypeForHistogramVec,
		LabelNames: GetLabels(InstanceGaugeLabelOrder),
	}
)

// 限流相关指标.
var (
	RateLimitGaugeNames []string = []string{
		MetricsNameRateLimitRequestTotal,
		MetricsNameRateLimitRequestPass,
		MetricsNameRateLimitRequestLimit,
	}

	RateLimitRequestTotal = metricDesc{
		Name:       MetricsNameRateLimitRequestTotal,
		Help:       "total of rate limit per period",
		MetricType: TypeForCounterVec,
		LabelNames: GetLabels(RateLimitGaugeLabelOrder),
	}

	RateLimitRequestPass = metricDesc{
		Name:       MetricsNameRateLimitRequestPass,
		Help:       "total of passed request per period",
		MetricType: TypeForCounterVec,
		LabelNames: GetLabels(RateLimitGaugeLabelOrder),
	}

	RateLimitRequestLimit = metricDesc{
		Name:       MetricsNameRateLimitRequestLimit,
		Help:       "total of limited request per period",
		MetricType: TypeForCounterVec,
		LabelNames: GetLabels(RateLimitGaugeLabelOrder),
	}
)

var (
	CircuitBreakerGaugeNames []string = []string{
		MetricsNameCircuitBreakerOpen,
		MetricsNameCircuitBreakerHalfOpen,
	}

	CircuitBreakerOpen = metricDesc{
		Name:       MetricsNameCircuitBreakerOpen,
		Help:       "total of opened circuit breaker",
		MetricType: TypeForGaugeVec,
		LabelNames: GetLabels(CircuitBreakerGaugeLabelOrder),
	}

	CircuitBreakerHalfOpen = metricDesc{
		Name:       MetricsNameCircuitBreakerHalfOpen,
		Help:       "total of half-open circuit breaker",
		MetricType: TypeForGaugeVec,
		LabelNames: GetLabels(CircuitBreakerGaugeLabelOrder),
	}
)

var metrcisDesces map[string]metricDesc = map[string]metricDesc{
	MetricsNameUpstreamRequestTotal:      UpstreamRequestTotal,
	MetricsNameUpstreamRequestSuccess:    UpstreamRequestSuccess,
	MetricsNameUpstreamRequestTimeout:    UpstreamRequestTimeout,
	MetricsNameUpstreamRequestMaxTimeout: UpstreamRequestMaxTimeout,
	MetricsNameUpstreamRequestDelay:      UpstreamRequestDelay,

	MetricsNameRateLimitRequestTotal: RateLimitRequestTotal,
	MetricsNameRateLimitRequestPass:  RateLimitRequestPass,
	MetricsNameRateLimitRequestLimit: RateLimitRequestLimit,

	MetricsNameCircuitBreakerOpen:     CircuitBreakerOpen,
	MetricsNameCircuitBreakerHalfOpen: CircuitBreakerHalfOpen,
}

type LabelValueSupplier func(val interface{}) string

func GetLabels(m map[string]LabelValueSupplier) []string {
	labels := make([]string, 0, len(m))

	for k := range m {
		labels = append(labels, k)
	}

	return labels
}

var (
	// InstanceGaugeLabelOrder 实例监控指标的label顺序
	InstanceGaugeLabelOrder map[string]LabelValueSupplier = map[string]LabelValueSupplier{
		// 被调方相关信息
		CalleeNamespace: func(args interface{}) string {
			val := args.(*model.ServiceCallResult)
			return val.GetCalledInstance().GetNamespace()
		},
		CalleeService: func(args interface{}) string {
			val := args.(*model.ServiceCallResult)
			return val.GetCalledInstance().GetService()
		},
		CalleeSubset: func(args interface{}) string {
			val := args.(*model.ServiceCallResult)
			return val.CalledInstance.GetLogicSet()
		},
		CalleeInstance: func(args interface{}) string {
			val := args.(*model.ServiceCallResult)
			return fmt.Sprintf("%s:%d", val.GetCalledInstance().GetHost(), val.GetCalledInstance().GetPort())
		},
		CalleeRetCode: func(args interface{}) string {
			val := args.(*model.ServiceCallResult)
			if val.GetRetCode() == nil {
				return NilValue
			}
			return fmt.Sprintf("%d", *val.GetRetCode())
		},

		// 主调方相关信息
		CallerLabels: func(args interface{}) string {
			val := args.(*model.ServiceCallResult)
			if val.SourceService == nil || len(val.SourceService.Metadata) == 0 {
				return ""
			}
			labels := val.SourceService.Metadata
			var ret []string
			for k, v := range labels {
				ret = append(ret, fmt.Sprintf("%s=%s", k, v))
			}
			return strings.Join(ret, ",")
		},
		CallerNamespace: func(args interface{}) string {
			val := args.(*model.ServiceCallResult)
			namespace := val.GetCallerNamespace()
			if namespace != "" {
				return namespace
			}
			return NilValue
		},
		CallerService: func(args interface{}) string {
			val := args.(*model.ServiceCallResult)
			service := val.GetCallerService()
			if service != "" {
				return service
			}
			return NilValue
		},
		CallerIP: func(args interface{}) string {
			return NilValue
		},
	}

	RateLimitGaugeLabelOrder map[string]LabelValueSupplier = map[string]LabelValueSupplier{
		CalleeNamespace: func(args interface{}) string {
			val := args.(*model.RateLimitGauge)
			return val.GetNamespace()
		},
		CalleeService: func(args interface{}) string {
			val := args.(*model.RateLimitGauge)
			return val.GetService()
		},
		CalleeMethod: func(args interface{}) string {
			val := args.(*model.RateLimitGauge)
			return val.Method
		},
		CallerLabels: func(args interface{}) string {
			val := args.(*model.RateLimitGauge)
			return formatLabelsToStr(val.Arguments)
		},
	}

	CircuitBreakerGaugeLabelOrder map[string]LabelValueSupplier = map[string]LabelValueSupplier{
		CalleeNamespace: func(args interface{}) string {
			val := args.(*model.CircuitBreakGauge)
			return val.GetCalledInstance().GetNamespace()
		},
		CalleeService: func(args interface{}) string {
			val := args.(*model.CircuitBreakGauge)
			return val.GetCalledInstance().GetService()
		},
		CalleeMethod: func(args interface{}) string {
			val := args.(*model.CircuitBreakGauge)
			return val.Method
		},
		CalleeSubset: func(args interface{}) string {
			val := args.(*model.CircuitBreakGauge)
			return val.GetCalledInstance().GetLogicSet()
		},
		CalleeInstance: func(args interface{}) string {
			val := args.(*model.CircuitBreakGauge)
			return fmt.Sprintf("%s:%d", val.GetCalledInstance().GetHost(), val.GetCalledInstance().GetPort())
		},
		CallerNamespace: func(args interface{}) string {
			val := args.(*model.CircuitBreakGauge)
			return val.GetNamespace()
		},
		CallerService: func(args interface{}) string {
			val := args.(*model.CircuitBreakGauge)
			return val.GetService()
		},
	}
)

func formatLabelsToStr(arguments []model.Argument) string {
	if len(arguments) == 0 {
		return ""
	}

	s := make([]string, 0, len(arguments))

	for _, argument := range arguments {
		s = append(s, argument.String())
	}

	return strings.Join(s, "|")
}

func buildMetrics() ([]prometheus.Collector, map[string]prometheus.Collector) {
	var (
		metricVecCaches = map[string]prometheus.Collector{}
		collectors      = make([]prometheus.Collector, 0, len(metrcisDesces))
	)

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
		collectors = append(collectors, collector)
		metricVecCaches[desc.Name] = collector
	}
	return collectors, metricVecCaches
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

func convertInsGaugeToLabels(val *model.ServiceCallResult, bindIP string) map[string]string {
	labels := make(map[string]string)
	for label, supplier := range InstanceGaugeLabelOrder {
		labels[label] = supplier(val)
	}
	labels[CallerIP] = bindIP
	return labels
}

func convertRateLimitGaugeToLabels(val *model.RateLimitGauge) map[string]string {
	labels := make(map[string]string)
	for label, supplier := range RateLimitGaugeLabelOrder {
		labels[label] = supplier(val)
	}
	return labels
}

func convertCircuitBreakGaugeToLabels(val *model.CircuitBreakGauge) map[string]string {
	labels := make(map[string]string)
	for label, supplier := range CircuitBreakerGaugeLabelOrder {
		labels[label] = supplier(val)
	}
	return labels
}
