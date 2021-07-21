/**
 * Tencent is pleased to support the open source community by making CL5 available.
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
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"gopkg.in/check.v1"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

//远程正常用例测试
type RemoteNormalTestingSuite struct {
	CommonRateLimitSuite
}

//用例名称
func (rt *RemoteNormalTestingSuite) GetName() string {
	return "RemoteNormalTestingSuite"
}

//SetUpSuite 启动测试套程序
func (rt *RemoteNormalTestingSuite) SetUpSuite(c *check.C) {
	rt.CommonRateLimitSuite.SetUpSuite(c, true)
}

//SetUpSuite 结束测试套程序
func (rt *RemoteNormalTestingSuite) TearDownSuite(c *check.C) {
	rt.CommonRateLimitSuite.TearDownSuite(c, rt)
}

//带标识的结果
type IndexResult struct {
	index int
	label string
	code  model.QuotaResultCode
}

// 测试不能设置polaris.metric为限流集群
func (rt *RemoteNormalTestingSuite) TestNoSetPolarisMetric(c *check.C) {
	log.Printf("Start TestNoSetPolarisMetric")
	cfg := api.NewConfiguration()
	consumer, err := api.NewProviderAPIByConfig(cfg)
	_ = consumer
	c.Assert(err, check.IsNil)
	consumer.Destroy()

	cfg = api.NewConfiguration()
	cfg.GetProvider().GetRateLimit().SetRateLimitCluster(config.ServerNamespace, "polaris.metric.test")
	consumer, err = api.NewProviderAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	consumer.Destroy()

	cfg = api.NewConfiguration()
	cfg.GetProvider().GetRateLimit().SetRateLimitCluster(config.ServerNamespace, config.ForbidServerMetricService)
	consumer, err = api.NewProviderAPIByConfig(cfg)
	fmt.Println(err)
	c.Assert(err, check.NotNil)
}

//测试远程精准匹配限流
func (rt *RemoteNormalTestingSuite) TestRemoteTwoDuration(c *check.C) {
	log.Printf("Start TestRemoteTwoDuration")
	//多个线程，然后每个线程一个client，每个client跑相同的labels
	workerCount := 4
	wg := &sync.WaitGroup{}
	wg.Add(workerCount)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	codeChan := make(chan IndexResult)
	var calledCount int64
	for i := 0; i < workerCount; i++ {
		go func(idx int) {
			defer wg.Done()
			cfg := config.NewDefaultConfiguration([]string{mockDiscoverAddress})
			limitAPI, err := api.NewLimitAPIByConfig(cfg)
			c.Assert(err, check.IsNil)
			defer limitAPI.Destroy()
			once := &sync.Once{}
			for {
				select {
				case <-ctx.Done():
					return
				default:
					resp := doSingleGetQuota(c, limitAPI, RemoteTestSvcName,
						map[string]string{labelMethod: "query", labelUin: "007"})
					atomic.AddInt64(&calledCount, 1)
					codeChan <- IndexResult{
						index: idx,
						code:  resp.Code,
					}
					once.Do(func() {
						time.Sleep(100 * time.Millisecond)
					})
					time.Sleep(5 * time.Millisecond)
				}
			}
		}(i)
	}
	allocatedPerSeconds := make([]int, 0, 20)
	var allocatedTotal int
	ctx1, cancel1 := context.WithCancel(context.Background())
	go func() {
		var totalPerSecond int
		var rejectIdx = make(map[int]bool)
		for {
			select {
			case <-ctx1.Done():
				return
			case idxResult := <-codeChan:
				if idxResult.code == api.QuotaResultOk {
					allocatedTotal++
					totalPerSecond++
					rejectIdx = make(map[int]bool)
				} else {
					rejectIdx[idxResult.index] = true
					if len(rejectIdx) >= workerCount && totalPerSecond > 0 {
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
	fmt.Printf("allocatedTotal is %d\n", allocatedTotal)
	for i, allocatedPerSecond := range allocatedPerSeconds {
		if i == 0 {
			//头部因为时间窗对齐原因，有可能出现不为100
			continue
		}
		if allocatedPerSecond < 100 {
			//中间出现了10s区间限流的情况，屏蔽
			continue
		}
		c.Assert(allocatedPerSecond >= 150 && allocatedPerSecond <= 230, check.Equals, true)
	}
	c.Assert(allocatedTotal >= 800 && allocatedTotal <= 1650, check.Equals, true)
}

//测试正则表达式的uin限流
func (rt *RemoteNormalTestingSuite) TestRemoteRegexV2(c *check.C) {
	log.Printf("Start TestRemoteRegexV2")
	//多个线程，然后每个线程一个client，每个client跑相同的labels
	workerCount := 4
	wg := &sync.WaitGroup{}
	wg.Add(workerCount)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	appIds := []string{"appId1", "appId2"}

	codeChan := make(chan IndexResult)
	var calledCount int64
	for i := 0; i < workerCount; i++ {
		go func(idx int) {
			defer wg.Done()
			cfg := config.NewDefaultConfiguration([]string{mockDiscoverAddress})
			limitAPI, err := api.NewLimitAPIByConfig(cfg)
			c.Assert(err, check.IsNil)
			defer limitAPI.Destroy()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					for _, appId := range appIds {
						resp := doSingleGetQuota(c, limitAPI, RemoteTestSvcName,
							map[string]string{labelAppId: appId})
						atomic.AddInt64(&calledCount, 1)
						codeChan <- IndexResult{
							index: idx,
							label: appId,
							code:  resp.Code,
						}
						time.Sleep(5 * time.Millisecond)
					}
				}
			}
		}(i)
	}
	appIdAllocatedPerSeconds := make(map[string][]int)
	for _, appId := range appIds {
		appIdAllocatedPerSeconds[appId] = make([]int, 0)
	}
	ctx1, cancel1 := context.WithCancel(context.Background())
	go func() {
		var appIdTotalPerSecond = make(map[string]int)
		var appIdRejectIdx = make(map[string]map[int]bool)
		for _, appId := range appIds {
			appIdRejectIdx[appId] = make(map[int]bool)
			appIdTotalPerSecond[appId] = 0
		}
		for {
			select {
			case <-ctx1.Done():
				return
			case idxResult := <-codeChan:
				rejectIdx := appIdRejectIdx[idxResult.label]
				totalPerSecond := appIdTotalPerSecond[idxResult.label]
				allocatedPerSeconds := appIdAllocatedPerSeconds[idxResult.label]
				if idxResult.code == api.QuotaResultOk {
					appIdTotalPerSecond[idxResult.label] = totalPerSecond + 1
					if len(rejectIdx) > 0 {
						appIdRejectIdx[idxResult.label] = make(map[int]bool)
					}
				} else {
					rejectIdx[idxResult.index] = true
					if len(rejectIdx) >= workerCount && totalPerSecond > 0 {
						allocatedPerSeconds = append(allocatedPerSeconds, totalPerSecond)
						appIdAllocatedPerSeconds[idxResult.label] = allocatedPerSeconds
						appIdTotalPerSecond[idxResult.label] = 0
					}
				}
			}
		}
	}()
	wg.Wait()
	cancel1()
	fmt.Printf("calledCount is %d\n", calledCount)
	fmt.Printf("appIdAllocatedPerSeconds is %v\n", appIdAllocatedPerSeconds)
	for _, allocatedPerSeconds := range appIdAllocatedPerSeconds {
		for i, allocatedPerSecond := range allocatedPerSeconds {
			if i == 0 {
				//头部因为时间窗对齐原因，有可能出现不为100
				continue
			}
			c.Assert(allocatedPerSecond >= 90 && allocatedPerSecond <= 120, check.Equals, true)
		}
	}
}

//测试正则表达式的uin限流
func (rt *RemoteNormalTestingSuite) TestRemoteRegexCombineV2(c *check.C) {
	log.Printf("Start TestRemoteRegexCombineV2")
	workerCount := 4
	wg := &sync.WaitGroup{}
	wg.Add(workerCount)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	appIds := []string{"appId1", "appId2"}

	codeChan := make(chan IndexResult)
	var calledCount int64
	for i := 0; i < workerCount; i++ {
		go func(idx int) {
			defer wg.Done()
			cfg := config.NewDefaultConfiguration([]string{mockDiscoverAddress})
			//测试通过SDK来设置集群名，兼容场景
			cfg.GetProvider().GetRateLimit().SetRateLimitCluster(config.ServerNamespace, rateLimitSvcName)
			limitAPI, err := api.NewLimitAPIByConfig(cfg)
			c.Assert(err, check.IsNil)
			defer limitAPI.Destroy()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					for _, appId := range appIds {
						resp := doSingleGetQuota(c, limitAPI, RemoteTestSvcName,
							map[string]string{labelTestUin: appId})
						atomic.AddInt64(&calledCount, 1)
						codeChan <- IndexResult{
							index: idx,
							label: appId,
							code:  resp.Code,
						}
						time.Sleep(5 * time.Millisecond)
					}
				}
			}
		}(i)
	}
	allocatedPerSeconds := make([]int, 0, 20)
	ctx1, cancel1 := context.WithCancel(context.Background())
	go func() {
		var totalPerSecond int
		var rejectIdx = make(map[int]bool)
		for {
			select {
			case <-ctx1.Done():
				return
			case idxResult := <-codeChan:
				if idxResult.code == api.QuotaResultOk {
					totalPerSecond++
					rejectIdx = make(map[int]bool)
				} else {
					rejectIdx[idxResult.index] = true
					if len(rejectIdx) >= workerCount && totalPerSecond > 0 {
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
			//头部因为时间窗对齐原因，有可能出现不为100
			continue
		}
		c.Assert(allocatedPerSecond >= 280 && allocatedPerSecond <= 350, check.Equals, true)
	}
}

//测试单机均摊模式限流
func (rt *RemoteNormalTestingSuite) TestRemoteShareEqually(c *check.C) {
	log.Printf("Start TestRemoteShareEqually")
	workerCount := 10
	wg := &sync.WaitGroup{}
	wg.Add(workerCount)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	codeChan := make(chan IndexResult)
	var calledCount int64
	startTime := model.CurrentMillisecond()
	for i := 0; i < workerCount; i++ {
		go func(idx int) {
			defer wg.Done()
			cfg := config.NewDefaultConfiguration([]string{mockDiscoverAddress})
			//测试通过SDK来设置集群名，兼容场景
			cfg.GetProvider().GetRateLimit().SetRateLimitCluster(config.ServerNamespace, rateLimitSvcName)
			limitAPI, err := api.NewLimitAPIByConfig(cfg)
			c.Assert(err, check.IsNil)
			defer limitAPI.Destroy()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					resp := doSingleGetQuota(c, limitAPI, RemoteTestSvcName,
						map[string]string{"appIdShare": "appShare"})
					atomic.AddInt64(&calledCount, 1)
					curTime := model.CurrentMillisecond()
					if curTime - startTime >= 1000 {
						//前500ms是上下线，不计算
						codeChan <- IndexResult{
							index: idx,
							code:  resp.Code,
						}
					}
					time.Sleep(5 * time.Millisecond)
				}
			}
		}(i)
	}
	allocatedPerSeconds := make([]int, 0, 20)
	ctx1, cancel1 := context.WithCancel(context.Background())
	go func() {
		var totalPerSecond int
		var rejectIdx = make(map[int]bool)
		for {
			select {
			case <-ctx1.Done():
				return
			case idxResult := <-codeChan:
				if idxResult.code == api.QuotaResultOk {
					totalPerSecond++
					rejectIdx = make(map[int]bool)
				} else {
					rejectIdx[idxResult.index] = true
					if len(rejectIdx) >= workerCount && totalPerSecond > 0 {
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
			//头部因为时间窗对齐原因，有可能出现不为100
			continue
		}
		c.Assert(allocatedPerSecond >= 170 && allocatedPerSecond <= 260, check.Equals, true)
	}
}