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
 * specific language governing permissionsr and limitations under the License.
 */

package ratelimit

import (
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"sort"
	"strings"
	"sync/atomic"
)

//将限流规则的labels转化为string
func MarshalRateLimitRuleLabels(labelsMap map[string]*namingpb.MatchString) string {
	var strSlice []string
	for k := range labelsMap {
		strSlice = append(strSlice, k)
	}
	sort.Strings(strSlice)
	builder := strings.Builder{}
	first := true
	for _, k := range strSlice {
		if !first {
			builder.WriteString("|")
		}
		first = false
		match := labelsMap[k]
		builder.WriteString(k)
		builder.WriteString(":")
		builder.WriteString(match.Value.GetValue())
	}
	return builder.String()
}

//原子地获取一个int64的值，并减去这段时间增加的值
func GetAtomicInt64(data *int64) int64 {
	res := atomic.LoadInt64(data)
	atomic.AddInt64(data, -res)
	return res
}
