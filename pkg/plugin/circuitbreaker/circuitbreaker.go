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

package circuitbreaker

import (
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"time"
)

//InstanceCircuitBreaker 【扩展点接口】节点熔断
type InstanceCircuitBreaker interface {
	plugin.Plugin
	//进行调用统计，返回当前实例是否需要进行立即熔断
	Stat(model.InstanceGauge) (bool, error)
	//进行熔断计算，返回需要进行状态转换的实例ID
	//入参包括全量服务实例，以及当前周期的健康探测结果
	CircuitBreak(instances []model.Instance) (*Result, error)
}

//熔断结算结果
type Result struct {
	Now time.Time
	//需要开启熔断器的实例ID
	InstancesToOpen model.HashSet
	//需要转换成半开状态的实例ID
	InstancesToHalfOpen model.HashSet
	//需要关闭熔断器的实例ID
	InstancesToClose model.HashSet
	//该熔断器在实例进入半开状态后最多允许的请求数
	RequestCountAfterHalfOpen int
}

// NewCircuitBreakerResult 创建熔断结果对象
func NewCircuitBreakerResult(now time.Time) *Result {
	return &Result{
		Now:                 now,
		InstancesToClose:    model.HashSet{},
		InstancesToHalfOpen: model.HashSet{},
		InstancesToOpen:     model.HashSet{}}
}

// Merge merge results
func (r *Result) Merge(result *Result) {
	if len(result.InstancesToOpen) > 0 {
		for k, v := range result.InstancesToOpen {
			r.InstancesToOpen[k] = v
		}
	}
	if len(result.InstancesToHalfOpen) > 0 {
		for k, v := range result.InstancesToHalfOpen {
			r.InstancesToHalfOpen[k] = v
		}
	}
	if len(result.InstancesToClose) > 0 {
		for k, v := range result.InstancesToClose {
			r.InstancesToClose[k] = v
		}
	}
}

// IsEmpty result has status changing instances
func (r *Result) IsEmpty() bool {
	return len(r.InstancesToClose) == 0 && len(r.InstancesToHalfOpen) == 0 && len(r.InstancesToOpen) == 0
}

//初始化
func init() {
	plugin.RegisterPluginInterface(common.TypeCircuitBreaker, new(InstanceCircuitBreaker))
}
