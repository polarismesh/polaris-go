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

package serviceroute

import (
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"math/rand"
	"net"
	"os"
	"strings"
	"time"

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

const (
	ruleServerIPAddr  = "127.0.0.1"
	ruleServerPort    = 8010 // 需要跟配置文件一致(sr_rule.yaml)
	ruleMonitorIPAddr = "127.0.0.1"
	ruleMonitorPort   = 8011
)

// namespace1:source_service -> namespace1:dest_service
const (
	// 服务默认命名空间
	defaultNamespace = "namespace1"
	// 主调服务
	callingService = "source_service"
	// 被调服务
	calledService = "dest_service"
	// 用于测试无路由规则的新被调服务
	newCalledService = "new_dest_service"
	// 用于测试错误路由规则的新被调服务
	badCalledService = "bad_dest_service"
	// 入规则没有sources为空
	badCalledService1 = "bad_dest_service1"
)

// 异常测试相关的服务及命名空间
const (
	productionNamespace = "Production"
	onlyInboundService  = "onlyInboundService"
	onlyOutboundService = "onlyOutboundService"
	bioService          = "bioService"
	testNamespace       = "Test"
)

// for metadata
const (
	metaSet1     = "set1"
	metaSet2     = "set2"
	metaSet4     = "set4"
	metaVersion1 = "v1"
	metaVersion2 = "v2"
)

// RuleRoutingTestingSuite 规则路由API测试套
type RuleRoutingTestingSuite struct {
	grpcServer   *grpc.Server
	grpcListener net.Listener
	mockServer   mock.NamingServer
	pbServices   map[model.ServiceKey]*namingpb.Service
}

// 注册路由规则
func registerRouteRuleByFile(mockServer mock.NamingServer, svc *namingpb.Service, path string) error {
	buf, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}
	route := &namingpb.Routing{}
	if err = jsonpb.UnmarshalString(string(buf), route); err != nil {
		return err
	}
	return mockServer.RegisterRouteRule(svc, route)
}

// 注册服务及路由规则
func registerServiceAndRules(mockServer mock.NamingServer, svcName string,
	namespace string, path string, instances []*namingpb.Instance) *namingpb.Service {
	// 创建Production路由规则，只有入没有出规则
	service := &namingpb.Service{
		Name:      &wrappers.StringValue{Value: svcName},
		Namespace: &wrappers.StringValue{Value: namespace},
		Token:     &wrappers.StringValue{Value: uuid.New().String()},
	}
	mockServer.RegisterService(service)
	if len(instances) > 0 {
		mockServer.RegisterServiceInstances(service, instances)
	}
	mockServer.DeregisterRouteRule(service)
	if len(path) == 0 {
		return service
	}
	err := registerRouteRuleByFile(mockServer, service, path)
	if err != nil {
		log.Fatalf("fail to register routeRule for %s, error is %v", path, err)
	}
	return service
}

// 构造高级server
func (t *RuleRoutingTestingSuite) setupAdvanceServer(mockServer mock.NamingServer) {
	num := 2
	onlyInboundInstances := make([]*namingpb.Instance, 0, num)
	onlyOutboundInstances := make([]*namingpb.Instance, 0, num)
	bioDirectionInstances := make([]*namingpb.Instance, 0, num)
	badInbound1Instances := make([]*namingpb.Instance, 0, num)
	for i := 0; i < num; i++ {
		onlyInboundInstances = append(onlyInboundInstances, &namingpb.Instance{
			Id:        &wrappers.StringValue{Value: uuid.New().String()},
			Service:   &wrappers.StringValue{Value: onlyInboundService},
			Namespace: &wrappers.StringValue{Value: productionNamespace},
			Host:      &wrappers.StringValue{Value: "127.0.0.1"},
			Port:      &wrappers.UInt32Value{Value: uint32(10010 + i)},
			Weight:    &wrappers.UInt32Value{Value: 100},
			Metadata: map[string]string{
				"env": "formal",
			},
		})
		onlyInboundInstances = append(onlyInboundInstances, &namingpb.Instance{
			Id:        &wrappers.StringValue{Value: uuid.New().String()},
			Service:   &wrappers.StringValue{Value: onlyInboundService},
			Namespace: &wrappers.StringValue{Value: productionNamespace},
			Host:      &wrappers.StringValue{Value: "127.0.0.1"},
			Port:      &wrappers.UInt32Value{Value: uint32(10010 + i)},
			Weight:    &wrappers.UInt32Value{Value: 100},
			Metadata: map[string]string{
				"env": fmt.Sprintf("set%d", i),
			},
		})

		onlyOutboundInstances = append(onlyOutboundInstances, &namingpb.Instance{
			Id:        &wrappers.StringValue{Value: uuid.New().String()},
			Service:   &wrappers.StringValue{Value: onlyOutboundService},
			Namespace: &wrappers.StringValue{Value: productionNamespace},
			Host:      &wrappers.StringValue{Value: "127.0.0.1"},
			Port:      &wrappers.UInt32Value{Value: uint32(10020 + i)},
			Weight:    &wrappers.UInt32Value{Value: 100},
			Metadata: map[string]string{
				"env": "formal",
			},
		})
		onlyOutboundInstances = append(onlyOutboundInstances, &namingpb.Instance{
			Id:        &wrappers.StringValue{Value: uuid.New().String()},
			Service:   &wrappers.StringValue{Value: onlyInboundService},
			Namespace: &wrappers.StringValue{Value: productionNamespace},
			Host:      &wrappers.StringValue{Value: "127.0.0.1"},
			Port:      &wrappers.UInt32Value{Value: uint32(10010 + i)},
			Weight:    &wrappers.UInt32Value{Value: 100},
			Metadata: map[string]string{
				"env": fmt.Sprintf("set%d", i),
			},
		})
		bioDirectionInstances = append(bioDirectionInstances, &namingpb.Instance{
			Id:        &wrappers.StringValue{Value: uuid.New().String()},
			Service:   &wrappers.StringValue{Value: bioService},
			Namespace: &wrappers.StringValue{Value: productionNamespace},
			Host:      &wrappers.StringValue{Value: "127.0.0.1"},
			Port:      &wrappers.UInt32Value{Value: uint32(10030 + i)},
			Weight:    &wrappers.UInt32Value{Value: 100},
			Metadata: map[string]string{
				"env": "formal",
			},
		})
		badInbound1Instances = append(badInbound1Instances, &namingpb.Instance{
			Id:        &wrappers.StringValue{Value: uuid.New().String()},
			Service:   &wrappers.StringValue{Value: badCalledService1},
			Namespace: &wrappers.StringValue{Value: productionNamespace},
			Host:      &wrappers.StringValue{Value: "127.0.0.1"},
			Port:      &wrappers.UInt32Value{Value: uint32(10030 + i)},
			Weight:    &wrappers.UInt32Value{Value: 100},
			Metadata: map[string]string{
				"env": "formal",
			},
		})
	}
	onlyInboundInstances = append(onlyInboundInstances, &namingpb.Instance{
		Id:        &wrappers.StringValue{Value: uuid.New().String()},
		Service:   &wrappers.StringValue{Value: onlyInboundService},
		Namespace: &wrappers.StringValue{Value: productionNamespace},
		Host:      &wrappers.StringValue{Value: "127.0.0.1"},
		Port:      &wrappers.UInt32Value{Value: uint32(10010 + 2)},
		Weight:    &wrappers.UInt32Value{Value: 100},
		Metadata: map[string]string{
			"env": fmt.Sprintf("set%d", 2),
		},
	})

	mockServer.RegisterNamespace(&namingpb.Namespace{
		Name:    &wrappers.StringValue{Value: productionNamespace},
		Comment: &wrappers.StringValue{Value: "production env"},
		Owners:  &wrappers.StringValue{Value: "RuleServiceRoute"},
	})

	// outbound路由规则
	registerServiceAndRules(
		mockServer, onlyOutboundService, productionNamespace,
		"testdata/route_rule/only_outbound.json", onlyOutboundInstances)
	registerServiceAndRules(
		mockServer, onlyInboundService, productionNamespace,
		"testdata/route_rule/only_inbound.json", onlyInboundInstances)
	registerServiceAndRules(
		mockServer, bioService, productionNamespace,
		"testdata/route_rule/bio_direction_bound.json", bioDirectionInstances)
	registerServiceAndRules(
		mockServer, badCalledService1, productionNamespace,
		"testdata/route_rule/bad_dest_service_no_source.json", badInbound1Instances)
}

// 构造基础server
func (t *RuleRoutingTestingSuite) setupBaseServer(mockServer mock.NamingServer) {
	// dest_service共4个实例, 0和2是v1, 1和3是v2
	num := 5
	sourceInstances := make([]*namingpb.Instance, 0)
	for i := 0; i < num; i++ {
		metadata := make(map[string]string)
		if i%2 == 0 {
			metadata["version"] = metaVersion1
			metadata["logic_set"] = metaSet1
		} else {
			metadata["version"] = metaVersion2
			metadata["logic_set"] = metaSet2
		}

		ins := &namingpb.Instance{
			Id:        &wrappers.StringValue{Value: uuid.New().String()},
			Service:   &wrappers.StringValue{Value: callingService},
			Namespace: &wrappers.StringValue{Value: defaultNamespace},
			Host:      &wrappers.StringValue{Value: "127.0.0.1"},
			Port:      &wrappers.UInt32Value{Value: uint32(10000 + i)},
			Weight:    &wrappers.UInt32Value{Value: uint32(rand.Intn(999) + 1)},
			Metadata:  metadata,
		}
		sourceInstances = append(sourceInstances, ins)
	}

	// dest_service共4个实例
	destInstances := make([]*namingpb.Instance, 0)
	// 创建四个subset, 每个set一个实例
	// 两个实例健康, 两个实例不健康
	for i := 0; i < num; i++ {
		var isHealthy bool
		metadata := make(map[string]string)
		switch i % num {
		case 0:
			isHealthy = true
			metadata["version"] = metaVersion1
		case 1:
			isHealthy = false
			metadata["logic_set"] = metaSet1
		case 2:
			isHealthy = false
			metadata["version"] = metaVersion2
		case 3:
			isHealthy = true
			metadata["logic_set"] = metaSet2
		default:
			// 增加一个没有元数据的健康实例
			isHealthy = true
		}

		ins := &namingpb.Instance{
			Id:        &wrappers.StringValue{Value: uuid.New().String()},
			Service:   &wrappers.StringValue{Value: calledService},
			Namespace: &wrappers.StringValue{Value: defaultNamespace},
			Host:      &wrappers.StringValue{Value: "127.0.0.1"},
			Port:      &wrappers.UInt32Value{Value: uint32(20000 + i)},
			Healthy:   &wrappers.BoolValue{Value: isHealthy},
			Weight:    &wrappers.UInt32Value{Value: uint32(rand.Intn(999) + 1)},
			Metadata:  metadata,
			HealthCheck: &namingpb.HealthCheck{
				Type: namingpb.HealthCheck_UNKNOWN,
			},
		}
		destInstances = append(destInstances, ins)
	}

	mockServer.RegisterNamespace(&namingpb.Namespace{
		Name:    &wrappers.StringValue{Value: defaultNamespace},
		Comment: &wrappers.StringValue{Value: "for rule service route test"},
		Owners:  &wrappers.StringValue{Value: "RuleServiceRoute"},
	})
	// outbound路由规则
	callingSvcKey := model.ServiceKey{
		Namespace: defaultNamespace,
		Service:   callingService,
	}
	t.pbServices[callingSvcKey] = registerServiceAndRules(mockServer, callingSvcKey.Service, callingSvcKey.Namespace,
		"testdata/route_rule/source_service.json", sourceInstances)
	calledSvcKey := model.ServiceKey{
		Namespace: defaultNamespace,
		Service:   calledService,
	}
	t.pbServices[calledSvcKey] = registerServiceAndRules(mockServer, calledSvcKey.Service, calledSvcKey.Namespace,
		"testdata/route_rule/dest_service.json", destInstances)
	// 创建一个新的服务, 用于测试无法匹配规则
	newCalledSvcKey := model.ServiceKey{
		Namespace: defaultNamespace,
		Service:   newCalledService,
	}
	t.pbServices[newCalledSvcKey] = registerServiceAndRules(
		mockServer, newCalledSvcKey.Service, newCalledSvcKey.Namespace, "", destInstances)

	// 创建一个新的错误路由服务, 用于测试错误路由规则
	badCalledSvcKey := model.ServiceKey{
		Namespace: defaultNamespace,
		Service:   badCalledService,
	}
	t.pbServices[badCalledSvcKey] = registerServiceAndRules(mockServer, badCalledSvcKey.Service,
		badCalledSvcKey.Namespace, "testdata/route_rule/bad_dest_service.json", destInstances)

	// 创建Production路由规则，只有入没有出规则
	onlyInboundSvcKey := model.ServiceKey{
		Namespace: productionNamespace,
		Service:   onlyInboundService,
	}
	t.pbServices[badCalledSvcKey] = registerServiceAndRules(mockServer, onlyInboundSvcKey.Service,
		onlyInboundSvcKey.Namespace, "testdata/route_rule/only_inbound.json", destInstances)
}

// SetUpSuite 设置模拟桩服务器
func (t *RuleRoutingTestingSuite) SetUpSuite(c *check.C) {
	var err error
	grpcOptions := make([]grpc.ServerOption, 0)
	maxStreams := 100000
	grpcOptions = append(grpcOptions, grpc.MaxConcurrentStreams(uint32(maxStreams)))

	// get the grpc server wired up
	grpc.EnableTracing = true

	t.pbServices = make(map[model.ServiceKey]*namingpb.Service, 0)
	t.grpcServer = grpc.NewServer(grpcOptions...)
	t.mockServer = mock.NewNamingServer()
	token := t.mockServer.RegisterServerService(config.ServerDiscoverService)
	t.mockServer.RegisterServerInstance(
		ruleServerIPAddr, ruleServerPort, config.ServerDiscoverService, token, true)
	t.setupBaseServer(t.mockServer)
	t.setupAdvanceServer(t.mockServer)
	namingpb.RegisterPolarisGRPCServer(t.grpcServer, t.mockServer)
	t.grpcListener, err = net.Listen("tcp", fmt.Sprintf("%s:%d", ruleServerIPAddr, ruleServerPort))
	if err != nil {
		log.Fatal(fmt.Sprintf("error listening appserver %v", err))
	}
	log.Printf("appserver listening on %s:%d\n", ruleServerIPAddr, ruleServerPort)
	go func() {
		t.grpcServer.Serve(t.grpcListener)
	}()
	awaitServerReady(ruleServerIPAddr, ruleServerPort)
}

// TearDownSuite 结束测试套程序
func (t *RuleRoutingTestingSuite) TearDownSuite(c *check.C) {
	t.grpcServer.Stop()
	util.InsertLog(t, c.GetTestLog())
}

// TestInboundRules inbound rules
func (t *RuleRoutingTestingSuite) TestInboundRules(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	cfg, err := config.LoadConfigurationByFile("testdata/sr_rule.yaml")
	c.Assert(err, check.IsNil)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	t.callDstService(consumer, c)

	// 将路由规则更新成错误的规则
	calledSvcKey := model.ServiceKey{
		Namespace: defaultNamespace,
		Service:   calledService,
	}
	err = registerRouteRuleByFile(
		t.mockServer, t.pbServices[calledSvcKey], "testdata/route_rule/dest_service_false_regex.json")
	c.Assert(err, check.IsNil)
	// 等待路由更新
	time.Sleep(10 * time.Second)
	// 校验失败，所以上一个规则仍然有效
	t.callDstService(consumer, c)
	err = registerRouteRuleByFile(
		t.mockServer, t.pbServices[calledSvcKey], "testdata/route_rule/dest_service.json")
	c.Assert(err, check.IsNil)
}

// 调用目标服务
func (t *RuleRoutingTestingSuite) callDstService(consumer api.ConsumerAPI, c *check.C) {
	request := &api.GetInstancesRequest{}
	request.FlowID = 1111
	request.Namespace = defaultNamespace
	request.Service = calledService
	request.Timeout = model.ToDurationPtr(3 * time.Second)
	request.SkipRouteFilter = false
	resp, err := consumer.GetInstances(request)
	c.Assert(err, check.IsNil)
	c.Assert(resp, check.NotNil)
	// 只能匹配到健康的元数据为version:metaSet2的一个实例
	c.Assert(len(resp.Instances), check.Equals, 1)
	for _, instance := range resp.Instances {
		meta := instance.GetMetadata()
		version, _ := meta["version"]
		c.Assert(version, check.Equals, "v2")
		logSet, _ := meta["logic_set"]
		c.Assert(logSet, check.Equals, "")
	}
}

// TestHasNoRules test has no rules
func (t *RuleRoutingTestingSuite) TestHasNoRules(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	cfg, err := config.LoadConfigurationByFile("testdata/sr_rule.yaml")
	c.Assert(err, check.IsNil)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	request := &api.GetInstancesRequest{}
	request.FlowID = 1111
	request.Namespace = defaultNamespace
	request.Service = newCalledService
	request.Timeout = model.ToDurationPtr(3 * time.Second)
	request.SkipRouteFilter = false
	resp, err := consumer.GetInstances(request)
	c.Assert(err, check.IsNil)
	c.Assert(resp, check.NotNil)
	// 返回可用实例
	c.Assert(len(resp.Instances), check.Equals, 3)
}

// TestHasInvalidRules test has invalid rules
func (t *RuleRoutingTestingSuite) TestHasInvalidRules(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	cfg, err := config.LoadConfigurationByFile("testdata/sr_rule.yaml")
	c.Assert(err, check.IsNil)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	request := &api.GetInstancesRequest{}
	request.FlowID = 1111
	request.Namespace = defaultNamespace
	request.Service = badCalledService
	request.Timeout = model.ToDurationPtr(3 * time.Second)
	request.SkipRouteFilter = false
	resp, err := consumer.GetInstances(request)
	c.Assert(err, check.IsNil)
	c.Assert(resp, check.NotNil)
	// 返回可用实例
	c.Assert(len(resp.Instances), check.Equals, 3)
}

// TestReturnDefault return default(兜底)
func (t *RuleRoutingTestingSuite) TestReturnDefault(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	cfg, err := config.LoadConfigurationByFile("testdata/sr_rule.yaml")
	c.Assert(err, check.IsNil)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	request := &api.GetInstancesRequest{}
	request.FlowID = 1111
	request.Namespace = defaultNamespace
	request.Service = calledService
	request.Timeout = model.ToDurationPtr(3 * time.Second)
	request.SkipRouteFilter = false
	// 调用5次, 查看是否返回健康实例
	runNum := 5
	for i := 0; i < runNum; i++ {
		resp, err := consumer.GetInstances(request)
		c.Assert(err, check.IsNil)
		c.Assert(resp, check.NotNil)
		c.Assert(len(resp.Instances), check.Equals, 1)
		for _, instance := range resp.Instances {
			meta := instance.GetMetadata()
			logSet, _ := meta["logic_set"]
			c.Assert(logSet, check.Equals, "")
			version, _ := meta["version"]
			c.Assert(version, check.Equals, metaVersion2)
		}
	}
}

// TestMatchInboundAndOutboundRules match inbound & outbound rules
func (t *RuleRoutingTestingSuite) TestMatchInboundAndOutboundRules(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	cfg, err := config.LoadConfigurationByFile("testdata/sr_rule.yaml")
	c.Assert(err, check.IsNil)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	request := &api.GetInstancesRequest{}
	request.FlowID = 1111
	request.Namespace = defaultNamespace
	request.Service = calledService
	request.Timeout = model.ToDurationPtr(3 * time.Second)
	request.SkipRouteFilter = false
	requestMetadata := make(map[string]string)
	requestMetadata["client"] = "android"
	request.SourceService = &model.ServiceInfo{
		Namespace: defaultNamespace,
		Service:   callingService,
		Metadata:  requestMetadata,
	}
	resp, err := consumer.GetInstances(request)
	c.Assert(err, check.IsNil)
	c.Assert(resp, check.NotNil)
	c.Assert(len(resp.Instances), check.Equals, 1)
	for _, instance := range resp.Instances {
		meta := instance.GetMetadata()
		version, _ := meta["version"]
		logSet, _ := meta["logic_set"]
		ok := version == metaVersion2 || logSet == metaSet2
		c.Assert(ok, check.Equals, true)
	}
}

// TestMatchMissingRouteRule 测试匹配残缺的路由规则
func (t *RuleRoutingTestingSuite) TestMatchMissingRouteRule(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	cfg, err := config.LoadConfigurationByFile("testdata/sr_rule.yaml")
	c.Assert(err, check.IsNil)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	var request *api.GetInstancesRequest

	// 没有源服务查询入规则为空的服务
	request = &api.GetInstancesRequest{}
	request.Namespace = productionNamespace
	request.Service = onlyOutboundService
	resp, err := consumer.GetInstances(request)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.Instances) > 0, check.Equals, true)

	// 指定源服务为出规则为空的服务查询入规则为空的服务
	request = &api.GetInstancesRequest{}
	request.Namespace = productionNamespace
	request.Service = onlyOutboundService
	request.SourceService = &model.ServiceInfo{
		Namespace: productionNamespace,
		Service:   onlyInboundService,
	}
	resp, err = consumer.GetInstances(request)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.Instances) > 0, check.Equals, true)
	fmt.Println(resp.Instances[0])

	// 指定源服务为出规则不为空的服务查询入规则为空的服务
	request = &api.GetInstancesRequest{}
	request.Namespace = productionNamespace
	request.Service = onlyOutboundService
	request.SourceService = &model.ServiceInfo{
		Namespace: productionNamespace,
		Service:   bioService,
	}
	resp, err = consumer.GetInstances(request)
	c.Assert(err, check.NotNil)
	sdkErr := err.(model.SDKError)
	c.Assert(sdkErr.ErrorCode(), check.Equals, model.ErrCodeRouteRuleNotMatch)
	c.Assert(strings.Contains(sdkErr.Error(), "sourceRuleFail"), check.Equals, true)

	// 指定错误的metadata查询入规则不为空的服务
	request = &api.GetInstancesRequest{}
	request.Namespace = productionNamespace
	request.Service = onlyInboundService
	request.SourceService = &model.ServiceInfo{
		Namespace: productionNamespace,
		Service:   bioService,
		Metadata: map[string]string{
			"env": "test",
		},
	}
	resp, err = consumer.GetInstances(request)
	c.Assert(err, check.NotNil)
	sdkErr = err.(model.SDKError)
	c.Assert(sdkErr.ErrorCode(), check.Equals, model.ErrCodeRouteRuleNotMatch)
	c.Assert(strings.Contains(sdkErr.Error(), "dstRuleFail"), check.Equals, true)

	// 不指定source，查询入规则不为空的服务
	request = &api.GetInstancesRequest{}
	request.Namespace = productionNamespace
	request.Service = onlyInboundService
	resp, err = consumer.GetInstances(request)
	c.Assert(err, check.NotNil)
	sdkErr = err.(model.SDKError)
	c.Assert(sdkErr.ErrorCode(), check.Equals, model.ErrCodeRouteRuleNotMatch)
	c.Assert(strings.Contains(sdkErr.Error(), "dstRuleFail"), check.Equals, true)
}

// TestOutboundRules inbound rules
func (t *RuleRoutingTestingSuite) TestOutboundRules(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	cfg, err := config.LoadConfigurationByFile("testdata/sr_rule.yaml")
	c.Assert(err, check.IsNil)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	request := &api.GetOneInstanceRequest{}
	request.Namespace = productionNamespace
	request.Service = onlyOutboundService
	request.SourceService = &model.ServiceInfo{
		Namespace: productionNamespace,
		Service:   onlyOutboundService,
		Metadata: map[string]string{
			"env": "formal",
		},
	}
	fmt.Println(request)
	for i := 0; i < 10; i++ {
		resp, err := consumer.GetOneInstance(request)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		if v, ok := resp.Instances[0].GetMetadata()["env"]; ok {
			c.Assert(v == "formal", check.Equals, true)
		} else {
			c.Assert(false, check.Equals, true)
		}
	}

	request1 := &api.GetInstancesRequest{}
	request1.Namespace = productionNamespace
	request1.Service = onlyInboundService
	request1.SourceService = &model.ServiceInfo{
		Namespace: productionNamespace,
		Service:   onlyOutboundService,
		Metadata: map[string]string{
			"env": "formal",
		},
	}
	resp, err := consumer.GetInstances(request1)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.GetInstances()), check.Equals, 2)
	for _, v := range resp.GetInstances() {
		if v1, ok := v.GetMetadata()["env"]; ok {
			c.Assert(v1 == "formal", check.Equals, true)
		} else {
			c.Assert(false, check.Equals, true)
		}
	}
}

// TestInboundRuleRegex 测试正则匹配inbound rules
func (t *RuleRoutingTestingSuite) TestInboundRuleRegex(c *check.C) {
	fmt.Println("-------TestInboundRuleRegex")
	defer util.DeleteDir(util.BackupDir)
	cfg, err := config.LoadConfigurationByFile("testdata/sr_rule.yaml")
	c.Assert(err, check.IsNil)

	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	request := &api.GetOneInstanceRequest{}
	request.Namespace = productionNamespace
	request.Service = onlyInboundService
	request.SourceService = &model.ServiceInfo{
		Namespace: productionNamespace,
		Service:   onlyInboundService,
		Metadata: map[string]string{
			"env": "ab",
		},
	}
	fmt.Println(request)
	for i := 0; i < 10; i++ {
		resp, err := consumer.GetOneInstance(request)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		// fmt.Println("----------------", resp.Instances[0])
		flag := false
		if v, ok := resp.Instances[0].GetMetadata()["env"]; ok {
			flag = strings.Contains(v, "set")
		}
		c.Assert(flag, check.Equals, true)
	}

}

// TestDestPriority 测试目标规则的优先级
func (t *RuleRoutingTestingSuite) TestDestPriority(c *check.C) {
	fmt.Println("-------TestDestPriority")
	defer util.DeleteDir(util.BackupDir)
	cfg, err := config.LoadConfigurationByFile("testdata/sr_rule.yaml")
	c.Assert(err, check.IsNil)

	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	request := &api.GetOneInstanceRequest{}
	request.Namespace = productionNamespace
	request.Service = onlyInboundService
	request.SourceService = &model.ServiceInfo{
		Namespace: productionNamespace,
		Service:   onlyInboundService,
		Metadata: map[string]string{
			"env": "testPriority",
		},
	}
	fmt.Println(request)
	for i := 0; i < 10; i++ {
		resp, err := consumer.GetOneInstance(request)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		// fmt.Println("----------------", resp.Instances[0])
		flag := false
		if v, ok := resp.Instances[0].GetMetadata()["env"]; ok {
			flag = strings.Contains(v, "set1")
		}
		c.Assert(flag, check.Equals, true)
	}
}

// TestDestWeight 测试目标规则的weight(包括0权重)
func (t *RuleRoutingTestingSuite) TestDestWeight(c *check.C) {
	fmt.Println("-------TestDestWeight")
	defer util.DeleteDir(util.BackupDir)
	cfg, err := config.LoadConfigurationByFile("testdata/sr_rule.yaml")
	c.Assert(err, check.IsNil)

	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	request := &api.GetOneInstanceRequest{}
	request.Namespace = productionNamespace
	request.Service = onlyInboundService
	request.SourceService = &model.ServiceInfo{
		Namespace: productionNamespace,
		Service:   onlyInboundService,
		Metadata: map[string]string{
			"env": "testWeight",
		},
	}
	fmt.Println(request)
	set1Num := 0
	for i := 0; i < 1000; i++ {
		resp, err := consumer.GetOneInstance(request)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		// fmt.Println("----------------", resp.Instances[0])
		if v, ok := resp.Instances[0].GetMetadata()["env"]; ok {
			if v == "set1" {
				set1Num++
			}
			c.Assert(v != "set2", check.Equals, true)
		} else {
			c.Assert(false, check.Equals, true)
		}
	}
	fmt.Println("-----set1Num", set1Num)
	c.Assert(math.Abs(600-float64(set1Num)) < 100, check.Equals, true)
}

// TestInboundNoSources 测试inbound no sources
func (t *RuleRoutingTestingSuite) TestInboundNoSources(c *check.C) {
	fmt.Println("-------TestInboundNoSources")
	defer util.DeleteDir(util.BackupDir)
	cfg, err := config.LoadConfigurationByFile("testdata/sr_rule.yaml")
	c.Assert(err, check.IsNil)

	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	request := &api.GetOneInstanceRequest{}
	request.Namespace = productionNamespace
	request.Service = badCalledService1
	request.SourceService = &model.ServiceInfo{
		Namespace: productionNamespace,
		Service:   onlyInboundService,
		Metadata: map[string]string{
			"env": "formal",
		},
	}
	fmt.Println(request)
	for i := 0; i < 10; i++ {
		resp, err := consumer.GetOneInstance(request)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
	}
}

// TestInboundAddAndDelete 测试inbound add and delete
func (t *RuleRoutingTestingSuite) TestInboundAddAndDelete(c *check.C) {
	serviceName := "InboundAddAndDelete"
	Instances := make([]*namingpb.Instance, 0, 2)
	for i := 0; i < 2; i++ {
		Instances = append(Instances, &namingpb.Instance{
			Id:        &wrappers.StringValue{Value: uuid.New().String()},
			Service:   &wrappers.StringValue{Value: serviceName},
			Namespace: &wrappers.StringValue{Value: productionNamespace},
			Host:      &wrappers.StringValue{Value: "127.0.0.1"},
			Port:      &wrappers.UInt32Value{Value: uint32(10030 + i)},
			Weight:    &wrappers.UInt32Value{Value: 100},
			Metadata: map[string]string{
				"env": "formal1",
			},
		})
	}
	service := &namingpb.Service{
		Name:      &wrappers.StringValue{Value: serviceName},
		Namespace: &wrappers.StringValue{Value: productionNamespace},
		Token:     &wrappers.StringValue{Value: uuid.New().String()},
	}
	t.mockServer.RegisterService(service)
	if len(Instances) > 0 {
		t.mockServer.RegisterServiceInstances(service, Instances)
	}

	defer util.DeleteDir(util.BackupDir)
	cfg, err := config.LoadConfigurationByFile("testdata/sr_rule.yaml")
	cfg.GetConsumer().GetLocalCache().SetServiceRefreshInterval(time.Second * 2)
	c.Assert(err, check.IsNil)

	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	request1 := &api.GetInstancesRequest{}
	request1.Namespace = productionNamespace
	request1.Service = serviceName
	resp, err := consumer.GetInstances(request1)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.GetInstances()), check.Equals, 2)

	err = registerRouteRuleByFile(t.mockServer, service, "testdata/route_rule/inbound_add_delete.json")
	fmt.Println("---------------registerRouteRuleByFile")
	c.Assert(err, check.IsNil)

	time.Sleep(time.Second * 6)

	request2 := &api.GetOneInstanceRequest{}
	request2.Namespace = productionNamespace
	request2.Service = serviceName
	_, err = consumer.GetOneInstance(request2)
	fmt.Println(err)
	c.Assert(err, check.NotNil)

	t.mockServer.DeregisterRouteRule(service)
	time.Sleep(time.Second * 6)
	_, err = consumer.GetOneInstance(request2)
	c.Assert(err, check.IsNil)

}

// TestInboundNoSourceService 测试只有入流量规则的前提下，不传入sourceService能否成功路由
func (t *RuleRoutingTestingSuite) TestInboundNoSourceService(c *check.C) {
	serviceName := "InboundNoSource"
	service := &namingpb.Service{
		Name:      &wrappers.StringValue{Value: serviceName},
		Namespace: &wrappers.StringValue{Value: productionNamespace},
		Token:     &wrappers.StringValue{Value: uuid.New().String()},
	}
	t.mockServer.RegisterService(service)

	err := registerRouteRuleByFile(t.mockServer, service, "testdata/route_rule/inbound_no_source.json")
	c.Assert(err, check.IsNil)

	formalInstances := make([]*namingpb.Instance, 0, 2)
	for i := 0; i < 1; i++ {
		formalInstances = append(formalInstances, &namingpb.Instance{
			Id:        &wrappers.StringValue{Value: uuid.New().String()},
			Service:   &wrappers.StringValue{Value: serviceName},
			Namespace: &wrappers.StringValue{Value: productionNamespace},
			Host:      &wrappers.StringValue{Value: "127.0.0.1"},
			Port:      &wrappers.UInt32Value{Value: uint32(10030 + i)},
			Weight:    &wrappers.UInt32Value{Value: 100},
			Metadata: map[string]string{
				"env": "formal1",
			},
		})
	}
	t.mockServer.RegisterServiceInstances(service, formalInstances)

	defaultInstances := make([]*namingpb.Instance, 0, 2)
	for i := 0; i < 1; i++ {
		defaultInstances = append(defaultInstances, &namingpb.Instance{
			Id:        &wrappers.StringValue{Value: uuid.New().String()},
			Service:   &wrappers.StringValue{Value: serviceName},
			Namespace: &wrappers.StringValue{Value: productionNamespace},
			Host:      &wrappers.StringValue{Value: "127.0.0.1"},
			Port:      &wrappers.UInt32Value{Value: uint32(10030 + i)},
			Weight:    &wrappers.UInt32Value{Value: 100},
			Metadata: map[string]string{
				"env": "default",
			},
		})
	}
	t.mockServer.RegisterServiceInstances(service, defaultInstances)
	cfg, err := config.LoadConfigurationByFile("testdata/sr_rule.yaml")
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	request1 := &api.GetOneInstanceRequest{}
	request1.Namespace = productionNamespace
	request1.Service = serviceName
	for i := 0; i < 10; i++ {
		resp, err := consumer.GetOneInstance(request1)
		c.Assert(err, check.IsNil)
		c.Assert(resp.Instances[0].GetMetadata()["env"], check.Equals, "default")
	}

	request2 := &api.GetOneInstanceRequest{}
	request2.Namespace = productionNamespace
	request2.Service = serviceName
	request2.SourceService = &model.ServiceInfo{Metadata: map[string]string{"env": "formal1"}}
	for i := 0; i < 10; i++ {
		resp, err := consumer.GetOneInstance(request2)
		c.Assert(err, check.IsNil)
		c.Assert(resp.Instances[0].GetMetadata()["env"], check.Equals, "formal1")
	}
}

// TestOneBaseEnvWithParameter 使用parameter路由，进行基线特性环境匹配
func (t *RuleRoutingTestingSuite) TestOneBaseEnvWithParameter(c *check.C) {
	serviceName := "OneBaseTwoFeatureCaller"
	service := &namingpb.Service{
		Name:      &wrappers.StringValue{Value: serviceName},
		Namespace: &wrappers.StringValue{Value: testNamespace},
		Token:     &wrappers.StringValue{Value: uuid.New().String()},
	}
	t.mockServer.RegisterService(service)

	serviceName = "OneBaseTwoFeatureCallee"
	service = &namingpb.Service{
		Name:      &wrappers.StringValue{Value: serviceName},
		Namespace: &wrappers.StringValue{Value: testNamespace},
		Token:     &wrappers.StringValue{Value: uuid.New().String()},
	}
	t.mockServer.RegisterService(service)
	err := registerRouteRuleByFile(t.mockServer, service, "testdata/route_rule/one_baseline.json")
	c.Assert(err, check.IsNil)

	// 注册具有相应标签的实例
	t.RegisterInstancesWithMetadataAndNum(service, []*InstanceMetadataAndNum{
		{map[string]string{"env": "base"}, 2},
		{map[string]string{"env": "feature0"}, 1},
		{map[string]string{"env": "feature1"}, 1},
	})

	cfg, err := config.LoadConfigurationByFile("testdata/sr_rule.yaml")
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	request1 := &api.GetOneInstanceRequest{}
	request1.Namespace = testNamespace
	request1.Service = serviceName
	request1.SourceService = &model.ServiceInfo{
		Service:   "OneBaseTwoFeatureCaller",
		Namespace: testNamespace,
		Metadata: map[string]string{
			"env": "feature0",
		},
	}
	for i := 0; i < 10; i++ {
		resp, err := consumer.GetOneInstance(request1)
		c.Assert(err, check.IsNil)
		c.Assert(resp.Instances[0].GetMetadata()["env"], check.Equals, "feature0")
	}

	request1.SourceService.Metadata["env"] = "feature1"
	for i := 0; i < 10; i++ {
		resp, err := consumer.GetOneInstance(request1)
		c.Assert(err, check.IsNil)
		c.Assert(resp.Instances[0].GetMetadata()["env"], check.Equals, "feature1")
	}
	request1.SourceService.Metadata["env"] = "featureNo"
	for i := 0; i < 10; i++ {
		resp, err := consumer.GetOneInstance(request1)
		c.Assert(err, check.IsNil)
		c.Assert(resp.Instances[0].GetMetadata()["env"], check.Equals, "base")
	}
	t.mockServer.DeregisterService(testNamespace, "OneBaseTwoFeatureCaller")
	t.mockServer.DeregisterService(testNamespace, "OneBaseTwoFeatureCallee")
}

// TestMultiBaseEnvWithVariable 服务实例：2个基线环境b，2个特性环境f
// case1：metadata带上特性环境名f，variable设置为基线环境名b，访问到特性环境f
// case2: metadata不带上环境名，variable设置为基线环境名b，访问到基线环境b
// case3：metadata不带上环境名，variable设置为不存在的基线环境名b1，返回错误
func (t *RuleRoutingTestingSuite) TestMultiBaseEnvWithVariable(c *check.C) {
	log.Printf("start to TestMultiBaseEnvWithVariable")
	serviceName := "MultiBaseTwoFeatureCaller"
	service := &namingpb.Service{
		Name:      &wrappers.StringValue{Value: serviceName},
		Namespace: &wrappers.StringValue{Value: testNamespace},
		Token:     &wrappers.StringValue{Value: uuid.New().String()},
	}
	t.mockServer.RegisterService(service)

	serviceName = "MultiBaseTwoFeatureCallee"
	service = &namingpb.Service{
		Name:      &wrappers.StringValue{Value: serviceName},
		Namespace: &wrappers.StringValue{Value: testNamespace},
		Token:     &wrappers.StringValue{Value: uuid.New().String()},
	}
	t.mockServer.RegisterService(service)
	err := registerRouteRuleByFile(t.mockServer, service, "testdata/route_rule/multi_baseline.json")
	c.Assert(err, check.IsNil)

	// 注册具有对应标签的实例
	t.RegisterInstancesWithMetadataAndNum(service, []*InstanceMetadataAndNum{
		{map[string]string{"env": "base"}, 2},
		{map[string]string{"env": "feature"}, 2},
	})

	cfg, err := config.LoadConfigurationByFile("testdata/sr_rule.yaml")
	cfg.GetGlobal().GetSystem().SetVariable("env", "base")
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	request1 := &api.GetOneInstanceRequest{}
	request1.Namespace = testNamespace
	request1.Service = serviceName
	request1.SourceService = &model.ServiceInfo{
		Service:   "MultiBaseTwoFeatureCaller",
		Namespace: testNamespace,
		Metadata: map[string]string{
			"env": "feature",
		},
	}

	for i := 0; i < 10; i++ {
		resp, err := consumer.GetOneInstance(request1)
		c.Assert(err, check.IsNil)
		c.Assert(resp.Instances[0].GetMetadata()["env"], check.Equals, "feature")
	}

	request1.SourceService.Metadata = nil
	for i := 0; i < 10; i++ {
		resp, err := consumer.GetOneInstance(request1)
		c.Assert(err, check.IsNil)
		c.Assert(resp.Instances[0].GetMetadata()["env"], check.Equals, "base")
	}

	cfg.GetGlobal().GetSystem().SetVariable("env", "baseNo")
	for i := 0; i < 10; i++ {
		_, err := consumer.GetOneInstance(request1)
		log.Printf("err %v", err)
		c.Assert(err, check.NotNil)
	}
}

// TestMultipleParameters 服务实例：2个基线环境b，2个特性环境f
func (t *RuleRoutingTestingSuite) TestMultipleParameters(c *check.C) {
	log.Printf("start to TestMultipleParameters")
	serviceName := "MultiParameters"
	service := &namingpb.Service{
		Name:      &wrappers.StringValue{Value: serviceName},
		Namespace: &wrappers.StringValue{Value: testNamespace},
		Token:     &wrappers.StringValue{Value: uuid.New().String()},
	}
	t.mockServer.RegisterService(service)
	err := registerRouteRuleByFile(t.mockServer, service, "testdata/route_rule/multi_parameters.json")
	c.Assert(err, check.IsNil)
	// 注册具有对应标签的实例
	t.RegisterInstancesWithMetadataAndNum(service, []*InstanceMetadataAndNum{
		{map[string]string{"k1": "v1"}, 2},
		{map[string]string{"k2": "v2", "k4": "v4"}, 2},
		{map[string]string{"k2": "v2-0", "k4": "v4"}, 2},
		{map[string]string{"k2": "v2", "k4": "v4-0"}, 2},
	})

	cfg, err := config.LoadConfigurationByFile("testdata/sr_rule.yaml")
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	request1 := &api.GetOneInstanceRequest{}
	request1.Namespace = testNamespace
	request1.Service = serviceName
	request1.SourceService = &model.ServiceInfo{
		Service:   "MultiParameters",
		Namespace: testNamespace,
		Metadata: map[string]string{
			"k1": "v1",
		},
	}
	for i := 0; i < 10; i++ {
		resp, err := consumer.GetOneInstance(request1)
		c.Assert(err, check.IsNil)
		c.Assert(resp.Instances[0].GetMetadata()["k1"], check.Equals, "v1")
	}

	request1.SourceService.Metadata = map[string]string{"k2": "v2", "k3": "v3", "k4": "v4"}
	for i := 0; i < 10; i++ {
		resp, err := consumer.GetOneInstance(request1)
		c.Assert(err, check.IsNil)
		c.Assert(resp.Instances[0].GetMetadata()["k2"], check.Equals, "v2")
		c.Assert(resp.Instances[0].GetMetadata()["k4"], check.Equals, "v4")
	}

	request1.SourceService.Metadata = map[string]string{"k2": "v2", "k3": "v3", "k4": "v4-0"}
	for i := 0; i < 10; i++ {
		resp, err := consumer.GetOneInstance(request1)
		c.Assert(err, check.IsNil)
		c.Assert(resp.Instances[0].GetMetadata()["k2"], check.Equals, "v2")
		c.Assert(resp.Instances[0].GetMetadata()["k4"], check.Equals, "v4-0")
	}

	request1.SourceService.Metadata = map[string]string{"k2": "v2", "k5": "v5", "k4": "v4"}
	for i := 0; i < 10; i++ {
		resp, err := consumer.GetOneInstance(request1)
		c.Assert(err, check.IsNil)
		c.Assert(resp.Instances[0].GetMetadata()["k2"], check.Equals, "v2-0")
		c.Assert(resp.Instances[0].GetMetadata()["k4"], check.Equals, "v4")
	}
}

// TestMultiVariables 服务实例：2个基线环境b，2个特性环境f
func (t *RuleRoutingTestingSuite) TestMultiVariables(c *check.C) {
	log.Printf("start to TestMultiVariables")
	serviceName := "MultiVariables"
	service := &namingpb.Service{
		Name:      &wrappers.StringValue{Value: serviceName},
		Namespace: &wrappers.StringValue{Value: testNamespace},
		Token:     &wrappers.StringValue{Value: uuid.New().String()},
	}
	t.mockServer.RegisterService(service)
	err := registerRouteRuleByFile(t.mockServer, service, "testdata/route_rule/multi_variables.json")
	c.Assert(err, check.IsNil)
	// 注册具有对应标签的实例
	t.RegisterInstancesWithMetadataAndNum(service, []*InstanceMetadataAndNum{
		{map[string]string{"k1": "v1"}, 2},
		{map[string]string{"k2": "v2", "k4": "v4"}, 2},
		{map[string]string{"k2": "v2-0", "k4": "v4"}, 2},
		{map[string]string{"k2": "v2", "k4": "v4-0"}, 2},
	})

	// 配置文件里面带有k1:v1的变量
	cfg, err := config.LoadConfigurationByFile("testdata/sr_rule_variable.yaml")
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	// cfg.GetGlobal().GetSystem().SetVariable("k1", "v1")
	request1 := &api.GetOneInstanceRequest{}
	request1.Namespace = testNamespace
	request1.Service = serviceName
	request1.SourceService = &model.ServiceInfo{
		Service:   "MultiVariables",
		Namespace: testNamespace,
		Metadata: map[string]string{
			"k1": "v1",
		},
	}
	// 使用了variable而不是sourceService里面的值，匹配到一个route
	for i := 0; i < 10; i++ {
		resp, err := consumer.GetOneInstance(request1)
		c.Assert(err, check.IsNil)
		c.Assert(resp.Instances[0].GetMetadata()["k1"], check.Equals, "v1")
	}

	os.Setenv("k2", "v2-1")
	os.Setenv("k3", "v3")
	os.Setenv("k4k", "v4")
	// 同时设置k2,优先使用system的配置
	cfg.GetGlobal().GetSystem().SetVariable("k2", "v2")
	// 使之无法匹配到第一个route
	cfg.GetGlobal().GetSystem().UnsetVariable("k1")
	request1.SourceService.Metadata = map[string]string{"k2": "v2", "k3": "v3"}
	// 匹配到第二个route
	for i := 0; i < 10; i++ {
		resp, err := consumer.GetOneInstance(request1)
		c.Assert(err, check.IsNil)
		c.Assert(resp.Instances[0].GetMetadata()["k2"], check.Equals, "v2")
		c.Assert(resp.Instances[0].GetMetadata()["k4"], check.Equals, "v4")
	}

	os.Unsetenv("k3")
	os.Setenv("k4", "v4")
	request1.SourceService.Metadata = map[string]string{"k2": "v2", "k5": "v5"}
	// 匹配到第三个route
	for i := 0; i < 10; i++ {
		resp, err := consumer.GetOneInstance(request1)
		log.Printf("err is %v", err)
		c.Assert(err, check.IsNil)
		c.Assert(resp.Instances[0].GetMetadata()["k2"], check.Equals, "v2-0")
		c.Assert(resp.Instances[0].GetMetadata()["k4"], check.Equals, "v4")
	}
}

// TestBadVariable test bad variable
func (t *RuleRoutingTestingSuite) TestBadVariable(c *check.C) {
	log.Printf("start to TestBadVariable")
	serviceName := "BadVariable"
	service := &namingpb.Service{
		Name:      &wrappers.StringValue{Value: serviceName},
		Namespace: &wrappers.StringValue{Value: testNamespace},
		Token:     &wrappers.StringValue{Value: uuid.New().String()},
	}
	t.mockServer.RegisterService(service)
	err := registerRouteRuleByFile(t.mockServer, service, "testdata/route_rule/bad_variable.json")
	c.Assert(err, check.IsNil)
	// 注册具有对应标签的实例
	t.RegisterInstancesWithMetadataAndNum(service, []*InstanceMetadataAndNum{
		{map[string]string{"k1": "v1"}, 2},
		{map[string]string{"k2": "v2", "k4": "v4"}, 2},
		{map[string]string{"k2": "v2-0", "k4": "v4"}, 2},
		{map[string]string{"k2": "v2", "k4": "v4-0"}, 2},
	})

	// 配置文件里面带有k1:v1的变量
	cfg, err := config.LoadConfigurationByFile("testdata/sr_rule_variable.yaml")
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	// cfg.GetGlobal().GetSystem().SetVariable("k1", "v1")
	request1 := &api.GetOneInstanceRequest{}
	request1.Namespace = testNamespace
	request1.Service = serviceName
	request1.SourceService = &model.ServiceInfo{
		Service:   "BadVariable",
		Namespace: testNamespace,
		Metadata: map[string]string{
			"k1": "v1",
		},
	}
	_, err = consumer.GetOneInstance(request1)
	log.Printf("err: %v", err)
	c.Assert(err, check.NotNil)
}

// TestParameterRegex test parameter regex
func (t *RuleRoutingTestingSuite) TestParameterRegex(c *check.C) {
	log.Printf("start to TestParameterRegex")
	serviceName := "RegexParameter"
	service := &namingpb.Service{
		Name:      &wrappers.StringValue{Value: serviceName},
		Namespace: &wrappers.StringValue{Value: testNamespace},
		Token:     &wrappers.StringValue{Value: uuid.New().String()},
	}
	t.mockServer.RegisterService(service)
	err := registerRouteRuleByFile(t.mockServer, service, "testdata/route_rule/regex_parameter.json")
	c.Assert(err, check.IsNil)
	// 注册具有对应标签的实例
	t.RegisterInstancesWithMetadataAndNum(service, []*InstanceMetadataAndNum{
		{map[string]string{"k2": "v2x"}, 2},
		{map[string]string{"k2": "v2xx"}, 2},
		{map[string]string{"k2": "v2d"}, 2},
		{map[string]string{"k2": "v2dd"}, 2},
	})

	// 配置文件里面带有k1:v1的变量
	cfg, err := config.LoadConfigurationByFile("testdata/sr_rule_variable.yaml")
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()
	request1 := &api.GetOneInstanceRequest{}
	request1.Namespace = testNamespace
	request1.Service = serviceName
	request1.SourceService = &model.ServiceInfo{
		Service:   "RegexParameter",
		Namespace: testNamespace,
		Metadata: map[string]string{
			"k1": "v1",
			"k2": "v2x+",
		},
	}
	var firstValue, secondValue int
	for i := 0; i < 10; i++ {
		resp, err := consumer.GetOneInstance(request1)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		k2Value := resp.GetInstances()[0].GetMetadata()["k2"]
		if k2Value == "v2x" {
			firstValue++
		}
		if k2Value == "v2xx" {
			secondValue++
		}
	}
	c.Assert(firstValue+secondValue, check.Equals, 10)
	c.Assert(firstValue > 0, check.Equals, true)
	c.Assert(secondValue > 0, check.Equals, true)

	firstValue, secondValue = 0, 0
	// 更换正则表达式
	request1.SourceService.Metadata = map[string]string{
		"k1": "v1",
		"k2": "v2d+",
	}
	for i := 0; i < 10; i++ {
		resp, err := consumer.GetOneInstance(request1)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		k2Value := resp.GetInstances()[0].GetMetadata()["k2"]
		if k2Value == "v2d" {
			firstValue++
		}
		if k2Value == "v2dd" {
			secondValue++
		}
	}
	c.Assert(firstValue+secondValue, check.Equals, 10)
	c.Assert(firstValue > 0, check.Equals, true)
	c.Assert(secondValue > 0, check.Equals, true)

	// 更换正则表达式 测试先行断言反向匹配
	log.Printf("Start to TestNegativeLookahead ")
	firstValue, secondValue = 0, 0
	request1.SourceService.Metadata = map[string]string{
		"k1": "v1",
		"k2": "((?!v2d+).)*",
	}
	for i := 0; i < 10; i++ {
		resp, err := consumer.GetOneInstance(request1)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		k2Value := resp.GetInstances()[0].GetMetadata()["k2"]
		if k2Value == "v2x" {
			firstValue++
		}
		if k2Value == "v2xx" {
			secondValue++
		}
	}
	c.Assert(firstValue+secondValue, check.Equals, 10)
	c.Assert(firstValue > 0, check.Equals, true)
	c.Assert(secondValue > 0, check.Equals, true)

	// 规则中 source 的正则表达式有问题，要进行报错
	request1.SourceService.Metadata = map[string]string{
		"k1": "*",
		"k2": "v2d+",
	}
	_, err = consumer.GetOneInstance(request1)
	log.Printf("invalid parameter source regex, err is: %v", err)
	c.Assert(err, check.NotNil)

	// 规则中 destination 的正则表达式有问题，要进行报错
	request1.SourceService.Metadata = map[string]string{
		"k1": "v1",
		"k2": "*",
	}
	_, err = consumer.GetOneInstance(request1)
	log.Printf("invalid parameter destination regex, err is: %v", err)
	c.Assert(err, check.NotNil)
}

// TestVariableRegex test variable regex
func (t *RuleRoutingTestingSuite) TestVariableRegex(c *check.C) {
	log.Printf("start to TestVariableRegex")
	serviceName := "RegexVariable"
	service := &namingpb.Service{
		Name:      &wrappers.StringValue{Value: serviceName},
		Namespace: &wrappers.StringValue{Value: testNamespace},
		Token:     &wrappers.StringValue{Value: uuid.New().String()},
	}
	t.mockServer.RegisterService(service)
	err := registerRouteRuleByFile(t.mockServer, service, "testdata/route_rule/regex_variable.json")
	c.Assert(err, check.IsNil)
	// 注册具有对应标签的实例
	t.RegisterInstancesWithMetadataAndNum(service, []*InstanceMetadataAndNum{
		{map[string]string{"k2": "v2x"}, 2},
		{map[string]string{"k2": "v2xx"}, 2},
		{map[string]string{"k2": "v2d"}, 2},
		{map[string]string{"k2": "v2dd"}, 2},
	})

	// 配置文件里面带有k1:v1的变量
	cfg, err := config.LoadConfigurationByFile("testdata/sr_rule_variable.yaml")
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	cfg.GetGlobal().GetSystem().SetVariable("k1-0", "v1d+")
	cfg.GetGlobal().GetSystem().SetVariable("k2-0", "v2d+")
	request1 := &api.GetOneInstanceRequest{}
	request1.Namespace = testNamespace
	request1.Service = serviceName
	request1.SourceService = &model.ServiceInfo{
		Service:   "RegexVariable",
		Namespace: testNamespace,
		Metadata: map[string]string{
			"k1": "v1d",
			"k2": "v2d",
		},
	}
	var firstValue, secondValue int
	for i := 0; i < 10; i++ {
		resp, err := consumer.GetOneInstance(request1)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		k2Value := resp.GetInstances()[0].GetMetadata()["k2"]
		if k2Value == "v2d" {
			firstValue++
		}
		if k2Value == "v2dd" {
			secondValue++
		}
	}
	c.Assert(firstValue+secondValue, check.Equals, 10)
	c.Assert(firstValue > 0, check.Equals, true)
	c.Assert(secondValue > 0, check.Equals, true)

	firstValue, secondValue = 0, 0
	// 更改 source 的 metadata，依然能匹配上正则表达式
	request1.SourceService.Metadata["k1"] = "v1dd"
	for i := 0; i < 10; i++ {
		resp, err := consumer.GetOneInstance(request1)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		k2Value := resp.GetInstances()[0].GetMetadata()["k2"]
		if k2Value == "v2d" {
			firstValue++
		}
		if k2Value == "v2dd" {
			secondValue++
		}
	}
	c.Assert(firstValue+secondValue, check.Equals, 10)
	c.Assert(firstValue > 0, check.Equals, true)
	c.Assert(secondValue > 0, check.Equals, true)

	// 更改 source 的 metadata，不能匹配上正则表达式
	request1.SourceService.Metadata["k1"] = "v1x"
	_, err = consumer.GetOneInstance(request1)
	c.Assert(err, check.NotNil)

	// 更换环境变量，匹配第二个规则
	cfg.GetGlobal().GetSystem().UnsetVariable("k1-0")
	cfg.GetGlobal().GetSystem().UnsetVariable("k2-0")
	cfg.GetGlobal().GetSystem().SetVariable("k1-1", "v1x+")
	cfg.GetGlobal().GetSystem().SetVariable("k2-1", "v2x+")

	firstValue, secondValue = 0, 0
	for i := 0; i < 10; i++ {
		resp, err := consumer.GetOneInstance(request1)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		k2Value := resp.GetInstances()[0].GetMetadata()["k2"]
		if k2Value == "v2x" {
			firstValue++
		}
		if k2Value == "v2xx" {
			secondValue++
		}
	}
	c.Assert(firstValue+secondValue, check.Equals, 10)
	c.Assert(firstValue > 0, check.Equals, true)
	c.Assert(secondValue > 0, check.Equals, true)

	// 更改环境变量，导致匹配第二个规则的 source的时候会出正则匹配问题
	cfg.GetGlobal().GetSystem().SetVariable("k1-1", "*")
	cfg.GetGlobal().GetSystem().SetVariable("k2-1", "v2x+")
	_, err = consumer.GetOneInstance(request1)
	log.Printf("invalid variable source regex, err is: %v", err)

	// 更改环境变量，导致匹配第二个规则的destination 的时候会出正则匹配问题
	cfg.GetGlobal().GetSystem().SetVariable("k1-1", "v1x+")
	cfg.GetGlobal().GetSystem().SetVariable("k2-1", "*")
	_, err = consumer.GetOneInstance(request1)
	log.Printf("invalid variable destination regex, err is: %v", err)
}

// InstanceMetadataAndNum 测试实例的 metadata 和 num
type InstanceMetadataAndNum struct {
	metadata map[string]string
	num      int
}

// RegisterInstancesWithMetadataAndNum 测试实例的 metadata 和 num
func (t *RuleRoutingTestingSuite) RegisterInstancesWithMetadataAndNum(svc *namingpb.Service, metadatas []*InstanceMetadataAndNum) {
	for idx, m := range metadatas {
		var instances []*namingpb.Instance
		for i := 0; i < m.num; i++ {
			instances = append(instances, &namingpb.Instance{
				Id:        &wrappers.StringValue{Value: uuid.New().String()},
				Service:   &wrappers.StringValue{Value: svc.Name.GetValue()},
				Namespace: &wrappers.StringValue{Value: svc.Namespace.GetValue()},
				Host:      &wrappers.StringValue{Value: fmt.Sprintf("127.0.0.%d", idx)},
				Port:      &wrappers.UInt32Value{Value: uint32(10040 + i)},
				Weight:    &wrappers.UInt32Value{Value: 100},
				Metadata:  m.metadata,
			})
		}
		t.mockServer.RegisterServiceInstances(svc, instances)
	}
}

// GetName 套件名字
func (t *RuleRoutingTestingSuite) GetName() string {
	return "RuleRouting"
}
