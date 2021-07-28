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

package hash

import (
	"github.com/modern-go/reflect2"
	"github.com/spaolacci/murmur3"
	"hash"
	"sync"
)

const DefaultHashFuncName = "murmur3"

var (
	murmur3HashPool = &sync.Pool{}
)

//通过seed的算法获取hash值
func murmur3HashWithSeed(buf []byte, seed uint32) (uint64, error) {
	var pooled = seed == 0
	var hasher hash.Hash64
	if pooled {
		poolValue := murmur3HashPool.Get()
		if !reflect2.IsNil(poolValue) {
			hasher = poolValue.(hash.Hash64)
			hasher.Reset()
		}
	}
	if nil == hasher {
		hasher = murmur3.New64WithSeed(seed)
	}
	var value uint64
	var err error
	if err = WriteBuffer(hasher, buf); nil == err {
		value = hasher.Sum64()
	}
	if pooled {
		murmur3HashPool.Put(hasher)
	}
	return value, err
}

//包初始化函数
func init() {
	RegisterHashFunc(DefaultHashFuncName, murmur3HashWithSeed)
}
