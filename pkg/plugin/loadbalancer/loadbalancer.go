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

package loadbalancer

import (
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

//负载均衡的过滤值成员
type Criteria struct {
	//用户传入用于计算hash的二进制流
	HashKey []byte
	//用户传入用于计算hash的int值
	HashValue uint64
	//分配时忽略半开实例，只有当没有其他节点时才分配半开节点
	IgnoreHalfOpen bool
	//必选，目标cluster
	Cluster *model.Cluster
	//可选，对于有状态的负载均衡方式，这里给出备份节点的返回数据
	ReplicateInfo ReplicateInfo
}

//备份节点信息
type ReplicateInfo struct {
	Count int
	Nodes []model.Instance
}

//LoadBalancer 【扩展点接口】负载均衡
type LoadBalancer interface {
	plugin.Plugin
	//进行负载均衡，选择一个实例
	ChooseInstance(criteria *Criteria, instances model.ServiceInstances) (model.Instance, error)
}

//初始化
func init() {
	plugin.RegisterPluginInterface(common.TypeLoadBalancer, new(LoadBalancer))
}
