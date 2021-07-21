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

package search

import (
	"testing"
)

//整形数组
type IntArray struct {
	array []uint64
}

//获取某个下标下面的值
func (i *IntArray) GetValue(idx int) uint64 {
	return i.array[idx]
}

//获取数组长度
func (i *IntArray) Count() int {
	return len(i.array)
}

//测试二分查找
func TestBinarySearch(t *testing.T) {
	arrayObj := &IntArray{}
	var base uint64 = 10000
	for i := 0; i < 20; i++ {
		arrayObj.array = append(arrayObj.array, base+uint64(i*2))
	}
	for i := 0; i < 20; i++ {
		idx := BinarySearch(arrayObj, base+uint64(i)*2+1)
		var expectIdx = i + 1
		if expectIdx == 20 {
			expectIdx = 0
		}
		if idx != expectIdx {
			t.Fatalf("idx is %d, expect %d", idx, expectIdx)
		}
	}

}
