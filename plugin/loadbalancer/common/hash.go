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
	"github.com/polarismesh/polaris-go/pkg/algorithm/hash"
	"github.com/polarismesh/polaris-go/pkg/plugin/loadbalancer"
)

//计算hash值
func CalcHashValue(criteria *loadbalancer.Criteria, hashFunc hash.HashFuncWithSeed) (uint64, error) {
	return CalcHashValueWithSeed(criteria, hashFunc, 0)
}

//计算hash值
func CalcHashValueWithSeed(
	criteria *loadbalancer.Criteria, hashFunc hash.HashFuncWithSeed, seed uint32) (uint64, error) {
	var hashValue uint64
	var err error
	if len(criteria.HashKey) > 0 {
		hashValue, err = hashFunc(criteria.HashKey, seed)
		if nil != err {
			return 0, err
		}
	} else {
		hashValue = criteria.HashValue
	}
	return hashValue, nil
}
