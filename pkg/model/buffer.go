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

package model

import (
	"bytes"
	"github.com/modern-go/reflect2"
	"sync"
)

var (
	//pooling the string slice
	//key is length, value is sync.Pool
	stringSlicePools = &sync.Map{}
	//字符串数组池
	byteBufferPools = &sync.Map{}
)

//获取字符串数组
func PoolGetStringSlice(size int) []string {
	stringSlicePoolValue, ok := stringSlicePools.Load(size)
	if !ok {
		stringSlicePoolValue, _ = stringSlicePools.LoadOrStore(size, &sync.Pool{})
	}
	pool := stringSlicePoolValue.(*sync.Pool)
	sliceValue := pool.Get()
	if reflect2.IsNil(sliceValue) {
		return make([]string, size)
	}
	return sliceValue.([]string)
}

//归还字符串数组
func PoolPutStringSlice(size int, slice []string) {
	stringSlicePoolValue, ok := stringSlicePools.Load(size)
	if !ok {
		stringSlicePoolValue, _ = stringSlicePools.LoadOrStore(size, &sync.Pool{})
	}
	pool := stringSlicePoolValue.(*sync.Pool)
	pool.Put(slice)
}

//通过池子获取字符串队列
func PoolGetByteBuffer(size int) *bytes.Buffer {
	byteBufferPoolValue, ok := byteBufferPools.Load(size)
	if !ok {
		byteBufferPoolValue, _ = byteBufferPools.LoadOrStore(size, &sync.Pool{})
	}
	pool := byteBufferPoolValue.(*sync.Pool)
	bufferValue := pool.Get()
	if reflect2.IsNil(bufferValue) {
		return bytes.NewBuffer(make([]byte, 0, size))
	}
	buf := bufferValue.(*bytes.Buffer)
	buf.Reset()
	return buf
}

//归还字节数组
func PoolPutByteBuffer(size int, buf *bytes.Buffer) {
	byteBufferPoolValue, ok := byteBufferPools.Load(size)
	if !ok {
		byteBufferPoolValue, _ = stringSlicePools.LoadOrStore(size, &sync.Pool{})
	}
	pool := byteBufferPoolValue.(*sync.Pool)
	pool.Put(buf)
}
