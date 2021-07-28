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
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/flow/data"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/localregistry"
	"github.com/polarismesh/polaris-go/pkg/plugin/outlierdetection"
	"time"
)

//创建健康检查的回调
func NewOutlierDetectCallBack(cfg config.Configuration, supplier plugin.Supplier) (*OutlierDetectCallBack, error) {
	var err error
	callback := &OutlierDetectCallBack{}
	if callback.outlierDetectionChain, err = data.GetOutlierDetectionChain(cfg, supplier); nil != err {
		return nil, err
	}
	if callback.registry, err = data.GetRegistry(cfg, supplier); nil != err {
		return nil, err
	}
	callback.interval = cfg.GetConsumer().GetOutlierDetectionConfig().GetCheckPeriod()
	return callback, nil
}

//健康探测回调任务
type OutlierDetectCallBack struct {
	//探活器
	outlierDetectionChain []outlierdetection.OutlierDetector
	//本地缓存
	registry localregistry.LocalRegistry
	//轮询间隔
	interval time.Duration
}

//执行任务
func (c *OutlierDetectCallBack) Process(
	taskKey interface{}, taskValue interface{}, lastProcessTime time.Time) model.TaskResult {
	svc := taskKey.(model.ServiceKey)
	svcInstances := c.registry.GetInstances(&svc, false, true)
	if !svcInstances.IsInitialized() || len(svcInstances.GetInstances()) == 0 {
		return model.CONTINUE
	}
	err := c.doOutlierDetectionService(svc, svcInstances)
	if nil != err {
		log.GetDetectLogger().Errorf("fail to update instances for %v, error: %v", svc, err)
		return model.CONTINUE
	}
	return model.CONTINUE
}

// doOutlierDetectionService 对一组服务进行探活逻辑
func (c *OutlierDetectCallBack) doOutlierDetectionService(
	svc model.ServiceKey, svcInstances model.ServiceInstances) error {
	if len(c.outlierDetectionChain) == 0 || len(svcInstances.GetInstances()) == 0 {
		return nil
	}
	// 保存探活的instance
	aliveInstance := make([]common.DetectResult, 0, len(svcInstances.GetInstances()))
	for _, oneInstance := range svcInstances.GetInstances() {
		if !oneInstance.IsHealthy() {
			// 不健康的实例，不进行探活
			continue
		}
		cbStatus := oneInstance.GetCircuitBreakerStatus()
		if cbStatus == nil {
			continue
		}
		if cbStatus.GetStatus() != model.Open {
			// 没有被熔断的svr，不进行探活
			continue
		}
		res := c.doOutlierDetectionInstance(svc, oneInstance)
		if res != nil && res.GetRetStatus() == model.RetSuccess {
			aliveInstance = append(aliveInstance, res)
		}
	}
	if len(aliveInstance) == 0 {
		return nil
	}
	// 构造请求，更新探测结果
	updateRequest := &localregistry.ServiceUpdateRequest{
		ServiceKey: svc,
	}
	updateRequest.Properties = make([]localregistry.InstanceProperties, 0, len(aliveInstance))
	for _, res := range aliveInstance {
		if res.GetDetectInstance() == nil {
			log.GetDetectLogger().Errorf("Detect Result Err")
			continue
		}
		instID := res.GetDetectInstance().GetId()
		updateRequest.Properties = append(updateRequest.Properties, localregistry.InstanceProperties{
			ID:      instID,
			Service: &updateRequest.ServiceKey,
			Properties: map[string]interface{}{localregistry.PropertyOutlierDetectorStatus: &outlierDetectorStatus{
				status:    model.Healthy,
				startTime: res.GetDetectTime(),
			}},
		})
	}
	log.GetDetectLogger().Infof("Detect UpdateRequest, request is %s", updateRequest)
	err := c.registry.UpdateInstances(updateRequest)
	if err != nil {
		return err
	}
	return nil
}

// doOutlierDetectionService 对一组服务中的一个服务实例进行探活逻辑
// 探测成功返回result，否则返回nil
func (c *OutlierDetectCallBack) doOutlierDetectionInstance(
	svc model.ServiceKey, oneInstance model.Instance) common.DetectResult {
	if len(c.outlierDetectionChain) == 0 {
		return nil
	}
	for _, outlierDetection := range c.outlierDetectionChain {
		result, err := outlierDetection.DetectInstance(oneInstance)
		if err != nil {
			log.GetDetectLogger().Errorf("timing_flow OutlierDetection Err:%s", err.Error())
			continue
		}
		if result == nil {
			continue
		}
		if result.GetRetStatus() == model.RetSuccess {
			return result
		}
	}
	return nil
}
