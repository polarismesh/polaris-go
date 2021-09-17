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

package data

import (
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/clock"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/circuitbreaker"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/healthcheck"
	"github.com/polarismesh/polaris-go/pkg/plugin/loadbalancer"
	"github.com/polarismesh/polaris-go/pkg/plugin/localregistry"
	"github.com/polarismesh/polaris-go/pkg/plugin/serverconnector"
	"github.com/polarismesh/polaris-go/pkg/plugin/servicerouter"
	"github.com/polarismesh/polaris-go/pkg/plugin/statreporter"
	"time"
)

//加载连接器插件
func GetServerConnector(
	cfg config.Configuration, supplier plugin.Supplier) (serverconnector.ServerConnector, error) {
	//加载服务端连接器
	protocol := cfg.GetGlobal().GetServerConnector().GetProtocol()
	targetPlugin, err := supplier.GetPlugin(common.TypeServerConnector, protocol)
	if nil != err {
		return nil, err
	}
	return targetPlugin.(serverconnector.ServerConnector), nil
}

//加载本地缓存插件
func GetRegistry(cfg config.Configuration, supplier plugin.Supplier) (localregistry.LocalRegistry, error) {
	localCacheType := cfg.GetConsumer().GetLocalCache().GetType()
	targetPlugin, err := supplier.GetPlugin(common.TypeLocalRegistry, localCacheType)
	if nil != err {
		return nil, err
	}
	return targetPlugin.(localregistry.LocalRegistry), nil
}

//获取熔断插件链
func GetCircuitBreakers(
	cfg config.Configuration, supplier plugin.Supplier) ([]circuitbreaker.InstanceCircuitBreaker, error) {
	cbChain := cfg.GetConsumer().GetCircuitBreaker().GetChain()
	when := cfg.GetConsumer().GetHealthCheck().GetWhen()
	var hasHealthCheckBreaker bool
	cbreakers := make([]circuitbreaker.InstanceCircuitBreaker, 0, len(cbChain))
	if len(cbChain) > 0 {
		for _, cbName := range cbChain {
			if cbName == config.DefaultCircuitBreakerErrCheck {
				hasHealthCheckBreaker = true
			}
			targetPlugin, err := supplier.GetPlugin(common.TypeCircuitBreaker, cbName)
			if nil != err {
				return nil, err
			}
			cbreakers = append(cbreakers, targetPlugin.(circuitbreaker.InstanceCircuitBreaker))
		}
	}
	if when == config.HealthCheckAlways && !hasHealthCheckBreaker {
		targetPlugin, err := supplier.GetPlugin(common.TypeCircuitBreaker, config.DefaultCircuitBreakerErrCheck)
		if nil != err {
			return nil, err
		}
		cbreakers = append(cbreakers, targetPlugin.(circuitbreaker.InstanceCircuitBreaker))
	}
	return cbreakers, nil
}

//获取健康探测插件列表
func GetHealthCheckers(cfg config.Configuration, supplier plugin.Supplier) ([]healthcheck.HealthChecker, error) {
	names := cfg.GetConsumer().GetHealthCheck().GetChain()
	healthCheckers := make([]healthcheck.HealthChecker, 0, len(names))
	if len(names) > 0 {
		for _, name := range names {
			targetPlugin, err := supplier.GetPlugin(common.TypeHealthCheck, name)
			if nil != err {
				return nil, err
			}
			healthCheckers = append(healthCheckers, targetPlugin.(healthcheck.HealthChecker))
		}
	}
	return healthCheckers, nil
}

//获取服务路由插件链
func GetServiceRouterChain(cfg config.Configuration, supplier plugin.Supplier) (*servicerouter.RouterChain, error) {
	filterChain := cfg.GetConsumer().GetServiceRouter().GetChain()
	filters := &servicerouter.RouterChain{
		Chain: make([]servicerouter.ServiceRouter, 0, len(filterChain)),
	}
	if len(filterChain) > 0 {
		for _, filter := range filterChain {
			targetPlugin, err := supplier.GetPlugin(common.TypeServiceRouter, filter)
			if nil != err {
				return nil, err
			}
			filters.Chain = append(filters.Chain, targetPlugin.(servicerouter.ServiceRouter))
		}
	}
	return filters, nil
}

//获取统计上报插件
func GetStatReporterChain(cfg config.Configuration, supplier plugin.Supplier) ([]statreporter.StatReporter, error) {
	reporterNames := cfg.GetGlobal().GetStatReporter().GetChain()
	reporterChain := make([]statreporter.StatReporter, 0, len(reporterNames))
	if len(reporterNames) > 0 {
		for _, reporter := range reporterNames {
			targetPlugin, err := supplier.GetPlugin(common.TypeStatReporter, reporter)
			if nil != err {
				return nil, err
			}
			reporterChain = append(reporterChain, targetPlugin.(statreporter.StatReporter))
		}
	}
	return reporterChain, nil
}

//获取负载均衡插件
func GetLoadBalancer(cfg config.Configuration, supplier plugin.Supplier) (loadbalancer.LoadBalancer, error) {
	lbType := cfg.GetConsumer().GetLoadbalancer().GetType()
	targetPlugin, err := supplier.GetPlugin(common.TypeLoadBalancer, lbType)
	if nil != err {
		return nil, err
	}
	return targetPlugin.(loadbalancer.LoadBalancer), nil
}

//获取负载均衡插件
func GetLoadBalancerByLbType(lbType string, supplier plugin.Supplier) (loadbalancer.LoadBalancer, error) {
	targetPlugin, err := supplier.GetPlugin(common.TypeLoadBalancer, lbType)
	if nil != err {
		return nil, err
	}
	return targetPlugin.(loadbalancer.LoadBalancer), nil
}

//同步调用的通用方法定义
type SingleInvoke func(request interface{}) (interface{}, error)

//通用的带重试的同步调用逻辑
func RetrySyncCall(name string, svcKey *model.ServiceKey,
	request interface{}, call SingleInvoke, param *model.ControlParam) (interface{}, model.SDKError) {
	retryTimes := -1
	var resp interface{}
	var err error
	retryInterval := param.RetryInterval
	for retryTimes < param.MaxRetry {
		startTime := clock.GetClock().Now()
		resp, err = call(request)
		consumeTime := clock.GetClock().Now().Sub(startTime)
		if nil == err {
			return resp, nil
		}
		sdkErr, ok := err.(model.SDKError)
		if !ok || !sdkErr.ErrorCode().Retryable() {
			return resp, sdkErr
		}
		retryTimes++
		if retryTimes >= param.MaxRetry {
			break
		}
		time.Sleep(retryInterval)
		log.GetBaseLogger().Warnf("retry %s for timeout, consume time %v,"+
			" Namespace: %s, Service: %s, retry times: %d",
			name, consumeTime, svcKey.Namespace, svcKey.Service, retryTimes)
	}
	return resp, model.NewSDKError(model.ErrCodeAPITimeoutError, err,
		fmt.Sprintf("fail to do %s after retry %v times", name, retryTimes))
}
