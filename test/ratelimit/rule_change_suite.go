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
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/flow"
	v1 "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/test/util"
	"github.com/golang/protobuf/ptypes/wrappers"
	"github.com/google/uuid"
	"gopkg.in/check.v1"
	"time"
)

//测试规则变更的测试套
type RuleChangeTestingSuite struct {
	CommonRateLimitSuite
}

//用例名称
func (rt *RuleChangeTestingSuite) GetName() string {
	return "RuleChangeTestingSuite"
}

//SetUpSuite 启动测试套程序
func (rt *RuleChangeTestingSuite) SetUpSuite(c *check.C) {
	rt.CommonRateLimitSuite.SetUpSuite(c, true)
}

//SetUpSuite 结束测试套程序
func (rt *RuleChangeTestingSuite) TearDownSuite(c *check.C) {
	rt.CommonRateLimitSuite.TearDownSuite(c, rt)
}

//测试规则被屏蔽
func (rt *RuleChangeTestingSuite) TestRuleDisabledV2(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	maxQps := 10000
	cfg := config.NewDefaultConfiguration([]string{mockDiscoverAddress})
	cfg.GetConsumer().GetLocalCache().SetPersistDir(util.BackupDir)
	limitAPI, err := api.NewLimitAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer limitAPI.Destroy()

	var passedCount int
	for i := 0; i < maxQps; i++ {
		resp := doSingleGetQuota(
			c, limitAPI, RuleChangeSvcName, map[string]string{labelMethod: "disable"})
		if resp.Code == api.QuotaResultOk {
			passedCount++
		}
	}
	fmt.Printf("0.passedCount is %v\n", passedCount)
	c.Assert(passedCount >= 100 && passedCount <= 200, check.Equals, true)

	rule := rt.rules[RuleChangeSvcName]
	rule.Rules[0].Disable = &wrappers.BoolValue{Value: true}
	rule.Rules[0].Revision = &wrappers.StringValue{Value: uuid.New().String()}
	rule.Revision = &wrappers.StringValue{Value: uuid.New().String()}

	time.Sleep(5 * time.Second)
	passedCount = 0
	for i := 0; i < maxQps; i++ {
		resp := doSingleGetQuota(
			c, limitAPI, RuleChangeSvcName, map[string]string{labelMethod: "disable"})
		if resp.Code == api.QuotaResultOk {
			passedCount++
		}
	}
	fmt.Printf("1.passedCount is %v\n", passedCount)
	c.Assert(passedCount, check.Equals, maxQps)

	//重新开启规则
	rule.Rules[0].Disable = &wrappers.BoolValue{Value: false}
	rule.Rules[0].Revision = &wrappers.StringValue{Value: uuid.New().String()}
	rule.Revision = &wrappers.StringValue{Value: uuid.New().String()}
	time.Sleep(5 * time.Second)
	passedCount = 0
	for i := 0; i < maxQps; i++ {
		resp := doSingleGetQuota(
			c, limitAPI, RuleChangeSvcName, map[string]string{labelMethod: "disable"})
		if resp.Code == api.QuotaResultOk {
			passedCount++
		}
	}
	fmt.Printf("2.passedCount is %v\n", passedCount)
	c.Assert(passedCount >= 100 && passedCount <= 200, check.Equals, true)
}

//测试限流配额发生变更
func (rt *RuleChangeTestingSuite) TestAmountChangedV2(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	maxQps := 1000
	cfg := config.NewDefaultConfiguration([]string{mockDiscoverAddress})
	cfg.GetConsumer().GetLocalCache().SetPersistDir(util.BackupDir)
	limitAPI, err := api.NewLimitAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer limitAPI.Destroy()

	var passedCount int
	for i := 0; i < maxQps; i++ {
		resp := doSingleGetQuota(
			c, limitAPI, RuleChangeSvcName, map[string]string{labelMethod: "amountChange"})
		if resp.Code == api.QuotaResultOk {
			passedCount++
		}
		if i == 0 {
			//等待初始化结束
			time.Sleep(80 * time.Millisecond)
		} else {
			time.Sleep(500 * time.Microsecond)
		}
	}
	fmt.Printf("0.passedCount is %v\n", passedCount)
	c.Assert(passedCount >= 100 && passedCount <= 210, check.Equals, true)

	rule := rt.rules[RuleChangeSvcName]
	rule.Rules[1].Amounts[0].MaxAmount = &wrappers.UInt32Value{Value: 400}
	rule.Rules[1].Revision = &wrappers.StringValue{Value: uuid.New().String()}
	rule.Revision = &wrappers.StringValue{Value: uuid.New().String()}

	time.Sleep(5 * time.Second)
	passedCount = 0
	for i := 0; i < maxQps; i++ {
		resp := doSingleGetQuota(
			c, limitAPI, RuleChangeSvcName, map[string]string{labelMethod: "amountChange"})
		if resp.Code == api.QuotaResultOk {
			passedCount++
		}
		if i == 0 {
			//等待初始化结束
			time.Sleep(80 * time.Millisecond)
		} else {
			time.Sleep(500 * time.Microsecond)
		}
	}
	fmt.Printf("1.passedCount is %v\n", passedCount)
	c.Assert(passedCount >= 400 && passedCount <= 810, check.Equals, true)
}

//测试限流标签发生变更
func (rt *RuleChangeTestingSuite) TestLabelsChanged(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	maxQps := 1000
	cfg := config.NewDefaultConfiguration([]string{mockDiscoverAddress})
	cfg.GetConsumer().GetLocalCache().SetPersistDir(util.BackupDir)
	limitAPI, err := api.NewLimitAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer limitAPI.Destroy()

	var passedCount int
	for i := 0; i < maxQps; i++ {
		resp := doSingleGetQuota(
			c, limitAPI, RuleChangeSvcName, map[string]string{labelMethod: "labelChange"})
		if resp.Code == api.QuotaResultOk {
			passedCount++
		}
		if i == 0 {
			//等待初始化结束
			fmt.Printf("start wait first\n")
			time.Sleep(1 * time.Second)
			fmt.Printf("end wait first\n")
		} else {
			time.Sleep(800 * time.Microsecond)
		}
	}
	fmt.Printf("0.passedCount is %v\n", passedCount)
	c.Assert(passedCount >= 100 && passedCount <= 210, check.Equals, true)

	rule := rt.rules[RuleChangeSvcName]
	rule.Rules[2].Labels[labelAppId] = &v1.MatchString{
		Type: v1.MatchString_EXACT,
		Value: &wrappers.StringValue{Value: "changedApp"},
	}
	rule.Rules[2].Revision = &wrappers.StringValue{Value: uuid.New().String()}
	rule.Revision = &wrappers.StringValue{Value: uuid.New().String()}

	time.Sleep(5 * time.Second)
	passedCount = 0
	for i := 0; i < maxQps; i++ {
		resp := doSingleGetQuota(
			c, limitAPI, RuleChangeSvcName, map[string]string{labelMethod: "labelChange", labelAppId: "changedApp"})
		if resp.Code == api.QuotaResultOk {
			passedCount++
		}
		if i == 0 {
			//等待初始化结束
			time.Sleep(50 * time.Millisecond)
		} else {
			time.Sleep(500 * time.Microsecond)
		}
	}
	fmt.Printf("1.passedCount is %v\n", passedCount)
	c.Assert(passedCount >= 100 && passedCount <= 210, check.Equals, true)

	passedCount = 0
	for i := 0; i < maxQps; i++ {
		resp := doSingleGetQuota(
			c, limitAPI, RuleChangeSvcName, map[string]string{labelMethod: "labelChange"})
		if resp.Code == api.QuotaResultOk {
			passedCount++
		}
		time.Sleep(500 * time.Microsecond)
	}
	fmt.Printf("2.passedCount is %v\n", passedCount)
	c.Assert(passedCount, check.Equals, maxQps)
}

//测试规则被删除
func (rt *RuleChangeTestingSuite) TestRuleDeleted(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	maxQps := 1000
	cfg := config.NewDefaultConfiguration([]string{mockDiscoverAddress})
	cfg.GetConsumer().GetLocalCache().SetPersistDir(util.BackupDir)
	limitAPI, err := api.NewLimitAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer limitAPI.Destroy()

	var passedCount int
	for i := 0; i < maxQps; i++ {
		resp := doSingleGetQuota(
			c, limitAPI, RuleDeletedSvcName, map[string]string{labelMethod: "ruleDeleted"})
		if resp.Code == api.QuotaResultOk {
			passedCount++
		}
		if i == 0 {
			//等待初始化结束
			time.Sleep(50 * time.Millisecond)
		} else {
			time.Sleep(500 * time.Microsecond)
		}
	}
	fmt.Printf("0.passedCount is %v\n", passedCount)
	c.Assert(passedCount >= 100 && passedCount <= 210, check.Equals, true)

	rt.mockServer.DeRegisterRateLimitRule(rt.services[RuleDeletedSvcName])
	time.Sleep(5 * time.Second)
	passedCount = 0
	for i := 0; i < maxQps; i++ {
		resp := doSingleGetQuota(
			c, limitAPI, RuleDeletedSvcName, map[string]string{labelMethod: "ruleDeleted"})
		if resp.Code == api.QuotaResultOk {
			passedCount++
		}
		time.Sleep(500 * time.Microsecond)
	}
	fmt.Printf("2.passedCount is %v\n", passedCount)
	c.Assert(passedCount, check.Equals, maxQps)
}

//测试服务被删除
func (rt *RuleChangeTestingSuite) TestServiceDeleted(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	maxQps := 1000
	cfg := config.NewDefaultConfiguration([]string{mockDiscoverAddress})
	cfg.GetConsumer().GetLocalCache().SetPersistDir(util.BackupDir)
	limitAPI, err := api.NewLimitAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer limitAPI.Destroy()

	var passedCount int
	for i := 0; i < maxQps; i++ {
		resp := doSingleGetQuota(
			c, limitAPI, SvcDeletedSvcName, map[string]string{labelMethod: "svcDeleted"})
		if resp.Code == api.QuotaResultOk {
			passedCount++
		}
		if i == 0 {
			//等待初始化结束
			time.Sleep(50 * time.Millisecond)
		} else {
			time.Sleep(500 * time.Microsecond)
		}
	}
	fmt.Printf("0.passedCount is %v\n", passedCount)
	c.Assert(passedCount >= 100 && passedCount <= 210, check.Equals, true)

	rt.mockServer.DeregisterService(namespaceTest, SvcDeletedSvcName)
	time.Sleep(5 * time.Second)
	engine := limitAPI.SDKContext().GetEngine().(*flow.Engine)
	wsCount := engine.FlowQuotaAssistant().CountRateLimitWindowSet()
	c.Assert(wsCount, check.Equals, 0)
}


