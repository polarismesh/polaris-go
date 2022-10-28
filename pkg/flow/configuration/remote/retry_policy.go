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

package remote

import (
	"time"
)

type retryPolicy struct {
	delayMinTime     time.Duration
	delayMaxTime     time.Duration
	currentDelayTime time.Duration
}

func (r *retryPolicy) success() {
	r.currentDelayTime = 0
}

func (r *retryPolicy) fail() {
	delayTime := r.currentDelayTime

	if delayTime == 0 {
		delayTime = r.delayMinTime
	} else {
		if r.currentDelayTime<<1 < r.delayMaxTime {
			delayTime = r.currentDelayTime << 1
		} else {
			delayTime = r.delayMaxTime
		}
	}

	r.currentDelayTime = delayTime
}

func (r *retryPolicy) delay() {
	time.Sleep(r.currentDelayTime * time.Second)
}
