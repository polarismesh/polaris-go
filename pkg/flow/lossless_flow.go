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

package flow

import (
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
)

// SyncLosslessRegister 同步进行服务注册
func (e *Engine) SyncLosslessRegister(instance *model.InstanceRegisterRequest) (*model.InstanceRegisterResponse,
	error) {
	// 当lossless为nil时, 说明本地未开启无损上下线功能插件, 直接注册
	if e.lossless == nil {
		log.GetBaseLogger().Infof("[Lossless Event] SyncLosslessRegister lossless is not enable, register directly")
		return e.SyncRegister(instance)
	}
	// 获取无损上线规则
	losslessRule, err := e.SyncGetServiceRule(model.EventLossless, &model.GetServiceRuleRequest{
		Namespace: instance.Namespace,
		Service:   instance.Service,
	})
	if err != nil {
		log.GetBaseLogger().Errorf("[Lossless Event] SyncLosslessRegister SyncGetServiceRule error: %v", err)
		return nil, err
	}
	log.GetBaseLogger().Infof("[Lossless Event] SyncLosslessRegister SyncGetServiceRule success: %v",
		model.JsonString(losslessRule))
	// 规则解析
	e.lossless.PreProcess(instance, losslessRule)
	// 无损上线
	resp, err := e.lossless.Process()
	if err != nil {
		log.GetBaseLogger().Errorf("[Lossless Event] SyncLosslessRegister SyncRegister error: %v", err)
		return resp, err
	}
	// 服务预热事件上报
	e.lossless.PostProcess()
	return resp, nil
}
