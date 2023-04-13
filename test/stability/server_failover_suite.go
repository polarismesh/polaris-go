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

	"github.com/golang/protobuf/ptypes/wrappers"
	"google.golang.org/grpc"
	"gopkg.in/check.v1"

	"github.com/polarismesh/polaris-go/pkg/config"
	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	"github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"github.com/polarismesh/polaris-go/test/mock"
	"github.com/polarismesh/polaris-go/test/util"
)

// server失效的测试用例
type ServerFailOverSuite struct {
	builtInServer   mock.NamingServer
	discoverServers []mock.NamingServer
	grpcServers     []*grpc.Server
}

var (
	builtInPortFailOver   = 8010
	discoverPortsFailOver = []int{8011, 8012, 8013, 8014, 8015}
)

const (
	listenHostFailOver    = "127.0.0.1"
	namespaceFailOver     = "FailOverNamespace"
	svcNameFailOver       = "testService_%d"
	svcCountFailOver      = 5
	instanceCountFailOver = 10
)

// SetUpSuite 启动测试套程序
func (t *ServerFailOverSuite) SetUpSuite(c *check.C) {
	util.DeleteDir(util.BackupDir)
	// get the grpc server wired up
	grpc.EnableTracing = true

	for _, port := range discoverPortsFailOver {
		grpcServer, mockServer := createListenMockServer(port)
		mockServer.RegisterNamespace(&apimodel.Namespace{
			Name: &wrappers.StringValue{
				Value: namespaceFailOver,
			},
		})
		for i := 0; i < svcCountFailOver; i++ {
			svc := &service_manage.Service{
				Namespace: &wrappers.StringValue{
					Value: namespaceFailOver,
				},
				Name: &wrappers.StringValue{
					Value: fmt.Sprintf(svcNameFailOver, i),
				},
			}
			mockServer.RegisterService(svc)
			mockServer.GenTestInstances(svc, instanceCountFailOver)
		}
		t.grpcServers = append(t.grpcServers, grpcServer)
		t.discoverServers = append(t.discoverServers, mockServer)
	}
	grpcServer, mockServer := createListenMockServer(builtInPortFailOver)
	t.builtInServer = mockServer
	t.grpcServers = append(t.grpcServers, grpcServer)
}

// 创建并监听mock服务端
func createListenMockServer(port int) (*grpc.Server, mock.NamingServer) {
	grpcOptions := make([]grpc.ServerOption, 0)
	maxStreams := 100000
	grpcOptions = append(grpcOptions, grpc.MaxConcurrentStreams(uint32(maxStreams)))
	grpcServer := grpc.NewServer(grpcOptions...)
	mockServer := mock.NewNamingServer()
	token := mockServer.RegisterServerService(config.ServerDiscoverService)
	for _, port := range discoverPortsFailOver {
		mockServer.RegisterServerInstance(listenHostFailOver, port, config.ServerDiscoverService, token, true)
	}
	service_manage.RegisterPolarisGRPCServer(grpcServer, mockServer)
	grpcListener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", listenHostFailOver, port))
	if err != nil {
		log.Fatal(fmt.Sprintf("error listening appserver %v", err))
	}
	log.Printf("appserver listening on %s:%d\n", listenHostFailOver, port)
	go func() {
		grpcServer.Serve(grpcListener)
	}()
	return grpcServer, mockServer
}

// SetUpSuite 启动测试套程序
func (t *ServerFailOverSuite) TearDownSuite(c *check.C) {
	for _, grpcServer := range t.grpcServers {
		grpcServer.Stop()
	}
	if util.DirExist(util.BackupDir) {
		os.RemoveAll(util.BackupDir)
	}
	util.InsertLog(t, c.GetTestLog())
}

// 获取用例名
func (t *ServerFailOverSuite) GetName() string {
	return "ServerFailOverSuite"
}

// 设置是否让服务发现server超时
func (t *ServerFailOverSuite) makeDiscoverTimeout(count int, value bool) {
	for i, server := range t.discoverServers {
		if i >= count {
			break
		}
		server.MakeForceOperationTimeout(mock.OperationDiscoverInstance, value)
		server.MakeForceOperationTimeout(mock.OperationDiscoverRouting, value)
	}
}

// 测试节点部分失效的场景
// unstable case
// func (t *ServerFailOverSuite) TestDiscoverFailOver(c *check.C) {
//	log.Printf("start to TestDefaultFailOver")
//	defer util.DeleteDir(util.BackupDir)
//
//	cfg := config.NewDefaultConfiguration(
//		[]string{fmt.Sprintf("%s:%d", listenHostFailOver, builtInPortFailOver)})
//	cfg.GetGlobal().GetStatReporter().SetEnable(false)
//	consumer, err := api.NewConsumerAPIByConfig(cfg)
//	c.Assert(err, check.IsNil)
//	defer consumer.Destroy()
//	time.Sleep(3 * time.Second)
//	t.makeDiscoverTimeout(len(t.discoverServers), true)
//	wg := &sync.WaitGroup{}
//	wg.Add(svcCountFailOver)
//	for i := 0; i < svcCountFailOver; i++ {
//		go func(idx int) {
//			defer wg.Done()
//			request := &api.GetOneInstanceRequest{}
//			request.Namespace = namespaceFailOver
//			request.Service = fmt.Sprintf(svcNameFailOver, idx)
//			_, err = consumer.GetOneInstance(request)
//			c.Assert(err, check.IsNil)
//		}(i)
//	}
//	time.Sleep(2 * time.Second)
//	t.makeDiscoverTimeout(len(t.discoverServers) - 2, false)
//	wg.Wait()
// }
