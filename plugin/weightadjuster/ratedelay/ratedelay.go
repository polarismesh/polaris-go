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

package ratedelay

import (
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

//Adjuster 根据错误率和时延来进行动态权重调整
type Adjuster struct {
	*plugin.PluginBase
}

//Type 插件类型
func (g *Adjuster) Type() common.Type {
	return common.TypeWeightAdjuster
}

//Name 插件名，一个类型下插件名唯一
func (g *Adjuster) Name() string {
	return "rateDelayAdjuster"
}

//Init 初始化插件
func (g *Adjuster) Init(ctx *plugin.InitContext) error {
	g.PluginBase = plugin.NewPluginBase(ctx)
	return nil
}

//Destroy 销毁插件，可用于释放资源
func (g *Adjuster) Destroy() error {
	return nil
}

//实时上报健康状态，并判断是否需要立刻进行动态权重调整，用于流量削峰
func (g *Adjuster) RealTimeAdjustDynamicWeight(model.InstanceGauge) (bool, error) {
	return false, nil
}

// 进行动态权重调整，返回调整后的动态权重
func (g *Adjuster) TimingAdjustDynamicWeight(service model.ServiceInstances) ([]*model.InstanceWeight, error) {
	return nil, nil
}

//init 注册插件
func init() {
	plugin.RegisterPlugin(&Adjuster{})
}
