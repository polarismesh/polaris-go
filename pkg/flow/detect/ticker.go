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

package detect

import (
	"context"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/flow/data"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/local"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/healthcheck"
	"github.com/polarismesh/polaris-go/pkg/plugin/localregistry"
	"sync"
	"sync/atomic"
	"time"
)

//创建健康检查的回调
func NewHealthCheckCallBack(cfg config.Configuration, supplier plugin.Supplier) (*HealthCheckCallBack, error) {
	var err error
	callback := &HealthCheckCallBack{
		mutex: &sync.Mutex{},
	}
	if callback.healthCheckers, err = data.GetHealthCheckers(cfg, supplier); nil != err {
		return nil, err
	}
	if callback.registry, err = data.GetRegistry(cfg, supplier); nil != err {
		return nil, err
	}
	callback.healthCheckConfig = cfg.GetConsumer().GetHealthCheck()
	return callback, nil
}

//健康探测回调任务
type HealthCheckCallBack struct {
	//探活器
	healthCheckers []healthcheck.HealthChecker
	//本地缓存
	registry localregistry.LocalRegistry
	//健康检查配置
	healthCheckConfig config.HealthCheckConfig
	//修改pendingInstances的锁
	mutex *sync.Mutex
	//正在执行健康检查的实例
	pendingInstances model.HashSet
	//任务取消函数
	taskWorkerCancel context.CancelFunc
	//任务队列
	taskChannels []chan model.Instance
	//任务下标
	taskIndex int64
}

const channelBuffer = 100

//执行任务
func (c *HealthCheckCallBack) Process(
	taskKey interface{}, taskValue interface{}, lastProcessTime time.Time) model.TaskResult {
	svc := taskKey.(model.ServiceKey)
	svcInstances := c.registry.GetInstances(&svc, false, true)
	if !svcInstances.IsInitialized() || len(svcInstances.GetInstances()) == 0 {
		return model.CONTINUE
	}
	err := c.doHealthCheckService(svcInstances)
	if nil != err {
		log.GetDetectLogger().Errorf("fail to update instances for %v, error: %v", svc, err)
		return model.CONTINUE
	}
	return model.CONTINUE
}

//OnTaskEvent 任务事件回调
func (c *HealthCheckCallBack) OnTaskEvent(event model.TaskEvent) {
	switch event {
	case model.EventStart:
		c.mutex.Lock()
		c.pendingInstances = model.HashSet{}
		c.taskIndex = 0
		c.mutex.Unlock()
		var taskWorkerCtx context.Context
		c.taskChannels = make([]chan model.Instance, 0, c.healthCheckConfig.GetConcurrency())
		taskWorkerCtx, c.taskWorkerCancel = context.WithCancel(context.Background())
		for i := 0; i < c.healthCheckConfig.GetConcurrency(); i++ {
			taskChan := make(chan model.Instance, channelBuffer)
			c.taskChannels = append(c.taskChannels, taskChan)
			go c.healthCheckLoop(taskChan, taskWorkerCtx)
		}
	case model.EventStop:
		c.taskWorkerCancel()
	}
}

func (c *HealthCheckCallBack) healthCheckLoop(taskChannel chan model.Instance, taskWorkerCtx context.Context) {
	for {
		select {
		case <-taskWorkerCtx.Done():
			log.GetDetectLogger().Infof("[HealthCheck] detect task has stopped")
			return
		case instance := <-taskChannel:
			err := c.processHealthCheck(&model.ServiceKey{
				Namespace: instance.GetNamespace(),
				Service:   instance.GetService(),
			}, instance)
			if nil != err {
				log.GetDetectLogger().Infof("[HealthCheck] fail to do health check, err is %v", err)
			}
		}
	}
}

func (c *HealthCheckCallBack) processHealthCheck(svc *model.ServiceKey, instance model.Instance) error {
	if !instance.IsHealthy() {
		// 不健康的实例，不进行探活
		return nil
	}
	when := c.healthCheckConfig.GetWhen()
	cbStatus := instance.GetCircuitBreakerStatus()
	if (cbStatus == nil || cbStatus.GetStatus() != model.Open) && when != config.HealthCheckAlways {
		return nil
	}
	success, curTime := c.doConcurrentHealthCheck(instance)
	var status model.HealthCheckStatus
	if success {
		status = model.Healthy
	} else {
		status = model.Dead
	}
	// 构造请求，更新探测结果
	updateRequest := &localregistry.ServiceUpdateRequest{
		ServiceKey: *svc,
		Properties: []localregistry.InstanceProperties{
			{
				ID:      instance.GetId(),
				Service: svc,
				Properties: map[string]interface{}{localregistry.PropertyHealthCheckStatus: &healthCheckingStatus{
					status:    status,
					startTime: curTime,
				}},
			},
		},
	}
	log.GetDetectLogger().Infof("[HealthCheck] detect UpdateRequest, request is %s", updateRequest)
	return c.registry.UpdateInstances(updateRequest)
}

func (c *HealthCheckCallBack) doConcurrentHealthCheck(instance model.Instance) (bool, time.Time) {
	curTime := time.Now()
	for _, checker := range c.healthCheckers {
		result, err := checker.DetectInstance(instance)
		if err != nil {
			log.GetDetectLogger().Errorf("[HealthCheck] timing_flow healthCheck Err:%s", err.Error())
			continue
		}
		if result == nil {
			continue
		}
		// all success = success
		if !result.IsSuccess() {
			return false, result.GetDetectTime()
		}
		curTime = result.GetDetectTime()
	}
	return true, curTime
}

// doHealthCheckService 对一组服务进行探活逻辑
func (c *HealthCheckCallBack) doHealthCheckService(svcInstances model.ServiceInstances) error {
	if len(c.healthCheckers) == 0 || len(svcInstances.GetInstances()) == 0 {
		return nil
	}
	// 保存探活的instance
	configWhen := c.healthCheckConfig.GetWhen()
	for _, oneInstance := range svcInstances.GetInstances() {
		if !oneInstance.IsHealthy() || oneInstance.IsIsolated() || oneInstance.GetWeight() == 0 {
			// 不健康, 隔离以及权重为0的实例，不进行探活
			continue
		}
		cbStatus := oneInstance.GetCircuitBreakerStatus()
		if configWhen == config.HealthCheckOnRecover && (cbStatus == nil || cbStatus.GetStatus() != model.Open) {
			// detect instance when failure
			continue
		}
		var instanceLocalValue local.InstanceLocalValue
		instanceLocalValue, ok := oneInstance.(local.InstanceLocalValue)
		if !ok {
			continue
		}
		activeDetectStatus := instanceLocalValue.GetActiveDetectStatus()
		if nil != activeDetectStatus && time.Since(activeDetectStatus.GetStartTime()) < c.healthCheckConfig.GetInterval() {
			continue
		}
		for i := 0; i < 5; i++ {
			var success bool
			nextIdx := atomic.AddInt64(&c.taskIndex, 1)
			select {
			case c.taskChannels[int(nextIdx)%c.healthCheckConfig.GetConcurrency()] <- oneInstance:
				success = true
			default:
				break
			}
			if success {
				break
			}
		}
	}
	return nil
}
