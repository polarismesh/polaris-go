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
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"

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
	svcUpdateNamespace = "testSvcUpdateNs"
	// 测试的默认服务名
	svcUpdateServiceSync = "svc1"
	// 测试的健康状态变更服务名
	svcHealthModifyServiceSync = "healthySvc1"
	// 测试服务器的默认地址
	svcUpdateIPAdress = "127.0.0.1"
	// 测试服务器的端口
	svcUpdatePort = 11211
	// 初始化服务实例数
	instanceCount = 4
	// 测试多服务首次并发拉取的场景
	svcCount = 50
	// 批量服务名
	batchSvcName = "batchSvc%d"
	// 不存在服务名
	notExistSvcName = "notExistSvc"
)

// 服务更新测试套
type ServiceUpdateSuite struct {
	mutex                 sync.Mutex
	mockServer            mock.NamingServer
	testService           *namingpb.Service
	testServiceToken      string
	testServiceInstances  []*namingpb.Instance
	healthModifyInstances []*namingpb.Instance
	grpcServer            *grpc.Server
	grpcListener          net.Listener
}

// SetUpSuite 启动测试套程序
func (t *ServiceUpdateSuite) SetUpSuite(c *check.C) {
	util.DeleteDir(util.BackupDir)
	grpcOptions := make([]grpc.ServerOption, 0)
	maxStreams := 100000
	grpcOptions = append(grpcOptions, grpc.MaxConcurrentStreams(uint32(maxStreams)))

	// get the grpc server wired up
	grpc.EnableTracing = true

	ipAddr := svcUpdateIPAdress
	shopPort := svcUpdatePort
	var err error
	t.grpcServer = grpc.NewServer(grpcOptions...)
	t.mockServer = mock.NewNamingServer()
	token := t.mockServer.RegisterServerService(config.ServerDiscoverService)
	t.mockServer.RegisterServerInstance(ipAddr, shopPort, config.ServerDiscoverService, token, true)
	t.mockServer.RegisterNamespace(&namingpb.Namespace{
		Name:    &wrappers.StringValue{Value: svcUpdateNamespace},
		Comment: &wrappers.StringValue{Value: "for service update test"},
		Owners:  &wrappers.StringValue{Value: "ConsumerAPI"},
	})
	t.testService = &namingpb.Service{
		Name:      &wrappers.StringValue{Value: svcUpdateServiceSync},
		Namespace: &wrappers.StringValue{Value: svcUpdateNamespace},
		Token:     &wrappers.StringValue{Value: uuid.New().String()},
	}
	t.testServiceToken = t.mockServer.RegisterService(t.testService)
	t.testServiceInstances = t.mockServer.GenTestInstances(t.testService, instanceCount)

	healthModifySvc := &namingpb.Service{
		Name:      &wrappers.StringValue{Value: svcHealthModifyServiceSync},
		Namespace: &wrappers.StringValue{Value: svcUpdateNamespace},
		Token:     &wrappers.StringValue{Value: uuid.New().String()},
	}
	t.mockServer.RegisterService(healthModifySvc)
	t.healthModifyInstances = t.mockServer.GenTestInstances(healthModifySvc, instanceCount)
	// 生成批量服务
	for i := 0; i < svcCount; i++ {
		svc := &namingpb.Service{
			Name:      &wrappers.StringValue{Value: fmt.Sprintf(batchSvcName, i)},
			Namespace: &wrappers.StringValue{Value: svcUpdateNamespace},
			Token:     &wrappers.StringValue{Value: uuid.New().String()},
		}
		t.mockServer.RegisterService(svc)
		t.mockServer.GenTestInstances(svc, instanceCount)
	}
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

// SetUpSuite 结束测试套程序
func (t *ServiceUpdateSuite) TearDownSuite(c *check.C) {
	t.grpcServer.Stop()
	if util.DirExist(util.BackupDir) {
		os.RemoveAll(util.BackupDir)
	}
	util.InsertLog(t, c.GetTestLog())
}

// 获取用例名
func (t *ServiceUpdateSuite) GetName() string {
	return "ServiceUpdateSuite"
}

const (
	modifyCount    = 7
	modifyIndex    = 0
	getWorkerCount = 10
)

// 比较实例列表
func compareInstances(expectInstances []*namingpb.Instance, obtainInstances []model.Instance) bool {
	obtainMap := make(map[string]model.Instance, 0)
	for _, instance := range obtainInstances {
		obtainMap[instance.GetId()] = instance
	}
	for _, instance := range expectInstances {
		id := instance.GetId().GetValue()
		var inst model.Instance
		var ok bool
		if inst, ok = obtainMap[id]; !ok {
			log.Printf("instance(id=%s, addr=%s:%d) not found",
				id, instance.GetHost().GetValue(), instance.GetPort().GetValue())
			return false
		}
		if instance.GetHealthy().GetValue() != inst.IsHealthy() {
			log.Printf("instance %s healthy not match", id)
			return false
		}

	}
	return true
}

// 测试添加实例是否可以通过同步SDK获取
func (t *ServiceUpdateSuite) TestDynamicAddService(c *check.C) {
	log.Printf("Start to TestDynamicAddService")
	defer util.DeleteDir(util.BackupDir)
	cfg := config.NewDefaultConfiguration([]string{fmt.Sprintf("%s:%d", svcUpdateIPAdress, svcUpdatePort)})
	enableStat := false
	cfg.Global.StatReporter.Enable = &enableStat
	cfg.GetConsumer().GetLocalCache().SetPersistDir(util.BackupDir)
	cfg.GetConsumer().GetLocalCache().SetServiceRefreshInterval(1 * time.Second)
	var err error
	var consumerAPI api.ConsumerAPI
	consumerAPI, err = api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumerAPI.Destroy()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	wg := &sync.WaitGroup{}
	wg.Add(getWorkerCount)
	for i := 0; i < getWorkerCount; i++ {
		go func(idx int) {
			defer wg.Done()
			log.Printf("start get worker %d", idx)
			var instances []model.Instance
		ForEnd:
			for {
				select {
				case <-ctx.Done():
					// log.Printf("context TestDynamicAddService exits")
					break ForEnd
				default:
					request := &api.GetInstancesRequest{}
					request.Namespace = svcUpdateNamespace
					request.Service = svcUpdateServiceSync
					request.SkipRouteFilter = true
					response, err := consumerAPI.GetInstances(request)
					c.Assert(err, check.IsNil)
					instances = response.Instances
					time.Sleep(1 * time.Second)
				}
			}
			syncRun(&t.mutex, func() {
				expectLength := len(t.testServiceInstances)
				c.Assert(len(instances), check.Equals, expectLength)
				c.Assert(compareInstances(t.testServiceInstances, instances), check.Equals, true)
			})
		}(i)
	}
	// 启动定时删除服务实例的协程
	go func() {
		log.Printf("start worker to set add/delete instance")
		var instanceBackup *namingpb.Instance
		for i := 0; i < modifyCount; i++ {
			time.Sleep(2500 * time.Millisecond)
			syncRun(&t.mutex, func() {
				if nil == instanceBackup {
					instanceBackup = t.testServiceInstances[modifyIndex]
					t.testServiceInstances = t.testServiceInstances[modifyIndex+1:]
					t.mockServer.SetServiceInstances(&model.ServiceKey{
						Namespace: t.testService.GetNamespace().GetValue(),
						Service:   t.testService.GetName().GetValue(),
					}, t.testServiceInstances)
					t.modifyRevision(model.EventInstances)
					log.Printf("delete instance %s:%d",
						instanceBackup.Host.GetValue(), instanceBackup.Port.GetValue())
				} else {
					t.testServiceInstances = append(t.testServiceInstances, instanceBackup)
					t.mockServer.SetServiceInstances(&model.ServiceKey{
						Namespace: t.testService.GetNamespace().GetValue(),
						Service:   t.testService.GetName().GetValue(),
					}, t.testServiceInstances)
					t.modifyRevision(model.EventInstances)
					log.Printf("restore instance %s:%d",
						instanceBackup.Host.GetValue(), instanceBackup.Port.GetValue())
					instanceBackup = nil
				}
			})
		}
	}()
	wg.Wait()
}

// 修改revision信息
func (t *ServiceUpdateSuite) modifyRevision(typ model.EventType) {
	t.mockServer.SetServiceRevision(t.testServiceToken, uuid.New().String(), model.ServiceEventKey{
		ServiceKey: model.ServiceKey{
			Namespace: t.testService.GetNamespace().GetValue(),
			Service:   t.testService.GetName().GetValue(),
		},
		Type: typ,
	})
}

// 同步运行代码块
func syncRun(mutex *sync.Mutex, handle func()) {
	mutex.Lock()
	defer mutex.Unlock()
	handle()
}

// 测试频繁变更实例健康状态是否会出现不一致问题
func (t *ServiceUpdateSuite) TestDynamicModifyInstance(c *check.C) {
	log.Printf("Start to TestDynamicModifyInstance")
	defer util.DeleteDir(util.BackupDir)
	cfg := config.NewDefaultConfiguration([]string{fmt.Sprintf("%s:%d", svcUpdateIPAdress, svcUpdatePort)})
	enableStat := false
	cfg.Global.StatReporter.Enable = &enableStat
	cfg.GetConsumer().GetLocalCache().SetPersistDir(util.BackupDir)
	cfg.GetConsumer().GetLocalCache().SetServiceRefreshInterval(1 * time.Second)
	var err error
	var consumerAPI api.ConsumerAPI
	consumerAPI, err = api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumerAPI.Destroy()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	wg := &sync.WaitGroup{}
	wg.Add(getWorkerCount)
	for i := 0; i < getWorkerCount; i++ {
		go func(idx int) {
			defer func() {
				wg.Done()
				log.Printf("routine %d exists", idx)
			}()
			log.Printf("start get worker %d", idx)
			var instances []model.Instance
		ForEnd:
			for {
				select {
				case <-ctx.Done():
					// log.Printf("context TestDynamicModifyInstance exits")
					break ForEnd
				default:
					request := &api.GetInstancesRequest{}
					request.Namespace = svcUpdateNamespace
					request.Service = svcHealthModifyServiceSync
					request.SkipRouteFilter = true
					response, err := consumerAPI.GetInstances(request)
					c.Assert(err, check.IsNil)
					instances = response.Instances
					// instancesCount := len(instances)
					// log.Printf("instances count %d", instancesCount)
					time.Sleep(1 * time.Second)
				}
			}
			syncRun(&t.mutex, func() {
				expectLength := len(t.healthModifyInstances)
				c.Assert(len(instances), check.Equals, expectLength)
				c.Assert(compareInstances(t.healthModifyInstances, instances), check.Equals, true)
			})
		}(i)
	}
	// 启动定时设置健康状态协程
	go func() {
		log.Printf("start worker to set healthy status")
		var lastHealthy bool
		for i := 0; i < modifyCount; i++ {
			time.Sleep(2500 * time.Millisecond)
			syncRun(&t.mutex, func() {
				instance := t.healthModifyInstances[modifyIndex]
				instance.Healthy = &wrappers.BoolValue{
					Value: lastHealthy,
				}
				t.modifyRevision(model.EventInstances)
				log.Printf("change instance (id=%s, addr=%s:%d) healthy to %v", instance.GetId().GetValue(),
					instance.Host.GetValue(), instance.Port.GetValue(), lastHealthy)
				lastHealthy = !lastHealthy
			})
		}
		log.Printf("end worker to set healthy status")
	}()
	wg.Wait()
}

// 批量拉取服务
func batchGetServices(c *check.C, consumerAPI api.ConsumerAPI) {
	wg := &sync.WaitGroup{}
	wg.Add(svcCount)
	for i := 0; i < svcCount; i++ {
		go func(idx int) {
			defer wg.Done()
			hasError := false
			svcName := fmt.Sprintf(batchSvcName, idx)
			request := &api.GetOneInstanceRequest{}
			request.Namespace = svcUpdateNamespace
			request.Service = svcName
			_, err := consumerAPI.GetOneInstance(request)
			if nil != err {
				hasError = true
				log.Printf("error on service %s:%s, detail: %v", request.Namespace, request.Service, err)
			}
			c.Assert(hasError, check.Equals, false)
		}(i)
	}
	wg.Wait()
}

// 测试服务超时被删除后，重新拉取的问题
func (t *ServiceUpdateSuite) TestFirstMultipleServices(c *check.C) {
	log.Printf("Start to TestMultipleServices")
	defer util.DeleteDir(util.BackupDir)
	cfg := config.NewDefaultConfiguration([]string{fmt.Sprintf("%s:%d", svcUpdateIPAdress, svcUpdatePort)})
	enableStat := false
	cfg.GetGlobal().GetStatReporter().SetEnable(enableStat)
	cfg.GetGlobal().GetAPI().SetTimeout(5 * time.Second)
	cfg.GetConsumer().GetLocalCache().SetPersistDir(util.BackupDir)
	cfg.GetConsumer().GetLocalCache().SetStartUseFileCache(false)
	cfg.GetGlobal().GetAPI().SetMaxRetryTimes(5)
	// 设置超时
	// cfg.GetConsumer().GetLocalCache().SetServiceExpireTime(10 * time.Second)
	// 设置处理一个服务需要50ms
	t.mockServer.SetMethodInterval(50 * time.Millisecond)
	var err error
	var consumerAPI api.ConsumerAPI
	consumerAPI, err = api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumerAPI.Destroy()
	request := &api.GetOneInstanceRequest{}
	request.Namespace = svcUpdateNamespace
	request.Service = notExistSvcName
	_, err = consumerAPI.GetOneInstance(request)
	c.Assert(err, check.NotNil)
	batchGetServices(c, consumerAPI)
	log.Printf("success to get services")
	log.Printf("start to wait expired")
	time.Sleep(40 * time.Second)
	log.Printf("end to wait expired")
	batchGetServices(c, consumerAPI)
}
