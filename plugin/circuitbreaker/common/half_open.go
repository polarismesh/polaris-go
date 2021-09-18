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

package common

import (
	"github.com/golang/protobuf/proto"
	"github.com/polarismesh/polaris-go/pkg/clock"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model/local"
	"math"
	"time"

	"github.com/polarismesh/polaris-go/pkg/metric"
	"github.com/polarismesh/polaris-go/pkg/model"
)

//半开调用结果统计维度
const (
	//半开成功数
	KeyHalfOpenSuccessCount int = iota
	//半开总请求数
	KeyHalfOpenRequestCount
	//半开错误统计维度总数
	MaxHalfOpenDimension
)

//是否强制隔离，当节点状态为不健康或者隔离时，则不会转换熔断半开状态
func forceIsolate(instance model.Instance) bool {
	return !instance.IsHealthy() || instance.IsIsolated()
}

// HalfOpenConversionHandler 半开状态变更处理器
type HalfOpenConversionHandler struct {
	//熔断器全局配置
	cbCfg config.CircuitBreakerConfig
	//健康检查判断器
	enableHealthCheck bool
}

//创建半开熔断器
func NewHalfOpenConversionHandler(cfg config.Configuration) *HalfOpenConversionHandler {
	handler := &HalfOpenConversionHandler{
		cbCfg:             cfg.GetConsumer().GetCircuitBreaker(),
		enableHealthCheck: cfg.GetConsumer().GetHealthCheck().GetWhen() != config.HealthCheckNever,
	}
	return handler
}

//获取恢复滑桶间隔
func (h *HalfOpenConversionHandler) GetRecoverBucketInterval() time.Duration {
	bucketSize := math.Ceil(float64(h.cbCfg.GetRecoverWindow()) / float64(h.cbCfg.GetRecoverNumBuckets()))
	return time.Duration(bucketSize)
}

//创建半开的统计窗口
func (h *HalfOpenConversionHandler) CreateHalfOpenMetricWindow(name string) *metric.SliceWindow {
	return metric.NewSliceWindow(name, h.cbCfg.GetRecoverNumBuckets(),
		h.GetRecoverBucketInterval(), MaxHalfOpenDimension, clock.GetClock().Now().UnixNano())
}

//halfOpenByCheck 打开了探测，通过探测结果来判断半开
func (h *HalfOpenConversionHandler) halfOpenByCheck(instance model.Instance, startTime time.Time) *bool {
	if !h.enableHealthCheck {
		return nil
	}
	var instanceLocalValue local.InstanceLocalValue
	var ok bool
	instanceLocalValue, ok = instance.(local.InstanceLocalValue)
	if !ok {
		return nil
	}
	odStatus := instanceLocalValue.GetActiveDetectStatus()
	if odStatus != nil && odStatus.GetStatus() == model.Healthy &&
		odStatus.GetStartTime().After(startTime) {
		return proto.Bool(true)
	}
	if odStatus != nil && odStatus.GetStatus() == model.Dead &&
		odStatus.GetStartTime().After(startTime) {
		return proto.Bool(false)
	}
	return nil
}

//OpenToHalfOpen 熔断器从打开到半开
func (h *HalfOpenConversionHandler) OpenToHalfOpen(instance model.Instance, now time.Time, cbName string) bool {
	cbStatus := instance.GetCircuitBreakerStatus()
	if nil == cbStatus || cbStatus.GetCircuitBreaker() != cbName || cbStatus.GetStatus() != model.Open {
		//判断状态以及是否当前熔断器
		return false
	}
	if forceIsolate(instance) {
		return false
	}
	//增加探测结果恢复判断
	startTime := cbStatus.GetStartTime()
	result := h.halfOpenByCheck(instance, startTime)
	if nil != result {
		return *result
	}
	return halfOpenByTimeout(now, startTime, h.cbCfg.GetSleepWindow())
}

//打开了探测，通过探测结果来判断半开
func halfOpenByTimeout(now time.Time, startTime time.Time, sleepWindow time.Duration) bool {
	if now.Sub(startTime) >= sleepWindow {
		//时间窗已经过去, 则恢复半开
		return true
	}
	return false
}

//半开状态日志打印间隔
const halfOpenLogInterval = 30 * time.Second

//统计半开后的请求分配次数
func GetRequestCountAfterHalfOpen(halfOpenWindow *metric.SliceWindow, timeRange *metric.TimeRange) int64 {
	return halfOpenWindow.CalcMetrics(KeyHalfOpenRequestCount, timeRange)
}

const (
	ToClose = iota
	ToOpen
	NoChange
)

//半开状态转换判断逻辑
func (h *HalfOpenConversionHandler) halfOpenConversion(now time.Time, instance model.Instance, cbName string) int {
	cbStatus := instance.GetCircuitBreakerStatus()
	if nil == cbStatus || cbStatus.GetCircuitBreaker() != cbName ||
		cbStatus.GetStatus() != model.HalfOpen {
		//判断状态以及是否当前熔断器，以及是否已经分配完所有的探测配额
		return NoChange
	}
	if forceIsolate(instance) {
		//健康检查失败，直接重新熔断
		return ToOpen
	}
	if cbStatus.GetFailRequestsAfterHalfOpen() > 0 {
		return ToOpen
	}

	//如果还有配额，那么保持状态不变
	//这里不考虑有失败调用的情况，因为出现失败调用的时候，在调用stat接口的时候，该实例就已经转化为熔断了
	if cbStatus.IsAvailable() {
		if now.Sub(cbStatus.GetStartTime()) >= halfOpenLogInterval {
			//半开太久的节点，需要打印日志
			log.GetDetectLogger().Infof("HalfOpenToOpen: instance(id=%s, host=%s, port=%d) halfOpen exceed %v, "+
				"startTime is %v, allocated reqCountAfterHalfOpen %d, reported reqCountAfterHalfOpen %d",
				instance.GetId(), instance.GetHost(), instance.GetPort(), halfOpenLogInterval,
				cbStatus.GetStartTime(), cbStatus.AllocatedRequestsAfterHalfOpen(), cbStatus.GetRequestsAfterHalfOpen())
		}
		//如果还有配额可以分配，那么保持状态
		return NoChange
	}

	allocatedRequests, reportedRequests := cbStatus.AllocatedRequestsAfterHalfOpen(), cbStatus.GetRequestsAfterHalfOpen()
	//在已经无法分配配额的情况下，首先判断分配配额数和上报请求数的数量
	if allocatedRequests > reportedRequests {
		finalAllocTime := cbStatus.GetFinalAllocateTimeInt64()
		//如果在最后的配额分配完了，过了半开周期还没有上报调用，那么认为这个调用失败了，进入熔断状态
		if finalAllocTime != 0 && now.Sub(time.Unix(0, finalAllocTime)) >= h.cbCfg.GetSleepWindow() {
			log.GetDetectLogger().Infof("HalfOpenToOpen: instance(id=%s, host=%s, port=%d) quota not available exceed %v, "+
				"startTime is %v, allocated reqCountAfterHalfOpen %d, reported reqCountAfterHalfOpen %d",
				instance.GetId(), instance.GetHost(), instance.GetPort(), h.cbCfg.GetSleepWindow(),
				cbStatus.GetStartTime(), allocatedRequests, reportedRequests)
			return ToOpen
		}
		//如果分配配额数量没有达到请求数，那么不要改变状态
		return NoChange
	}

	//如果没有失败调用，并且分配请求和上报请求数相同，说明所有调用都成功了，转换为正常状态
	return ToClose
}

//熔断器的半开状态转换
func (h *HalfOpenConversionHandler) HalfOpenConversion(now time.Time, instance model.Instance, cbName string) int {
	return h.halfOpenConversion(now, instance, cbName)
}

//获取半开后分配的请求数
func (h *HalfOpenConversionHandler) GetRequestCountAfterHalfOpen() int {
	return h.cbCfg.GetRequestCountAfterHalfOpen()
}

//统计半开状态的调用量以及成功失败数
//当达到半开次数阈值时，返回true，代表立刻进行状态判断
func (h *HalfOpenConversionHandler) StatHalfOpenCalls(cbStatus model.CircuitBreakerStatus, gauge model.InstanceGauge) bool {
	reqCountAfterHalfOpen := cbStatus.AddRequestCountAfterHalfOpen(1, gauge.GetRetStatus() == model.RetSuccess)
	//如果调用失败，直接恢复成熔断状态
	if gauge.GetRetStatus() != model.RetSuccess && cbStatus.AcquireStatusLock() {
		calledInstance := gauge.GetCalledInstance()
		log.GetBaseLogger().Infof("instance(service=%s, namespace=%s, host=%s, port=%d, instanceId=%s) "+
			"stat trigger halfOpen maxReqCount limit for reqCount %v",
			gauge.GetService(), gauge.GetNamespace(), calledInstance.GetHost(), calledInstance.GetPort(),
			calledInstance.GetId(), reqCountAfterHalfOpen)
		return true
	}
	return false
}
