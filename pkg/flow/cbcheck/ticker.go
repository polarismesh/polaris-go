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

package cbcheck

import (
	"github.com/modern-go/reflect2"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/flow/data"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/circuitbreaker"
	"github.com/polarismesh/polaris-go/pkg/plugin/localregistry"
	"time"
)

//创建定时熔断任务回调
func NewCircuitBreakCallBack(cfg config.Configuration, supplier plugin.Supplier) (*CircuitBreakCallBack, error) {
	var err error
	callBack := &CircuitBreakCallBack{}
	if callBack.registry, err = data.GetRegistry(cfg, supplier); nil != err {
		return nil, err
	}
	if callBack.circuitBreakerChain, err = data.GetCircuitBreakers(cfg, supplier); nil != err {
		return nil, err
	}
	callBack.interval = cfg.GetConsumer().GetCircuitBreaker().GetCheckPeriod()
	return callBack, nil
}

//定时熔断任务回调
type CircuitBreakCallBack struct {
	//熔断器
	circuitBreakerChain []circuitbreaker.InstanceCircuitBreaker
	//本地缓存
	registry localregistry.LocalRegistry
	//轮询间隔
	interval time.Duration
}

//执行任务
func (c *CircuitBreakCallBack) Process(
	taskKey interface{}, taskValue interface{}, lastProcessTime time.Time) model.TaskResult {
	if !lastProcessTime.IsZero() && time.Since(lastProcessTime) < c.interval {
		return model.SKIP
	}
	svc := taskKey.(model.ServiceKey)
	log.GetDetectLogger().Debugf("start to do timing circuitBreak check for %s, "+
		"checkPeriod %v, now is %v", svc, c.interval, time.Now())
	svcInstances := c.registry.GetInstances(&svc, false, true)
	if !svcInstances.IsInitialized() || len(svcInstances.GetInstances()) == 0 {
		log.GetDetectLogger().Infof("instances not initialized for %s", svc)
		return model.CONTINUE
	}
	request, err := c.
		doCircuitBreakForService(svc, svcInstances, nil, "")
	var resultStr = "nil"
	if nil != request {
		resultStr = request.String()
	}
	if nil != err {
		log.GetDetectLogger().Errorf(
			"fail to do timing circuitBreak check for %s, result is %s, error: %v", svc, resultStr, err)
		return model.CONTINUE
	}
	log.GetDetectLogger().Debugf("success to timing circuitBreak check for %s, result is %s", svc, resultStr)
	return model.CONTINUE
}

//OnTaskEvent 任务事件回调
func (c *CircuitBreakCallBack) OnTaskEvent(event model.TaskEvent) {

}

//对服务进行熔断判断操作
func (c *CircuitBreakCallBack) doCircuitBreakForService(svc model.ServiceKey, svcInstances model.ServiceInstances,
	instance model.Instance, cbName string) (*localregistry.ServiceUpdateRequest, error) {
	allResults := make(map[string]*circuitbreaker.Result, 0)
	var instances []model.Instance
	if reflect2.IsNil(instance) {
		instances = svcInstances.GetInstances()
	} else {
		instances = []model.Instance{instance}
	}
	if len(instances) == 0 {
		return nil, nil
	}
	for _, instance := range instances {
		if len(c.circuitBreakerChain) == 0 {
			continue
		}
		for _, circuitBreaker := range c.circuitBreakerChain {
			if len(cbName) > 0 && circuitBreaker.Name() != cbName {
				continue
			}
			result, err := circuitBreaker.CircuitBreak([]model.Instance{instance})
			if nil != err {
				log.GetBaseLogger().Errorf("fail to do timingCircuitBreak %s for %v, instance %s:%d, error: %v",
					circuitBreaker.Name(), svc, instance.GetHost(), instance.GetPort(), err)
				continue
			}
			if nil == result {
				continue
			}
			if lastResult, ok := allResults[circuitBreaker.Name()]; ok {
				lastResult.Merge(result)
			} else {
				allResults[circuitBreaker.Name()] = result
			}
			break
		}
	}
	//批量更新状态
	updateRequest := buildServiceUpdateRequest(svc, allResults)
	if len(updateRequest.Properties) == 0 {
		return nil, nil
	}
	return updateRequest, c.registry.UpdateInstances(updateRequest)
}

//清理实例集合，剔除重复数
func cleanInstanceSet(instanceSet model.HashSet, allInstances model.HashSet) {
	for instID := range instanceSet {
		if allInstances.Contains(instID) {
			instanceSet.Delete(instID)
		} else {
			allInstances.Add(instID)
		}
	}
}

//构建实例更新数据
func buildInstanceProperty(now time.Time, allowedRequests int, instances model.HashSet,
	request *localregistry.ServiceUpdateRequest, cbName string, status model.Status) {
	if len(instances) == 0 {
		return
	}
	for instID := range instances {
		request.Properties = append(request.Properties, localregistry.InstanceProperties{
			ID:      instID.(string),
			Service: &request.ServiceKey,
			Properties: map[string]interface{}{localregistry.PropertyCircuitBreakerStatus: &circuitBreakerStatus{
				circuitBreaker:           cbName,
				status:                   status,
				startTime:                now,
				maxHalfOpenAllowReqTimes: allowedRequests,
				halfOpenQuota:            int32(allowedRequests),
			}},
		})
	}
}

//构造服务更新数据
func buildServiceUpdateRequest(
	svc model.ServiceKey, results map[string]*circuitbreaker.Result) *localregistry.ServiceUpdateRequest {
	request := &localregistry.ServiceUpdateRequest{
		ServiceKey: svc,
	}
	for cbName, result := range results {
		buildInstanceProperty(result.Now, result.RequestCountAfterHalfOpen, result.InstancesToHalfOpen,
			request, cbName, model.HalfOpen)
		buildInstanceProperty(result.Now, result.RequestCountAfterHalfOpen, result.InstancesToOpen,
			request, cbName, model.Open)
		buildInstanceProperty(result.Now, result.RequestCountAfterHalfOpen, result.InstancesToClose,
			request, cbName, model.Close)
	}
	return request
}
