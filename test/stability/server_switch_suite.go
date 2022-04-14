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
	"log"
	"net"
	"os"
	"time"

	"github.com/golang/protobuf/ptypes/wrappers"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"gopkg.in/check.v1"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/pkg/network"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/serverconnector"
	"github.com/polarismesh/polaris-go/test/mock"
	"github.com/polarismesh/polaris-go/test/util"
)

const (
	mockListenHost = "127.0.0.1"
	testNamespace  = "testns"
	testSvcName    = "switchTestService"
	count          = 10
)

var (
	mockPorts   = []int{10090, 10091, 10092, 10093, 10094}
	builtinPort = 10095
)

// ServerSwitchSuite 服务切换测试套
type ServerSwitchSuite struct {
	builtinServer mock.NamingServer
	mockServers   []mock.NamingServer
	grpcServers   []*grpc.Server
}

// SetUpSuite 启动测试套程序
func (t *ServerSwitchSuite) SetUpSuite(c *check.C) {
	grpcOptions := make([]grpc.ServerOption, 0)
	maxStreams := 100000
	grpcOptions = append(grpcOptions, grpc.MaxConcurrentStreams(uint32(maxStreams)))

	// get the grpc server wired up
	grpc.EnableTracing = true

	var err error
	testService := &namingpb.Service{
		Name:      &wrappers.StringValue{Value: testSvcName},
		Namespace: &wrappers.StringValue{Value: testNamespace},
		Token:     &wrappers.StringValue{Value: uuid.New().String()},
	}
	discoverService := &namingpb.Service{
		Name:      &wrappers.StringValue{Value: config.ServerDiscoverService},
		Namespace: &wrappers.StringValue{Value: config.ServerNamespace},
		Token:     &wrappers.StringValue{Value: uuid.New().String()},
	}
	instances := make([]*namingpb.Instance, 0, 2)
	for _, port := range mockPorts {
		instances = append(instances, &namingpb.Instance{
			Id:        &wrappers.StringValue{Value: uuid.New().String()},
			Service:   &wrappers.StringValue{Value: config.ServerDiscoverService},
			Namespace: &wrappers.StringValue{Value: config.ServerNamespace},
			Host:      &wrappers.StringValue{Value: mockListenHost},
			Port:      &wrappers.UInt32Value{Value: uint32(port)},
			Weight:    &wrappers.UInt32Value{Value: 100},
			Healthy:   &wrappers.BoolValue{Value: true},
			Metadata:  map[string]string{"protocol": "grpc"},
		})
	}
	for _, port := range mockPorts {
		grpcServer := grpc.NewServer(grpcOptions...)
		t.grpcServers = append(t.grpcServers, grpcServer)
		mockServer := mock.NewNamingServer()
		t.mockServers = append(t.mockServers, mockServer)
		mockServer.RegisterService(testService)
		mockServer.GenTestInstances(testService, count)
		mockServer.RegisterService(discoverService)
		mockServer.SetServiceInstances(&model.ServiceKey{
			Namespace: config.ServerNamespace,
			Service:   config.ServerDiscoverService,
		}, instances)
		namingpb.RegisterPolarisGRPCServer(grpcServer, mockServer)
		grpcListener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", mockListenHost, port))
		if err != nil {
			log.Fatal(fmt.Sprintf("error listening appserver %v", err))
		}
		log.Printf("appserver listening on %s:%d\n", mockListenHost, port)
		go func() {
			grpcServer.Serve(grpcListener)
		}()
	}
	// 纯埋点server，只包含系统服务信息，不包含自身实例以及其他服务信息
	t.builtinServer = mock.NewNamingServer()
	t.builtinServer.RegisterService(discoverService)
	t.builtinServer.SetServiceInstances(&model.ServiceKey{
		Namespace: config.ServerNamespace,
		Service:   config.ServerDiscoverService,
	}, instances)
	builtinGrpcServer := grpc.NewServer(grpcOptions...)
	t.grpcServers = append(t.grpcServers, builtinGrpcServer)
	builtinListener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", mockListenHost, builtinPort))
	if err != nil {
		log.Fatal(fmt.Sprintf("error listening built appserver %v", err))
	}
	namingpb.RegisterPolarisGRPCServer(builtinGrpcServer, t.builtinServer)
	log.Printf("appserver listening on %s:%d\n", mockListenHost, builtinPort)
	go func() {
		builtinGrpcServer.Serve(builtinListener)
	}()
}

// TearDownSuite SetUpSuite 结束测试套程序
func (t *ServerSwitchSuite) TearDownSuite(c *check.C) {
	for _, grpcServer := range t.grpcServers {
		grpcServer.Stop()
	}
	if util.DirExist(util.BackupDir) {
		os.RemoveAll(util.BackupDir)
	}
	util.InsertLog(t, c.GetTestLog())
}

// GetName 获取用例名
func (t *ServerSwitchSuite) GetName() string {
	return "ServiceUpdateSuite"
}

// 获取连接管理器的方法
type managerGetter interface {
	GetConnectionManager() network.ConnectionManager
}

// TestSwitchServer 测试切换后台sever，以及在获取到了discover之后，是否会继续向埋点server请求
func (t *ServerSwitchSuite) TestSwitchServer(c *check.C) {
	log.Printf("start to TestSwitchServer")
	defer util.DeleteDir(util.BackupDir)
	interval := 1*time.Second + 10*time.Millisecond
	var enableStat bool
	configuration := config.NewDefaultConfiguration(
		[]string{fmt.Sprintf("%s:%d", mockListenHost, builtinPort)})
	configuration.Global.ServerConnector.SetServerSwitchInterval(interval)
	configuration.GetGlobal().GetSystem().GetDiscoverCluster().SetNamespace(config.ServerNamespace)
	configuration.GetGlobal().GetSystem().GetDiscoverCluster().SetService(config.ServerDiscoverService)
	configuration.GetGlobal().GetServerConnector().SetConnectTimeout(50 * time.Millisecond)
	configuration.GetGlobal().GetServerConnector().SetConnectionIdleTimeout(100 * time.Millisecond)
	configuration.Global.System.DiscoverCluster.RefreshInterval = model.ToDurationPtr(2 * time.Second)
	configuration.Consumer.LocalCache.PersistDir = util.BackupDir
	configuration.Global.StatReporter.Enable = &enableStat
	consumerApi, err := api.NewConsumerAPIByConfig(configuration)
	c.Assert(err, check.IsNil)
	defer consumerApi.Destroy()
	sdkCtx := consumerApi.SDKContext()
	plug, err := sdkCtx.GetPlugins().GetPlugin(common.TypeServerConnector, config.DefaultServerConnector)
	c.Assert(err, check.IsNil)
	manager := plug.(*serverconnector.Proxy).ServerConnector.(managerGetter).GetConnectionManager()
	loopCount := 2000
	// 等待从埋点server中获取得到discover的信息
	log.Printf("waiting 20s to get discover service info from builtin server")
	time.Sleep(20 * time.Second)
	log.Printf("end waiting")
	startTime := time.Now()
	var getInstancesReq *api.GetOneInstanceRequest
	getInstancesReq = &api.GetOneInstanceRequest{}
	getInstancesReq.Namespace = testNamespace
	getInstancesReq.Service = testSvcName
	allServers := make([]string, 0, len(mockPorts))
	for _, port := range mockPorts {
		allServers = append(allServers, fmt.Sprintf("%s:%d", mockListenHost, port))
	}
	switchedServers := make(map[string]int, 0)
	_, err = consumerApi.GetOneInstance(getInstancesReq)
	c.Assert(err, check.IsNil)
	log.Printf("start to do getInstance for loopCount %d", loopCount)
	for i := 0; i < loopCount; i++ {
		time.Sleep(100 * time.Millisecond)
		if time.Since(startTime) >= interval {
			conn, err := manager.GetConnection("Discover", config.DiscoverCluster)
			c.Assert(err, check.IsNil)
			switchedServers[conn.Address] = switchedServers[conn.Address] + 1
			conn.Release("Discover")
			startTime = time.Now()
		}
	}
	log.Printf("end to do getInstance")
	// var hasNotFoundServer bool
	for _, allServer := range allServers {
		if n, ok := switchedServers[allServer]; !ok {
			fmt.Printf("server %s not found\n", allServer)
			// hasNotFoundServer = true
		} else {
			fmt.Printf("server found %s, %d times\n", allServer, n)
		}
	}
	// c.Assert(hasNotFoundServer, check.Equals, false)
	// 只要有切换就行，后面权重轮询才考虑是否切换到所有的server
	c.Assert(len(switchedServers) > 1, check.Equals, true)
	notBuiltinDiscoverReqs := 0
	discoverKey := &model.ServiceKey{
		Namespace: config.ServerNamespace,
		Service:   config.ServerDiscoverService,
	}
	for i, ms := range t.mockServers {
		requests := ms.GetServiceRequests(&model.ServiceKey{
			Namespace: testNamespace,
			Service:   testSvcName,
		})
		log.Printf("requests to %s:%d for %s:%s, %d",
			mockListenHost, mockPorts[i], testNamespace, testSvcName, requests)
		// c.Assert(requests > 0, check.Equals, true)
		notBuiltinDiscoverReqs += ms.GetServiceRequests(discoverKey)
	}
	log.Printf("request to all not builtin discover servers for discover %d", notBuiltinDiscoverReqs)
	c.Assert(notBuiltinDiscoverReqs > 0, check.Equals, true)
	builtinDiscoverReqs := t.builtinServer.GetServiceRequests(discoverKey)
	log.Printf("request to builtin discover servers for discover %d", builtinDiscoverReqs)
	// 向埋点server请求discover的次数必须小于向discover请求的次数，并且要小于（20 / 2）
	c.Assert(builtinDiscoverReqs < notBuiltinDiscoverReqs, check.Equals, true)
	c.Assert(builtinDiscoverReqs < 10, check.Equals, true)
}
