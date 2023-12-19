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

package window

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWindowResetWindow(t *testing.T) {
	opts := Options{Size: 3}
	window := NewWindow(opts)
	for i := 0; i < opts.Size; i++ {
		window.Append(i, 1.0)
	}
	window.ResetWindow()
	for i := 0; i < opts.Size; i++ {
		assert.Equal(t, len(window.Bucket(i).Points), 0)
	}
}

func TestWindowResetBucket(t *testing.T) {
	opts := Options{Size: 3}
	window := NewWindow(opts)
	for i := 0; i < opts.Size; i++ {
		window.Append(i, 1.0)
	}
	window.ResetBucket(1)
	assert.Equal(t, len(window.Bucket(1).Points), 0)
	assert.Equal(t, window.Bucket(0).Points[0], float64(1.0))
	assert.Equal(t, window.Bucket(2).Points[0], float64(1.0))
}

func TestWindowResetBuckets(t *testing.T) {
	opts := Options{Size: 3}
	window := NewWindow(opts)
	for i := 0; i < opts.Size; i++ {
		window.Append(i, 1.0)
	}
	window.ResetBuckets(0, 3)
	for i := 0; i < opts.Size; i++ {
		assert.Equal(t, len(window.Bucket(i).Points), 0)
	}
}

func TestWindowAppend(t *testing.T) {
	opts := Options{Size: 3}
	window := NewWindow(opts)
	for i := 0; i < opts.Size; i++ {
		window.Append(i, 1.0)
	}
	for i := 0; i < opts.Size; i++ {
		assert.Equal(t, window.Bucket(i).Points[0], float64(1.0))
	}
}

func TestWindowAdd(t *testing.T) {
	opts := Options{Size: 3}
	window := NewWindow(opts)
	window.Append(0, 1.0)
	window.Add(0, 1.0)
	assert.Equal(t, window.Bucket(0).Points[0], float64(2.0))
}

func TestWindowSize(t *testing.T) {
	opts := Options{Size: 3}
	window := NewWindow(opts)
	assert.Equal(t, window.Size(), 3)
}
