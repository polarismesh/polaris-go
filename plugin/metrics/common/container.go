// Tencent is pleased to support the open source community by making polaris-go available.
//
// Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
//
// Licensed under the BSD 3-Clause License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// https://opensource.org/licenses/BSD-3-Clause
//
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissionsr and limitations under the License.
//

package common

import (
	"sync"
)

type MarkedContainer struct {
	mutex sync.RWMutex
	data  map[int64]StatMetric
}

func (mc *MarkedContainer) GetValue(signature int64) StatMetric {
	mc.mutex.RLock()
	defer mc.mutex.RUnlock()

	return mc.data[signature]
}

func (mc *MarkedContainer) DelValue(signature int64) {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()

	delete(mc.data, signature)
}

func (mc *MarkedContainer) PutValue(signature int64, info StatMetric) {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()

	mc.data[signature] = info
}

func (mc *MarkedContainer) GetValues() []StatMetric {
	mc.mutex.RLock()
	defer mc.mutex.RUnlock()

	ret := make([]StatMetric, 0, len(mc.data))
	for _, v := range mc.data {
		ret = append(ret, v)
	}
	return ret
}

func NewMarkedViewContainer() *MarkedViewContainer {
	return &MarkedViewContainer{
		data: map[string]struct{}{},
	}
}

type MarkedViewContainer struct {
	mutex sync.RWMutex
	data  map[string]struct{}
}

func (mc *MarkedViewContainer) addMarkedName(markedName string) {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()

	mc.data[markedName] = struct{}{}
}

func (mc *MarkedViewContainer) removeMarkedName(markedName string) {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()

	delete(mc.data, markedName)
}

func (mc *MarkedViewContainer) existMarkedName(markedName string) bool {
	mc.mutex.RLock()
	defer mc.mutex.RUnlock()

	_, ok := mc.data[markedName]
	return ok
}
