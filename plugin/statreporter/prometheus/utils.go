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
//@Author: springliao
//@Description:
//@Time: 2021/10/19 15:57

package prometheus

import (
	"math"
	"sort"
)

const (
	SeparatorByte byte  = 255
	FnvHashOffset int64 = math.MaxInt64
	FnvHashPrime  int64 = 1099511628211
)

func FnvHashAdd(h int64, s string) int64 {
	arr := []byte(s)
	for i := range arr {
		h = FnvHashAddByte(h, arr[i])
	}
	return h
}

func FnvHashAddByte(h int64, b byte) int64 {
	h ^= int64(b)
	h *= FnvHashPrime
	return h
}

// LabelsToSignature Convert map data to an INT64
func LabelsToSignature(labels map[string]string) int64 {
	if len(labels) == 0 {
		return FnvHashOffset
	}

	labelNames := make([]string, len(labels))
	pos := 0
	for k := range labels {
		labelNames[pos] = k
		pos++
	}

	sort.Strings(labelNames)

	sum := FnvHashOffset
	for i := range labelNames {
		sum = FnvHashAdd(sum, labelNames[i])
		sum = FnvHashAddByte(sum, SeparatorByte)
		sum = FnvHashAdd(sum, labels[labelNames[i]])
		sum = FnvHashAddByte(sum, SeparatorByte)
	}

	return sum
}

// CopyLabels Copy the labels
func CopyLabels(labels map[string]string) map[string]string {
	newLabels := make(map[string]string)

	for k, v := range labels {
		newLabels[k] = v
	}
	return newLabels
}
