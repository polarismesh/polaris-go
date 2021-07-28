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

package ratelimit

import "github.com/polarismesh/polaris-go/pkg/flow/quota"

//统计限流次数
type limitedStat struct {
	reason        string
	limitedNum    int64
	passNum       int64
	validDuration int64
}

type limitedStatKey struct {
	Duration uint32
	Mode     quota.LimitMode
}

type passStat struct {
	passNum int64
}

//存储限流统计数据
type statData struct {
	trafficShapingLimited map[quota.LimitMode]*limitedStat
	amountStats           map[limitedStatKey]*limitedStat
	ruleMatchLabels string
}

//limitedStat的数组
type limitedSlice []*limitedStat

//len
func (l limitedSlice) Len() int {
	return len(l)
}

//swap
func (l limitedSlice) Swap(i, j int) {
	l[i], l[j] = l[j], l[i]
}

//less
func (l limitedSlice) Less(i, j int) bool {
	return l[i].validDuration > l[j].validDuration
}
