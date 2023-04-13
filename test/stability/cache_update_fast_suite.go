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
	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	"github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"google.golang.org/grpc"
	"gopkg.in/check.v1"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/localregistry"
	"github.com/polarismesh/polaris-go/test/mock"
	"github.com/polarismesh/polaris-go/test/util"
)

const (
	mockServerHost       = "127.0.0.1:1949"
	falseMockeServerHost = "127.0.0.1:1950"
	testCacheNs          = "Test"
)

var testCacheSvcs = []string{"cacheUpdate1", "cacheUpdate2", "cacheUpdate3", "cacheUpdate4"}

var testCacheTokens = []string{"abf588ceee5b48bbb68ba69ef5f5347e", "5039fdecf0d54def9317fa30c01a5e05",
	"4591c171bed64672bf703613a2336bf2", "47812d435e294c7e937be901979867d6"}

var newCacheInstNums = []int{6, 10, 8, 5}

var testServices = make([]*service_manage.Service, 4, 4)

// CacheFastUpdateSuite 缓存持久化测试套件
type CacheFastUpdateSuite struct {
	grpcServer           *grpc.Server
	grpcListener         net.Listener
	mockServer           mock.NamingServer
	discoverInstances    []model.Instance
	healthCheckInstances []model.Instance
}

// SetUpSuite 初始化测试套件
func (t *CacheFastUpdateSuite) SetUpSuite(c *check.C) {
	util.DeleteDir(util.BackupDir)

	var err error

	grpcOptions := make([]grpc.ServerOption, 0)
	maxStreams := 100000
	grpcOptions = append(grpcOptions, grpc.MaxConcurrentStreams(uint32(maxStreams)))
	grpc.EnableTracing = true

	t.grpcServer = grpc.NewServer(grpcOptions...)
	t.mockServer = mock.NewNamingServer()
	t.mockServer.RegisterServerServices("127.0.0.1", 1949)
	t.mockServer.RegisterNamespace(&apimodel.Namespace{
		Name:    &wrappers.StringValue{Value: testCacheNs},
		Comment: &wrappers.StringValue{Value: "for cache update test"},
		Owners:  &wrappers.StringValue{Value: "CacheUpdater"},
	})

	for i := 0; i < 4; i++ {
		testServices[i] = &service_manage.Service{
			Name:      &wrappers.StringValue{Value: testCacheSvcs[i]},
			Namespace: &wrappers.StringValue{Value: testCacheNs},
			Token:     &wrappers.StringValue{Value: testCacheTokens[i]},
		}
		t.mockServer.RegisterService(testServices[i])
		t.mockServer.GenTestInstances(testServices[i], newCacheInstNums[i])
	}

	service_manage.RegisterPolarisGRPCServer(t.grpcServer, t.mockServer)

	t.grpcListener, err = net.Listen("tcp", mockServerHost)
	if err != nil {
		log.Fatal(fmt.Sprintf("error listening appserver %v", err))
	}
	log.Printf("appserver listening on %s\n", mockServerHost)
	go func() {
		t.grpcServer.Serve(t.grpcListener)
	}()
}

// 测试套件名字
func (t *CacheFastUpdateSuite) GetName() string {
	return "Cache"
}

// 销毁套件
func (t *CacheFastUpdateSuite) TearDownSuite(c *check.C) {
	t.grpcServer.Stop()
	if util.DirExist(util.BackupDir) {
		os.RemoveAll(util.BackupDir)
	}
	util.InsertLog(t, c.GetTestLog())
}

// 测试当可以正常连接埋点server的时候，缓存是否按照预想更新
func (t *CacheFastUpdateSuite) TestCacheUpdateFast(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	log.Printf("start to TestCacheUpdateFast")
	// 复制用于测试的缓存文件
	err := util.CopyDir("testdata/test_cache/cacheFastUpdate", util.BackupDir)
	c.Assert(err, check.IsNil)
	// 顺便测试系统服务是否会更新
	err = util.CopyDir("testdata/test_cache/systemFastUpdate", util.BackupDir)
	c.Assert(err, check.IsNil)
	t.testCacheUpdate(c, false)
}

// 测试当不能正常连接埋点server的时候，是否能降级返回从缓存文件加载的服务信息
func (t *CacheFastUpdateSuite) TestCacheUpdateDegrade(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	log.Printf("start to TestCacheUpdateDegrade")
	// 复制用于测试的缓存文件
	err := util.CopyDir("testdata/test_cache/cacheFastUpdate", util.BackupDir)
	c.Assert(err, check.IsNil)
	t.testCacheUpdate(c, true)
}

// 检测从缓存中加载的服务是否会被快速更新
func (t *CacheFastUpdateSuite) testCacheUpdate(c *check.C, failToUpdate bool) {
	var configuration *config.ConfigurationImpl
	// 当failToupdate表示是否连接正确的mockserver，为false时连接正确的，true时连接错误的
	if failToUpdate {
		configuration = config.NewDefaultConfiguration([]string{falseMockeServerHost})
	} else {
		configuration = config.NewDefaultConfiguration([]string{mockServerHost})
	}
	configuration.GetConsumer().GetLocalCache().SetStartUseFileCache(false)

	configuration.Consumer.LocalCache.PersistDir = util.BackupDir
	configuration.Consumer.LocalCache.ServiceRefreshInterval = model.ToDurationPtr(10 * time.Second)
	sdkCtx, err := api.InitContextByConfig(configuration)
	c.Assert(err, check.IsNil)
	defer sdkCtx.Destroy()
	registry, err := sdkCtx.GetPlugins().GetPlugin(common.TypeLocalRegistry, "inmemory")
	c.Assert(err, check.IsNil)
	regPlug := registry.(localregistry.LocalRegistry)
	if !failToUpdate {
		time.Sleep(3 * time.Second)
	}
	consumer := api.NewConsumerAPIByContext(sdkCtx)
	for i := 0; i < 4; i++ {
		svcInst := regPlug.GetInstances(&model.ServiceKey{
			Namespace: testCacheNs,
			Service:   testCacheSvcs[i],
		}, false, false)
		c.Assert(svcInst.IsInitialized(), check.Equals, false)
	}

	if !failToUpdate {
		request := &api.GetInstancesRequest{}
		request.FlowID = 1111
		request.Timeout = model.ToDurationPtr(500 * time.Millisecond)
		request.SkipRouteFilter = true
		for i := 0; i < 4; i++ {
			var err error
			request.Namespace = testCacheNs
			request.Service = testCacheSvcs[i]
			_, err = consumer.GetInstances(request)
			c.Assert(err, check.IsNil)
		}
		time.Sleep(time.Second * 3)
	}

	t.checkServerRegistryInstanceSame(consumer, !failToUpdate, c)
	for i := 0; i < 4; i++ {
		t.mockServer.GenTestInstances(testServices[i], 2)
	}
	log.Printf("waiting 15s to update loaded cache svc again")
	time.Sleep(15 * time.Second)
	t.checkServerRegistryInstanceSame(consumer, !failToUpdate, c)
}

// 检查localregistry和mockserver的服务信息的一致性
// expectedResult表示期待的结果是一致还是不一致
func (t *CacheFastUpdateSuite) checkServerRegistryInstanceSame(consumer api.ConsumerAPI,
	expectedResult bool, c *check.C) {
	request := &api.GetInstancesRequest{}
	request.FlowID = 1111
	request.Timeout = model.ToDurationPtr(500 * time.Millisecond)
	request.SkipRouteFilter = true
	for i := 0; i < 4; i++ {
		var err error
		var registryInsts model.ServiceInstances
		request.Namespace = testCacheNs
		request.Service = testCacheSvcs[i]
		registryInsts, err = consumer.GetInstances(request)
		// expectedResult == true, 则 failToUpdate == false，为可以正常连接，因此不能有 error 报错
		// c.Assert(err == nil, check.Equals, expectedResult)
		c.Assert(err, check.IsNil)
		cacheSvcKey := &model.ServiceKey{
			Namespace: testCacheNs,
			Service:   testCacheSvcs[i],
		}
		pbInsts := t.mockServer.GetServiceInstances(cacheSvcKey)
		modelInsts := make([]model.Instance, len(pbInsts), len(pbInsts))
		for j, p := range pbInsts {
			modelInsts[j] = &pb.InstanceInProto{Instance: p}
		}
		c.Assert(registryInsts.IsInitialized(), check.Equals, true)
		log.Printf("num of instances of %s is %d", cacheSvcKey, len(registryInsts.GetInstances()))
		c.Assert(util.SameInstances(registryInsts.GetInstances(), modelInsts), check.Equals, expectedResult)
		n := t.mockServer.GetServiceRequests(cacheSvcKey)
		t.mockServer.ClearServiceRequests(cacheSvcKey)
		log.Printf("requests for %s:%s is %d", cacheSvcKey.Namespace, cacheSvcKey.Service, n)
		c.Assert(n > 0, check.Equals, expectedResult)
	}
	t.mockServer.MakeOperationTimeout(mock.OperationDiscoverInstance, false)
}
