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

package rand

import (
	"math/rand"
	"testing"
	"time"
)

const maxInt = 1000

var (
	scalableRand *ScalableRand
)

//测试可扩展的随机数
func BenchmarkScalableRand_Intn(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			scalableRand.Intn(maxInt)
		}
	})
}

//测试默认的随机数
func BenchmarkRand_Intn(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			rand.Intn(maxInt)
		}
	})
}

//测试可扩展随机数功能
func TestScalableRand_Intn(t *testing.T) {
	for i := 0; i < 10000; i++ {
		scalableRand.Intn(maxInt)
	}
}

//初始化
func init() {
	scalableRand = NewScalableRand()
	rand.Seed(time.Now().UnixNano())
}
