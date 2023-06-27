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

package circuitbreak

import (
	"fmt"
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
	commontest "github.com/polarismesh/polaris-go/test/common"
	"github.com/polarismesh/polaris-go/test/mock"
	"github.com/polarismesh/polaris-go/test/util"
)

const (
	// 测试的默认命名空间
	checkAlwaysNamespace = "testCheckAlways"
	// 测试的默认服务名
	checkAlwaysService = "svcAlways"
	// 测试服务器的默认地址
	checkAlwaysIPAdress = "127.0.0.1"
	// 测试服务器的端口
	checkAlwaysPort = commontest.HealthCheckAlwaysSuitServerPort
	// 所有实例数量
	instanceTotal = 5
)

// HealthCheckAlwaysTestingSuite 消费者API测试套
type HealthCheckAlwaysTestingSuite struct {
	grpcServer   *grpc.Server
	grpcListener net.Listener
	serviceToken string
}

// GetName 套件名字
func (t *HealthCheckAlwaysTestingSuite) GetName() string {
	return "HealthCheckAlwaysSuite"
}

// SetUpSuite 启动测试套程序
func (t *HealthCheckAlwaysTestingSuite) SetUpSuite(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	grpcOptions := make([]grpc.ServerOption, 0)
	maxStreams := 100000
	grpcOptions = append(grpcOptions, grpc.MaxConcurrentStreams(uint32(maxStreams)))

	// get the grpc server wired up
	grpc.EnableTracing = true

	ipAddr := checkAlwaysIPAdress
	shopPort := checkAlwaysPort
	var err error
	t.grpcServer = grpc.NewServer(grpcOptions...)
	t.serviceToken = uuid.New().String()
	mockServer := mock.NewNamingServer()
	token := mockServer.RegisterServerService(config.ServerDiscoverService)
	mockServer.RegisterServerInstance(ipAddr, shopPort, config.ServerDiscoverService, token, true)
	mockServer.RegisterNamespace(&apimodel.Namespace{
		Name:    &wrappers.StringValue{Value: checkAlwaysNamespace},
		Comment: &wrappers.StringValue{Value: "for healthCheck api test"},
		Owners:  &wrappers.StringValue{Value: "healthCheck"},
	})
	testService := &service_manage.Service{
		Name:      &wrappers.StringValue{Value: checkAlwaysService},
		Namespace: &wrappers.StringValue{Value: checkAlwaysNamespace},
		Token:     &wrappers.StringValue{Value: t.serviceToken},
	}
	mockServer.RegisterService(testService)
	mockServer.GenTestInstancesWithHostPort(testService, instanceTotal, "127.0.0.1", 1024)
	service_manage.RegisterPolarisGRPCServer(t.grpcServer, mockServer)
	t.grpcListener, err = net.Listen("tcp", fmt.Sprintf("%s:%d", ipAddr, shopPort))
	if err != nil {
		_ = util.DeleteDir(util.BackupDir)
		log.Fatal(fmt.Sprintf("error listening appserver %v", err))
	}
	log.Printf("appserver listening on %s:%d\n", ipAddr, shopPort)
	go func() {
		t.grpcServer.Serve(t.grpcListener)
	}()
}

// TearDownSuite 结束测试套程序
func (t *HealthCheckAlwaysTestingSuite) TearDownSuite(c *check.C) {
	t.grpcServer.Stop()
	if util.DirExist(util.BackupDir) {
		os.RemoveAll(util.BackupDir)
	}
	util.InsertLog(t, c.GetTestLog())
}

const (
	healthPort1025 = 1025

	healthPort1030 = 1027
)

// TestHttpDetectAlways 测试持久化探测
func (t *HealthCheckAlwaysTestingSuite) TestHttpDetectAlways(c *check.C) {
	healthPorts := []int{healthPort1025, healthPort1030}
	for _, port := range healthPorts {
		go func(hport int) {
			// 起tcp服务
			address := fmt.Sprintf("%s:%d", "127.0.0.1", hport)
			log.Printf("Start  Server:%s", address)
			err := startHTTPServer(address, 10, 200)
			c.Assert(err, check.IsNil)
		}(port)
	}
	cfg, err := config.LoadConfigurationByFile("testdata/healthcheck.yaml")
	c.Assert(err, check.IsNil)
	cfg.GetConsumer().GetHealthCheck().SetChain([]string{"http"})
	cfg.GetConsumer().GetHealthCheck().SetWhen(config.HealthCheckAlways)
	cfg.GetConsumer().GetHealthCheck().SetConcurrency(10)
	sdkCtx, err := api.InitContextByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer sdkCtx.Destroy()
	consumerAPI := api.NewConsumerAPIByContext(sdkCtx)
	// 随机获取一个实例，并将这个实例作为熔断的目标
	request := &api.GetInstancesRequest{}
	request.Namespace = checkAlwaysNamespace
	request.Service = checkAlwaysService
	var resp *model.InstancesResponse
	resp, err = consumerAPI.GetInstances(request)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.Instances), check.Equals, instanceTotal)
	time.Sleep(10 * time.Second)
	resp, err = consumerAPI.GetInstances(request)
	c.Assert(err, check.IsNil)
	// for _, instance := range resp.Instances {
	//	fmt.Printf("instance :%d, value is %v\n", instance.GetPort(), instance.GetCircuitBreakerStatus())
	// }
	c.Assert(len(resp.Instances), check.Equals, len(healthPorts))
}
