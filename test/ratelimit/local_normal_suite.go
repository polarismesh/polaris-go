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

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/check.v1"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

// 直接拒绝正常用例测试
type LocalNormalTestingSuite struct {
	CommonRateLimitSuite
}

// 用例名称
func (rt *LocalNormalTestingSuite) GetName() string {
	return "LocalNormalTestingSuite"
}

// SetUpSuite 启动测试套程序
func (rt *LocalNormalTestingSuite) SetUpSuite(c *check.C) {
	rt.CommonRateLimitSuite.SetUpSuite(c, false)
}

// SetUpSuite 结束测试套程序
func (rt *LocalNormalTestingSuite) TearDownSuite(c *check.C) {
	rt.CommonRateLimitSuite.TearDownSuite(c, rt)
}

// 测试本地精准匹配限流
func (rt *LocalNormalTestingSuite) TestLocalExact(c *check.C) {
	// 多个线程跑一段时间，看看每秒通过多少，以及总共通过多少
	workerCount := 4
	wg := &sync.WaitGroup{}
	wg.Add(workerCount)
	cfg := config.NewDefaultConfiguration([]string{mockDiscoverAddress})
	limitAPI, err := api.NewLimitAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer limitAPI.Destroy()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	codeChan := make(chan model.QuotaResultCode)
	var calledCount int64
	for i := 0; i < workerCount; i++ {
		go func(idx int) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					resp := doSingleGetQuota(c, limitAPI, LocalTestSvcName, "query",
						map[string]string{labelUin: "007"})
					atomic.AddInt64(&calledCount, 1)
					codeChan <- resp.Code
				}
			}
		}(i)
	}
	allocatedPerSeconds := make([]int, 0, 20)
	var allocatedTotal int
	ctx1, cancel1 := context.WithCancel(context.Background())
	go func() {
		var totalPerSecond int
		var rejectCount int
		for {
			select {
			case <-ctx1.Done():
				return
			case code := <-codeChan:
				if code == api.QuotaResultOk {
					allocatedTotal++
					totalPerSecond++
					rejectCount = 0
				} else {
					rejectCount++
					if rejectCount >= workerCount && totalPerSecond > 0 {
						allocatedPerSeconds = append(allocatedPerSeconds, totalPerSecond)
						totalPerSecond = 0
					}
				}
			}
		}
	}()
	wg.Wait()
	cancel1()
	fmt.Printf("calledCount is %d\n", calledCount)
	fmt.Printf("allocatedPerSeconds is %v\n", allocatedPerSeconds)
	for i, allocatedPerSecond := range allocatedPerSeconds {
		if i == 0 {
			// 头部因为时间窗对齐原因，有可能出现不为100
			continue
		}
		if allocatedPerSecond < 5 {
			continue
		}
		c.Assert(allocatedPerSecond >= 195 && allocatedPerSecond <= 205, check.Equals, true)
	}
	fmt.Printf("allocatedTotal is %d\n", allocatedTotal)
	c.Assert(allocatedTotal >= 700 && allocatedTotal <= 1700, check.Equals, true)
}

// 应用ID到限流结果
type AppIdResult struct {
	appId string
	code  model.QuotaResultCode
}

// 测试本地精准匹配限流
func (rt *LocalNormalTestingSuite) TestLocalRegexSpread(c *check.C) {
	// 2个线程跑20秒，看看每秒通过多少，以及总共通过多少
	workerCount := 2
	appIds := []string{"app1", "app2", "app3", "app4", "app5"}
	wg := &sync.WaitGroup{}
	wg.Add(workerCount * len(appIds))
	cfg := config.NewDefaultConfiguration([]string{mockDiscoverAddress})
	limitAPI, err := api.NewLimitAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	codeChan := make(chan AppIdResult)
	var calledCount int64
	for _, appId := range appIds {
		for i := 0; i < workerCount; i++ {
			go func(idx int, thisAppId string) {
				defer wg.Done()
				for {
					select {
					case <-ctx.Done():
						return
					default:
						resp := doSingleGetQuota(c, limitAPI, LocalTestSvcName, "",
							map[string]string{labelAppId: thisAppId})
						atomic.AddInt64(&calledCount, 1)
						codeChan <- AppIdResult{
							appId: thisAppId,
							code:  resp.Code,
						}
						time.Sleep(1 * time.Millisecond)
					}
				}
			}(i, appId)
		}
	}
	appIdToAllocated := make(map[string][]int)
	for _, appId := range appIds {
		appIdToAllocated[appId] = make([]int, 0)
	}
	ctx1, cancel1 := context.WithCancel(context.Background())
	go func() {
		var appIdTotalPerSecond = make(map[string]int)
		var appIdRejectCount = make(map[string]int)
		for {
			select {
			case <-ctx1.Done():
				return
			case appIdResult := <-codeChan:
				totalPerSecond := appIdTotalPerSecond[appIdResult.appId]
				rejectCount := appIdRejectCount[appIdResult.appId]
				code := appIdResult.code
				if code == api.QuotaResultOk {
					totalPerSecond++
					rejectCount = 0
				} else {
					rejectCount++
					if rejectCount >= workerCount && totalPerSecond > 0 {
						allocatedPerSeconds := appIdToAllocated[appIdResult.appId]
						allocatedPerSeconds = append(allocatedPerSeconds, totalPerSecond)
						appIdToAllocated[appIdResult.appId] = allocatedPerSeconds
						totalPerSecond = 0
					}
				}
				appIdTotalPerSecond[appIdResult.appId] = totalPerSecond
				appIdRejectCount[appIdResult.appId] = rejectCount
			}
		}
	}()
	wg.Wait()
	cancel1()
	fmt.Printf("calledCount is %d\n", calledCount)
	fmt.Printf("allocatedPerSeconds is %v\n", appIdToAllocated)
	for appId, allocatedPerSeconds := range appIdToAllocated {
		fmt.Printf("allocatedPerSeconds for appId %s is %v\n", appId, allocatedPerSeconds)
		for i, allocatedPerSecond := range allocatedPerSeconds {
			if i == 0 {
				// 头部因为时间窗对齐原因，有可能出现不为100
				continue
			}
			if allocatedPerSecond < 5 {
				continue
			}
			c.Assert(allocatedPerSecond >= 95 && allocatedPerSecond <= 200, check.Equals, true)
		}
	}
}

// TestLocalRegexCombine 测试本地正则合并匹配限流
func (rt *LocalNormalTestingSuite) TestLocalRegexCombine(c *check.C) {
	// 4个线程跑20秒，看看每秒通过多少，以及总共通过多少
	workerCount := 2
	appIds := []string{"app1", "app2"}
	routineCount := workerCount * len(appIds)
	wg := &sync.WaitGroup{}
	wg.Add(routineCount)
	cfg := config.NewDefaultConfiguration([]string{mockDiscoverAddress})
	limitAPI, err := api.NewLimitAPIByConfig(cfg)
	c.Assert(err, check.IsNil)

	codeChan := make(chan model.QuotaResultCode, 10240)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer func() {
		cancel()
		close(codeChan)
	}()

	var calledCount int64
	for _, appId := range appIds {
		for i := 0; i < workerCount; i++ {
			go func(idx int, thisAppId string) {
				defer wg.Done()
				for {
					select {
					case <-ctx.Done():
						return
					default:
						resp := doSingleGetQuota(c, limitAPI, LocalTestSvcName, "",
							map[string]string{labelTestUin: thisAppId})
						atomic.AddInt64(&calledCount, 1)
						codeChan <- resp.Code
					}
				}
			}(i, appId)
		}
	}
	allocatedPerSeconds := make([]int, 0, 20)
	ctx1, cancel1 := context.WithCancel(context.Background())
	var totalRejectCount int32
	var totalPaasCount int32
	go func() {
		var totalPerSecond int
		var rejectCount int
		for {
			select {
			case <-ctx1.Done():
				return
			case code := <-codeChan:
				if code == api.QuotaResultOk {
					totalPerSecond++
					rejectCount = 0
					atomic.AddInt32(&totalPaasCount, 1)
				} else {
					rejectCount++
					atomic.AddInt32(&totalRejectCount, 1)
					if rejectCount >= routineCount && totalPerSecond > 0 {
						allocatedPerSeconds = append(allocatedPerSeconds, totalPerSecond)
						totalPerSecond = 0
					}
				}
			}
		}
	}()
	wg.Wait()
	cancel1()
	fmt.Printf("calledCount is %d\n", calledCount)
	fmt.Printf("allocatedPerSeconds is %v\n", allocatedPerSeconds)
	c.Assert(totalRejectCount > 0, check.Equals, true)
	c.Assert(totalPaasCount > 0, check.Equals, true)
}
