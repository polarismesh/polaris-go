// Package test mock test for set division
/*
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
	"github.com/polarismesh/polaris-go/test/mock"
	"github.com/polarismesh/polaris-go/test/util"
	"log"
	"net"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/golang/protobuf/ptypes/wrappers"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"gopkg.in/check.v1"
)

const (
	setDivisionServerIPAddr        = "127.0.0.1"
	setDivisionServerPort          = 8011 // 需要跟配置文件一致(sr_setdivision.yaml)
	setDivisionMonitorIPAddr       = "127.0.0.1"
	setDivisionMonitorPort         = 8012
	internalSetEnableKey           = "internal-enable-set"
	setEnable                      = "Y"
	internalSetNameKey             = "internal-set-name"
	serverService           string = "trpc.settestapp.settestserver"
	clientService           string = "trpc.settestapp.settestclient"
	// 服务默认命名空间
	setNamespace = "setnamespace"
)

var sourceInstanceMeta = map[int]map[string]string{
	0: {},
	1: {internalSetEnableKey: setEnable, internalSetNameKey: "a.b.c"},
	2: {internalSetEnableKey: setEnable, internalSetNameKey: "a.b.*"},
	3: {internalSetEnableKey: setEnable, internalSetNameKey: "b.d.c"},
	4: {internalSetEnableKey: setEnable, internalSetNameKey: "a.k.c"},
	5: {internalSetEnableKey: setEnable, internalSetNameKey: "a.b.d"},
}

var dstInstanceMeta = map[int]map[string]string{
	0: {},
	1: {internalSetEnableKey: setEnable, internalSetNameKey: "a.b.c"},
	2: {internalSetEnableKey: setEnable, internalSetNameKey: "a.b.*"},
	3: {internalSetEnableKey: setEnable, internalSetNameKey: "a.d.c"},
}

// SetDivisionTestingSuite set分组API测试套
type SetDivisionTestingSuite struct {
	grpcServer   *grpc.Server
	grpcListener net.Listener
	mockServer   mock.NamingServer
	pbServices   map[model.ServiceKey]*namingpb.Service
}

// SetUpSuite  设置模拟桩
func (t *SetDivisionTestingSuite) SetUpSuite(c *check.C) {
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
		setDivisionServerIPAddr, setDivisionServerPort, config.ServerDiscoverService, token, true)
	t.setupBaseServer(t.mockServer, sourceInstanceMeta, clientService)
	t.setupBaseServer(t.mockServer, dstInstanceMeta, serverService)
	namingpb.RegisterPolarisGRPCServer(t.grpcServer, t.mockServer)
	t.grpcListener, err = net.Listen(
		"tcp", fmt.Sprintf("%s:%d", setDivisionServerIPAddr, setDivisionServerPort))
	if nil != err {
		log.Fatal(fmt.Sprintf("error listening appserver %v", err))
	}
	log.Printf("appserver listening on %s:%d\n", setDivisionServerIPAddr, setDivisionServerPort)
	go func() {
		t.grpcServer.Serve(t.grpcListener)
	}()

}

// TearDownSuite 结束测试套程序
func (t *SetDivisionTestingSuite) TearDownSuite(c *check.C) {
	t.grpcServer.Stop()
	util.InsertLog(t, c.GetTestLog())
}

// registerService 注册服务和节点
func registerService(mockServer mock.NamingServer, serviceName string,
	namespace string, instances []*namingpb.Instance) *namingpb.Service {
	service := &namingpb.Service{
		Name:      &wrappers.StringValue{Value: serviceName},
		Namespace: &wrappers.StringValue{Value: namespace},
		Token:     &wrappers.StringValue{Value: uuid.New().String()},
	}
	mockServer.RegisterService(service)
	if len(instances) > 0 {
		mockServer.RegisterServiceInstances(service, instances)
	}
	return service
}

// 设置北极星测试实例
func (t *SetDivisionTestingSuite) setupBaseServer(mockServer mock.NamingServer,
	instanceMeta map[int]map[string]string, serviceName string) {
	allinstances := make([]*namingpb.Instance, 0)
	for index, meta := range instanceMeta {
		instance := &namingpb.Instance{
			Id:        &wrappers.StringValue{Value: uuid.New().String()},
			Service:   &wrappers.StringValue{Value: serviceName},
			Namespace: &wrappers.StringValue{Value: setNamespace},
			Host:      &wrappers.StringValue{Value: setDivisionServerIPAddr},
			Port:      &wrappers.UInt32Value{Value: uint32(10010 + index)},
			Weight:    &wrappers.UInt32Value{Value: 100},
			Healthy:   &wrappers.BoolValue{Value: true},
			Metadata:  meta,
		}

		allinstances = append(allinstances, instance)
	}
	mockServer.RegisterNamespace(&namingpb.Namespace{
		Name:    &wrappers.StringValue{Value: setNamespace},
		Comment: &wrappers.StringValue{Value: "set namespace "},
		Owners:  &wrappers.StringValue{Value: "SetDivisionRouter"},
	})
	srvKey := model.ServiceKey{
		Namespace: setNamespace,
		Service:   serviceName,
	}
	t.pbServices[srvKey] = registerService(mockServer, serviceName, setNamespace, allinstances)

}

// sourceSendRequest 设置source service 都metadata ，并发送请求
func sourceSendRequest(num int, consumer api.ConsumerAPI) (*model.InstancesResponse, error) {
	var request *api.GetInstancesRequest
	request = &api.GetInstancesRequest{}
	request.Namespace = setNamespace
	request.Service = serverService

	meta := sourceInstanceMeta[num]
	request.SourceService = &model.ServiceInfo{
		Namespace: setNamespace,
		Service:   clientService,
		Metadata:  meta,
	}

	resp, err := consumer.GetInstances(request)
	return resp, err
}

// TestSetExcatMatch 测试set刚好匹配的情况
func (t *SetDivisionTestingSuite) TestSetExcatMatch(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	cfg, err := config.LoadConfigurationByFile("testdata/sr_setdivision.yaml")
	c.Assert(err, check.IsNil)
	cfg.GetGlobal().GetStatReporter().SetChain([]string{config.DefaultServiceRouteReporter})
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	resp, err := sourceSendRequest(1, consumer)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.Instances), check.Equals, 1)
	c.Assert(resp.Instances[0].GetMetadata()[internalSetNameKey], check.Equals, "a.b.c")
	time.Sleep(2 * time.Second)
}

// TestSameGroup 测试没有本分组，匹配最后的情况
func (t *SetDivisionTestingSuite) TestSameGroup(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	cfg, err := config.LoadConfigurationByFile("testdata/sr_setdivision.yaml")
	c.Assert(err, check.IsNil)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	resp, err := sourceSendRequest(5, consumer)

	c.Assert(err, check.IsNil)
	c.Assert(len(resp.Instances), check.Equals, 1)
	c.Assert(resp.Instances[0].GetMetadata()[internalSetNameKey], check.Equals, "a.b.*")
	time.Sleep(2 * time.Second)


}

// TestNoSet 测试主调没启用set的情况
func (t *SetDivisionTestingSuite) TestNoSet(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	cfg, err := config.LoadConfigurationByFile("testdata/sr_setdivision.yaml")
	c.Assert(err, check.IsNil)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	resp, err := sourceSendRequest(0, consumer)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.Instances), check.Equals, 4)
	time.Sleep(2 * time.Second)

}

// TestDstNotSet 测试被调没启用set的情况
func (t *SetDivisionTestingSuite) TestDstNotSet(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	cfg, err := config.LoadConfigurationByFile("testdata/sr_setdivision.yaml")
	c.Assert(err, check.IsNil)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	resp, err := sourceSendRequest(3, consumer)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.Instances), check.Equals, 4)
	time.Sleep(2 * time.Second)

}

// TestAllGroup 测试主调的 set分组为*的情况
func (t *SetDivisionTestingSuite) TestAllGroup(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	cfg, err := config.LoadConfigurationByFile("testdata/sr_setdivision.yaml")
	c.Assert(err, check.IsNil)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	resp, err := sourceSendRequest(2, consumer)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.Instances) == 2, check.Equals, true)
	for _, instance := range resp.Instances {
		meta := instance.GetMetadata()
		setName := meta[internalSetNameKey]
		c.Assert(meta[internalSetEnableKey], check.Equals, setEnable)
		ok := setName == "a.b.c" || setName == "a.b.*"
		c.Assert(ok, check.Equals, true)
	}
	time.Sleep(2 * time.Second)

}

// TestSetNotMatch 启用set了，但set不匹配，返回空
func (t *SetDivisionTestingSuite) TestSetNotMatch(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	cfg, err := config.LoadConfigurationByFile("testdata/sr_setdivision.yaml")
	c.Assert(err, check.IsNil)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	resp, err := sourceSendRequest(4, consumer)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.Instances) == 0, check.Equals, true)
	time.Sleep(2 * time.Second)

}

// TestDestinationSet 测试使用了destination  set的情况
func (t *SetDivisionTestingSuite) TestDestinationSet(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	cfg, err := config.LoadConfigurationByFile("testdata/sr_setdivision.yaml")
	c.Assert(err, check.IsNil)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	var request *api.GetInstancesRequest
	request = &api.GetInstancesRequest{}
	request.Namespace = setNamespace
	request.Service = serverService

	meta := sourceInstanceMeta[1]
	request.SourceService = &model.ServiceInfo{
		Namespace: setNamespace,
		Service:   clientService,
		Metadata:  meta,
	}

	request.Metadata = dstInstanceMeta[3]
	resp, err := consumer.GetInstances(request)

	c.Assert(err, check.IsNil)
	c.Assert(len(resp.Instances), check.Equals, 1)
	c.Assert(resp.Instances[0].GetMetadata()[internalSetNameKey], check.Equals, dstInstanceMeta[3][internalSetNameKey])
	time.Sleep(2 * time.Second)

}

// GetName 套件名字
func (t *SetDivisionTestingSuite) GetName() string {
	return "SetDivisionRouting"
}
