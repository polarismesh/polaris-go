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

package discover

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/golang/protobuf/ptypes/wrappers"
	"github.com/google/uuid"
	"github.com/modern-go/reflect2"
	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	"github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"github.com/polarismesh/specification/source/go/api/v1/traffic_manage"
	"google.golang.org/grpc"
	"gopkg.in/check.v1"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	commontest "github.com/polarismesh/polaris-go/test/common"
	"github.com/polarismesh/polaris-go/test/mock"
	"github.com/polarismesh/polaris-go/test/util"
	commontest "github.com/polarismesh/polaris-go/test/common"
)

const (
	// 测试的默认命名空间
	consumerNamespace = "testns"
	// 测试的默认服务名
	consumerService = "svc1"
	// 测试服务器的默认地址
	consumerIPAddress = "127.0.0.1"
	// 测试服务器的端口
	consumerPort = commontest.ConsumerSuitServerPort
	// env name for config file
	envName = "server_addr"
)

const (
	// 直接过滤的实例数
	normalInstances    = 3
	isolatedInstances  = 2
	unhealthyInstances = 1
	allInstances       = normalInstances + isolatedInstances + unhealthyInstances
)

// ConsumerTestingSuite 消费者API测试套
type ConsumerTestingSuite struct {
	mockServer   mock.NamingServer
	grpcServer   *grpc.Server
	grpcListener net.Listener
	serviceToken string
	testService  *service_manage.Service
}

// GetName 套件名字
func (t *ConsumerTestingSuite) GetName() string {
	return "Consumer"
}

// SetUpSuite 启动测试套程序
func (t *ConsumerTestingSuite) SetUpSuite(c *check.C) {
	grpcOptions := make([]grpc.ServerOption, 0)
	maxStreams := 100000
	grpcOptions = append(grpcOptions, grpc.MaxConcurrentStreams(uint32(maxStreams)))
	// get the grpc server wired up
	grpc.EnableTracing = true

	ipAddr := consumerIPAddress
	shopPort := consumerPort
	var err error
	err = os.Setenv(envName, fmt.Sprintf("%s:%d", consumerIPAddress, consumerPort))
	c.Assert(err, check.IsNil)
	t.grpcServer = grpc.NewServer(grpcOptions...)
	t.serviceToken = uuid.New().String()
	t.mockServer = mock.NewNamingServer()
	token := t.mockServer.RegisterServerService(config.ServerDiscoverService)
	t.mockServer.RegisterServerInstance(ipAddr, shopPort, config.ServerDiscoverService, token, true)
	t.mockServer.RegisterNamespace(&apimodel.Namespace{
		Name:    &wrappers.StringValue{Value: consumerNamespace},
		Comment: &wrappers.StringValue{Value: "for consumer api test"},
		Owners:  &wrappers.StringValue{Value: "ConsumerAPI"},
	})
	t.mockServer.RegisterServerServices(ipAddr, shopPort)
	t.testService = &service_manage.Service{
		Name:      &wrappers.StringValue{Value: consumerService},
		Namespace: &wrappers.StringValue{Value: consumerNamespace},
		Token:     &wrappers.StringValue{Value: t.serviceToken},
	}
	t.mockServer.RegisterService(t.testService)
	t.mockServer.GenTestInstances(t.testService, normalInstances)
	t.mockServer.GenInstancesWithStatus(t.testService, isolatedInstances, mock.IsolatedStatus, 2048)
	t.mockServer.GenInstancesWithStatus(t.testService, unhealthyInstances, mock.UnhealthyStatus, 4096)

	service_manage.RegisterPolarisGRPCServer(t.grpcServer, t.mockServer)
	t.grpcListener, err = net.Listen("tcp", fmt.Sprintf("%s:%d", ipAddr, shopPort))
	if err != nil {
		log.Fatalf("error listening appserver %v", err)
	}
	log.Printf("appserver listening on %s:%d\n", ipAddr, shopPort)
	go func() {
		t.grpcServer.Serve(t.grpcListener)
	}()
}

// TearDownSuite 结束测试套程序
func (t *ConsumerTestingSuite) TearDownSuite(c *check.C) {
	t.grpcServer.GracefulStop()
	util.InsertLog(t, c.GetTestLog())
}

// TestInitConsumerConfigByFile 测试初始化消费者配置文件
func (t *ConsumerTestingSuite) TestInitConsumerConfigByFile(c *check.C) {
	log.Printf("Start TestInitConsumerConfigByFile")
	ctx, err := api.InitContextByFile("testdata/consumer.yaml")
	c.Assert(err, check.IsNil)
	ctx.Destroy()
}

// TestInitConsumerConfigByDefault 测试以无文件默认配置初始化消费者api
func (t *ConsumerTestingSuite) TestInitConsumerConfigByDefault(c *check.C) {
	log.Printf("Start TestInitConsumerConfigByDefault")
	cfg := config.NewDefaultConfiguration([]string{fmt.Sprintf("127.0.0.1:%s", consumerPort)})
	enableStat := false
	cfg.Global.StatReporter.Enable = &enableStat
	consumerAPI, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	consumerAPI.Destroy()
}

// TestGetInstancesNormal 测试获取多个服务实例
func (t *ConsumerTestingSuite) TestGetInstancesNormal(c *check.C) {
	log.Printf("Start TestGetInstancesNormal")
	t.testGetInstances(c, false)
}

// 在mockTimeout宏中，执行测试逻辑
func (t *ConsumerTestingSuite) runWithMockTimeout(mockTimeout bool, handle func()) {
	t.mockServer.MakeOperationTimeout(mock.OperationDiscoverInstance, mockTimeout)
	t.mockServer.MakeOperationTimeout(mock.OperationDiscoverRouting, mockTimeout)
	defer func() {
		defer t.mockServer.MakeOperationTimeout(mock.OperationDiscoverInstance, false)
		defer t.mockServer.MakeOperationTimeout(mock.OperationDiscoverRouting, false)
	}()
	handle()
}

// TestGetInstancesTimeout 测试获取多个服务实例
func (t *ConsumerTestingSuite) TestGetInstancesTimeout(c *check.C) {
	log.Printf("Start TestGetInstancesTimeout")
	t.mockServer.SetPrintDiscoverReturn(true)
	time.Sleep(time.Millisecond * 10)
	t.testGetInstances(c, true)
	t.mockServer.SetPrintDiscoverReturn(false)
}

// 测试获取多个服务实例
func (t *ConsumerTestingSuite) testGetInstances(c *check.C, mockTimeout bool) {
	t.runWithMockTimeout(mockTimeout, func() {
		sdkContext, err := api.InitContextByFile("testdata/consumer.yaml")
		sdkContext.GetConfig().GetConsumer().GetLocalCache().SetStartUseFileCache(false)
		c.Assert(err, check.IsNil)
		consumer := api.NewConsumerAPIByContext(sdkContext)
		defer consumer.Destroy()
		time.Sleep(2 * time.Second)
		request := &api.GetInstancesRequest{}
		request.FlowID = 1111
		request.Namespace = consumerNamespace
		request.Service = consumerService
		startTime := time.Now()
		resp, err := consumer.GetInstances(request)
		endTime := time.Now()
		consumeTime := endTime.Sub(startTime)
		fmt.Printf("time consume is %v\n", consumeTime)
		if err != nil {
			fmt.Printf("err recv is %v\n", err)
		}
		c.Assert(err, check.IsNil)
		for i, ist := range resp.Instances {
			fmt.Printf("inst %d, %v\n", i, ist)
		}
		c.Assert(len(resp.Instances), check.Equals, normalInstances)

		request.FlowID = 1112
		request.Namespace = consumerNamespace
		request.Service = consumerService
		request.SkipRouteFilter = true
		svcInstances, err := consumer.GetInstances(request)
		c.Assert(err, check.IsNil)
		var unhealthyInstancesCount int
		for _, instance := range svcInstances.GetInstances() {
			c.Assert(instance.IsIsolated(), check.Equals, false)
			if !instance.IsHealthy() {
				unhealthyInstancesCount++
			}
		}
		c.Assert(unhealthyInstancesCount, check.Equals, unhealthyInstances)
		c.Assert(allInstances-len(svcInstances.GetInstances()), check.Equals, isolatedInstances)
		callResult := &api.ServiceCallResult{}
		callResult.CalledInstance = svcInstances.GetInstances()[0]
		callResult.SetDelay(consumeTime)
		callResult.SetRetCode(200)
		callResult.RetStatus = model.RetSuccess
		err = consumer.UpdateServiceCallResult(callResult)
		c.Assert(err, check.IsNil)
		time.Sleep(5 * time.Second)
	})
}

// 测试获取单个服务实例
func (t *ConsumerTestingSuite) testGetOneInstance(c *check.C, mockTimeout bool) {
	t.runWithMockTimeout(mockTimeout, func() {
		consumer, err := api.NewConsumerAPIByFile("testdata/consumer.yaml")
		defer consumer.Destroy()
		c.Assert(err, check.IsNil)
		time.Sleep(2 * time.Second)
		request := &api.GetOneInstanceRequest{}
		request.FlowID = 1111
		request.Namespace = consumerNamespace
		request.Service = consumerService
		for i := 0; i < 30; i++ {
			startTime := time.Now()
			resp, err := consumer.GetOneInstance(request)
			endTime := time.Now()
			consumedTime := endTime.Sub(startTime)
			if consumedTime.Milliseconds() > 0 {
				fmt.Printf("time consume is %v\n", consumedTime)
			}
			if !mockTimeout {
				c.Assert(consumedTime < 100*time.Millisecond, check.Equals, true)
			}
			c.Assert(err, check.IsNil)
			c.Assert(len(resp.Instances), check.Equals, 1)
			inst := resp.Instances[0]
			c.Assert(inst.IsIsolated(), check.Equals, false)
			c.Assert(inst.IsHealthy(), check.Equals, true)
		}
	})
}

// TestGetAllInstanceNormal 测试获取单个服务实例
func (t *ConsumerTestingSuite) TestGetAllInstanceNormal(c *check.C) {
	log.Printf("Start TestGetAllInstanceNormal")
	t.testGetAllInstance(c, false)
}

// TestGetAllInstanceTimeout 测试获取单个服务实例
func (t *ConsumerTestingSuite) TestGetAllInstanceTimeout(c *check.C) {
	log.Printf("Start TestGetAllInstanceTimeout")
	t.testGetAllInstance(c, true)
}

// 测试获取全量服务实例
func (t *ConsumerTestingSuite) testGetAllInstance(c *check.C, mockTimeout bool) {
	t.runWithMockTimeout(mockTimeout, func() {
		consumer, err := api.NewConsumerAPIByFile("testdata/consumer.yaml")
		c.Assert(err, check.IsNil)
		defer consumer.Destroy()
		time.Sleep(2 * time.Second)
		request := &api.GetAllInstancesRequest{}
		request.FlowID = 1111
		request.Namespace = consumerNamespace
		request.Service = consumerService
		for i := 0; i < 30; i++ {
			startTime := time.Now()
			resp, err := consumer.GetAllInstances(request)
			endTime := time.Now()
			consumedTime := endTime.Sub(startTime)
			if consumedTime.Milliseconds() > 0 {
				fmt.Printf("time consume is %v\n", consumedTime)
			}
			if !mockTimeout {
				c.Assert(consumedTime < 100*time.Millisecond, check.Equals, true)
			}
			c.Assert(err, check.IsNil)
			c.Assert(len(resp.Instances), check.Equals, allInstances)
		}
	})
}

// TestSideCarUpdateServiceCallResult 测试获取单个实例后，NewServiceCallResult调用查找instance，上报调用结果
func (t *ConsumerTestingSuite) TestSideCarUpdateServiceCallResult(c *check.C) {
	log.Printf("Start TestSideCarUpdateServiceCallResult")
	t.mockServer.MakeOperationTimeout(mock.OperationDiscoverInstance, false)
	t.mockServer.MakeOperationTimeout(mock.OperationDiscoverRouting, false)
	consumer, err := api.NewConsumerAPIByFile("testdata/consumer.yaml")
	defer consumer.Destroy()
	c.Assert(err, check.IsNil)
	time.Sleep(2 * time.Second)
	request := &api.GetOneInstanceRequest{}
	request.FlowID = 1112
	request.Namespace = consumerNamespace
	request.Service = consumerService
	startTime := time.Now()
	resp, err := consumer.GetOneInstance(request)
	endTime := time.Now()
	consumedTime := endTime.Sub(startTime)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.Instances), check.Equals, 1)
	inst := resp.Instances[0]
	c.Assert(inst.IsIsolated(), check.Equals, false)
	c.Assert(inst.IsHealthy(), check.Equals, true)
	util.DeleteDir(util.BackupDir)
	// 测试ServiceCallResult代码
	insReq := api.InstanceRequest{
		InstanceID: inst.GetId(),
		ServiceKey: model.ServiceKey{
			Namespace: inst.GetNamespace(),
			Service:   inst.GetService(),
		},
	}
	svcCallResult, err := api.NewServiceCallResult(consumer.SDKContext(), insReq)
	c.Assert(err, check.IsNil)
	c.Assert(svcCallResult, check.NotNil)
	svcCallResult.SetRetStatus(model.RetSuccess)
	svcCallResult.SetRetCode(200)
	svcCallResult.SetDelay(consumedTime)
	err = consumer.UpdateServiceCallResult(svcCallResult)
	c.Assert(err, check.IsNil)
	// invalid InstanceRequest Test
	invalidInsReq := api.InstanceRequest{
		InstanceID: "",
		ServiceKey: model.ServiceKey{
			Namespace: inst.GetNamespace(),
			Service:   inst.GetService(),
		},
	}
	nilResult, err := api.NewServiceCallResult(consumer.SDKContext(), invalidInsReq)
	c.Assert(err, check.NotNil)
	c.Assert(nilResult, check.IsNil)
}

// 测试以错误的参数请求实例
func (t *ConsumerTestingSuite) testGetInstancesError(c *check.C, mockTimeout bool) {
	t.runWithMockTimeout(mockTimeout, func() {
		consumer, err := api.NewConsumerAPIByFile("testdata/consumer.yaml")
		c.Assert(err, check.IsNil)
		defer consumer.Destroy()
		request := &api.GetInstancesRequest{}
		request.FlowID = 1111
		request.Namespace = "errNS"
		request.Service = "errSVC"
		if !mockTimeout {
			resp, err := consumer.GetInstances(request)
			c.Assert(err, check.IsNil)
			c.Assert(resp.NotExists, check.Equals, true)
		} else {
			_, err := consumer.GetInstances(request)
			c.Assert(err, check.NotNil)
		}
	})
}

// TestGetInstancesErrorNormal 测试以错误的参数请求实例
func (t *ConsumerTestingSuite) TestGetInstancesErrorNormal(c *check.C) {
	log.Printf("Start TestGetInstancesErrorNormal")
	t.testGetInstancesError(c, false)
}

// 构建服务路由规则
func (t *ConsumerTestingSuite) buildServiceRoutes() {
	//
	//
	//
	// 进站规则
	t.mockServer.RegisterRouteRule(t.testService, &traffic_manage.Routing{
		Revision:  &wrappers.StringValue{Value: uuid.New().String()},
		Service:   &wrappers.StringValue{Value: consumerService},
		Namespace: &wrappers.StringValue{Value: consumerNamespace},
		Inbounds: []*traffic_manage.Route{
			{
				// 指定源服务为任意服务, 否则因为没有sourceServiceInfo会匹配不了
				Sources: []*traffic_manage.Source{
					{
						Service:   &wrappers.StringValue{Value: "*"},
						Namespace: &wrappers.StringValue{Value: "*"}},
				},
				// 根据不同逻辑set来进行目标服务分区路由
				Destinations: []*traffic_manage.Destination{
					{
						Metadata: map[string]*apimodel.MatchString{
							"logic_set": {
								Type:  apimodel.MatchString_EXACT,
								Value: &wrappers.StringValue{Value: "test"}},
						},
						Priority: &wrappers.UInt32Value{Value: 1},
						Weight:   &wrappers.UInt32Value{Value: 100},
					},
					{
						Metadata: map[string]*apimodel.MatchString{
							"logic_set": {
								Type:  apimodel.MatchString_EXACT,
								Value: &wrappers.StringValue{Value: "test"},
							},
						},
						Priority: &wrappers.UInt32Value{Value: 0},
						Weight:   &wrappers.UInt32Value{Value: 100},
					}},
			},
		},
	})
}

// 获取路由规则的测试
func (t *ConsumerTestingSuite) testGetRouteRule(c *check.C, mockTimeout bool) {
	t.runWithMockTimeout(mockTimeout, func() {
		t.buildServiceRoutes()
		defer t.mockServer.DeregisterRouteRule(t.testService)
		consumer, err := api.NewConsumerAPIByFile("testdata/consumer.yaml")
		c.Assert(err, check.IsNil)
		defer consumer.Destroy()
		req := &api.GetServiceRuleRequest{}
		req.FlowID = 1
		req.Namespace = consumerNamespace
		req.Service = consumerService
		rule, err := consumer.GetRouteRule(req)
		c.Assert(err, check.IsNil)
		c.Assert(reflect2.IsNil(rule.GetValue()), check.Equals, false)
	})
}

// TestGetRouteRuleNormal 获取路由规则的测试
func (t *ConsumerTestingSuite) TestGetRouteRuleNormal(c *check.C) {
	log.Printf("Start TestGetRouteRuleNormal")
	t.testGetRouteRule(c, false)
}

// TestGetRouteRuleTimeout 获取路由规则的测试
func (t *ConsumerTestingSuite) TestGetRouteRuleTimeout(c *check.C) {
	log.Printf("Start TestGetRouteRuleTimeout")
	t.testGetRouteRule(c, true)
}

const (
	workerCount = 10
	hostPattern = "10.11.123.%d"
)

// TestMultiGet 测试多协程同时获取多个服务，看看会不会出现服务信息串了的问题
func (t *ConsumerTestingSuite) TestMultiGet(c *check.C) {
	log.Printf("Start TestMultiGet")
	cfg := config.NewDefaultConfiguration(
		[]string{fmt.Sprintf("%s:%d", consumerIPAddress, consumerPort)})
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()
	for i := 0; i < workerCount; i++ {
		testService := &service_manage.Service{
			Name:      &wrappers.StringValue{Value: fmt.Sprintf("%s_%d", consumerService, i)},
			Namespace: &wrappers.StringValue{Value: consumerNamespace},
			Token:     &wrappers.StringValue{Value: uuid.New().String()},
		}
		t.mockServer.RegisterService(testService)
		t.mockServer.GenTestInstancesWithHostPort(
			testService, normalInstances, fmt.Sprintf(hostPattern, i), 10080)
	}

	wg := &sync.WaitGroup{}
	wg.Add(workerCount)
	for i := 0; i < workerCount; i++ {
		go func(idx int) {
			defer func() {
				log.Printf("worker %d done", idx)
				wg.Done()
			}()
			for j := 0; j < 1000; j++ {
				req := &api.GetOneInstanceRequest{}
				req.Namespace = consumerNamespace
				req.Service = fmt.Sprintf("%s_%d", consumerService, idx)
				result, err := consumer.GetOneInstance(req)
				c.Assert(err, check.IsNil)
				c.Assert(result.Instances[0].GetHost(), check.Equals, fmt.Sprintf(hostPattern, idx))
			}
		}(i)
	}
	wg.Wait()
}

// TestConsumerInit .
func (t *ConsumerTestingSuite) TestConsumerInit(c *check.C) {
	log.Printf("Start TestConsumerInit")
	cfg := config.NewDefaultConfiguration(
		[]string{fmt.Sprintf("%s:%d", consumerIPAddress, consumerPort)})
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	testServiceNoraml := &service_manage.Service{
		Name:      &wrappers.StringValue{Value: "initNormalService"},
		Namespace: &wrappers.StringValue{Value: consumerNamespace},
		Token:     &wrappers.StringValue{Value: uuid.New().String()},
	}
	t.mockServer.RegisterService(testServiceNoraml)
	t.mockServer.GenTestInstancesWithHostPort(
		testServiceNoraml, normalInstances, "127.0.0.1", 10080)

	initTestService := fmt.Sprintf("%s_%s", consumerService, "initTest")
	testService := &service_manage.Service{
		Name:      &wrappers.StringValue{Value: initTestService},
		Namespace: &wrappers.StringValue{Value: consumerNamespace},
		Token:     &wrappers.StringValue{Value: uuid.New().String()},
	}
	t.mockServer.RegisterService(testService)
	t.mockServer.GenTestInstancesWithHostPort(
		testService, normalInstances, "127.0.0.1", 10080)

	req := api.InitCalleeServiceRequest{}
	req.Namespace = consumerNamespace
	req.Service = initTestService
	err = consumer.InitCalleeService(&req)
	c.Check(err, check.IsNil)
}

// 测试如果server不返回首次请求，能不能正常获取实例
// func (t *ConsumerTestingSuite) TestGetOneInstanceNoReturn(c *check.C) {
//	log.Printf("Start TestGetOneInstanceNoReturn")
//	defer util.DeleteDir(util.BackupDir)
//	consumer, err := api.NewConsumerAPIByFile("testdata/consumer.yaml")
//	defer consumer.Destroy()
//	c.Assert(err, check.IsNil)
//	time.Sleep(2 * time.Second)
//	t.mockServer.SetPrintDiscoverReturn(true)
//	defer t.mockServer.SetPrintDiscoverReturn(false)
//	request := &api.GetOneInstanceRequest{}
//	request.FlowID = 1111
//	request.Namespace = consumerNamespace
//	request.Service = consumerService
//	svcEventKey := model.ServiceEventKey{
//		ServiceKey: model.ServiceKey{
//			Namespace: consumerNamespace,
//			Service:   consumerService,
//		},
//		Type: model.EventRouting,
//	}
//	t.mockServer.SetFirstNoReturn(svcEventKey)
//	defer t.mockServer.UnsetFirstNoReturn(svcEventKey)
//	_, err = consumer.GetOneInstance(request)
//	c.Assert(err, check.IsNil)
// }

// 测试可靠性默认服务名
const reliableConsumerService = "reliableSvc1"

// TestMultiGetWhenUpdate 测试多协程获取服务，且当时服务有大量实例正在上线
func (t *ConsumerTestingSuite) TestMultiGetWhenUpdate(c *check.C) {
	log.Printf("Start TestMultiGetWhenUpdate")
	cfg := config.NewDefaultConfiguration(
		[]string{fmt.Sprintf("%s:%d", consumerIPAddress, consumerPort)})
	cfg.GetConsumer().GetServiceRouter().SetChain([]string{config.DefaultServiceRouterRuleBased,
		config.DefaultServiceRouterNearbyBased, config.DefaultServiceRouterCanary})
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()
	svcKey := &model.ServiceKey{
		Namespace: consumerNamespace,
		Service:   reliableConsumerService,
	}
	testService := &service_manage.Service{
		Name:      &wrappers.StringValue{Value: svcKey.Service},
		Namespace: &wrappers.StringValue{Value: svcKey.Namespace},
		Token:     &wrappers.StringValue{Value: uuid.New().String()},
	}
	t.mockServer.RegisterService(testService)
	host := "127.0.0.1"
	var basePort uint32 = 8080
	instances := []*service_manage.Instance{
		{
			Id:      &wrappers.StringValue{Value: uuid.New().String()},
			Host:    &wrappers.StringValue{Value: host},
			Port:    &wrappers.UInt32Value{Value: basePort},
			Weight:  &wrappers.UInt32Value{Value: 100},
			Healthy: &wrappers.BoolValue{Value: true},
		},
	}
	t.mockServer.SetServiceInstances(svcKey, instances)
	count := 2
	wg := &sync.WaitGroup{}
	wg.Add(count)
	timeout := 20 * time.Second
	for i := 0; i < count; i++ {
		go func(idx int) {
			log.Printf("start discover worker %d", idx)
			defer wg.Done()
			timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			for {
				select {
				case <-timeoutCtx.Done():
					return
				default:
					req := &api.GetOneInstanceRequest{}
					req.Namespace = svcKey.Namespace
					req.Service = svcKey.Service
					_, err := consumer.GetOneInstance(req)
					c.Assert(err, check.IsNil)
				}
			}
		}(i)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		log.Printf("start circuitbreak worker")
		for {
			select {
			case <-ctx.Done():
				return
			default:
				req := &api.GetOneInstanceRequest{}
				req.Namespace = config.ServerNamespace
				req.Service = config.ServerDiscoverService
				resp, err := consumer.GetOneInstance(req)
				c.Assert(err, check.IsNil)
				result := &api.ServiceCallResult{}
				result.CalledInstance = resp.Instances[0]
				result.SetRetCode(1000)
				result.SetRetStatus(api.RetFail)
				result.SetDelay(1 * time.Second)
				consumer.UpdateServiceCallResult(result)
				time.Sleep(5 * time.Second)
				result = &api.ServiceCallResult{}
				result.CalledInstance = resp.Instances[0]
				result.SetRetCode(0)
				result.SetRetStatus(api.RetSuccess)
				result.SetDelay(1 * time.Second)
				consumer.UpdateServiceCallResult(result)
				time.Sleep(2 * time.Second)
			}
		}
	}()
	go func() {
		log.Printf("start register worker")
		var idx uint32 = 1
		var sleepCounter int
		for {
			select {
			case <-ctx.Done():
				return
			default:
				nextPort := basePort + idx
				idx++
				instances = append(instances, &service_manage.Instance{
					Id:      &wrappers.StringValue{Value: uuid.New().String()},
					Host:    &wrappers.StringValue{Value: host},
					Port:    &wrappers.UInt32Value{Value: nextPort},
					Weight:  &wrappers.UInt32Value{Value: 100},
					Healthy: &wrappers.BoolValue{Value: true}})
				t.mockServer.SetServiceInstances(svcKey, instances)
				sleepCounter++
				if sleepCounter%100 == 0 {
					time.Sleep(1 * time.Second)
				}
			}
		}
	}()
	wg.Wait()
	cancel()
	log.Printf("TestMultiGetWhenUpdate done")
}
