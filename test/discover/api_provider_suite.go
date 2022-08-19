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
	"fmt"
	"log"
	"net"
	"time"

	"github.com/golang/protobuf/ptypes/wrappers"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"gopkg.in/check.v1"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/test/mock"
	"github.com/polarismesh/polaris-go/test/util"
)

const (
	providerNamespace    = "providerNS"
	providerService      = "providerSVC"
	providerIPAddress    = "127.0.0.1"
	providerPort         = 8008
	providerInstanceIP   = "127.0.0.2"
	providerInstancePort = 8848
)

// ProviderTestingSuite Provider测试套件
type ProviderTestingSuite struct {
	mockServer   mock.NamingServer
	grpcServer   *grpc.Server
	grpcListener net.Listener
	serviceToken string
	provider     api.ProviderAPI
}

// GetName 套件名字
func (t *ProviderTestingSuite) GetName() string {
	return "Provider"
}

// SetUpSuite 初始化测试套件
func (t *ProviderTestingSuite) SetUpSuite(c *check.C) {
	fmt.Printf("ProviderTestingSuite Start\n")
	grpcOptions := make([]grpc.ServerOption, 0)
	maxStreams := 100000
	grpcOptions = append(grpcOptions, grpc.MaxConcurrentStreams(uint32(maxStreams)))

	// get the grpc server wired up
	grpc.EnableTracing = true

	ipAddr := providerIPAddress
	shopPort := providerPort
	var err error
	t.grpcServer = grpc.NewServer(grpcOptions...)
	t.serviceToken = uuid.New().String()
	t.mockServer = mock.NewNamingServer()
	t.mockServer.RegisterServerServices(ipAddr, shopPort)
	t.mockServer.RegisterNamespace(&namingpb.Namespace{
		Name:    &wrappers.StringValue{Value: providerNamespace},
		Comment: &wrappers.StringValue{Value: "for consumer api test"},
		Owners:  &wrappers.StringValue{Value: "ConsumerAPI"},
	})
	testService := &namingpb.Service{
		Name:      &wrappers.StringValue{Value: providerService},
		Namespace: &wrappers.StringValue{Value: providerNamespace},
		Token:     &wrappers.StringValue{Value: t.serviceToken},
	}
	// 注册测试服务
	t.mockServer.RegisterService(testService)
	// 注册系统服务
	t.mockServer.RegisterServerServices(ipAddr, shopPort)
	// 代理到GRPC服务回调
	namingpb.RegisterPolarisGRPCServer(t.grpcServer, t.mockServer)
	// 进行端口监听
	t.grpcListener, err = net.Listen("tcp", fmt.Sprintf("%s:%d", ipAddr, shopPort))
	if err != nil {
		log.Fatal(fmt.Sprintf("error listening appserver %v", err))
	}
	log.Printf("appserver listening on %s:%d\n", ipAddr, shopPort)
	go func() {
		t.grpcServer.Serve(t.grpcListener)
	}()
	cfg, err := config.LoadConfigurationByFile("testdata/consumer.yaml")
	c.Assert(err, check.IsNil)
	t.provider, err = api.NewProviderAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	time.Sleep(2 * time.Second)
}

// TearDownSuite 结束测试套程序
func (t *ProviderTestingSuite) TearDownSuite(c *check.C) {
	t.provider.Destroy()
	t.grpcServer.Stop()
	util.InsertLog(t, c.GetTestLog())
}

// TestInitProviderAPIByDefault 测试以无文件默认配置初始化providerAPI
func (t *ProviderTestingSuite) TestInitProviderAPIByDefault(c *check.C) {
	log.Printf("Start TestInitProviderAPIByDefault")
	defer util.DeleteDir(util.BackupDir)
	cfg := config.NewDefaultConfiguration([]string{fmt.Sprintf("%s:%d", providerIPAddress, providerPort)})
	enableStat := false
	cfg.Consumer.LocalCache.PersistDir = "testdata/backup"
	cfg.Global.StatReporter.Enable = &enableStat
	providerAPI, err := api.NewProviderAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	providerAPI.Destroy()
}

// TestProviderNormal 测试ProviderAPI的三个功能，register，heartbeat，deregister
func (t *ProviderTestingSuite) TestProviderNormal(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	log.Printf("Start TestProviderNormal")
	t.testProvider(c, false)
}

// TestProviderTimeout 测试ProviderAPI的三个功能，register，heartbeat，deregister
func (t *ProviderTestingSuite) TestProviderTimeout(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	log.Printf("Start TestProviderTimeout")
	t.testProvider(c, true)
}

// 通用的provider接口测试函数
func (t *ProviderTestingSuite) testProvider(c *check.C, timeout bool) {
	defer util.DeleteDir(util.BackupDir)
	t.mockServer.MakeOperationTimeout(mock.OperationRegistry, timeout)
	t.mockServer.MakeOperationTimeout(mock.OperationHeartbeat, timeout)
	t.mockServer.MakeOperationTimeout(mock.OperationDeRegistry, timeout)
	registerReq := &api.InstanceRegisterRequest{}
	registerReq.Namespace = providerNamespace
	registerReq.Service = providerService
	registerReq.Host = providerInstanceIP
	registerReq.Port = providerInstancePort
	registerReq.ServiceToken = t.serviceToken
	// 先注册待心跳的服务实例
	regResp, err := t.provider.Register(registerReq)
	c.Assert(err, check.IsNil)
	fmt.Printf("registered instance id: %v\n", regResp.InstanceID)

	heartbeatReq := &api.InstanceHeartbeatRequest{}
	heartbeatReq.InstanceID = regResp.InstanceID
	heartbeatReq.ServiceToken = t.serviceToken
	heartbeatReq.Namespace = providerNamespace
	heartbeatReq.Service = providerService
	// heartbeatReq.Host = providerInstanceIP
	// heartbeatReq.Port = providerInstancePort
	// 进行心跳上报
	err = t.provider.Heartbeat(heartbeatReq)
	c.Assert(err, check.IsNil)

	deregisterReq := &api.InstanceDeRegisterRequest{}
	deregisterReq.ServiceToken = t.serviceToken
	deregisterReq.InstanceID = regResp.InstanceID
	// 反注册服务实例
	err = t.provider.Deregister(deregisterReq)
	c.Assert(err, check.IsNil)

	errDeregisterReq := &api.InstanceDeRegisterRequest{}
	errDeregisterReq.Namespace = providerNamespace
	errDeregisterReq.Port = providerInstancePort
	errDeregisterReq.Host = providerInstanceIP
	errDeregisterReq.Service = providerService
	err = t.provider.Deregister(errDeregisterReq)
	c.Assert(err, check.NotNil)
	fmt.Printf("Error to deregister: %v\n", err.Error())
}
