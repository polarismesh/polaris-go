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

package stability

import (
	"fmt"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	plog "github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/test/util"
	"gopkg.in/check.v1"
	"log"
	"runtime"
	"strings"
	"time"
)

//SDKContextDestroySuite
type SDKContextDestroySuite struct {
}

//设置套件
func (s *SDKContextDestroySuite) SetUpSuite(c *check.C) {
}

//销毁套件
func (s *SDKContextDestroySuite) TearDownSuite(c *check.C) {
}

//获取用例名
func (s *SDKContextDestroySuite) GetName() string {
	return "SDKContextDestroySuite"
}

//打印整体堆栈信息
func getAllStack() string {
	buf := make([]byte, 64<<10)
	buf = buf[:runtime.Stack(buf, true)]
	return string(buf)
}

const lumberJackPrefix = "github.com/natefinch/lumberjack.(*Logger).millRun"

//从调用栈解析routines数量，返回普通routines数量，lumberjack数量
func parseRoutines(stack string) (int, int) {
	fmt.Printf("%s", stack)
	var normalCount int
	var lumberjackCount int
	lines := strings.Split(stack, "\n\n")
	for _, line := range lines {
		rawLine := strings.TrimSpace(line)
		if !strings.HasPrefix(rawLine, "goroutine") {
			continue
		}
		if strings.Index(rawLine, lumberJackPrefix) > -1 {
			lumberjackCount++
		} else {
			normalCount++
		}
	}
	return normalCount, lumberjackCount
}

//测试consumer api的销毁
func (s *SDKContextDestroySuite) TestConsumerDestroy(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	log.Printf("Start to TestConsumerDestroy")
	//等待golang一些系统初始化任务完成
	time.Sleep(2 * time.Second)
	configuration := api.NewConfiguration()
	configuration.GetGlobal().GetServerConnector().SetAddresses([]string{"127.0.0.1:10011"})
	configuration.GetConsumer().GetLocalCache().SetPersistDir(util.BackupDir)
	var routinesCount int
	var preCreateRoutinesNum int
	routinesCount, _ = parseRoutines(getAllStack())
	preCreateRoutinesNum = routinesCount
	log.Printf("preCreateRoutinesNum %v", preCreateRoutinesNum)
	consumer, err := api.NewConsumerAPIByConfig(configuration)
	c.Assert(err, check.IsNil)
	log.Printf("consumerAPI runningRoutinesNum %v", runtime.NumGoroutine())
	consumer.Destroy()
	time.Sleep(5 * time.Second)
	var lumberjackCount int
	routinesCount, lumberjackCount = parseRoutines(getAllStack())
	afterDestroyRoutinesNum := routinesCount
	log.Printf("afterDestroyRoutinesNum %v", afterDestroyRoutinesNum)
	c.Assert(preCreateRoutinesNum, check.Equals, afterDestroyRoutinesNum)
	c.Assert(lumberjackCount < plog.MaxLogger, check.Equals, true)
}

//测试provider api的销毁
func (s *SDKContextDestroySuite) TestProviderDestroy(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	log.Printf("Start to TestProviderDestroy")
	//等待golang一些系统初始化任务完成
	time.Sleep(2 * time.Second)
	configuration := api.NewConfiguration()
	configuration.GetConsumer().GetLocalCache().SetPersistDir(util.BackupDir)
	var routinesCount int
	var preCreateRoutinesNum int
	routinesCount, _ = parseRoutines(getAllStack())
	preCreateRoutinesNum = routinesCount
	log.Printf("preCreateRoutinesNum %v", preCreateRoutinesNum)
	provider, err := api.NewProviderAPIByConfig(configuration)
	c.Assert(err, check.IsNil)
	log.Printf("providerAPI runningRoutinesNum %v", runtime.NumGoroutine())
	provider.Destroy()
	time.Sleep(5 * time.Second)
	var lumberjackCount int
	routinesCount, lumberjackCount = parseRoutines(getAllStack())
	afterDestroyRoutinesNum := routinesCount
	log.Printf("afterDestroyRoutinesNum %v", afterDestroyRoutinesNum)
	c.Assert(preCreateRoutinesNum, check.Equals, afterDestroyRoutinesNum)
	c.Assert(lumberjackCount < plog.MaxLogger, check.Equals, true)
}

func (s *SDKContextDestroySuite) TestServiceSpecific(c *check.C) {
	ctx, err := api.InitContextByFile("testdata/service_sp_conf.yaml")
	c.Check(err, check.IsNil)
	cb1 := ctx.GetConfig().GetConsumer().GetServiceSpecific("Test", "TestName1").GetServiceCircuitBreaker()
	c.Check(cb1, check.NotNil)
	c.Check(cb1.GetCheckPeriod(), check.Equals, time.Second*5)

	cb2 := ctx.GetConfig().GetConsumer().GetServiceSpecific("Test", "TestName2")
	c.Check(cb2, check.IsNil)

	cb1 = ctx.GetConfig().GetConsumer().GetServiceSpecific(config.ServerNamespace, config.ServerDiscoverService).GetServiceCircuitBreaker()
	c.Check(cb1, check.NotNil)
	errCount := cb1.GetErrorCountConfig()
	c.Check(errCount, check.NotNil)
	c.Check(errCount.GetContinuousErrorThreshold(), check.Equals, 1)
	router := ctx.GetConfig().GetConsumer().GetServiceSpecific(config.ServerNamespace, config.ServerDiscoverService).GetServiceRouter()
	c.Check(router, check.NotNil)
	nearby := router.GetNearbyConfig()
	c.Check(nearby, check.NotNil)
	c.Check(nearby.GetMatchLevel(), check.Equals, config.RegionLevel)
}
