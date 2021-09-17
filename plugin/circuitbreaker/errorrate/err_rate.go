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

package errorrate

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

//CircuitBreaker 基于错误率的默认熔断规则
type CircuitBreaker struct {
	*plugin.PluginBase
	cfg             config.ErrorRateConfig
	halfOpenHandler *common.HalfOpenConversionHandler
}

//Type 插件类型
func (g *CircuitBreaker) Type() common2.Type {
	return common2.TypeCircuitBreaker
}

//Name 插件名，一个类型下插件名唯一
func (g *CircuitBreaker) Name() string {
	return config.DefaultCircuitBreakerErrRate
}

//Init 初始化插件
func (g *CircuitBreaker) Init(ctx *plugin.InitContext) error {
	g.PluginBase = plugin.NewPluginBase(ctx)
	g.cfg = ctx.Config.GetConsumer().GetCircuitBreaker().GetErrorRateConfig()
	ctx.Plugins.RegisterEventSubscriber(common2.OnInstanceLocalValueCreated, common2.PluginEventHandler{
		Callback: g.generateSliceWindow,
	})
	g.halfOpenHandler = common.NewHalfOpenConversionHandler(ctx.Config)
	return nil
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
	//错误率统计窗口下标
	metricIdxErrRate = iota
	//半开统计窗口下标
	metricIdxHalfOpen
	//最大窗口下标
	metricIdxMax
)

//统计维度
const (
	//总请求数
	keyRequestCount = iota
	//错误数
	keyFailCount
	//总统计维度
	maxDimension
)

var (
	addMetricWindow = func(gauge model.InstanceGauge, bucket *metric.Bucket) int64 {
		bucket.AddMetric(keyRequestCount, 1)
		if gauge.GetRetStatus() == model.RetFail {
			bucket.AddMetric(keyFailCount, 1)
		}
		return 0
	}
)

//正常状态下的统计
func (g *CircuitBreaker) regularStat(gauge model.InstanceGauge, metricWindow *metric.SliceWindow) {
	metricWindow.AddGauge(gauge, addMetricWindow)
}

//获取实例的滑窗
func (g *CircuitBreaker) getSliceWindows(instance model.Instance) []*metric.SliceWindow {
	instanceInProto := instance.(*pb.InstanceInProto)
	return instanceInProto.GetSliceWindows(g.ID())
}

//实时上报健康状态并进行失败率统计
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
	g.regularStat(gauge, metricWindows[metricIdxErrRate])
	return false, nil
}

//熔断器从关闭到打开
func (g *CircuitBreaker) closeToOpen(instance model.Instance, metricWindow *metric.SliceWindow, now time.Time) bool {
	cbStatus := instance.GetCircuitBreakerStatus()
	if nil != cbStatus && cbStatus.GetStatus() != model.Close {
		return false
	}
	//统计错误率
	timeRange := &metric.TimeRange{
		Start: now.Add(0 - g.cfg.GetMetricStatTimeWindow()),
		End:   now.Add(metricWindow.GetBucketInterval()),
	}
	values := metricWindow.CalcMetricsInMultiDimensions([]int{keyRequestCount, keyFailCount}, timeRange)
	reqCount := values[0]
	failCount := values[1]
	if reqCount == 0 || reqCount < int64(g.cfg.GetRequestVolumeThreshold()) {
		//未达到其实请求数阈值
		return false
	}
	failRatio := float64(failCount) / float64(reqCount)
	errRateThreshold := ToErrorRateThreshold(g.cfg.GetErrorRatePercent())
	if failRatio >= errRateThreshold {
		//错误率达标
		log.GetDetectLogger().Infof(
			"closeToOpen %s: instance(id=%s, address=%s:%d) match condition for failRatio=%.2f(threshold=%.2f)",
			g.Name(), instance.GetId(), instance.GetHost(), instance.GetPort(), failRatio, errRateThreshold)
		return true
	}
	return false
}

//转换成熔断错误率阈值
func ToErrorRateThreshold(errorRatePercent int) float64 {
	return float64(errorRatePercent) / 100
}

//生成滑窗
func (g *CircuitBreaker) generateSliceWindow(event *common2.PluginEvent) error {
	localValue := event.EventObject.(*local.DefaultInstanceLocalValue)
	metricWindows := make([]*metric.SliceWindow, metricIdxMax)
	metricWindows[metricIdxErrRate] = metric.NewSliceWindow(g.Name(),
		g.cfg.GetMetricNumBuckets(), g.cfg.GetBucketInterval(), maxDimension, clock.GetClock().Now().UnixNano())
	metricWindows[metricIdxHalfOpen] = g.halfOpenHandler.CreateHalfOpenMetricWindow(g.Name())
	localValue.SetSliceWindows(g.ID(), metricWindows)
	return nil
}

//定期进行熔断计算，返回需要进行状态转换的实例ID
//入参包括全量服务实例，以及当前周期的健康探测结果
func (g *CircuitBreaker) CircuitBreak(instances []model.Instance) (*circuitbreaker.Result, error) {
	result := circuitbreaker.NewCircuitBreakerResult(clock.GetClock().Now())
	for _, instance := range instances {
		metricWindows := g.getSliceWindows(instance)
		if g.closeToOpen(instance, metricWindows[metricIdxErrRate], result.Now) {
			log.GetBaseLogger().Warnf("ErrRate circuitbreaker: close to open, instance:"+
				" Id: %s, Namespace: %s, Service: %s, Host: %s, Port: %v\n",
				instance.GetId(), instance.GetNamespace(), instance.GetService(), instance.GetHost(), instance.GetPort())
			result.InstancesToOpen.Add(instance.GetId())
			continue
		}
		if g.halfOpenHandler.OpenToHalfOpen(instance, result.Now, g.Name()) {
			log.GetBaseLogger().Infof("ErrRate circuitbreaker: open to halfopen, instance:"+
				" Id: %s, Namespace: %s, Service: %s, Host: %s, Port: %v\n",
				instance.GetId(), instance.GetNamespace(), instance.GetService(), instance.GetHost(), instance.GetPort())
			result.InstancesToHalfOpen.Add(instance.GetId())
			continue
		}

		halfOpenChange := g.halfOpenHandler.HalfOpenConversion(result.Now, instance, g.Name())
		switch halfOpenChange {
		case common.ToOpen:
			log.GetBaseLogger().Warnf("ErrRate circuitbreaker: halfopen to open, instance:"+
				" Id: %s, Namespace: %s, Service: %s, Host: %s, Port: %v\n",
				instance.GetId(), instance.GetNamespace(), instance.GetService(), instance.GetHost(), instance.GetPort())
			result.InstancesToOpen.Add(instance.GetId())
		case common.ToClose:
			log.GetBaseLogger().Infof("ErrRate circuitbreaker: halfopen to close, instance:"+
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

//init 插件注册
func init() {
	plugin.RegisterConfigurablePlugin(&CircuitBreaker{}, &Config{})
}
