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

package monitor

import (
	"github.com/polarismesh/polaris-go/pkg/clock"
	"github.com/polarismesh/polaris-go/pkg/model"
	"sync/atomic"
)

//记录相关维度值
type dimensionRecord struct {
	data32         []uint32
	data64         []int64
	lastUpdateTime int64
	lastReadTime   int64
}

//检查是否更新过
func (d *dimensionRecord) IsMetricUpdate() bool {
	return atomic.LoadInt64(&d.lastUpdateTime) > atomic.LoadInt64(&d.lastReadTime)
}

//设置更新时间
func (d *dimensionRecord) SetLastReadTime() {
	atomic.StoreInt64(&d.lastUpdateTime, clock.GetClock().Now().UnixNano())
}

//创建维度记录
func newDimensionRecord(size32 int, size64 int) *dimensionRecord {
	res := &dimensionRecord{}
	if size32 > 0 {
		res.data32 = make([]uint32, size32)
	}
	if size64 > 0 {
		res.data64 = make([]int64, size64)
	}
	return res
}

//获取某些32位维度下面的值
func (d *dimensionRecord) getDimension32Values(dimensions []int) []uint32 {
	res := make([]uint32, len(dimensions))
	for _, idx := range dimensions {
		res[idx] = atomic.LoadUint32(&d.data32[idx])
		if res[idx] > 0 {
			atomic.AddUint32(&d.data32[idx], ^(res[idx] - 1))
		}
	}
	return res
}

//获取某些64位维度下面的值
func (d *dimensionRecord) getDimensions64Values(dimensions []int) []int64 {
	res := make([]int64, len(dimensions))
	for _, idx := range dimensions {
		res[idx] = atomic.LoadInt64(&d.data64[idx])
		if res[idx] > 0 {
			atomic.AddInt64(&d.data64[idx], -res[idx])
		}
	}
	return res
}

//为32位记录添加值
func (d *dimensionRecord) add32Dimensions(idx int, value uint32) {
	atomic.AddUint32(&d.data32[idx], value)
	atomic.StoreInt64(&d.lastUpdateTime, clock.GetClock().Now().UnixNano())
}

//为64位记录添加值
func (d *dimensionRecord) add64Dimensions(idx int, value int64) {
	atomic.AddInt64(&d.data64[idx], value)
	atomic.StoreInt64(&d.lastUpdateTime, clock.GetClock().Now().UnixNano())
}

//往某些维度添加值
func (d *dimensionRecord) AddValue(gauge model.InstanceGauge, addFunc addDimensionFunc) {
	addFunc(gauge, d)
}

//为dimensionRecord添加值
type addDimensionFunc func(gauge model.InstanceGauge, d *dimensionRecord)
