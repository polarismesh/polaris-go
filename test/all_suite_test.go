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

package test

import (
	"log"
	"net/http"
	_ "net/http/pprof"
	"testing"

	. "gopkg.in/check.v1"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/test/circuitbreak"
	"github.com/polarismesh/polaris-go/test/discover"
	"github.com/polarismesh/polaris-go/test/loadbalance"
	"github.com/polarismesh/polaris-go/test/ratelimit"
	"github.com/polarismesh/polaris-go/test/reporthandler"
	"github.com/polarismesh/polaris-go/test/serviceroute"
	"github.com/polarismesh/polaris-go/test/stability"
	"github.com/polarismesh/polaris-go/test/subscribe"
)

// Test 测试用例主入口
func Test(t *testing.T) {
	go func() {
		log.Println(http.ListenAndServe("LOCALHOST:6060", nil))
	}()
	TestingT(t)
}

// 初始化测试套
func init() {
	logDir := "testdata/test_log"
	if err := api.ConfigLoggers(logDir, api.DebugLog); err != nil {
		log.Fatalf("fail to ConfigLoggers: %v", err)
	}
	// sdkcontext 销毁测试
	Suite(&stability.SDKContextDestroySuite{})
	// consumer api测试
	Suite(&discover.ConsumerTestingSuite{})
	// provider api 测试
	Suite(&discover.ProviderTestingSuite{})
	// 负载均衡测试
	Suite(&loadbalance.LBTestingSuite{})
	//缓存持久化测试
	Suite(&stability.CacheTestingSuite{})
	// 熔断测试
	Suite(&circuitbreak.CircuitBreakSuite{})
	// 健康探测测试
	Suite(&circuitbreak.HealthCheckTestingSuite{})
	// 持久探测测试
	Suite(&circuitbreak.HealthCheckAlwaysTestingSuite{})
	// 就近路由接入测试
	Suite(&serviceroute.NearbyTestingSuite{})
	// 服务定时更新测试
	Suite(&stability.ServiceUpdateSuite{})
	// 后台server连接切换测试
	Suite(&stability.ServerSwitchSuite{})
	// 规则路由测试
	Suite(&serviceroute.RuleRoutingTestingSuite{})
	// dstmeta路由插件测试
	Suite(&serviceroute.DstMetaTestingSuite{})
	// 埋点server可靠性测试
	Suite(&stability.DefaultServerSuite{})
	// 缓存快速更新测试
	Suite(&stability.CacheFastUpdateSuite{})
	// set分组测试
	Suite(&serviceroute.SetDivisionTestingSuite{})
	// server异常调用测试
	Suite(&stability.ServerFailOverSuite{})
	// 消息订阅 测试
	Suite(&subscribe.EventSubscribeSuit{})
	//金丝雀路由测试
	Suite(&serviceroute.CanaryTestingSuite{})
	// 内部服务结构测试
	Suite(&loadbalance.InnerServiceLBTestingSuite{})
	// 基础本地限流用例测试
	Suite(&ratelimit.LocalNormalTestingSuite{})
	// ReportClient相关测试用例
	Suite(&reporthandler.ReporthandlerTestingSuite{})
	//// 限流规则变更用例测试
	//Suite(&ratelimit.RuleChangeTestingSuite{})
	//// 基础远程限流用例测试
	//Suite(&ratelimit.RemoteNormalTestingSuite{})
}
