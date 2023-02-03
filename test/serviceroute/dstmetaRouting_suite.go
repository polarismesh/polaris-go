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
	"log"
	"net"

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
	// 测试的默认命名空间
	dstMetaNamespace = "dstMetaNs"
	// 测试的默认服务名
	dstMetaService = "dstMetaSvc"
	// 测试服务器的默认地址
	dstMetaIPAddress = "127.0.0.1"
	// 测试服务器的端口
	dstMetaPort = 8118
	// 测试monitor的默认地址
	dstMetaMonitorAddress = "127.0.0.1"
	// 测试monitor的端口
	dstMetaMonitorPort = 8119
)

const (
	// 带元数据的实例数
	addMetaCount = 2
	// 直接过滤的实例数
	normalInstances = 3
)

const (
	// 测试直接过滤的KEY
	addMetaKey = "env"
	// 测试直接过滤的Value
	addMetaValue = "test"
	// 错误的直接过滤的Value
	wrongAddMetaValue = "test1"
)

// 元数据过滤路由插件测试用例
type DstMetaTestingSuite struct {
	mockServer   mock.NamingServer
	grpcServer   *grpc.Server
	grpcListener net.Listener
	serviceToken string
	testService  *namingpb.Service
}

// 套件名字
func (t *DstMetaTestingSuite) GetName() string {
	return "DstMetaTestingSuite"
}

// SetUpSuite 启动测试套程序
func (t *DstMetaTestingSuite) SetUpSuite(c *check.C) {
	grpcOptions := make([]grpc.ServerOption, 0)
	maxStreams := 100000
	grpcOptions = append(grpcOptions, grpc.MaxConcurrentStreams(uint32(maxStreams)))

	// get the grpc server wired up
	grpc.EnableTracing = true

	ipAddr := dstMetaIPAddress
	shopPort := dstMetaPort
	var err error
	t.grpcServer = grpc.NewServer(grpcOptions...)
	t.serviceToken = uuid.New().String()
	t.mockServer = mock.NewNamingServer()
	token := t.mockServer.RegisterServerService(config.ServerDiscoverService)
	t.mockServer.RegisterServerInstance(ipAddr, shopPort, config.ServerDiscoverService, token, true)
	t.mockServer.RegisterNamespace(&namingpb.Namespace{
		Name:    &wrappers.StringValue{Value: dstMetaNamespace},
		Comment: &wrappers.StringValue{Value: "for consumer api test"},
		Owners:  &wrappers.StringValue{Value: "ConsumerAPI"},
	})
	t.mockServer.RegisterServerServices(ipAddr, shopPort)
	t.testService = &namingpb.Service{
		Name:      &wrappers.StringValue{Value: dstMetaService},
		Namespace: &wrappers.StringValue{Value: dstMetaNamespace},
		Token:     &wrappers.StringValue{Value: t.serviceToken},
	}
	t.mockServer.RegisterService(t.testService)
	t.mockServer.GenTestInstances(t.testService, normalInstances)
	t.mockServer.GenTestInstancesWithMeta(t.testService, addMetaCount, map[string]string{addMetaKey: addMetaValue})

	namingpb.RegisterPolarisGRPCServer(t.grpcServer, t.mockServer)
	t.grpcListener, err = net.Listen("tcp", fmt.Sprintf("%s:%d", ipAddr, shopPort))
	if err != nil {
		log.Fatalf("error listening appserver %v", err)
	}
	log.Printf("appserver listening on %s:%d\n", ipAddr, shopPort)
	go func() {
		t.grpcServer.Serve(t.grpcListener)
	}()

}

// SetUpSuite 结束测试套程序
func (t *DstMetaTestingSuite) TearDownSuite(c *check.C) {
	t.grpcServer.Stop()
	util.InsertLog(t, c.GetTestLog())
}

// 测试正常获取元数据实例
func (t *DstMetaTestingSuite) TestGetMetaNormal(c *check.C) {
	cfg := config.NewDefaultConfiguration(
		[]string{fmt.Sprintf("%s:%d", dstMetaIPAddress, dstMetaPort)})
	cfg.GetConsumer().GetServiceRouter().SetChain([]string{config.DefaultServiceRouterDstMeta})
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()
	request := &api.GetInstancesRequest{}
	request.Namespace = dstMetaNamespace
	request.Service = dstMetaService
	request.Metadata = map[string]string{
		addMetaKey: addMetaValue,
	}
	resp, err := consumer.GetInstances(request)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.Instances), check.Equals, addMetaCount)
}

// 测试正常获取元数据实例
func (t *DstMetaTestingSuite) TestGetMetaWrong(c *check.C) {
	cfg := config.NewDefaultConfiguration(
		[]string{fmt.Sprintf("%s:%d", dstMetaIPAddress, dstMetaPort)})
	cfg.GetConsumer().GetServiceRouter().SetChain([]string{config.DefaultServiceRouterDstMeta})
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	c.Assert(consumer, check.NotNil)
	defer consumer.Destroy()
	request := &api.GetInstancesRequest{}
	request.Namespace = dstMetaNamespace
	request.Service = dstMetaService
	request.Metadata = map[string]string{
		addMetaKey: wrongAddMetaValue,
	}
	_, err = consumer.GetInstances(request)
	c.Assert(err, check.NotNil)
	c.Assert(err.(model.SDKError).ErrorCode(), check.Equals, model.ErrCodeDstMetaMismatch)
}

// 测试元数据路由兜底策略正确设置类型
func (t *DstMetaTestingSuite) TestFailOverDefaultMetaWrongWithType(c *check.C) {
	consumer, err := t.buildFaultOverConsumer()
	c.Assert(err, check.IsNil)
	// 没有设置type
	request := t.buildFaultOverDefaultInstancesRequest()
	_, err = consumer.GetOneInstance(request)
	c.Assert(err, check.NotNil)
	c.Assert(err.(model.SDKError).ErrorCode(), check.Equals, model.ErrCodeAPIInvalidArgument)
}

// 测试元数据路由兜底策略：通配所有可用ip实例
func (t *DstMetaTestingSuite) TestFailOverDefaultMetaNormalWithGetOneHealth(c *check.C) {
	consumer, err := t.buildFaultOverConsumer()
	c.Assert(err, check.IsNil)
	request := t.buildFaultOverDefaultInstancesRequest()
	request.FailOverDefaultMeta.Type = model.GetOneHealth
	resp, err := consumer.GetOneInstance(request)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.Instances), check.Equals, 1)
}

// 测试元数据路由兜底策略：匹配不带 metaData key路由
func (t *DstMetaTestingSuite) TestFailOverDefaultMetaNormalWithNotContainMeta(c *check.C) {
	consumer, err := t.buildFaultOverConsumer()
	c.Assert(err, check.IsNil)
	request := t.buildFaultOverDefaultInstancesRequest()
	request.FailOverDefaultMeta.Type = model.NotContainMetaKey
	resp, err := consumer.GetOneInstance(request)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.Instances), check.Equals, 1)
}

// 测试元数据路由兜底策略：自定义meta
func (t *DstMetaTestingSuite) TestFailOverDefaultMetaNormalWithCustomMeta(c *check.C) {
	consumer, err := t.buildFaultOverConsumer()
	c.Assert(err, check.IsNil)
	request := t.buildFaultOverDefaultInstancesRequest()
	request.FailOverDefaultMeta.Type = model.CustomMeta
	request.FailOverDefaultMeta.Meta = map[string]string{
		addMetaKey: addMetaValue,
	}
	resp, err := consumer.GetOneInstance(request)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.Instances), check.Equals, 1)
	c.Assert(resp.Instances[0].GetMetadata()[addMetaKey], check.Equals, addMetaValue)
}

// 测试元数据路由兜底策略：自定义meta
func (t *DstMetaTestingSuite) TestFailOverDefaultMetaWrongWithCustomMeta01(c *check.C) {
	consumer, err := t.buildFaultOverConsumer()
	c.Assert(err, check.IsNil)
	request := t.buildFaultOverDefaultInstancesRequest()
	// 没有设置自定义meta
	request.FailOverDefaultMeta.Type = model.CustomMeta
	_, err = consumer.GetOneInstance(request)
	c.Assert(err, check.NotNil)
	c.Assert(err.(model.SDKError).ErrorCode(), check.Equals, model.ErrCodeAPIInvalidArgument)
}

// 测试元数据路由兜底策略：自定义meta
func (t *DstMetaTestingSuite) TestFailOverDefaultMetaWrongWithCustomMeta02(c *check.C) {
	consumer, err := t.buildFaultOverConsumer()
	c.Assert(err, check.IsNil)
	request := t.buildFaultOverDefaultInstancesRequest()
	// 设置错误的自定义meta依旧找不到
	request.FailOverDefaultMeta.Type = model.CustomMeta
	request.FailOverDefaultMeta.Meta = map[string]string{
		addMetaKey: wrongAddMetaValue,
	}
	_, err = consumer.GetOneInstance(request)
	c.Assert(err, check.NotNil)
	c.Assert(err.(model.SDKError).ErrorCode(), check.Equals, model.ErrCodeDstMetaMismatch)
}

func (t *DstMetaTestingSuite) buildFaultOverConsumer() (api.ConsumerAPI, error) {
	cfg := config.NewDefaultConfiguration(
		[]string{fmt.Sprintf("%s:%d", dstMetaIPAddress, dstMetaPort)})
	cfg.GetConsumer().GetServiceRouter().SetChain([]string{config.DefaultServiceRouterDstMeta})
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	return consumer, err
}

func (t *DstMetaTestingSuite) buildFaultOverDefaultInstancesRequest() *api.GetOneInstanceRequest {
	request := &api.GetOneInstanceRequest{}
	request.Namespace = dstMetaNamespace
	request.Service = dstMetaService
	request.Metadata = map[string]string{
		addMetaKey: wrongAddMetaValue,
	}
	request.EnableFailOverDefaultMeta = true
	return request
}
