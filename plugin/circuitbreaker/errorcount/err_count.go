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

package errorcount

import (
	"github.com/polarismesh/polaris-go/pkg/clock"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/metric"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/local"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/circuitbreaker"
	common2 "github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/plugin/circuitbreaker/common"
	"time"
)

//熔断器
type CircuitBreaker struct {
	*plugin.PluginBase
	wholeCfg        config.Configuration
	cfg             config.ErrorCountConfig
	halfOpenHandler *common.HalfOpenConversionHandler
}

//Type 插件类型
func (g *CircuitBreaker) Type() common2.Type {
	return common2.TypeCircuitBreaker
}

//Name 插件名，一个类型下插件名唯一
func (g *CircuitBreaker) Name() string {
	return config.DefaultCircuitBreakerErrCount
}

//Init 初始化插件
func (g *CircuitBreaker) Init(ctx *plugin.InitContext) error {
	g.PluginBase = plugin.NewPluginBase(ctx)
	g.wholeCfg = ctx.Config
	g.cfg = ctx.Config.GetConsumer().GetCircuitBreaker().GetErrorCountConfig()
	ctx.Plugins.RegisterEventSubscriber(common2.OnInstanceLocalValueCreated, common2.PluginEventHandler{
		Callback: g.generateSliceWindow,
	})
	g.halfOpenHandler = common.NewHalfOpenConversionHandler(ctx.Config)
	return nil
}

//获取实例的滑窗
func (g *CircuitBreaker) getSliceWindows(instance model.Instance) []*metric.SliceWindow {
	instanceInProto := instance.(*pb.InstanceInProto)
	return instanceInProto.GetSliceWindows(g.ID())
}

//Destroy 销毁插件，可用于释放资源
func (g *CircuitBreaker) Destroy() error {
	return nil
}

// enable
func (g *CircuitBreaker) IsEnable(cfg config.Configuration) bool {
	if cfg.GetGlobal().GetSystem().GetMode() == model.ModeWithAgent {
		return false
	} else {
		return true
	}
}

const (
	//错误数统计窗口下标
	metricIdxErrCount = iota
	//最大窗口下标
	metricIdxMax
)

//统计维度
const (
	//连续错误数
	keyContinuousFailCount = iota + common.MaxHalfOpenDimension
	//总统计维度
	maxDimension
)

var (
	addMetricWindow = func(gauge model.InstanceGauge, bucket *metric.Bucket) int64 {
		var failCount int64
		if gauge.GetRetStatus() == model.RetFail {
			failCount = bucket.AddMetric(keyContinuousFailCount, 1)
		} else {
			//一次成功则重置连续失败次数为0
			bucket.SetMetric(keyContinuousFailCount, 0)
		}
		return failCount
	}
)

//正常状态下的统计
func (g *CircuitBreaker) regularStat(gauge model.InstanceGauge, metricWindow *metric.SliceWindow) bool {
	failCount := metricWindow.AddGauge(gauge, addMetricWindow)
	cfg := g.GetErrorCountConfig(gauge.GetNamespace(), gauge.GetService())
	//只有相同才发，避免发送多次实时任务
	if failCount == int64(cfg.GetContinuousErrorThreshold()) {
		calledInstance := gauge.GetCalledInstance()
		log.GetBaseLogger().Infof("instance(service=%s, namespace=%s, host=%s, port=%d, instanceId=%s) "+
			"stat trigger errCount limit for failCount %v equals to %v",
			gauge.GetService(), gauge.GetNamespace(), calledInstance.GetHost(), calledInstance.GetPort(),
			calledInstance.GetId(), failCount, cfg.GetContinuousErrorThreshold())
		return true
	}
	return false
}

//实时上报健康状态并进行连续失败熔断判断，返回当前实例是否需要进行立即熔断
func (g *CircuitBreaker) Stat(gauge model.InstanceGauge) (bool, error) {
	instance := gauge.GetCalledInstance()
	cbStatus := instance.GetCircuitBreakerStatus()
	if nil != cbStatus && cbStatus.GetStatus() == model.Open {
		//熔断状态不进行统计
		return false, nil
	}
	metricWindows := g.getSliceWindows(gauge.GetCalledInstance())
	if nil != cbStatus && cbStatus.GetStatus() == model.HalfOpen && cbStatus.GetCircuitBreaker() == g.Name() {
		return g.halfOpenHandler.StatHalfOpenCalls(cbStatus, gauge), nil
	}
	return g.regularStat(gauge, metricWindows[metricIdxErrCount]), nil
}

func (g *CircuitBreaker) GetErrorCountConfig(namespace string, service string) config.ErrorCountConfig {
	cfg := g.cfg
	serviceSp := g.wholeCfg.GetConsumer().GetServiceSpecific(namespace, service)
	if serviceSp != nil {
		cfg = serviceSp.GetServiceCircuitBreaker().GetErrorCountConfig()
	}
	return cfg
}

//熔断器从关闭到打开
func (g *CircuitBreaker) closeToOpen(instance model.Instance, metricWindow *metric.SliceWindow, now time.Time) bool {
	cbStatus := instance.GetCircuitBreakerStatus()
	if nil != cbStatus && cbStatus.GetStatus() != model.Close {
		return false
	}
	//统计错误率
	cfg := g.GetErrorCountConfig(instance.GetNamespace(), instance.GetService())
	timeRange := &metric.TimeRange{
		Start: now.Add(0 - cfg.GetMetricStatTimeWindow()),
		End:   now.Add(metricWindow.GetBucketInterval()),
	}
	failCount := metricWindow.CalcMetrics(keyContinuousFailCount, timeRange)
	if log.GetBaseLogger().IsLevelEnabled(log.TraceLog) {
		log.GetBaseLogger().Tracef(
			"failCount to calc closeToOpen is %d for instance %s", failCount, instance.GetId())
	}
	if failCount >= int64(cfg.GetContinuousErrorThreshold()) {
		//达到阈值可进行熔断
		log.GetDetectLogger().Infof(
			"closeToOpen %s: instance(id=%s, address=%s:%d) match condition for failCount=%d(threshold=%d)",
			g.Name(), instance.GetId(), instance.GetHost(), instance.GetPort(),
			failCount, cfg.GetContinuousErrorThreshold())
		return true
	}
	return false
}

//定期或触发式进行熔断计算，返回需要进行状态转换的实例ID
//入参包括全量服务实例，以及当前周期的健康探测结果
func (g *CircuitBreaker) CircuitBreak(instances []model.Instance) (*circuitbreaker.Result, error) {
	result := circuitbreaker.NewCircuitBreakerResult(clock.GetClock().Now())
	for _, instance := range instances {
		log.GetBaseLogger().Tracef("invoke %s circuitBreaker for instance %s", g.Name(), instance.GetId())
		metricWindows := g.getSliceWindows(instance)
		if g.closeToOpen(instance, metricWindows[metricIdxErrCount], result.Now) {
			log.GetDetectLogger().Warnf("ErrCount circuitbreaker: close to open, instance:"+
				" Id: %s, Namespace: %s, Service: %s, Host: %s, Port: %v",
				instance.GetId(), instance.GetNamespace(), instance.GetService(), instance.GetHost(), instance.GetPort())
			result.InstancesToOpen.Add(instance.GetId())
			continue
		}
		if g.halfOpenHandler.OpenToHalfOpen(instance, result.Now, g.Name()) {
			log.GetDetectLogger().Infof("ErrCount circuitbreaker: open to halfopen, instance:"+
				" Id: %s, Namespace: %s, Service: %s, Host: %s, Port: %v",
				instance.GetId(), instance.GetNamespace(), instance.GetService(), instance.GetHost(), instance.GetPort())
			result.InstancesToHalfOpen.Add(instance.GetId())
			continue
		}

		halfOpenChange := g.halfOpenHandler.HalfOpenConversion(result.Now, instance, g.Name())
		switch halfOpenChange {
		case common.ToOpen:
			log.GetDetectLogger().Warnf("ErrCount circuitbreaker: halfopen to open, instance:"+
				" Id: %s, Namespace: %s, Service: %s, Host: %s, Port: %v",
				instance.GetId(), instance.GetNamespace(), instance.GetService(), instance.GetHost(), instance.GetPort())
			result.InstancesToOpen.Add(instance.GetId())
		case common.ToClose:
			log.GetDetectLogger().Infof("ErrCount circuitbreaker: halfopen to close, instance:"+
				" Id: %s, Namespace: %s, Service: %s, Host: %s, Port: %v",
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

//生成滑窗
func (g *CircuitBreaker) generateSliceWindow(event *common2.PluginEvent) error {
	localValue := event.EventObject.(*local.DefaultInstanceLocalValue)
	metricWindows := make([]*metric.SliceWindow, metricIdxMax)
	metricWindows[metricIdxErrCount] = metric.NewSliceWindow(g.Name(),
		g.cfg.GetMetricNumBuckets(), g.cfg.GetBucketInterval(), maxDimension, clock.GetClock().Now().UnixNano())
	//metricWindows[metricIdxHalfOpen] = g.halfOpenHandler.CreateHalfOpenMetricWindow(g.Name())
	localValue.SetSliceWindows(g.ID(), metricWindows)
	return nil
}

//init 插件注册
func init() {
	plugin.RegisterConfigurablePlugin(&CircuitBreaker{}, &Config{})
}
