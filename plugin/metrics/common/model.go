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

package common

import (
	"fmt"
	"sort"
	"strings"

	"github.com/polarismesh/polaris-go/pkg/model"
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
	CalleeResult    = "callee_result"
	CallerNamespace = "caller_namespace"
	CallerService   = "caller_service"
	CallerIP        = "caller_ip"
	CallerLabels    = "caller_labels"
	MetricNameLabel = "metric_name"
	RuleName        = "rule_name"

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

type LabelValueSupplier func(val interface{}) string

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
		CalleeMethod: func(args interface{}) string {
			val := args.(*model.ServiceCallResult)
			return val.GetMethod()
		},
		CalleeResult: func(args interface{}) string {
			val := args.(*model.ServiceCallResult)
			retStatus := string(val.GetRetStatus())
			if retStatus != "" {
				return retStatus
			}
			return NilValue
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
			sort.Strings(ret)
			return strings.Join(ret, "|")
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
		RuleName: func(args interface{}) string {
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
		RuleName: func(args interface{}) string {
			val := args.(*model.RateLimitGauge)
			if val.RuleName != "" {
				return val.RuleName
			}
			return NilValue
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
	sort.Strings(s)
	return strings.Join(s, "|")
}

func ConvertInsGaugeToLabels(val *model.ServiceCallResult, bindIP string) map[string]string {
	labels := make(map[string]string)
	for label, supplier := range InstanceGaugeLabelOrder {
		labels[label] = supplier(val)
	}
	labels[CallerIP] = bindIP
	return labels
}

func ConvertRateLimitGaugeToLabels(val *model.RateLimitGauge) map[string]string {
	labels := make(map[string]string)
	for label, supplier := range RateLimitGaugeLabelOrder {
		labels[label] = supplier(val)
	}
	return labels
}

func ConvertCircuitBreakGaugeToLabels(val *model.CircuitBreakGauge) map[string]string {
	labels := make(map[string]string)
	for label, supplier := range CircuitBreakerGaugeLabelOrder {
		labels[label] = supplier(val)
	}
	return labels
}
