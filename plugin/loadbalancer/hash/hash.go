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

package hash

import (
	"github.com/polarismesh/polaris-go/pkg/algorithm/hash"
	"github.com/polarismesh/polaris-go/pkg/algorithm/search"
	mconfig "github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/loadbalancer"
	lbcommon "github.com/polarismesh/polaris-go/plugin/loadbalancer/common"
)

// LoadBalancer hash负载均衡插件
type LoadBalancer struct {
	*plugin.PluginBase
	cfg      *Config
	hashFunc hash.HashFuncWithSeed
}

// Type 插件类型
func (g *LoadBalancer) Type() common.Type {
	return common.TypeLoadBalancer
}

// Name 插件名，一个类型下插件名唯一
func (g *LoadBalancer) Name() string {
	return mconfig.DefaultLoadBalancerHash
}

// Init 初始化插件
func (g *LoadBalancer) Init(ctx *plugin.InitContext) error {
	g.PluginBase = plugin.NewPluginBase(ctx)
	var err error
	g.cfg = ctx.Config.GetConsumer().GetLoadbalancer().GetPluginConfig(g.Name()).(*Config)
	g.hashFunc, err = hash.GetHashFunc(g.cfg.HashFunction)
	if err != nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, err, "fail to init hashFunc")
	}
	return nil
}

// Destroy 销毁插件，可用于释放资源
func (g *LoadBalancer) Destroy() error {
	return nil
}

// ChooseInstance 获取单个服务实例
func (g *LoadBalancer) ChooseInstance(criteria *loadbalancer.Criteria,
	inputInstances model.ServiceInstances) (model.Instance, error) {
	if len(criteria.DynamicWeight) > 0 {
		return g.chooseInstanceWithDynamicWeight(criteria, inputInstances)
	}
	targetInstances, err := lbcommon.SelectAvailableInstanceSetFromCriteria(criteria, inputInstances)
	if err != nil {
		return nil, err
	}
	hashValue, err := lbcommon.CalcHashValue(criteria, g.hashFunc)
	if err != nil {
		return nil, model.NewSDKError(model.ErrCodeInternalError, err, "fail to cal hash value")
	}
	targetValue := hashValue % uint64(targetInstances.TotalWeight())
	weightedSlice := targetInstances
	// 按照权重区间来寻找
	targetIndex := search.BinarySearch(weightedSlice, uint64(targetValue))
	instanceIndex := targetInstances.GetInstances()[targetIndex]

	instance := inputInstances.GetServiceClusters().GetServiceInstances().GetInstances()[instanceIndex.Index]

	return instance, nil
}

// TODO: 更新逻辑
// chooseInstanceWithDynamicWeight 使用动态权重进行基于hash的权重选择
func (g *LoadBalancer) chooseInstanceWithDynamicWeight(criteria *loadbalancer.Criteria,
	inputInstances model.ServiceInstances) (model.Instance, error) {
	instances := inputInstances.GetInstances()
	if len(instances) == 0 {
		return nil, model.NewSDKError(model.ErrCodeAPIInstanceNotFound, nil,
			"no instance found for %s:%s", inputInstances.GetNamespace(), inputInstances.GetService())
	}

	dynamicWeights := criteria.DynamicWeight
	hashValue, err := lbcommon.CalcHashValue(criteria, g.hashFunc)
	if err != nil {
		return nil, model.NewSDKError(model.ErrCodeInternalError, err, "fail to cal hash value")
	}

	// 计算总权重
	totalWeight := 0
	for _, instance := range instances {
		totalWeight += getWeight(dynamicWeights, instance)
	}
	if totalWeight == 0 {
		return nil, model.NewSDKError(model.ErrCodeAPIInstanceNotFound, nil,
			"all instances weight 0 for %s:%s", inputInstances.GetNamespace(), inputInstances.GetService())
	}

	// 按hash值映射到权重区间
	targetValue := hashValue % uint64(totalWeight)
	accumulate := uint64(0)
	for _, instance := range instances {
		accumulate += uint64(getWeight(dynamicWeights, instance))
		if targetValue < accumulate {
			return instance, nil
		}
	}

	// 兜底
	return instances[0], nil
}

// getWeight 获取实例权重，优先使用动态权重
func getWeight(dynamicWeights map[string]*model.InstanceWeight, instance model.Instance) int {
	if len(dynamicWeights) == 0 {
		return instance.GetWeight()
	}
	if w, ok := dynamicWeights[instance.GetId()]; ok {
		return int(w.DynamicWeight)
	}
	return instance.GetWeight()
}

// init 注册插件
func init() {
	plugin.RegisterConfigurablePlugin(&LoadBalancer{}, &Config{})
}
