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

//hash负载均衡插件
type LoadBalancer struct {
	*plugin.PluginBase
	cfg      *Config
	hashFunc hash.HashFuncWithSeed
}

//Type 插件类型
func (g *LoadBalancer) Type() common.Type {
	return common.TypeLoadBalancer
}

//Name 插件名，一个类型下插件名唯一
func (g *LoadBalancer) Name() string {
	return mconfig.DefaultLoadBalancerHash
}

//Init 初始化插件
func (g *LoadBalancer) Init(ctx *plugin.InitContext) error {
	g.PluginBase = plugin.NewPluginBase(ctx)
	var err error
	g.cfg = ctx.Config.GetConsumer().GetLoadbalancer().GetPluginConfig(g.Name()).(*Config)
	g.hashFunc, err = hash.GetHashFunc(g.cfg.HashFunction)
	if nil != err {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, err, "fail to init hashFunc")
	}
	return nil
}

//Destroy 销毁插件，可用于释放资源
func (g *LoadBalancer) Destroy() error {
	return nil
}

//ChooseInstance 获取单个服务实例
func (g *LoadBalancer) ChooseInstance(criteria *loadbalancer.Criteria,
	inputInstances model.ServiceInstances) (model.Instance, error) {
	cluster := criteria.Cluster
	svcClusters := inputInstances.GetServiceClusters()
	clusterValue := cluster.GetClusterValue()
	var instance model.Instance
	svcInstances := svcClusters.GetServiceInstances()
	targetInstances := lbcommon.SelectAvailableInstanceSet(clusterValue, cluster.HasLimitedInstances,
		cluster.IncludeHalfOpen)
	if targetInstances.TotalWeight() == 0 {
		return nil, model.NewSDKError(model.ErrCodeAPIInstanceNotFound, nil,
			"instances of %s in cluster %s all weight 0 (instance count %d) in load balance",
			svcClusters.GetServiceKey(), *cluster, targetInstances.Count())
	}
	hashValue, err := lbcommon.CalcHashValue(criteria, g.hashFunc)
	if nil != err {
		return nil, model.NewSDKError(model.ErrCodeInternalError, err, "fail to cal hash value")
	}
	targetValue := hashValue % uint64(targetInstances.TotalWeight())
	weightedSlice := targetInstances
	//按照权重区间来寻找
	targetIndex := search.BinarySearch(weightedSlice, uint64(targetValue))
	instanceIndex := targetInstances.GetInstances()[targetIndex]
	instance = svcInstances.GetInstances()[instanceIndex.Index]
	return instance, nil
}

//init 注册插件
func init() {
	plugin.RegisterConfigurablePlugin(&LoadBalancer{}, &Config{})
}
