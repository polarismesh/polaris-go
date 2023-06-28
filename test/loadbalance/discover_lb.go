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

package loadbalance

import (
	"fmt"
	"log"
	"math"
	"net"
	"os"

	"github.com/golang/protobuf/ptypes/wrappers"
	"github.com/google/uuid"
	"github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"google.golang.org/grpc"
	"gopkg.in/check.v1"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/network"
	"github.com/polarismesh/polaris-go/test/mock"
	"github.com/polarismesh/polaris-go/test/util"
)

// InnerServiceLBTestingSuite 消费者API测试套
type InnerServiceLBTestingSuite struct {
	grpcServer        *grpc.Server
	grpcListener      net.Listener
	idInstanceWeights map[instanceKey]int
	idInstanceCalls   map[instanceKey]int
	mockServer        mock.NamingServer

	monitorToken string
}

// SetUpSuite 设置模拟桩服务器
func (t *InnerServiceLBTestingSuite) SetUpSuite(c *check.C) {
	grpcOptions := make([]grpc.ServerOption, 0)
	maxStreams := 100000
	grpcOptions = append(grpcOptions, grpc.MaxConcurrentStreams(uint32(maxStreams)))

	// get the grpc server wired up
	grpc.EnableTracing = true

	ipAddr := lbIPAddr
	shopPort := lbPort
	var err error
	t.grpcServer = grpc.NewServer(grpcOptions...)
	t.mockServer = mock.NewNamingServer()
	token := t.mockServer.RegisterServerService(config.ServerDiscoverService)
	t.mockServer.RegisterServerInstance(ipAddr, shopPort, config.ServerDiscoverService, token, true)

	service_manage.RegisterPolarisGRPCServer(t.grpcServer, t.mockServer)
	t.grpcListener, err = net.Listen("tcp", fmt.Sprintf("%s:%d", ipAddr, shopPort))
	if err != nil {
		log.Fatal(fmt.Sprintf("error listening appserver %v", err))
	}
	log.Printf("appserver listening on %s:%d\n", ipAddr, shopPort)
	go func() {
		if err := t.grpcServer.Serve(t.grpcListener); err != nil {
			panic(err)
		}
	}()
	t.monitorToken = t.mockServer.RegisterServerService(config.ServerMonitorService)
	waitServerReady()
}

// TearDownSuite SetUpSuite 结束测试套程序
func (t *InnerServiceLBTestingSuite) TearDownSuite(c *check.C) {
	t.grpcServer.GracefulStop()
	if util.DirExist(util.BackupDir) {
		os.RemoveAll(util.BackupDir)
	}
}

// TestConnManger 测试连接管理器
func (t *InnerServiceLBTestingSuite) TestConnManger(c *check.C) {
	service := &service_manage.Service{
		Name:      &wrappers.StringValue{Value: config.ServerMonitorService},
		Namespace: &wrappers.StringValue{Value: config.ServerNamespace},
		Token:     &wrappers.StringValue{Value: uuid.New().String()},
	}
	var Instances []*service_manage.Instance

	Instances = append(Instances, &service_manage.Instance{
		Id:        &wrappers.StringValue{Value: uuid.New().String()},
		Service:   &wrappers.StringValue{Value: config.ServerMonitorService},
		Namespace: &wrappers.StringValue{Value: config.ServerNamespace},
		Host:      &wrappers.StringValue{Value: "127.0.0.1"},
		Port:      &wrappers.UInt32Value{Value: uint32(10030 + 1)},
		Weight:    &wrappers.UInt32Value{Value: uint32(100)},
		Metadata: map[string]string{
			"protocol": "grpc",
		},
	})
	Instances = append(Instances, &service_manage.Instance{
		Id:        &wrappers.StringValue{Value: uuid.New().String()},
		Service:   &wrappers.StringValue{Value: config.ServerMonitorService},
		Namespace: &wrappers.StringValue{Value: config.ServerNamespace},
		Host:      &wrappers.StringValue{Value: "127.0.0.1"},
		Port:      &wrappers.UInt32Value{Value: uint32(10030 + 2)},
		Weight:    &wrappers.UInt32Value{Value: uint32(300)},
		Metadata: map[string]string{
			"protocol": "grpc",
		},
	})
	Instances = append(Instances, &service_manage.Instance{
		Id:        &wrappers.StringValue{Value: uuid.New().String()},
		Service:   &wrappers.StringValue{Value: config.ServerMonitorService},
		Namespace: &wrappers.StringValue{Value: config.ServerNamespace},
		Host:      &wrappers.StringValue{Value: "127.0.0.1"},
		Port:      &wrappers.UInt32Value{Value: uint32(10030 + 3)},
		Weight:    &wrappers.UInt32Value{Value: uint32(500)},
		Metadata: map[string]string{
			"protocol": "grpc",
		},
	})
	fmt.Println(Instances)
	t.mockServer.RegisterServiceInstances(service, Instances)
	cfg := config.NewDefaultConfiguration([]string{fmt.Sprintf("%s:%d", lbIPAddr, lbPort)})
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	mgr, err := network.NewConnectionManager(cfg, consumer.SDKContext().GetValueContext())
	c.Assert(err, check.IsNil)

	w100Count := 0
	w300Count := 0
	w500Count := 0

	for i := 0; i < 10000; i++ {
		_, inst, err := mgr.GetHashExpectedInstance(config.MonitorCluster, []byte(fmt.Sprintf("%d", i)))
		c.Assert(err, check.IsNil)
		weight := inst.GetWeight()
		switch weight {
		case 100:
			w100Count++
		case 300:
			w300Count++
		case 500:
			w500Count++
		}
	}
	fmt.Println(w100Count, w300Count, w500Count)
	a1 := float64(w300Count) / float64(w100Count)
	a2 := float64(w500Count) / float64(w100Count)
	fmt.Println(a1, a2)
	c.Assert(math.Abs(a1-3) < 0.8, check.Equals, true)
	c.Assert(math.Abs(a2-5) < 0.8, check.Equals, true)
}
