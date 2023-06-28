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
	"bufio"
	"bytes"
	"errors"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"testing"

	. "gopkg.in/check.v1"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/test/circuitbreak"
	"github.com/polarismesh/polaris-go/test/discover"
	"github.com/polarismesh/polaris-go/test/loadbalance"
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

var (
	suitFunc = map[string]func(){
		"ConsumerTestingSuite":          mockConsumerTestingSuite,
		"ProviderTestingSuite":          mockProviderTestingSuite,
		"LBTestingSuite":                mockLBTestingSuite,
		"CircuitBreakSuite":             mockCircuitBreakSuite,
		"HealthCheckTestingSuite":       mockHealthCheckTestingSuite,
		"HealthCheckAlwaysTestingSuite": mockHealthCheckAlwaysTestingSuite,
		"NearbyTestingSuite":            mockNearbyTestingSuite,
		"RuleRoutingTestingSuite":       mockRuleRoutingTestingSuite,
		"DstMetaTestingSuite":           mockDstMetaTestingSuite,
		"SetDivisionTestingSuite":       mockSetDivisionTestingSuite,
		"CanaryTestingSuite":            mockCanaryTestingSuite,
		"CacheTestingSuite":             mockCacheTestingSuite,
		"ServiceUpdateSuite":            mockServiceUpdateSuite,
		"ServerSwitchSuite":             mockServerSwitchSuite,
		"DefaultServerSuite":            mockDefaultServerSuite,
		"CacheFastUpdateSuite":          mockCacheFastUpdateSuite,
		"ServerFailOverSuite":           mockServerFailOverSuite,
		"EventSubscribeSuit":            mockEventSubscribeSuit,
		"InnerServiceLBTestingSuite":    mockInnerServiceLBTestingSuite,
		"LocalNormalTestingSuite":       func() {},
		"RuleChangeTestingSuite":        func() {},
		"RemoteNormalTestingSuite":      func() {},
	}
)

// 初始化测试套
func init() {
	logDir := "testdata/test_log"
	if err := api.ConfigLoggers(logDir, api.DebugLog); err != nil {
		log.Fatalf("fail to ConfigLoggers: %v", err)
	}
	var suits []string
	suitType := os.Getenv("SDK_SUIT_TEST")
	if len(suitType) != 0 {
		suits = []string{suitType}
	} else {
		content, err := ioutil.ReadFile("suit.txt")
		if err != nil {
			panic(err)
		}
		reader := bufio.NewReader(bytes.NewBuffer(content))
		for {
			line, _, err := reader.ReadLine()
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				panic(err)
			}
			lineStr := string(line)
			if lineStr != "" {
				suits = append(suits, lineStr)
			}
		}
	}
	for i := range suits {
		runner, ok := suitFunc[suits[i]]
		if ok {
			runner()
		}
	}

	// // 基础本地限流用例测试
	// Suite(&ratelimit.LocalNormalTestingSuite{})
	// // 限流规则变更用例测试
	// Suite(&ratelimit.RuleChangeTestingSuite{})
	// // 基础远程限流用例测试
	// Suite(&ratelimit.RemoteNormalTestingSuite{})
}

func mockConsumerTestingSuite() {
	Suite(&discover.ConsumerTestingSuite{})
}

func mockProviderTestingSuite() {
	Suite(&discover.ProviderTestingSuite{})
}

func mockLBTestingSuite() {
	Suite(&loadbalance.LBTestingSuite{})
}

// 内部服务结构测试
func mockInnerServiceLBTestingSuite() {
	Suite(&loadbalance.InnerServiceLBTestingSuite{})
}

func mockCircuitBreakSuite() {
	Suite(&circuitbreak.CircuitBreakSuite{})
}

func mockHealthCheckTestingSuite() {
	Suite(&circuitbreak.HealthCheckTestingSuite{})
}

func mockHealthCheckAlwaysTestingSuite() {
	Suite(&circuitbreak.HealthCheckAlwaysTestingSuite{})
}

func mockNearbyTestingSuite() {
	Suite(&serviceroute.NearbyTestingSuite{})
}

func mockRuleRoutingTestingSuite() {
	Suite(&serviceroute.RuleRoutingTestingSuite{})
}

func mockDstMetaTestingSuite() {
	Suite(&serviceroute.DstMetaTestingSuite{})
}

func mockSetDivisionTestingSuite() {
	Suite(&serviceroute.SetDivisionTestingSuite{})
}

func mockCanaryTestingSuite() {
	Suite(&serviceroute.CanaryTestingSuite{})
}

// 缓存持久化测试
func mockCacheTestingSuite() {
	Suite(&stability.CacheTestingSuite{})
}

// 服务定时更新测试
func mockServiceUpdateSuite() {
	Suite(&stability.ServiceUpdateSuite{})
}

// 后台server连接切换测试
func mockServerSwitchSuite() {
	Suite(&stability.ServerSwitchSuite{})
}

// 埋点server可靠性测试
func mockDefaultServerSuite() {
	Suite(&stability.DefaultServerSuite{})
}

// 缓存快速更新测试
func mockCacheFastUpdateSuite() {
	Suite(&stability.CacheFastUpdateSuite{})
}

// server异常调用测试
func mockServerFailOverSuite() {
	Suite(&stability.ServerFailOverSuite{})
}

// 消息订阅 测试
func mockEventSubscribeSuit() {
	Suite(&subscribe.EventSubscribeSuit{})
}
