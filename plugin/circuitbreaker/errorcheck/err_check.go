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

package errorcheck

import (
	"github.com/polarismesh/polaris-go/pkg/clock"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/local"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/circuitbreaker"
	common2 "github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/plugin/circuitbreaker/common"
	"time"
)

// CircuitBreaker 通过定时探测进行熔断的熔断器
type CircuitBreaker struct {
	*plugin.PluginBase
	healthCheckCfg  config.HealthCheckConfig
	halfOpenHandler *common.HalfOpenConversionHandler
}

//插件类型
func (g *CircuitBreaker) Type() common2.Type {
	return common2.TypeCircuitBreaker
}

//插件名，一个类型下插件名唯一
func (g *CircuitBreaker) Name() string {
	return config.DefaultCircuitBreakerErrCheck
}

//初始化插件
func (g *CircuitBreaker) Init(ctx *plugin.InitContext) error {
	g.PluginBase = plugin.NewPluginBase(ctx)
	g.healthCheckCfg = ctx.Config.GetConsumer().GetHealthCheck()
	g.halfOpenHandler = common.NewHalfOpenConversionHandler(ctx.Config)
	return nil
}

//销毁插件，可用于释放资源
func (g *CircuitBreaker) Destroy() error {
	return nil
}

//插件是否启用
func (g *CircuitBreaker) IsEnable(cfg config.Configuration) bool {
	return true
}

//进行调用统计，返回当前实例是否需要进行立即熔断
func (g *CircuitBreaker) Stat(model.InstanceGauge) (bool, error) {
	return false, nil
}

//进行熔断计算，返回需要进行状态转换的实例ID
//入参包括全量服务实例，以及当前周期的健康探测结果
func (g *CircuitBreaker) CircuitBreak(instances []model.Instance) (*circuitbreaker.Result, error) {
	if g.healthCheckCfg.GetWhen() != config.HealthCheckAlways {
		return nil, nil
	}
	result := circuitbreaker.NewCircuitBreakerResult(clock.GetClock().Now())
	for _, instance := range instances {
		log.GetBaseLogger().Tracef("invoke %s circuitBreaker for instance %s", g.Name(), instance.GetId())
		if g.closeToOpen(instance, result.Now) {
			log.GetBaseLogger().Warnf("ErrCheck circuitbreaker: close to open, instance:"+
				" Id: %s, Namespace: %s, Service: %s, Host: %s, Port: %v\n",
				instance.GetId(), instance.GetNamespace(), instance.GetService(), instance.GetHost(), instance.GetPort())
			result.InstancesToOpen.Add(instance.GetId())
			continue
		}
		if g.halfOpenHandler.OpenToHalfOpen(instance, result.Now, g.Name()) {
			log.GetBaseLogger().Infof("ErrCheck circuitbreaker: open to halfopen, instance:"+
				" Id: %s, Namespace: %s, Service: %s, Host: %s, Port: %v\n",
				instance.GetId(), instance.GetNamespace(), instance.GetService(), instance.GetHost(), instance.GetPort())
			result.InstancesToHalfOpen.Add(instance.GetId())
			continue
		}

		halfOpenChange := g.halfOpenHandler.HalfOpenConversion(result.Now, instance, g.Name())
		switch halfOpenChange {
		case common.ToOpen:
			log.GetBaseLogger().Warnf("ErrCheck circuitbreaker: halfopen to open, instance:"+
				" Id: %s, Namespace: %s, Service: %s, Host: %s, Port: %v\n",
				instance.GetId(), instance.GetNamespace(), instance.GetService(), instance.GetHost(), instance.GetPort())
			result.InstancesToOpen.Add(instance.GetId())
		case common.ToClose:
			log.GetBaseLogger().Infof("ErrCheck circuitbreaker: halfopen to close, instance:"+
				" Id: %s, Namespace: %s, Service: %s, Host: %s, Port: %v\n",
				instance.GetId(), instance.GetNamespace(), instance.GetService(), instance.GetHost(), instance.GetPort())
			result.InstancesToClose.Add(instance.GetId())
		}
	}
	if result.IsEmpty() {
		return nil, nil
	}
	result.RequestCountAfterHalfOpen = g.halfOpenHandler.GetRequestCountAfterHalfOpen()
	return result, nil
}

// openByCheck 打开了探测，通过探测结果来判断熔断器开启
func (g *CircuitBreaker) closeToOpen(instance model.Instance, now time.Time) bool {
	cbStatus := instance.GetCircuitBreakerStatus()
	if nil != cbStatus && cbStatus.GetStatus() != model.Close {
		return false
	}
	var instanceLocalValue local.InstanceLocalValue
	var ok bool
	instanceLocalValue, ok = instance.(local.InstanceLocalValue)
	if !ok {
		return false
	}
	odStatus := instanceLocalValue.GetActiveDetectStatus()
	//log.GetBaseLogger().Infof("health check status is %v, name %s, instance %s:%d", odStatus, g.Name(), instance.GetHost(), instance.GetPort())
	if odStatus != nil && odStatus.GetStatus() == model.Dead &&
		now.Sub(odStatus.GetStartTime()) <= g.healthCheckCfg.GetInterval() {
		return true
	}
	return false
}

//init 插件注册
func init() {
	plugin.RegisterConfigurablePlugin(&CircuitBreaker{}, nil)
}
