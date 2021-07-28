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

package rand

import "github.com/polarismesh/polaris-go/pkg/algorithm/search"

//带权重的数组
type WeightedSlice interface {
	search.SearchableSlice
	//获取总权重值
	TotalWeight() int
}

//通过随机权重算法选择值
func SelectWeightedRandItem(scalableRand *ScalableRand, slice WeightedSlice) int {
	selector := scalableRand.Intn(slice.TotalWeight())
	return search.BinarySearch(slice, uint64(selector))
}
