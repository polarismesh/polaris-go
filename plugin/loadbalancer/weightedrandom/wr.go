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
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/loadbalancer"
	lbcommon "github.com/polarismesh/polaris-go/plugin/loadbalancer/common"
)

// WRLoadBalancer weightedrandom负载均衡插件
type WRLoadBalancer struct {
	*plugin.PluginBase
	scalableRand *rand.ScalableRand
	log          *log.ContextLogger
}

// Type 插件类型
func (g *WRLoadBalancer) Type() common.Type {
	return common.TypeLoadBalancer
}

// Name 插件名，一个类型下插件名唯一
func (g *WRLoadBalancer) Name() string {
	return config.DefaultLoadBalancerWR
}

// Init 初始化插件
func (g *WRLoadBalancer) Init(ctx *plugin.InitContext) error {
	g.log = ctx.ValueCtx.GetContextLogger()
	g.PluginBase = plugin.NewPluginBase(ctx)
	g.scalableRand = rand.NewScalableRand()
	g.log.GetBaseLogger().Infof("weightedrandom load balancer initialized")
	return nil
}

// Destroy 销毁插件，可用于释放资源
func (g *WRLoadBalancer) Destroy() error {
	return nil
}

// ChooseInstance 获取单个服务实例
func (g *WRLoadBalancer) ChooseInstance(criteria *loadbalancer.Criteria,
	svcInstances model.ServiceInstances) (model.Instance, error) {
	// 当存在动态权重时，使用动态权重进行权重随机选择
	if len(criteria.DynamicWeight) > 0 {
		g.log.GetBaseLogger().Debugf("[WRLoadBalancer] ChooseInstance using dynamic weight path, dynamicWeight "+
			"entries: %d", len(criteria.DynamicWeight))
		return g.chooseInstanceWithDynamicWeight(criteria, svcInstances)
	}
	g.log.GetBaseLogger().Debugf("[WRLoadBalancer] ChooseInstance using static weight path")
	return g.clusterBasedChooseInstance(criteria.IgnoreHalfOpen, criteria.Cluster, svcInstances.GetServiceClusters())
}

// 基于集群进行负载均衡选择，性能最高
func (g *WRLoadBalancer) clusterBasedChooseInstance(ignoreHalfOpen bool,
	cluster *model.Cluster, svcClusters model.ServiceClusters) (model.Instance, error) {
	clusterValue := cluster.GetClusterValue()
	var instance model.Instance
	svcInstances := svcClusters.GetServiceInstances()
	targetInstances := lbcommon.SelectAvailableInstanceSet(clusterValue, cluster.HasLimitedInstances,
		cluster.IncludeHalfOpen)
	if targetInstances.TotalWeight() == 0 {
		return nil, model.NewSDKError(model.ErrCodeAPIInstanceNotFound, nil,
			"instances of %s in cluster %s all weight 0 (instance count %d) in load balance, includeHalfOpen: %v",
			svcClusters.GetServiceKey(), *cluster, targetInstances.Count(), cluster.IncludeHalfOpen)
	}
	g.log.GetBaseLogger().Debugf("[WRLoadBalancer] clusterBasedChooseInstance service %s, "+
		"available instances: %d, totalWeight: %d",
		svcClusters.GetServiceKey(), targetInstances.Count(), targetInstances.TotalWeight())
	// 优化进行随机半开节点的分配
	instance = g.clusterBasedSelectWeightedInstance(svcInstances, targetInstances)
	if nil == instance {
		// 一般不会走到这一步，除非BUG，这里只是做个预案
		g.log.GetBaseLogger().Warnf("[WRLoadBalancer] clusterBasedSelectWeightedInstance returned nil, using random "+
			"fallback for service %s", svcClusters.GetServiceKey())
		selector := g.getSelector(targetInstances.Count())
		instanceIndex := targetInstances.GetInstances()[selector]
		instance = svcInstances.GetInstances()[instanceIndex.Index]
	}
	return instance, nil
}

// 获取随机数的selector
func (g *WRLoadBalancer) getSelector(totalWeight int) int {
	return g.scalableRand.Intn(totalWeight)
}

// 基于集群进行权重随机选择合适权重的服务实例
func (g *WRLoadBalancer) clusterBasedSelectWeightedInstance(
	svcInstances model.ServiceInstances, instances *model.InstanceSet) model.Instance {
	index := rand.SelectWeightedRandItem(g.scalableRand, instances)
	if index >= 0 {
		instanceIndex := instances.GetInstances()[index]
		return svcInstances.GetInstances()[instanceIndex.Index]
	}
	return nil
}

// init 注册插件
func init() {
	plugin.RegisterPlugin(&WRLoadBalancer{})
}
