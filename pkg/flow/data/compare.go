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

package data

import "github.com/polarismesh/polaris-go/pkg/model"

//全相同的比较器
type AllEqualsComparable struct {
}

//比较
func (a *AllEqualsComparable) CompareTo(value interface{}) int {
	return 0
}

// 最终校验是否可以删除该任务
func (a *AllEqualsComparable) EnsureDeleted(value interface{}) bool {
	return true
}

//服务主键比较器
type ServiceKeyComparable struct {
	SvcKey model.ServiceKey
}

//比较
func (a *ServiceKeyComparable) CompareTo(value interface{}) int {
	inComparable, ok := value.(*ServiceKeyComparable)
	if !ok {
		return -1
	}
	if inComparable.SvcKey.Service == a.SvcKey.Service && inComparable.SvcKey.Namespace == a.SvcKey.Namespace {
		return 0
	}
	return 1
}

// 最终校验是否可以删除该任务
func (a *ServiceKeyComparable) EnsureDeleted(value interface{}) bool {
	return true
}
