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

import "strings"

func labelsToSignature(labels map[string]string, order []string) int64 {
	desc := []string{}
	for i := range order {
		desc = append(desc, order[i]+"|"+labels[order[i]])
	}
	key := strings.Join(desc, ",")
	return Fnv32(key)
}

func Fnv32(key string) int64 {
	hash := uint32(2166136261)
	const prime32 = uint32(16777619)
	for i := 0; i < len(key); i++ {
		hash *= prime32
		hash ^= uint32(key[i])
	}
	return int64(hash)
}
