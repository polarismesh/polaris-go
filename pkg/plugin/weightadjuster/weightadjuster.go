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

package weightadjuster

import (
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

//WeightAdjuster 【扩展点接口】动态权重调整接口
type WeightAdjuster interface {
	plugin.Plugin
	//实时上报健康状态，并判断是否需要立刻进行动态权重调整，用于流量削峰
	RealTimeAdjustDynamicWeight(model.InstanceGauge) (bool, error)
	// 进行动态权重调整，返回调整后的动态权重
	TimingAdjustDynamicWeight(service model.ServiceInstances) ([]*model.InstanceWeight, error)
}

//初始化
func init() {
	plugin.RegisterPluginInterface(common.TypeWeightAdjuster, new(WeightAdjuster))
}
