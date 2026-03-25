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

package weightedrandom

import (
	"github.com/polarismesh/polaris-go/pkg/algorithm/rand"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/loadbalancer"
	lbcommon "github.com/polarismesh/polaris-go/plugin/loadbalancer/common"
)

// dynamicWeightSlice 基于动态权重的累积权重数组，实现 WeightedSlice 接口以支持二分查找
type dynamicWeightSlice struct {
	instances        []model.Instance
	accumulateWeight []uint64
	total            int
}

// GetValue 获取某个下标的累积权重值
func (d *dynamicWeightSlice) GetValue(idx int) uint64 {
	return d.accumulateWeight[idx]
}

// Count 获取数组长度
func (d *dynamicWeightSlice) Count() int {
	return len(d.instances)
}

// TotalWeight 获取总权重
func (d *dynamicWeightSlice) TotalWeight() int {
	return d.total
}

// debugf 在打印 debug 日志前先判断日志级别，避免不必要的参数序列化开销
func (g *WRLoadBalancer) debugf(format string, args ...interface{}) {
	logger := g.log.GetBaseLogger()
	if logger.IsLevelEnabled(log.DebugLog) {
		logger.Debugf(format, args...)
	}
}

// buildDynamicWeightSlice 构建基于动态权重的累积权重数组
func (g *WRLoadBalancer) buildDynamicWeightSlice(dynamicWeights map[string]*model.InstanceWeight,
	instances []model.Instance) *dynamicWeightSlice {
	slice := &dynamicWeightSlice{
		instances:        instances,
		accumulateWeight: make([]uint64, len(instances)),
	}
	accumulate := uint64(0)
	for i, instance := range instances {
		w := g.getWeight(dynamicWeights, instance)
		accumulate += uint64(w)
		slice.accumulateWeight[i] = accumulate
		g.debugf("[WRLoadBalancer] buildDynamicWeightSlice instance %s: weight=%d, accumulate=%d", instance.GetId(), w,
			accumulate)
	}
	slice.total = int(accumulate)
	g.debugf("[WRLoadBalancer] buildDynamicWeightSlice completed: instanceCount=%d, totalWeight=%d", len(instances),
		slice.total)
	return slice
}

// chooseInstanceWithDynamicWeight 使用动态权重进行权重随机选择
func (g *WRLoadBalancer) chooseInstanceWithDynamicWeight(criteria *loadbalancer.Criteria,
	svcInstances model.ServiceInstances) (model.Instance, error) {
	// 通过集群筛选可用实例（与 clusterBasedChooseInstance 对齐）
	cluster := criteria.Cluster
	svcClusters := svcInstances.GetServiceClusters()
	clusterValue := cluster.GetClusterValue()
	targetInstanceSet := lbcommon.SelectAvailableInstanceSet(clusterValue, cluster.HasLimitedInstances,
		cluster.IncludeHalfOpen)
	instances := targetInstanceSet.GetRealInstances()
	if len(instances) == 0 {
		return nil, model.NewSDKError(model.ErrCodeAPIInstanceNotFound, nil, "no instance found for %s in cluster %s",
			svcClusters.GetServiceKey(), *cluster)
	}

	g.debugf("[WRLoadBalancer] chooseInstanceWithDynamicWeight called for service %s, available instances: %d, "+
		"dynamicWeight entries: %d", svcClusters.GetServiceKey(), len(instances), len(criteria.DynamicWeight))

	dynamicWeights := criteria.DynamicWeight
	// 构建基于动态权重的累积权重数组
	weightSlice := g.buildDynamicWeightSlice(dynamicWeights, instances)
	if weightSlice.TotalWeight() == 0 {
		return nil, model.NewSDKError(model.ErrCodeAPIInstanceNotFound, nil,
			"all instances weight 0 for %s in cluster %s", svcClusters.GetServiceKey(), *cluster)
	}

	// 使用与 clusterBasedSelectWeightedInstance 相同的二分查找方式进行选择
	index := rand.SelectWeightedRandItem(g.scalableRand, weightSlice)
	if index >= 0 && index < len(instances) {
		selected := instances[index]
		g.debugf("[WRLoadBalancer] chooseInstanceWithDynamicWeight selected instance %s (index=%d), host=%s, "+
			"port=%d, totalWeight=%d", selected.GetId(), index, selected.GetHost(), selected.GetPort(),
			weightSlice.TotalWeight())
		return selected, nil
	}

	// 兜底：一般不会走到这一步，除非有并发修改，使用真随机选择
	g.log.GetBaseLogger().Warnf("[WRLoadBalancer] chooseInstanceWithDynamicWeight fallback, index: %d, totalWeight: %d",
		index, weightSlice.TotalWeight())
	return instances[g.scalableRand.Intn(len(instances))], nil
}

// getWeight 获取实例权重，优先使用动态权重
func (g *WRLoadBalancer) getWeight(dynamicWeights map[string]*model.InstanceWeight, instance model.Instance) int {
	if len(dynamicWeights) == 0 {
		return instance.GetWeight()
	}
	if w, ok := dynamicWeights[instance.GetId()]; ok {
		g.debugf("[WRLoadBalancer] getWeight instance %s using dynamic weight: %d (base: %d)", instance.GetId(),
			w.DynamicWeight, w.BaseWeight)
		return int(w.DynamicWeight)
	}
	g.debugf("[WRLoadBalancer] getWeight instance %s using static weight: %d (no dynamic weight found)",
		instance.GetId(), instance.GetWeight())
	return instance.GetWeight()
}
