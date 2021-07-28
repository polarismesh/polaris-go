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

package search

//可搜索的数组
type SearchableSlice interface {
	//获取某个下标下面的值
	GetValue(idx int) uint64
	//获取数组长度
	Count() int
}

//通过循环方式进行二分查找
func selectLoop(weightedIndexes SearchableSlice, selector uint64) int {
	var count = weightedIndexes.Count()
	var lowp = 0
	var highp = count
	for {
		var midp = (lowp + highp) / 2

		if midp == count {
			return 0
		}

		var midval = weightedIndexes.GetValue(midp)
		var midval1 uint64
		if midp != 0 {
			midval1 = weightedIndexes.GetValue(midp - 1)
		}
		if selector <= midval && selector > midval1 {
			return midp
		}

		if midval < selector {
			lowp = midp + 1
		} else {
			highp = midp - 1
		}

		if lowp > highp {
			return 0
		}
	}
}

//二分查找
func BinarySearch(weightedIndexes SearchableSlice, selector uint64) int {
	//return selectRecursive(weightedIndexes, 0, weightedIndexes.Count(), selector)
	return selectLoop(weightedIndexes, selector)
}
