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

package ringhash

import (
	"github.com/polarismesh/polaris-go/pkg/algorithm/hash"
	mconfig "github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/loadbalancer"
	lbcommon "github.com/polarismesh/polaris-go/plugin/loadbalancer/common"
)

//ketama算法的一致性hash负载均衡器
type KetamaLoadBalancer struct {
	*plugin.PluginBase
	cfg      *Config
	hashFunc hash.HashFuncWithSeed
}

//插件类型
func (k *KetamaLoadBalancer) Type() common.Type {
	return common.TypeLoadBalancer
}

//插件名，一个类型下插件名唯一
func (k *KetamaLoadBalancer) Name() string {
	return mconfig.DefaultLoadBalancerRingHash
}

//初始化插件
func (k *KetamaLoadBalancer) Init(ctx *plugin.InitContext) error {
	k.PluginBase = plugin.NewPluginBase(ctx)
	k.cfg = ctx.Config.GetConsumer().GetLoadbalancer().GetPluginConfig(k.Name()).(*Config)
	var err error
	k.hashFunc, err = hash.GetHashFunc(k.cfg.HashFunction)
	if nil != err {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, err, "fail to init hashFunc")
	}
	return nil
}

//构建一次性hash环
func (k *KetamaLoadBalancer) getOrBuildHashRing(instSet *model.InstanceSet) (model.ExtendedSelector, error) {
	selector := instSet.GetSelector(k.ID())
	if nil != selector {
		return selector, nil
	}
	//防止服务刚上线或重建hash环时，由于selector为空，大量并发请求进入创建continuum逻辑，出现OOM
	instSet.GetLock().Lock()
	defer instSet.GetLock().Unlock()
	//获取锁后再次检查selector是否已经被创建
	selector = instSet.GetSelector(k.ID())
	if nil != selector {
		return selector, nil
	}
	continuum, err := NewContinuum(instSet, k.cfg.VnodeCount, k.hashFunc, k.ID())
	instSet.SetSelector(continuum)
	return continuum, err
}

//ChooseInstance 获取单个服务实例
func (k *KetamaLoadBalancer) ChooseInstance(criteria *loadbalancer.Criteria,
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
	selector, err := k.getOrBuildHashRing(targetInstances)
	if nil != err {
		return nil, model.NewSDKError(model.ErrCodeInternalError, err, "fail to build ring, err is %v", err)
	}
	index, nodes, err := selector.Select(criteria)
	if nil != err {
		return nil, model.NewSDKError(model.ErrCodeInternalError, err, "fail to select from ring")
	}
	if nil != nodes {
		criteria.ReplicateInfo.Nodes = nodes.GetInstances()
	}
	var instance model.Instance
	instance = svcInstances.GetInstances()[index]
	return instance, nil
}

//销毁插件，可用于释放资源
func (k *KetamaLoadBalancer) Destroy() error {
	return nil
}

//init 注册插件
func init() {
	plugin.RegisterConfigurablePlugin(&KetamaLoadBalancer{}, &Config{})
}
