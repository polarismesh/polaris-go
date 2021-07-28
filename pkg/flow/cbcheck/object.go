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

package cbcheck

import (
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/clock"
	"github.com/polarismesh/polaris-go/pkg/model"
	"sync/atomic"
	"time"
)

//熔断状态模型，实现了CircuitBreakerStatus接口
type circuitBreakerStatus struct {
	circuitBreaker string
	status         model.Status
	startTime      time.Time
	finalAllocTime int64

	// 半开后最多请求此数
	maxHalfOpenAllowReqTimes int
	// 半开后配个分配 初始化为maxHalfOpenAllowReqTimes
	halfOpenQuota int32
	// 半开后，分配的个数
	allocatedRequestsAfterHalfOpen int32

	// 半开后发生请求的次数
	halfOpenRequests int32
	// 半开后发生的失败请求数
	halfOpenFailRequests int32

	// 状态发生转变的锁
	statusLock int32
}

//获取在半开之后，分配出去的请求数，即getOneInstance接口返回这个实例的次数
func (c *circuitBreakerStatus) AllocatedRequestsAfterHalfOpen() int32 {
	return atomic.LoadInt32(&c.allocatedRequestsAfterHalfOpen)
}

//获取状态转换锁
func (c *circuitBreakerStatus) AcquireStatusLock() bool {
	return atomic.CompareAndSwapInt32(&c.statusLock, 0, 1)
}

//获取进入半开状态之后分配的请求数
func (c *circuitBreakerStatus) GetRequestsAfterHalfOpen() int32 {
	return atomic.LoadInt32(&c.halfOpenRequests)
}

//获取进入半开状态之后的成功请求数
func (c *circuitBreakerStatus) GetFailRequestsAfterHalfOpen() int32 {
	return atomic.LoadInt32(&c.halfOpenFailRequests)
}

//添加半开状态下面的请求数
func (c *circuitBreakerStatus) AddRequestCountAfterHalfOpen(n int32, success bool) int32 {
	if !success {
		atomic.AddInt32(&c.halfOpenFailRequests, n)
	}
	return atomic.AddInt32(&c.halfOpenRequests, n)
}

//标识被哪个熔断器熔断
func (c *circuitBreakerStatus) GetCircuitBreaker() string {
	return c.circuitBreaker
}

//熔断状态
func (c *circuitBreakerStatus) GetStatus() model.Status {
	return c.status
}

//状态转换的时间
func (c *circuitBreakerStatus) GetStartTime() time.Time {
	return c.startTime
}

//打印熔断状态数据
func (c circuitBreakerStatus) String() string {
	return fmt.Sprintf("{circuitBreaker:%s, status:%v, startTime:%v}",
		c.circuitBreaker, c.status, c.startTime)
}

//是否可以分配请求
func (c *circuitBreakerStatus) IsAvailable() bool {
	if nil == c || model.Close == c.status {
		return true
	}
	if model.Open == c.status {
		return false
	}
	return atomic.LoadInt32(&c.halfOpenQuota) > 0
}

//获取分配了最后配额的时间
func (c *circuitBreakerStatus) GetFinalAllocateTimeInt64() int64 {
	return atomic.LoadInt64(&c.finalAllocTime)
}

//执行实例到请求的分配
func (c *circuitBreakerStatus) Allocate() bool {
	if nil == c || model.Close == c.status {
		return true
	}
	if model.Open == c.status {
		return false
	}
	left := atomic.AddInt32(&c.halfOpenQuota, -1)
	//如果刚好是0，即分配了最后一个半开配额，记录时间
	if left == 0 {
		atomic.StoreInt64(&c.finalAllocTime, clock.GetClock().Now().UnixNano())
	}
	//如果进行了分配的话，那么只要left大于等于0即可
	allocated := left >= 0
	if allocated {
		atomic.AddInt32(&c.allocatedRequestsAfterHalfOpen, 1)
	}
	return allocated
}
