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

package common

import (
	"github.com/polarismesh/polaris-go/pkg/model"
)

//选择可用实例集合
//优先选择健康（close以及存在半开配额实例）
//如果不满足健康，则选择close+半开实例
func SelectAvailableInstanceSet(clsValue *model.ClusterValue, hasLimitedInstances bool,
	includeHalfOpen bool) *model.InstanceSet {
	//targetInstances := clsValue.GetInstancesSet(hasLimitedInstances, false)
	//if targetInstances.TotalWeight() == 0 {
	//	return clsValue.GetInstancesSet(hasLimitedInstances, true)
	//}
	//return targetInstances
	targetInstances := clsValue.GetInstancesSet(hasLimitedInstances, includeHalfOpen)
	return targetInstances
}
