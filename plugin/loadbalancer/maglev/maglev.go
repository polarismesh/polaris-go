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

package maglev

import (
	"github.com/polarismesh/polaris-go/pkg/algorithm/hash"
	mconfig "github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/loadbalancer"
	lbcommon "github.com/polarismesh/polaris-go/plugin/loadbalancer/common"
)

//基于maglev算法的负载均衡器
//maglev算法基于论文：https://static.googleusercontent.com/media/research.google.com/en//pubs/archive/44824.pdf
type MaglevLoadBalancer struct {
	*plugin.PluginBase
	cfg      *Config
	hashFunc hash.HashFuncWithSeed
}

//插件类型
func (m *MaglevLoadBalancer) Type() common.Type {
	return common.TypeLoadBalancer
}

//插件名，一个类型下插件名唯一
func (m *MaglevLoadBalancer) Name() string {
	return mconfig.DefaultLoadBalancerMaglev
}

//初始化插件
func (m *MaglevLoadBalancer) Init(ctx *plugin.InitContext) error {
	m.PluginBase = plugin.NewPluginBase(ctx)
	m.cfg = ctx.Config.GetConsumer().GetLoadbalancer().GetPluginConfig(m.Name()).(*Config)
	var err error
	m.hashFunc, err = hash.GetHashFunc(m.cfg.HashFunction)
	if nil != err {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, err, "fail to init hashFunc")
	}
	return nil
}

//构建一次性hash环
func (m *MaglevLoadBalancer) getOrBuildHashRing(instSet *model.InstanceSet) (model.ExtendedSelector, error) {
	selector := instSet.GetSelector(m.ID())
	if nil != selector {
		return selector, nil
	}
	//防止服务刚上线或重建Table时，由于selector为空，大量并发请求进入NewTable逻辑，出现OOM
	instSet.GetLock().Lock()
	defer instSet.GetLock().Unlock()
	//获取锁后再次检查selector是否已经被创建
	selector = instSet.GetSelector(m.ID())
	if nil != selector {
		return selector, nil
	}
	tableSelector, err := NewTable(instSet, uint64(m.cfg.TableSize), m.hashFunc, m.ID())
	instSet.SetSelector(tableSelector)
	return tableSelector, err
}

//ChooseInstance 获取单个服务实例
func (m *MaglevLoadBalancer) ChooseInstance(criteria *loadbalancer.Criteria,
	inputInstances model.ServiceInstances) (model.Instance, error) {
	cluster := criteria.Cluster
	svcClusters := inputInstances.GetServiceClusters()
	clusterValue := cluster.GetClusterValue()
	svcInstances := svcClusters.GetServiceInstances()
	targetInstances := lbcommon.SelectAvailableInstanceSet(clusterValue, cluster.HasLimitedInstances,
		cluster.IncludeHalfOpen)
	if targetInstances.TotalWeight() == 0 {
		return nil, model.NewSDKError(model.ErrCodeAPIInstanceNotFound, nil,
			"instances of %s in cluster %s all weight 0 (instance count %d) in load balance",
			svcClusters.GetServiceKey(), *cluster, targetInstances.Count())
	}
	selector, err := m.getOrBuildHashRing(targetInstances)
	if nil != err {
		return nil, model.NewSDKError(model.ErrCodeInternalError, err, "fail to build maglev table")
	}
	index, replicateNodes, err := selector.Select(criteria)
	if nil != err {
		return nil, model.NewSDKError(model.ErrCodeInternalError, err, "fail to select from maglev table")
	}
	if nil != replicateNodes {
		criteria.ReplicateInfo.Nodes = replicateNodes.GetInstances()
	}
	var instance model.Instance
	instance = svcInstances.GetInstances()[index]
	return instance, nil
}

//销毁插件，可用于释放资源
func (m *MaglevLoadBalancer) Destroy() error {
	return nil
}

//init 注册插件
func init() {
	plugin.RegisterConfigurablePlugin(&MaglevLoadBalancer{}, &Config{})
}
