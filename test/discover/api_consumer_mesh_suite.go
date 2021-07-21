/**
 * Tencent is pleased to support the open source community by making CL5 available.
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
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/test/mock"
	"github.com/polarismesh/polaris-go/test/util"
	"github.com/golang/protobuf/ptypes/wrappers"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"gopkg.in/check.v1"
	"log"
	"net"
	"time"
)

//ConsumerMeshTestingSuite 消费者API测试套
type ConsumerMeshTestingSuite struct {
	mockServer   mock.NamingServer
	grpcServer   *grpc.Server
	grpcListener net.Listener
	serviceToken string
	testService  *namingpb.Service
}

//套件名字
func (t *ConsumerMeshTestingSuite) GetName() string {
	return "Consumer"
}

//SetUpSuite 启动测试套程序
func (t *ConsumerMeshTestingSuite) SetUpSuite(c *check.C) {
	grpcOptions := make([]grpc.ServerOption, 0)
	maxStreams := 100000
	grpcOptions = append(grpcOptions, grpc.MaxConcurrentStreams(uint32(maxStreams)))

	log.Printf("ConsumerMeshTestingSuite SetUpSuite")

	// get the grpc server wired up
	grpc.EnableTracing = true

	ipAddr := consumerIPAddress
	shopPort := consumerPort
	var err error
	t.grpcServer = grpc.NewServer(grpcOptions...)
	t.serviceToken = uuid.New().String()
	t.mockServer = mock.NewNamingServer()
	token := t.mockServer.RegisterServerService(config.ServerDiscoverService)
	t.mockServer.RegisterServerInstance(ipAddr, shopPort, config.ServerDiscoverService, token, true)
	t.mockServer.RegisterNamespace(&namingpb.Namespace{
		Name:    &wrappers.StringValue{Value: consumerNamespace},
		Comment: &wrappers.StringValue{Value: "for consumer api test"},
		Owners:  &wrappers.StringValue{Value: "ConsumerAPI"},
	})
	t.mockServer.RegisterServerServices(ipAddr, shopPort)
	t.testService = &namingpb.Service{
		Name:      &wrappers.StringValue{Value: consumerService},
		Namespace: &wrappers.StringValue{Value: consumerNamespace},
		Token:     &wrappers.StringValue{Value: t.serviceToken},
	}
	t.mockServer.RegisterService(t.testService)
	t.mockServer.GenTestInstances(t.testService, normalInstances)
	t.mockServer.GenInstancesWithStatus(t.testService, isolatedInstances, mock.IsolatedStatus, 2048)
	t.mockServer.GenInstancesWithStatus(t.testService, unhealthyInstances, mock.UnhealthyStatus, 4096)

	namingpb.RegisterPolarisGRPCServer(t.grpcServer, t.mockServer)
	t.grpcListener, err = net.Listen("tcp", fmt.Sprintf("%s:%d", ipAddr, shopPort))
	if nil != err {
		log.Fatal(fmt.Sprintf("error listening appserver %v", err))
	}
	log.Printf("appserver listening on %s:%d\n", ipAddr, shopPort)
	go func() {
		t.grpcServer.Serve(t.grpcListener)
	}()
}

//SetUpSuite 结束测试套程序
func (t *ConsumerMeshTestingSuite) TearDownSuite(c *check.C) {
	t.grpcServer.Stop()
	util.InsertLog(t, c.GetTestLog())
}

//测试获取批量服务
func (t *ConsumerMeshTestingSuite) TestGetServices(c *check.C) {
	log.Printf("Start TestGetServices")
	defer util.DeleteDir(util.BackupDir)
	t.runWithMockTimeout(false, func() {
		sdkContext, err := api.InitContextByFile("testdata/consumer.yaml")
		c.Assert(err, check.IsNil)
		consumer := api.NewConsumerAPIByContext(sdkContext)
		defer consumer.Destroy()
		time.Sleep(2 * time.Second)

		testbus := "ExistBusiness"

		//目标服务
		serviceToken1 := uuid.New().String()
		testService1 := &namingpb.Service{
			Name:      &wrappers.StringValue{Value: testbus + "2222/" + api.MeshVirtualService},
			Namespace: &wrappers.StringValue{Value: consumerNamespace},
			Token:     &wrappers.StringValue{Value: serviceToken1},
			Business:  &wrappers.StringValue{Value: testbus},
		}
		t.mockServer.RegisterService(testService1)

		//辅助服务
		serviceToken2 := uuid.New().String()
		testService2 := &namingpb.Service{
			Name:      &wrappers.StringValue{Value: "BUSINESS/" + testbus},
			Namespace: &wrappers.StringValue{Value: consumerNamespace},
			Token:     &wrappers.StringValue{Value: serviceToken2},
			//Business: &wrappers.StringValue{Value: testbus},
		}
		t.mockServer.RegisterService(testService2)

		request := &api.GetServicesRequest{}
		request.FlowID = 1111
		request.Namespace = consumerNamespace
		request.Business = testbus
		request.EnableBusiness = true

		startTime := time.Now()

		resp, err := consumer.GetServicesByBusiness(request)
		endTime := time.Now()
		consumeTime := endTime.Sub(startTime)
		fmt.Printf("time consume is %v\n", consumeTime)
		if nil != err {
			fmt.Printf("err recv is %v\n", err)
		}
		c.Assert(err, check.IsNil)
		servicesRecived := resp.GetValue().([]*namingpb.Service)
		c.Assert(len(servicesRecived), check.Equals, 1)
		log.Printf("TestGetServices done", resp, len(servicesRecived), servicesRecived)

		//add one
		serviceToken1 = uuid.New().String()
		testService1 = &namingpb.Service{
			Name:      &wrappers.StringValue{Value: testbus + "2222/" + api.MeshVirtualService},
			Namespace: &wrappers.StringValue{Value: consumerNamespace},
			Token:     &wrappers.StringValue{Value: serviceToken1},
			Business:  &wrappers.StringValue{Value: testbus},
		}
		t.mockServer.RegisterService(testService1)

		time.Sleep(4 * time.Second)
		resp, err = consumer.GetServicesByBusiness(request)
		if nil != err {
			fmt.Printf("err recv is %v\n", err)
		}
		c.Assert(err, check.IsNil)
		servicesRecived = resp.GetValue().([]*namingpb.Service)
		c.Assert(len(servicesRecived), check.Equals, 2)
		log.Printf("TestGetServices done", resp, len(servicesRecived))

		time.Sleep(2 * time.Second)
	})
}

//测试获取网格数据
func (t *ConsumerMeshTestingSuite) TestGetMesh(c *check.C) {
	log.Printf("Start TestGetMesh")
	meshname := "mesh001"
	//service
	serviceToken1 := uuid.New().String()
	testService1 := &namingpb.Service{
		Name:      &wrappers.StringValue{Value: meshname},
		Namespace: &wrappers.StringValue{Value: ""},
		Token:     &wrappers.StringValue{Value: serviceToken1},
	}
	t.mockServer.RegisterService(testService1)
	//mesh
	t.mockServer.RegisterMesh(&namingpb.Service{
		//Namespace: &wrappers.StringValue{Value: "mesh"},
		Namespace: &wrappers.StringValue{Value: ""},
	}, "", &namingpb.Mesh{
		Id:     &wrappers.StringValue{Value: meshname},
		Owners: &wrappers.StringValue{Value: "bilinhe"},
		Services: []*namingpb.MeshService{
			{
				MeshName:  &wrappers.StringValue{Value: meshname},
				Service:   &wrappers.StringValue{Value: "n"},
				Namespace: &wrappers.StringValue{Value: "space"},
			},
		},
	})
	//request
	mreq := &api.GetMeshRequest{}
	//mreq.Namespace = "mesh"
	//mreq.Namespace = ""
	mreq.MeshId = meshname

	sdkContext, err := api.InitContextByFile("testdata/consumer.yaml")
	c.Assert(err, check.IsNil)
	consumer := api.NewConsumerAPIByContext(sdkContext)
	defer consumer.Destroy()
	time.Sleep(2 * time.Second)
	resp, err := consumer.GetMesh(mreq)
	log.Printf("======>", resp, resp.Value.(*namingpb.Mesh).Owners)

	//
	serviceToken11 := uuid.New().String()
	testService11 := &namingpb.Service{
		Name:      &wrappers.StringValue{Value: meshname},
		Namespace: &wrappers.StringValue{Value: "d"},
		Token:     &wrappers.StringValue{Value: serviceToken11},
	}
	t.mockServer.RegisterService(testService11)
	req := &api.GetInstancesRequest{}
	req.Namespace = "dd"
	req.Service = meshname
	resp2, err2 := consumer.GetInstances(req)
	log.Printf("instances:   ", resp2, err2)
}

//测试获取网格规则
func (t *ConsumerMeshTestingSuite) TestGetMeshConfig(c *check.C) {
	log.Printf("Start TestGetMeshConfig")
	t.testGetMeshConfig(c, false, true, true)

}

//测试获取不存在的网格规则
func (t *ConsumerMeshTestingSuite) TestGetMeshConfigNotExist(c *check.C) {
	log.Printf("Start TestGetMeshConfigNotExist")
	t.testGetMeshConfig(c, false, false, true)
}

//测试获取类型不匹配的网格规则
func (t *ConsumerMeshTestingSuite) TestGetMeshConfigTypeNotMatch(c *check.C) {
	log.Printf("Start TestGetMeshConfigTypeNotMatch")
	t.testGetMeshConfig(c, false, true, false)
}

//在mockTimeout宏中，执行测试逻辑
func (t *ConsumerMeshTestingSuite) runWithMockTimeout(mockTimeout bool, handle func()) {
	t.mockServer.MakeOperationTimeout(mock.OperationDiscoverInstance, mockTimeout)
	t.mockServer.MakeOperationTimeout(mock.OperationDiscoverRouting, mockTimeout)
	defer func() {
		defer t.mockServer.MakeOperationTimeout(mock.OperationDiscoverInstance, false)
		defer t.mockServer.MakeOperationTimeout(mock.OperationDiscoverRouting, false)
	}()
	handle()
}

////测试获取多个服务实例
func (t *ConsumerMeshTestingSuite) testGetMeshConfig(c *check.C, mockTimeout bool, exist bool, typematch bool) {
	defer util.DeleteDir(util.BackupDir)
	t.runWithMockTimeout(mockTimeout, func() {
		sdkContext, err := api.InitContextByFile("testdata/consumer.yaml")
		c.Assert(err, check.IsNil)
		consumer := api.NewConsumerAPIByContext(sdkContext)
		defer consumer.Destroy()
		time.Sleep(2 * time.Second)

		testbus := "ExistBusiness"

		//添加server网格规则
		t.mockServer.RegisterMeshConfig(&namingpb.Service{
			Namespace: &wrappers.StringValue{Value: consumerNamespace},
			Business:  &wrappers.StringValue{Value: testbus},
		}, model.MeshVirtualService,
			&namingpb.MeshConfig{
				MeshId:   &wrappers.StringValue{Value: testbus},
				Revision: &wrappers.StringValue{Value: time.Now().String()},
				Resources: []*namingpb.MeshResource{
					{
						TypeUrl:  &wrappers.StringValue{Value: api.MeshVirtualService},
						Revision: &wrappers.StringValue{Value: time.Now().String()},
						Body:     &wrappers.StringValue{Value: mock.VS},
					},
				},
			})
		//
		serviceToken := uuid.New().String()
		testService := &namingpb.Service{
			Name:      &wrappers.StringValue{Value: model.MeshPrefix + model.MeshKeySpliter + testbus + model.MeshKeySpliter + api.MeshVirtualService},
			Namespace: &wrappers.StringValue{Value: consumerNamespace},
			Token:     &wrappers.StringValue{Value: serviceToken},
		}
		t.mockServer.RegisterService(testService)

		request := &api.GetMeshConfigRequest{}
		request.FlowID = 1111
		request.Namespace = consumerNamespace
		if exist {
			request.MeshId = testbus
		} else {
			request.MeshId = "NotExistBusiness"
		}
		if typematch {
			request.MeshType = api.MeshVirtualService
		} else {
			request.MeshType = api.MeshDestinationRule
		}

		startTime := time.Now()

		for i := 0; i < 7; i++ {
			if i == 3 {
				time.Sleep(4 * time.Second)
			}
			resp, err := consumer.GetMeshConfig(request)
			endTime := time.Now()
			consumeTime := endTime.Sub(startTime)
			fmt.Printf("time consume is %v\n", consumeTime)
			if nil != err {
				fmt.Printf("err recv is %v\n", err)
			}
			if exist && typematch {
				c.Assert(err, check.IsNil)
				c.Assert(resp.GetValue().(*namingpb.MeshConfig).Resources[0].TypeUrl.GetValue(), check.Equals, api.MeshVirtualService)
				log.Printf("Mesh Revision", resp.GetValue().(*namingpb.MeshConfig).Resources[0].GetRevision().GetValue(), resp.Revision)
				log.Printf("mesh Resources", resp.GetValue().(*namingpb.MeshConfig).Resources[0].Body)
			} else {
				c.Assert(err, check.NotNil)
			}
		}

		//log.Printf("GetMeshConfig done", resp, resp.GetValue().(*namingpb.MeshConfig).Resources[0])
		log.Printf("GetMeshConfig done")

		time.Sleep(2 * time.Second)
	})
}
