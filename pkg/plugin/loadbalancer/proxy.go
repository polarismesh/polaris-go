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

//proxy of LoadBalancer
type Proxy struct {
	LoadBalancer
	engine model.Engine
}

//设置
func (p *Proxy) SetRealPlugin(plug plugin.Plugin, engine model.Engine) {
	p.LoadBalancer = plug.(LoadBalancer)
	p.engine = engine
}

type SelectStatus struct {
	HasLimitedInstances bool
	IncludeHalfOpen     bool
}

//proxy LoadBalancer ChooseInstance
func (p *Proxy) ChooseInstance(criteria *Criteria, instances model.ServiceInstances) (model.Instance, error) {
	//第一次进行负载均衡，包括半开实例
	criteria.Cluster.IncludeHalfOpen = true
	firstResult, firstErr := p.LoadBalancer.ChooseInstance(criteria, instances)
	//如果出错了，直接返回错误
	if firstErr != nil {
		return firstResult, firstErr
	}
	//熔断状态分配流量成功，返回结果
	cbStatus := firstResult.GetCircuitBreakerStatus()
	if cbStatus == nil || cbStatus.Allocate() {
		return firstResult, nil
	}

	//第一次因为熔断状态分配流量不成功，进行第二次负载均衡，这一次不包括半开实例
	criteria.Cluster.IncludeHalfOpen = false
	secondResult, secondErr := p.LoadBalancer.ChooseInstance(criteria, instances)
	//如果没有出现错误，那么直接返回第二次的结果
	if secondErr == nil {
		return secondResult, secondErr
	}
	//否则，直接返回第一次的结果
	//目前可能的情况是，所有实例都是半开，所以第二次负载均衡会返回实例权重为0的错误，第一次返回了一个半开实例；
	//在这种情况下，选择返回第一次的结果，即一个配额用完的半开实例
	return firstResult, nil
}

//注册proxy
func init() {
	plugin.RegisterPluginProxy(common.TypeLoadBalancer, &Proxy{})
}
