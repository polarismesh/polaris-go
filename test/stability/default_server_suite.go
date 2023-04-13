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
	"io/ioutil"
	"log"
	"net"
	"os"
	"time"

	"github.com/golang/protobuf/ptypes/wrappers"
	"github.com/google/uuid"
	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	"github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"google.golang.org/grpc"
	"gopkg.in/check.v1"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/localregistry"
	"github.com/polarismesh/polaris-go/test/mock"
	"github.com/polarismesh/polaris-go/test/util"
)

const (
	defaultTestNS  = "defTest"
	defaultTestSVC = "defTestService"

	defaultTestIP   = "127.0.0.1"
	defaultTestPORT = 9652

	wrongServerIp   = "127.0.0.1"
	wrongServerPort = 10086

	testCacheDir = "testdata/test_cache/"
)

// DefaultServerSuite 系统服务缓存测试套
type DefaultServerSuite struct {
	grpcServer        *grpc.Server
	grpcListener      net.Listener
	serviceToken      string
	testService       *service_manage.Service
	mockServer        mock.NamingServer
	downPolarisServer []*service_manage.Instance
	upPolarisServer   []*service_manage.Instance
	downHeahthServer  []*service_manage.Instance
	serviceReq        *api.GetOneInstanceRequest
	serverReq         *api.GetInstancesRequest
}

// SetUpSuite 初始化套件
func (t *DefaultServerSuite) SetUpSuite(c *check.C) {
	grpcOptions := make([]grpc.ServerOption, 0)
	maxStreams := 100000
	grpcOptions = append(grpcOptions, grpc.MaxConcurrentStreams(uint32(maxStreams)))
	grpc.EnableTracing = true

	var err error
	t.grpcServer = grpc.NewServer(grpcOptions...)
	t.serviceToken = uuid.New().String()
	t.mockServer = mock.NewNamingServer()
	t.mockServer.RegisterNamespace(&apimodel.Namespace{
		Name:    &wrappers.StringValue{Value: defaultTestNS},
		Comment: &wrappers.StringValue{Value: "for default service test"},
	})
	t.testService = &service_manage.Service{
		Name:      &wrappers.StringValue{Value: defaultTestSVC},
		Namespace: &wrappers.StringValue{Value: defaultTestNS},
		Token:     &wrappers.StringValue{Value: t.serviceToken},
	}
	t.mockServer.RegisterService(t.testService)
	// 注册系统服务
	t.mockServer.RegisterServerServices(defaultTestIP, defaultTestPORT)
	t.mockServer.GenTestInstances(t.testService, 50)

	service_manage.RegisterPolarisGRPCServer(t.grpcServer, t.mockServer)

	t.grpcListener, err = net.Listen("tcp", fmt.Sprintf("%s:%d", defaultTestIP, defaultTestPORT))
	if err != nil {
		log.Fatal(fmt.Sprintf("error listening appserver %v", err))
	}
	log.Printf("appserver listening on %s:%d\n", defaultTestIP, defaultTestPORT)

	t.downPolarisServer = append(t.downPolarisServer, &service_manage.Instance{
		Id:        &wrappers.StringValue{Value: uuid.New().String()},
		Service:   &wrappers.StringValue{Value: config.ServerDiscoverService},
		Namespace: &wrappers.StringValue{Value: config.ServerNamespace},
		Host:      &wrappers.StringValue{Value: wrongServerIp},
		Port:      &wrappers.UInt32Value{Value: wrongServerPort},
		Protocol:  &wrappers.StringValue{Value: "grpc"},
		Weight:    &wrappers.UInt32Value{Value: 100},
		Healthy:   &wrappers.BoolValue{Value: true},
		Metadata:  map[string]string{"protocol": "grpc"},
		ServiceToken: &wrappers.StringValue{Value: t.mockServer.GetServiceToken(&model.ServiceKey{
			Namespace: config.ServerNamespace,
			Service:   config.ServerDiscoverService,
		})},
	})

	t.downHeahthServer = append(t.downHeahthServer, &service_manage.Instance{
		Id:        &wrappers.StringValue{Value: uuid.New().String()},
		Service:   &wrappers.StringValue{Value: config.ServerHeartBeatService},
		Namespace: &wrappers.StringValue{Value: config.ServerNamespace},
		Host:      &wrappers.StringValue{Value: wrongServerIp},
		Port:      &wrappers.UInt32Value{Value: wrongServerPort},
		Protocol:  &wrappers.StringValue{Value: "grpc"},
		Weight:    &wrappers.UInt32Value{Value: 100},
		Healthy:   &wrappers.BoolValue{Value: true},
		Metadata:  map[string]string{"protocol": "grpc"},
		ServiceToken: &wrappers.StringValue{Value: t.mockServer.GetServiceToken(&model.ServiceKey{
			Namespace: config.ServerNamespace,
			Service:   config.ServerHeartBeatService,
		})},
	})

	t.serviceReq = &api.GetOneInstanceRequest{}
	t.serviceReq.Namespace = defaultTestNS
	t.serviceReq.Service = defaultTestSVC

	t.serverReq = &api.GetInstancesRequest{}
	t.serverReq.Namespace = config.ServerNamespace
	t.serverReq.Service = config.ServerDiscoverService
	t.serverReq.SourceService = &model.ServiceInfo{
		Namespace: config.ServerNamespace,
		Service:   config.ServerDiscoverService,
		Metadata: map[string]string{
			"protocol": "grpc",
		},
	}

	go func() {
		t.grpcServer.Serve(t.grpcListener)
	}()
}

// GetName 套件名字
func (t *DefaultServerSuite) GetName() string {
	return "DefaultServer"
}

// TearDownSuite 销毁套件
func (t *DefaultServerSuite) TearDownSuite(c *check.C) {
	t.grpcServer.Stop()
	util.InsertLog(t, c.GetTestLog())
}

// TestDefaultFailOver 测试预埋server挂了一个后能否快速切换，以及预埋server都挂了后，能否使用本地缓存恢复
func (t *DefaultServerSuite) TestDefaultFailOver(c *check.C) {
	// 测试预埋server挂了一个后能否快速切换
	cfg := config.NewDefaultConfiguration(
		[]string{"127.0.0.1:7655", fmt.Sprintf("%s:%d", defaultTestIP, defaultTestPORT)})
	cfg.GetGlobal().GetSystem().GetDiscoverCluster().SetNamespace(config.ServerNamespace)
	cfg.GetGlobal().GetSystem().GetDiscoverCluster().SetService(config.ServerDiscoverService)
	cfg.GetConsumer().GetLocalCache().SetPersistDir(util.BackupDir)
	cfg.GetConsumer().GetLocalCache().SetPersistEnable(true)
	cfg.GetGlobal().GetStatReporter().SetEnable(false)
	cfg.GetGlobal().GetAPI().SetTimeout(3 * time.Second)
	defer util.DeleteDir(util.BackupDir)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)

	for i := 0; i < 20; i++ {
		t.serviceReq.FlowID = uint64(i)
		oneReps, err := consumer.GetOneInstance(t.serviceReq)
		c.Assert(err, check.IsNil)
		log.Printf("ip from polaris-server is %s, index is %d\n", oneReps.GetInstance(), i)
	}
	time.Sleep(10 * time.Second)
	consumer.Destroy()

	failCfg := config.NewDefaultConfiguration(
		[]string{"127.0.0.1:7655", "127.0.0.1:7654"})
	failCfg.Consumer.LocalCache.PersistDir = util.BackupDir

	// 删除缓存后不能获取服务信息
	util.DeleteDir(util.BackupDir)
	failConsumer2, err := api.NewConsumerAPIByConfig(failCfg)
	c.Assert(err, check.IsNil)
	t.serviceReq.Timeout = model.ToDurationPtr(3 * time.Second)
	for i := 0; i < 3; i++ {
		t.serviceReq.FlowID = uint64(i)
		log.Printf("get instance with wrong cfg, %v\n", t.serviceReq.FlowID)
		resp, err := failConsumer2.GetOneInstance(t.serviceReq)
		c.Assert(err, check.NotNil)
		c.Assert(resp, check.IsNil)
		// log.Printf("ip from polaris-server is %+v, index is %d\n", resp.Instances[0], i)
	}
	failConsumer2.Destroy()

	// //预埋server都挂了后，能否使用本地缓存恢复
	// t.restoreCache(config.ServerNamespace, config.ServerDiscoverService)
	// failConsumer, err := api.NewConsumerAPIByConfig(failCfg)
	// c.Assert(err, check.IsNil)
	// for i := 0; i < 20; i++ {
	//	t.serviceReq.FlowID = uint64(i)
	//	resp, err = failConsumer.GetOneInstance(t.serviceReq)
	//	c.Assert(err, check.IsNil)
	//	log.Printf("ip from polaris-server is %+v, index is %d\n", resp.Instances[0], i)
	// }
	// log.Printf("finished TestDefaultFailOver\n")
	// failConsumer.Destroy()
}

// TestHealthyServerDown .
// 测试健康检测服务器挂掉或者部分挂掉后的情景
// 如果服务器全挂的话，heartbeat多次该服务器就会被熔断
// 在服务器全挂之后，重新上线了可用的服务器，依然可以正常心跳
func (t *DefaultServerSuite) TestHealthyServerDown(c *check.C) {
	util.DeleteDir(util.BackupDir)
	healthKey := &model.ServiceKey{Namespace: config.ServerNamespace, Service: config.ServerHeartBeatService}
	var upHealthServer []*service_manage.Instance
	upHealthServer = append(upHealthServer, t.mockServer.GetServiceInstances(healthKey)...)

	t.mockServer.SetServiceInstances(healthKey, t.downHeahthServer)

	// 测试polaris server挂了一些后能否继续使用
	cfg := config.NewDefaultConfiguration([]string{fmt.Sprintf("%s:%d", defaultTestIP, defaultTestPORT)})
	cfg.GetGlobal().GetSystem().GetDiscoverCluster().SetNamespace(config.ServerNamespace)
	cfg.GetGlobal().GetSystem().GetDiscoverCluster().SetService(config.ServerDiscoverService)
	cfg.GetGlobal().GetSystem().GetHealthCheckCluster().SetNamespace(config.ServerNamespace)
	cfg.GetGlobal().GetSystem().GetHealthCheckCluster().SetService(config.ServerHeartBeatService)
	cfg.GetConsumer().GetLocalCache().SetPersistDir(util.BackupDir)
	cfg.GetGlobal().GetStatReporter().SetEnable(false)
	cfg.GetConsumer().GetCircuitBreaker().SetSleepWindow(100 * time.Second)
	cfg.GetConsumer().GetCircuitBreaker().GetErrorCountConfig().SetContinuousErrorThreshold(3)
	cfg.Global.System.HealthCheckCluster.RefreshInterval = model.ToDurationPtr(3 * time.Second)
	defer util.DeleteDir(util.BackupDir)
	sdkCtx, err := api.InitContextByConfig(cfg)
	c.Assert(err, check.IsNil)
	provider := api.NewProviderAPIByContext(sdkCtx)
	c.Assert(err, check.IsNil)
	registry, err := sdkCtx.GetPlugins().GetPlugin(common.TypeLocalRegistry, "inmemory")
	regPlug := registry.(localregistry.LocalRegistry)
	c.Assert(err, check.IsNil)

	testKey := &model.ServiceKey{
		Namespace: defaultTestNS,
		Service:   defaultTestSVC,
	}

	heartBeatInstance := t.mockServer.GetServiceInstances(testKey)[0]

	heartbeatReq := &model.InstanceHeartbeatRequest{
		Service:      defaultTestSVC,
		ServiceToken: t.testService.Token.GetValue(),
		Namespace:    defaultTestNS,
		InstanceID:   heartBeatInstance.GetId().GetValue(),
		Host:         heartBeatInstance.GetHost().GetValue(),
		Port:         int(heartBeatInstance.GetPort().GetValue()),
		Timeout:      model.ToDurationPtr(3 * time.Second),
	}

	for i := 0; i < 8; i++ {
		req := &api.InstanceHeartbeatRequest{}
		req.InstanceHeartbeatRequest = *heartbeatReq
		err := provider.Heartbeat(req)
		c.Assert(err, check.NotNil)
		log.Printf("%v error heartbeat, expected err %v\n", i, err)
	}
	time.Sleep(1 * time.Second)
	svcInstances := regPlug.GetInstances(&model.ServiceKey{
		Service:   config.ServerHeartBeatService,
		Namespace: config.ServerNamespace,
	}, false, true)
	var instance model.Instance
	for _, inst := range svcInstances.GetInstances() {
		if inst.GetId() == t.downHeahthServer[0].GetId().GetValue() {
			instance = inst
			break
		}
	}
	c.Assert(instance, check.NotNil)
	cbStatus := instance.GetCircuitBreakerStatus()
	c.Assert(cbStatus, check.NotNil)
	log.Printf(
		"downserver is %s:%d, status is %v", instance.GetHost(), instance.GetPort(), cbStatus.GetStatus())
	c.Assert(cbStatus.GetStatus(), check.Equals, model.Open)
	// 重新上线实例
	var someDownHealthServers []*service_manage.Instance
	someDownHealthServers = append(someDownHealthServers, upHealthServer...)
	someDownHealthServers = append(someDownHealthServers, t.downHeahthServer...)
	t.mockServer.SetServiceInstances(healthKey, someDownHealthServers)
	log.Printf("start to resume some health servers")
	time.Sleep(5 * time.Second)
	log.Printf("end to resume some health servers")
	for i := 0; i < 4; i++ {
		req := &api.InstanceHeartbeatRequest{}
		req.InstanceHeartbeatRequest = *heartbeatReq
		err := provider.Heartbeat(req)
		c.Assert(err, check.IsNil)
		log.Printf("%v normal heartbeat, success\n", i)
	}

	provider.Destroy()
	t.mockServer.SetServiceInstances(healthKey, upHealthServer)
}

// 重置预存的cache
func (t *DefaultServerSuite) restoreCache(namespace string, serviceName string) {
	if !util.DirExist(util.BackupDir) {
		os.MkdirAll(util.BackupDir, 0744)
	}
	fileName1 := "svc#" + namespace + "#" + serviceName + "#instance.json"
	fileName2 := "svc#" + namespace + "#" + serviceName + "#routing.json"
	input1, _ := ioutil.ReadFile(testCacheDir + serviceName + "/" + fileName1)
	ioutil.WriteFile(util.BackupDir+"/"+fileName1, input1, 0644)

	input2, _ := ioutil.ReadFile(testCacheDir + serviceName + "/" + fileName2)
	ioutil.WriteFile(util.BackupDir+"/"+fileName2, input2, 0644)
}
