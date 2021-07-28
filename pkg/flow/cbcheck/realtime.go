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
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/modern-go/reflect2"
)

//实时熔断任务
type RealTimeLimitTask struct {
	//服务信息
	SvcKey model.ServiceKey
	//实例ID
	InstID string
	//机器IP
	Host string
	//端口号
	Port int
	//熔断器名字
	CbName string
}

//创建实时熔断任务
func NewCircuitBreakRealTimeCallBack(
	callBack *CircuitBreakCallBack, task *RealTimeLimitTask) *CircuitBreakRealTimeCallBack {
	return &CircuitBreakRealTimeCallBack{
		commonCallBack: callBack,
		task:           task,
	}
}

//实时熔断任务回调
type CircuitBreakRealTimeCallBack struct {
	commonCallBack *CircuitBreakCallBack
	task           *RealTimeLimitTask
}

//处理实时任务
func (c *CircuitBreakRealTimeCallBack) Process() {
	svcInstances := c.commonCallBack.registry.GetInstances(&c.task.SvcKey, false, true)
	if !svcInstances.IsInitialized() {
		return
	}
	svcInstanceInProto := svcInstances.(*pb.ServiceInstancesInProto)
	instance := svcInstanceInProto.GetInstance(c.task.InstID)
	if reflect2.IsNil(instance) {
		return
	}
	request, err := c.commonCallBack.doCircuitBreakForService(c.task.SvcKey, nil, instance, c.task.CbName)
	var resultStr = "nil"
	if nil != request {
		resultStr = request.String()
	}
	if nil != err {
		log.GetDetectLogger().Errorf("fail to do realtime circuitBreak check for %s, result is %s, error: %v",
			c.task.SvcKey, resultStr, err)
		return
	}
	log.GetDetectLogger().Infof(
		"success to realtime circuitBreak check for %s, result is %s", c.task.SvcKey, resultStr)
}
