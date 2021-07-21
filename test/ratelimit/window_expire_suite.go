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
	"fmt"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/flow"
	"github.com/polarismesh/polaris-go/pkg/flow/data"
	"github.com/polarismesh/polaris-go/plugin/serverconnector/grpc"
	"gopkg.in/check.v1"
	"time"
)

//远程正常用例测试
type WindowExpireTestingSuite struct {
	CommonRateLimitSuite
}

//用例名称
func (rt *WindowExpireTestingSuite) GetName() string {
	return "WindowExpireTestingSuite"
}

//SetUpSuite 启动测试套程序
func (rt *WindowExpireTestingSuite) SetUpSuite(c *check.C) {
	rt.CommonRateLimitSuite.SetUpSuite(c, true)
}

//SetUpSuite 结束测试套程序
func (rt *WindowExpireTestingSuite) TearDownSuite(c *check.C) {
	rt.CommonRateLimitSuite.TearDownSuite(c, rt)
}

//测试UIN过期
func (rt *WindowExpireTestingSuite) TestUinExpiredLocal(c *check.C) {
	maxSize := 11
	maxQps := 10000
	cfg := config.NewDefaultConfiguration([]string{mockDiscoverAddress})
	//设置最大的uin数量
	cfg.GetProvider().GetRateLimit().SetMaxWindowSize(maxSize - 1)
	cfg.GetProvider().GetRateLimit().SetPurgeInterval(1 * time.Second)
	limitAPI, err := api.NewLimitAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer limitAPI.Destroy()

	var userIdToQps = make(map[string]int)
	for q := 0; q < maxQps; q++ {
		for i := 0; i < maxSize; i++ {
			key := fmt.Sprintf("user-%d", i)
			resp := doSingleGetQuota(
				c, limitAPI, WindowExpireSvcName, map[string]string{labelUin: key})
			if resp.Code == api.QuotaResultOk {
				userIdToQps[key] = userIdToQps[key] + 1
			}
		}
	}
	fmt.Printf("0.quotaUsed is %v\n", userIdToQps)
	for i := 0; i < maxSize; i++ {
		key := fmt.Sprintf("user-%d", i)
		if i == maxSize-1 {
			//超过最大限制的则不进行限流
			c.Assert(userIdToQps[key], check.Equals, maxQps)
		} else {
			c.Assert(userIdToQps[key] < maxQps, check.Equals, true)
		}
	}

	//等待淘汰
	time.Sleep(3 * time.Second)
	//重新获取并计算
	userIdToQps = make(map[string]int)
	for q := 0; q < maxQps; q++ {
		for i := maxSize - 1; i >= 0; i-- {
			key := fmt.Sprintf("user-%d", i)
			resp := doSingleGetQuota(
				c, limitAPI, WindowExpireSvcName, map[string]string{labelUin: key})
			if resp.Code == api.QuotaResultOk {
				userIdToQps[key] = userIdToQps[key] + 1
			}
		}
	}
	fmt.Printf("1.quotaUsed is %v\n", userIdToQps)
	for i := 0; i < maxSize; i++ {
		key := fmt.Sprintf("user-%d", i)
		if i == 0 {
			//超过最大限制的则不进行限流
			c.Assert(userIdToQps[key], check.Equals, maxQps)
		} else {
			c.Assert(userIdToQps[key] < maxQps, check.Equals, true)
		}
	}
}

//测试UIN过期
func (rt *WindowExpireTestingSuite) TestUinExpiredRemote(c *check.C) {
	maxSize := 11
	maxQps := 10000
	cfg := config.NewDefaultConfiguration([]string{mockDiscoverAddress})
	//设置最大的uin数量
	cfg.GetProvider().GetRateLimit().SetMaxWindowSize(maxSize - 1)
	cfg.GetProvider().GetRateLimit().SetPurgeInterval(1 * time.Second)
	cfg.GetGlobal().GetServerConnector().SetMessageTimeout(200 * time.Millisecond)
	cfg.GetGlobal().GetServerConnector().SetConnectionIdleTimeout(1 * time.Second)
	limitAPI, err := api.NewLimitAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer limitAPI.Destroy()

	var userIdToQps = make(map[string]int)
	for q := 0; q < maxQps; q++ {
		for i := 0; i < maxSize; i++ {
			key := fmt.Sprintf("user-%d", i)
			resp := doSingleGetQuota(
				c, limitAPI, WindowExpireSvcName, map[string]string{labelAppId: "remote", labelUin: key})
			if resp.Code == api.QuotaResultOk {
				userIdToQps[key] = userIdToQps[key] + 1
			}
		}
	}
	fmt.Printf("0.quotaUsed is %v\n", userIdToQps)
	for i := 0; i < maxSize; i++ {
		key := fmt.Sprintf("user-%d", i)
		if i == maxSize-1 {
			//超过最大限制的则不进行限流
			c.Assert(userIdToQps[key], check.Equals, maxQps)
		} else {
			c.Assert(userIdToQps[key] < maxQps, check.Equals, true)
		}
	}
	//检查定时任务，以及后端连接是否已经被回收
	engine := limitAPI.SDKContext().GetEngine().(*flow.Engine)
	taskValues := engine.FlowQuotaAssistant().TaskValues()
	c.Assert(taskValues.Started(), check.Equals, true)

	connector, err := data.GetServerConnector(cfg, engine.PluginSupplier())
	c.Assert(err, check.IsNil)
	asyncRateLimitConnector := connector.GetAsyncRateLimitConnector().(*grpc.AsyncRateLimitConnector)
	c.Assert(asyncRateLimitConnector.StreamCount(), check.Equals, 1)

	//等待淘汰
	time.Sleep(3 * time.Second)
	c.Assert(taskValues.Started(), check.Equals, false)
	time.Sleep(4 * time.Second)
	c.Assert(asyncRateLimitConnector.StreamCount(), check.Equals, 0)
}
