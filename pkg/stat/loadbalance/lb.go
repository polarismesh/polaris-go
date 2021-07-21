/**
 * Tencent is pleased to support the open source community by making CL5 available.
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

package loadbalance

import (
	"github.com/polarismesh/polaris-go/pkg/model"
	"sync"
)

//负载均衡统计数据
type LoadBalanceGauge struct {
	model.EmptyInstanceGauge
	Inst model.Instance
}

//LoadBalanceGauge池子
var loadBalanceStatPool = &sync.Pool{}

//从池子中获取一个LoadBalanceGauge
func GetLoadBalanceStatFromPool() *LoadBalanceGauge {
	value := loadBalanceStatPool.Get()
	if nil == value {
		return &LoadBalanceGauge{}
	}
	return value.(*LoadBalanceGauge)
}

//将LoadBalanceGauge放回pool
func PoolPutLoadBalanceStat(gauge *LoadBalanceGauge) {
	loadBalanceStatPool.Put(gauge)
}

//获取被选中的实例
func (l *LoadBalanceGauge) GetCalledInstance() model.Instance {
	return l.Inst
}
