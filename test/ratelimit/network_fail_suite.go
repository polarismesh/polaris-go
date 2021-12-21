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
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"gopkg.in/check.v1"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	rlimitV2 "github.com/polarismesh/polaris-go/pkg/model/pb/metric/v2"
)

// 网络异常测试套
type NetworkFailTestingSuite struct {
	CommonRateLimitSuite
	mockRateLimitServer *MockRateLimitServer
	mockGRPCServer      *grpc.Server
	mockGRPCListener    net.Listener
}

// 用例名称
func (rt *NetworkFailTestingSuite) GetName() string {
	return "NetworkFailTestingSuite"
}

// SetUpSuite 启动测试套程序
func (rt *NetworkFailTestingSuite) SetUpSuite(c *check.C) {
	rt.CommonRateLimitSuite.SetUpSuite(c, false)
	// 限流打桩服务
	rt.mockRateLimitServer = NewMockRateLimitServer()
	rt.mockGRPCServer = grpc.NewServer()
	rlimitV2.RegisterRateLimitGRPCV2Server(rt.mockGRPCServer, rt.mockRateLimitServer)
	var err error
	rt.mockGRPCListener, err = net.Listen(
		"tcp", fmt.Sprintf("%s:%d", mockRateLimitHost, mockRateLimitPort))
	if nil != err {
		log.Fatal(fmt.Sprintf("error listening mock rateLimitServer: %v", err))
	}
	go func() {
		rt.mockGRPCServer.Serve(rt.mockGRPCListener)
	}()
	// 打桩的server
	rLimitSvc, ok := rt.services[mockRateLimitName]
	c.Assert(ok, check.Equals, true)
	token := rLimitSvc.GetToken().GetValue()
	rt.mockServer.RegisterServerInstance(mockRateLimitHost, mockRateLimitPort, mockRateLimitName, token, true)
	// 无法连通的server
	rLimitSvc, ok = rt.services[NotExistsRateLimitName]
	c.Assert(ok, check.Equals, true)
	token = rLimitSvc.GetToken().GetValue()
	rt.mockServer.RegisterServerInstance(
		mockRateLimitHost, NotExistRateLimitPort, NotExistsRateLimitName, token, true)
}

// SetUpSuite 结束测试套程序
func (rt *NetworkFailTestingSuite) TearDownSuite(c *check.C) {
	rt.mockGRPCServer.Stop()
	rt.CommonRateLimitSuite.TearDownSuite(c, rt)
}

// 测试初始化接口4xx失败
func (rt *NetworkFailTestingSuite) TestInit4xx(c *check.C) {
	ctxCount := 2
	rt.mockRateLimitServer.MarkOperation4XX(OperationInit)
	rt.mockRateLimitServer.SetMockMaxAmount(600)
	defer rt.mockRateLimitServer.Reset()
	limits := make([]api.LimitAPI, 0, ctxCount)
	for i := 0; i < ctxCount; i++ {
		cfg := config.NewDefaultConfiguration([]string{mockDiscoverAddress})
		limitAPI, err := api.NewLimitAPIByConfig(cfg)
		c.Assert(err, check.IsNil)
		rt.mockRateLimitServer.SetClientKey(
			limitAPI.SDKContext().GetEngine().GetContext().GetClientId(), uint32(i)+1)
		limits = append(limits, limitAPI)
	}
	defer func() {
		for _, limit := range limits {
			limit.Destroy()
		}
	}()
	wg := &sync.WaitGroup{}
	wg.Add(ctxCount)
	var passed int32
	for i := 0; i < ctxCount; i++ {
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				resp := doSingleGetQuota(c, limits[idx],
					NetworkFailSvcName, map[string]string{labelMethod: "fail4xx"})
				if resp.Code == api.QuotaResultOk {
					atomic.AddInt32(&passed, 1)
				}
				time.Sleep(1 * time.Millisecond)
			}
		}(i)
	}
	wg.Wait()
	fmt.Printf("passed amount is %d\n", passed)
	// 远端初始化失败，使用本地配额
	c.Assert(passed >= 200 && passed <= 400, check.Equals, true)
}

// 测试上报接口延迟较高
func (rt *NetworkFailTestingSuite) TestDelayReturn(c *check.C) {
	ctxCount := 2
	rt.mockRateLimitServer.MarkOperationDelay(OperationInit)
	rt.mockRateLimitServer.MarkOperationDelay(OperationReport)
	rt.mockRateLimitServer.SetMockMaxAmount(1000)
	defer rt.mockRateLimitServer.Reset()
	limits := make([]api.LimitAPI, 0, ctxCount)
	for i := 0; i < ctxCount; i++ {
		cfg := config.NewDefaultConfiguration([]string{mockDiscoverAddress})
		limitAPI, err := api.NewLimitAPIByConfig(cfg)
		c.Assert(err, check.IsNil)
		rt.mockRateLimitServer.SetClientKey(
			limitAPI.SDKContext().GetEngine().GetContext().GetClientId(), uint32(i)+1)
		limits = append(limits, limitAPI)
	}
	defer func() {
		for _, limit := range limits {
			limit.Destroy()
		}
	}()
	wg := &sync.WaitGroup{}
	wg.Add(ctxCount)
	var passed int32
	for i := 0; i < ctxCount; i++ {
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 800; j++ {
				resp := doSingleGetQuota(c, limits[idx],
					NetworkFailSvcName, map[string]string{labelMethod: "initReturnDelay"})
				if resp.Code == api.QuotaResultOk {
					atomic.AddInt32(&passed, 1)
				}
				if j == 0 {
					time.Sleep(2500 * time.Millisecond)
				} else {
					time.Sleep(1 * time.Millisecond)
				}
			}
		}(i)
	}
	wg.Wait()
	fmt.Printf("passed amount is %d\n", passed)
	// 远端初始化失败，使用本地配额
	c.Assert(passed >= 400 && passed <= 700, check.Equals, true)
}

// 测试上报接口不返回
func (rt *NetworkFailTestingSuite) TestReportNoReturn(c *check.C) {
	ctxCount := 2
	rt.mockRateLimitServer.MarkOperationNoReturn(OperationReport)
	rt.mockRateLimitServer.SetMockMaxAmount(1000)
	defer rt.mockRateLimitServer.Reset()
	limits := make([]api.LimitAPI, 0, ctxCount)
	for i := 0; i < ctxCount; i++ {
		cfg := config.NewDefaultConfiguration([]string{mockDiscoverAddress})
		limitAPI, err := api.NewLimitAPIByConfig(cfg)
		c.Assert(err, check.IsNil)
		rt.mockRateLimitServer.SetClientKey(
			limitAPI.SDKContext().GetEngine().GetContext().GetClientId(), uint32(i)+1)
		limits = append(limits, limitAPI)
	}
	defer func() {
		for _, limit := range limits {
			limit.Destroy()
		}
	}()
	wg := &sync.WaitGroup{}
	wg.Add(ctxCount)
	var passed int32
	for i := 0; i < ctxCount; i++ {
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				resp := doSingleGetQuota(c, limits[idx],
					NetworkFailSvcName, map[string]string{labelMethod: "reportDelay"})
				if resp.Code == api.QuotaResultOk {
					atomic.AddInt32(&passed, 1)
				}
				if j == 0 {
					time.Sleep(1500 * time.Millisecond)
				} else {
					time.Sleep(1 * time.Millisecond)
				}
			}
		}(i)
	}
	wg.Wait()
	fmt.Printf("passed amount is %d\n", passed)
	// 远端初始化失败，使用本地配额
	c.Assert(passed >= 100 && passed <= 220, check.Equals, true)
}

// 测试节点down机
func (rt *NetworkFailTestingSuite) TestConnectFail(c *check.C) {
	cfg := config.NewDefaultConfiguration([]string{mockDiscoverAddress})
	limitAPI, err := api.NewLimitAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	var passed int32
	startTime := time.Now()
	for j := 0; j < 2000; j++ {
		resp := doSingleGetQuota(c, limitAPI,
			NetworkFailSvcName, map[string]string{labelMethod: "serverConnectFail"})
		if resp.Code == api.QuotaResultOk {
			atomic.AddInt32(&passed, 1)
		}
		time.Sleep(1 * time.Millisecond)
	}
	timePassed := time.Since(startTime)
	fmt.Printf("passed amount is %d, timePassed is %v\n", passed, timePassed)
	// 远端初始化失败，使用本地配额
	c.Assert(passed >= 150 && passed <= 300, check.Equals, true)
}
