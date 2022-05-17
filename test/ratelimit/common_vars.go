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
	"encoding/binary"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"

	"github.com/golang/protobuf/jsonpb"
	"github.com/golang/protobuf/ptypes/wrappers"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"gopkg.in/check.v1"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/test/mock"
	"github.com/polarismesh/polaris-go/test/util"
)

var (
	// 限流规则的路径
	rulePaths = map[string]string{
		LocalTestSvcName:    "testdata/ratelimit_rule_v2/local_normal.json",
		RemoteTestSvcName:   "testdata/ratelimit_rule_v2/remote_normal.json",
		WindowExpireSvcName: "testdata/ratelimit_rule_v2/window_expire.json",
		RuleChangeSvcName:   "testdata/ratelimit_rule_v2/rule_change.json",
		RuleDeletedSvcName:  "testdata/ratelimit_rule_v2/rule_delete.json",
		SvcDeletedSvcName:   "testdata/ratelimit_rule_v2/svc_delete.json",
		NetworkFailSvcName:  "testdata/ratelimit_rule_v2/network_fail.json",
	}
	svcNames = map[string]string{
		rateLimitSvcName:       config.ServerNamespace,
		mockRateLimitName:      config.ServerNamespace,
		NotExistsRateLimitName: config.ServerNamespace,
		LocalTestSvcName:       namespaceTest,
		RemoteTestSvcName:      namespaceTest,
		WindowExpireSvcName:    namespaceTest,
		RuleChangeSvcName:      namespaceTest,
		RuleDeletedSvcName:     namespaceTest,
		SvcDeletedSvcName:      namespaceTest,
		NetworkFailSvcName:     namespaceTest,
	}
	mockDiscoverAddress = fmt.Sprintf("%s:%d", discoverHost, discoverPort)
	rateLimitHost       = int2ip(rateLimitHostInt)
)

const (
	discoverHost = "127.0.0.1"
	discoverPort = 10083
	// rateLimitHostInt      = 159780726
	rateLimitHostInt      = 2130706433
	rateLimitPort         = 18081
	rateLimitHttpPort     = 18080
	mockRateLimitHost     = "127.0.0.1"
	mockRateLimitPort     = 10077
	NotExistRateLimitPort = 10090
)

const (
	namespaceTest          = "Test"
	rateLimitSvcName       = "polaris.metric.test.ide"
	mockRateLimitName      = "polaris.metric.test.mock"
	NotExistsRateLimitName = "polaris.metric.test.not.exists"
	LocalTestSvcName       = "LocalTestSvcV2"
	RemoteTestSvcName      = "RemoteTestSvcV2"
	WindowExpireSvcName    = "ExpireTestSvcV2"
	RuleChangeSvcName      = "RuleChangeTestSvcV2"
	RuleDeletedSvcName     = "RuleDeleteTestSvcV2"
	SvcDeletedSvcName      = "ServiceDeleteTestSvcV2"
	NetworkFailSvcName     = "NetworkFailTestSvcV2"
)

const (
	labelMethod  = "method"
	labelUin     = "uin"
	labelAppId   = "appId"
	labelTestUin = "test_uin"
)

// 限流通用的测试套
type CommonRateLimitSuite struct {
	grpcServer   *grpc.Server
	grpcListener net.Listener
	mockServer   mock.NamingServer

	services map[string]*namingpb.Service
	rules    map[string]*namingpb.RateLimit
}

// 整形转地址
func int2ip(nn uint32) string {
	ip := make(net.IP, 4)
	binary.BigEndian.PutUint32(ip, nn)
	return ip.String()
}

// SetUpSuite 启动测试套程序
func (cr *CommonRateLimitSuite) SetUpSuite(c *check.C, startRemote bool) {
	util.DeleteDir(util.BackupDir)
	cr.grpcServer, cr.grpcListener, cr.mockServer = util.SetupMockDiscover(discoverHost, discoverPort)
	log.Printf("discover-server listening on %s\n", mockDiscoverAddress)
	go func() {
		cr.grpcServer.Serve(cr.grpcListener)
	}()
	cr.services = registerServices(cr.mockServer)
	if startRemote {
		rLimitSvc, ok := cr.services[rateLimitSvcName]
		c.Assert(ok, check.Equals, true)
		token := rLimitSvc.GetToken().GetValue()
		instance := cr.mockServer.RegisterServerInstance(
			rateLimitHost, rateLimitPort, rateLimitSvcName, token, true)
		instance.Metadata = map[string]string{"protocol": "grpc"}
		instance = cr.mockServer.RegisterServerInstance(
			rateLimitHost, rateLimitHttpPort, rateLimitSvcName, token, true)
		instance.Metadata = map[string]string{"protocol": "http"}
	}
	// 注册限流服务器地址
	var err error
	cr.rules, err = loadRateLimitRules()
	c.Assert(err, check.IsNil)
	for svcName, rule := range cr.rules {
		svc, ok := cr.services[svcName]
		c.Assert(ok, check.Equals, true)
		err = cr.mockServer.RegisterRateLimitRule(svc, rule)
		c.Assert(err, check.IsNil)
	}
}

// SetUpSuite 结束测试套程序
func (cr *CommonRateLimitSuite) TearDownSuite(c *check.C, s util.NamingTestSuite) {
	cr.grpcServer.Stop()
	if util.DirExist(util.BackupDir) {
		os.RemoveAll(util.BackupDir)
	}
	util.InsertLog(s, c.GetTestLog())
}

// 加载限流规则文件
func loadRateLimitRules() (map[string]*namingpb.RateLimit, error) {
	outputs := make(map[string]*namingpb.RateLimit, 0)
	for svcName, rulePath := range rulePaths {
		buf, err := ioutil.ReadFile(rulePath)
		if err != nil {
			return nil, err
		}
		rateLimit := &namingpb.RateLimit{}
		if err = jsonpb.UnmarshalString(string(buf), rateLimit); err != nil {
			return nil, err
		}
		outputs[svcName] = rateLimit
		rateLimit.Revision = &wrappers.StringValue{
			Value: uuid.New().String(),
		}
		rules := rateLimit.GetRules()
		if len(rules) == 0 {
			continue
		}
		for _, rule := range rules {
			rule.Revision = &wrappers.StringValue{
				Value: uuid.New().String(),
			}
		}
	}
	return outputs, nil
}

// 注册所需的所有服务
func registerServices(mockServer mock.NamingServer) map[string]*namingpb.Service {
	services := make(map[string]*namingpb.Service)
	for svcName, namespace := range svcNames {
		serviceToken := uuid.New().String()
		service := &namingpb.Service{
			Name:      &wrappers.StringValue{Value: svcName},
			Namespace: &wrappers.StringValue{Value: namespace},
			Token:     &wrappers.StringValue{Value: serviceToken},
		}
		services[svcName] = service
		mockServer.RegisterService(service)
	}
	return services
}

// 单次获取限流配额
func doSingleGetQuota(
	c *check.C, limitAPI api.LimitAPI, svcName string, labels map[string]string) *model.QuotaResponse {
	quotaReq := api.NewQuotaRequest()
	quotaReq.SetNamespace(namespaceTest)
	quotaReq.SetService(svcName)
	quotaReq.SetLabels(labels)
	future, err := limitAPI.GetQuota(quotaReq)
	c.Assert(err, check.IsNil)
	return future.Get()
}
