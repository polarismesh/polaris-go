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

package loadbalance

import (
	"fmt"
	"math"

	"github.com/gonum/stat"
)

// 计算标准差
func calStdDev(idInstanceWeights map[instanceKey]int, idInstanceCalls map[instanceKey]int, cbid instanceKey) float64 {
	var adjustedCalls []float64
	for id, calls := range idInstanceCalls {
		if id == cbid {
			continue
		}
		weight := idInstanceWeights[id]
		adjustedCalls = append(adjustedCalls, float64(calls)/float64(weight))
	}
	fmt.Printf("ajusted calls is %v\n", adjustedCalls)
	return stat.StdDev(adjustedCalls, nil)
}

// 计算各个实例在负载均衡后调用次数比例与权重比例的差距，
// 参数为需要剔除计算的实例id
func calDiff(idInstanceWeights map[instanceKey]int, idInstanceCalls map[instanceKey]int, cbid instanceKey) float64 {
	// resFile := "loadDiff.csv"
	totalDiff := float64(0)
	totalWeights := 0
	var keys []instanceKey
	for k, v := range idInstanceWeights {
		// fmt.Println(k)
		if k == cbid {
			continue
		}
		keys = append(keys, k)
		totalWeights += v
	}
	totalCalls := 0
	for _, v := range idInstanceCalls {
		totalCalls += v
	}
	if totalWeights <= 0 || totalCalls <= 0 {
		return 1.0
	}
	maxDiff := 0.0
	for i := 0; i < len(keys); i++ {
		// fmt.Println(keys[i])
		c, ok := idInstanceCalls[keys[i]]
		if !ok {
			c = 0
		}
		w, _ := idInstanceWeights[keys[i]]
		weightRate := float64(w) / float64(totalWeights)
		callsRate := float64(c) / float64(totalCalls)
		diff := math.Abs(weightRate - callsRate)
		if diff > maxDiff {
			maxDiff = diff
		}
		totalDiff += diff
		fmt.Printf("instance %v: weight:%v/%v(total), calls:%v/%v(total)\n", keys[i], w, totalWeights, c, totalCalls)
	}
	fmt.Printf("Max diff %v\n", maxDiff)
	// outWriter.Flush()
	return totalDiff
}
