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

package trigger

import (
	"sync/atomic"
)

type ConsecutiveCounter struct {
	*baseCounter
	maxCount          int64
	consecutiveErrors int32
}

func NewConsecutiveCounter(name string, opt *Options) *ConsecutiveCounter {
	c := &ConsecutiveCounter{
		baseCounter: newBaseCounter(name, opt),
	}
	c.init()
	return c
}

func (c *ConsecutiveCounter) init() {
	c.log.Infof("[CircuitBreaker][Counter] consecutiveCounter(%s) initialized, resource(%s)", c.ruleName, c.res.String())
	c.maxCount = int64(c.triggerCondition.GetErrorCount())
}

func (c *ConsecutiveCounter) Report(success bool) {
	if c.isSuspend() {
		return
	}
	if !success {
		currentSum := atomic.AddInt32(&c.consecutiveErrors, 1)
		if currentSum == int32(c.maxCount) {
			c.suspend()
			atomic.StoreInt32(&c.consecutiveErrors, 0)
			c.handler.CloseToOpen(c.ruleName)
		}
	} else {
		atomic.StoreInt32(&c.consecutiveErrors, 0)
	}
}
