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

package loadbalance

import (
	"fmt"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/loadbalancer"
	"github.com/polarismesh/polaris-go/pkg/plugin/localregistry"
	"github.com/polarismesh/polaris-go/plugin/loadbalancer/ringhash"
	monitorpb "github.com/polarismesh/polaris-go/plugin/statreporter/pb/v1"
	"github.com/polarismesh/polaris-go/test/mock"
	"github.com/polarismesh/polaris-go/test/util"
	"github.com/golang/protobuf/ptypes/wrappers"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"gopkg.in/check.v1"
	"log"
	"net"
	"os"
	"time"
)

const (
	lbNamespace      = "lbNS"
	lbService        = "lbSvc"
	lbIPAddr         = "127.0.0.1"
	lbPort           = 8008
	lbMonitorIP      = "127.0.0.1"
	lbMonitorPort    = 8009
	lbPartialService = "lbPartialSvc"
	lbAllFailService = "lbAllFailSvc"
)

//校验因子
type matchFactor struct {
	totalDiff float64
	stdDev    float64
}

var (
	lbTypeToFactor = map[string]matchFactor{
		config.DefaultLoadBalancerWR: {
			totalDiff: 0.025,
			stdDev:    1,
		},
		config.DefaultLoadBalancerRingHash: {
			totalDiff: 0.3,
			stdDev:    10,
		},
		config.DefaultLoadBalancerMaglev: {
			totalDiff: 0.025,
			stdDev:    1.5,
		},
		config.DefaultLoadBalancerL5CST: {
			totalDiff: 0.3,
			stdDev:    10,
		},
	}
)

//实例key
type instanceKey struct {
	Host string
	Port uint32
}

//LBTestingSuite 消费者API测试套
type LBTestingSuite struct {
	grpcServer        *grpc.Server
	grpcListener      net.Listener
	idInstanceWeights map[instanceKey]int
	idInstanceCalls   map[instanceKey]int
	mockServer        mock.NamingServer

	//monitor
	monitorServer   mock.MonitorServer
	monitorToken    string
	grpcMonitor     *grpc.Server
	monitorListener net.Listener
}

var (
	//健康的服务名
	lbHealthyService   *namingpb.Service
	lbHealthyInstances []*namingpb.Instance
)

//设置模拟桩服务器
func (t *LBTestingSuite) SetUpSuite(c *check.C) {
	grpcOptions := make([]grpc.ServerOption, 0)
	maxStreams := 100000
	grpcOptions = append(grpcOptions, grpc.MaxConcurrentStreams(uint32(maxStreams)))

	// get the grpc server wired up
	grpc.EnableTracing = true

	ipAddr := lbIPAddr
	shopPort := lbPort
	var err error
	t.grpcServer = grpc.NewServer(grpcOptions...)
	t.grpcMonitor = grpc.NewServer(grpcOptions...)
	t.mockServer = mock.NewNamingServer()
	token := t.mockServer.RegisterServerService(config.ServerDiscoverService)
	t.mockServer.RegisterServerInstance(ipAddr, shopPort, config.ServerDiscoverService, token, true)
	t.mockServer.RegisterNamespace(&namingpb.Namespace{
		Name:    &wrappers.StringValue{Value: lbNamespace},
		Comment: &wrappers.StringValue{Value: "for loadbalance test"},
		Owners:  &wrappers.StringValue{Value: "LoadBalancer"},
	})
	//全部健康的服务
	serviceToken := uuid.New().String()
	lbHealthyService = &namingpb.Service{
		Name:      &wrappers.StringValue{Value: lbService},
		Namespace: &wrappers.StringValue{Value: lbNamespace},
		Token:     &wrappers.StringValue{Value: serviceToken},
	}
	t.mockServer.RegisterService(lbHealthyService)
	lbHealthyInstances = t.mockServer.GenTestInstances(lbHealthyService, 10)

	t.monitorServer = mock.NewMonitorServer()
	t.monitorToken = t.mockServer.RegisterServerService(config.ServerMonitorService)
	t.mockServer.RegisterServerInstance(lbMonitorIP, lbMonitorPort, config.ServerMonitorService, t.monitorToken, true)
	t.mockServer.RegisterRouteRule(&namingpb.Service{
		Name:      &wrappers.StringValue{Value: config.ServerMonitorService},
		Namespace: &wrappers.StringValue{Value: config.ServerNamespace}},
		t.mockServer.BuildRouteRule(config.ServerNamespace, config.ServerMonitorService))
	monitorpb.RegisterGrpcAPIServer(t.grpcMonitor, t.monitorServer)
	t.monitorListener, err = net.Listen("tcp", fmt.Sprintf("%s:%d", lbMonitorIP, lbMonitorPort))
	if nil != err {
		log.Fatal(fmt.Sprintf("error listening monitor %v", err))
	}
	log.Printf("moniator server listening on %s:%d\n", lbMonitorIP, lbMonitorPort)
	go func() {
		t.grpcMonitor.Serve(t.monitorListener)
	}()

	//部分实例不健康的服务
	serviceToken = uuid.New().String()
	testPartialService := &namingpb.Service{
		Name:      &wrappers.StringValue{Value: lbPartialService},
		Namespace: &wrappers.StringValue{Value: lbNamespace},
		Token:     &wrappers.StringValue{Value: serviceToken},
	}
	t.mockServer.RegisterService(testPartialService)
	t.mockServer.GenTestInstances(testPartialService, 40)
	t.mockServer.GenInstancesWithStatus(testPartialService, 10, mock.UnhealthyStatus, 2048)

	//全部实例不健康的服务
	serviceToken = uuid.New().String()
	testAllFailService := &namingpb.Service{
		Name:      &wrappers.StringValue{Value: lbAllFailService},
		Namespace: &wrappers.StringValue{Value: lbNamespace},
		Token:     &wrappers.StringValue{Value: serviceToken},
	}
	t.mockServer.RegisterService(testAllFailService)
	t.mockServer.GenInstancesWithStatus(testAllFailService, 50, mock.UnhealthyStatus, 2048)

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
func (t *LBTestingSuite) TearDownSuite(c *check.C) {
	t.grpcServer.Stop()
	t.grpcMonitor.Stop()
	if util.DirExist(util.BackupDir) {
		os.RemoveAll(util.BackupDir)
	}
	util.InsertLog(t, c.GetTestLog())
}

//通用负载均衡测试逻辑
func (t *LBTestingSuite) testLoadBalance(c *check.C, service string, lbType string) {
	defer util.DeleteDir(util.BackupDir)
	cfg, err := config.LoadConfigurationByFile("testdata/consumer.yaml")
	c.Assert(err, check.IsNil)
	cfg.Consumer.Loadbalancer.Type = lbType
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()
	request := &api.GetInstancesRequest{}
	request.FlowID = 1111
	request.Namespace = lbNamespace
	request.Service = service
	request.Timeout = model.ToDurationPtr(5 * time.Second)
	resp, err := consumer.GetInstances(request)
	c.Assert(err, check.IsNil)
	t.genInstanceWeights(resp)
	oneRequest := &api.GetOneInstanceRequest{}
	oneRequest.FlowID = 1111
	oneRequest.Namespace = lbNamespace
	oneRequest.Service = service
	oneRequest.Timeout = model.ToDurationPtr(2 * time.Second)
	for i := 0; i < 100000; i++ {
		oneRequest.HashKey = []byte(uuid.New().String())
		instanceResp, err := consumer.GetOneInstance(oneRequest)
		c.Assert(err, check.IsNil)
		instance := instanceResp.Instances[0]
		c, ok := t.idInstanceCalls[instanceKey{
			Host: instance.GetHost(),
			Port: instance.GetPort(),
		}]
		if !ok {
			t.idInstanceCalls[instanceKey{
				Host: instance.GetHost(),
				Port: instance.GetPort(),
			}] = 1
		} else {
			t.idInstanceCalls[instanceKey{
				Host: instance.GetHost(),
				Port: instance.GetPort(),
			}] = c + 1
		}
	}
	totalDiff := calDiff(t.idInstanceWeights, t.idInstanceCalls, instanceKey{})
	fmt.Printf("total diff is %v\n", totalDiff)
	totalStdDev := calStdDev(t.idInstanceWeights, t.idInstanceCalls, instanceKey{})
	fmt.Printf("total stdDev is %.4f\n", totalStdDev)
	factor := lbTypeToFactor[lbType]
	c.Assert(totalDiff < factor.totalDiff, check.Equals, true)
	c.Assert(totalStdDev < factor.stdDev, check.Equals, true)
}

//func (t *LBTestingSuite) checkLoadBalanceReport(loadbalancer string, service string, c *check.C) {
//	time.Sleep(6 * time.Second)
//	lbStats := t.monitorServer.GetLbStat()
//	t.monitorServer.SetLbStat(nil)
//	hasCheck := false
//	uploadNum := make(map[instanceKey]int)
//	for _, lbs := range lbStats {
//		if lbs.Service == service {
//			hasCheck = true
//			c.Assert(loadbalancer, check.Equals, lbs.Loadbalancer)
//			for _, instLbs := range lbs.GetInstanceInfo() {
//				c, ok := uploadNum[instanceKey{
//					Host: instLbs.GetIp(),
//					Port: instLbs.GetPort(),
//				}]
//				if ok {
//					uploadNum[instanceKey{
//						Host: instLbs.GetIp(),
//						Port: instLbs.GetPort(),
//					}] = c + int(instLbs.GetChooseNum())
//				} else {
//					uploadNum[instanceKey{
//						Host: instLbs.GetIp(),
//						Port: instLbs.GetPort(),
//					}] = int(instLbs.GetChooseNum())
//				}
//			}
//		}
//	}
//	c.Assert(hasCheck, check.Equals, true)
//	for k, v := range t.idInstanceCalls {
//		c.Assert(v, check.Equals, uploadNum[k])
//		delete(uploadNum, k)
//	}
//	c.Assert(len(uploadNum), check.Equals, 0)
//}

//负载均衡测试WeightRandom
func (t *LBTestingSuite) TestAllHealthyLoadBalanceWR(c *check.C) {
	log.Printf("Start TestAllHealthyLoadBalanceWeightRandom")
	t.testLoadBalance(c, lbService, config.DefaultLoadBalancerWR)
}

//负载均衡测试RingHash
func (t *LBTestingSuite) TestAllHealthyLoadBalanceRing(c *check.C) {
	log.Printf("Start TestAllHealthyLoadBalanceHashRing")
	t.testLoadBalance(c, lbService, config.DefaultLoadBalancerRingHash)
}

//负载均衡测试Maglev
func (t *LBTestingSuite) TestAllHealthyLoadBalanceMaglev(c *check.C) {
	log.Printf("Start TestAllHealthyLoadBalanceMaglev")
	t.testLoadBalance(c, lbService, config.DefaultLoadBalancerMaglev)
}

//负载均衡测试l5 ringHash
func (t *LBTestingSuite) TestAllHealthyLoadBalanceL5RingHash(c *check.C) {
	log.Printf("Start TestAllHealthyLoadBalanceMaglev")
	t.testLoadBalance(c, lbService, config.DefaultLoadBalancerL5CST)
}

//部分健康负载均衡测试WeightRandom
func (t *LBTestingSuite) TestPartialLoadBalanceWR(c *check.C) {
	log.Printf("Start TestPartialLoadBalanceWeightRandom")
	t.testLoadBalance(c, lbPartialService, config.DefaultLoadBalancerWR)
}

//部分健康负载均衡测试RingHash
func (t *LBTestingSuite) TestPartialLoadBalanceRing(c *check.C) {
	log.Printf("Start TestPartialLoadBalanceHashRing")
	t.testLoadBalance(c, lbPartialService, config.DefaultLoadBalancerRingHash)
}

//部分健康负载均衡测试Maglev
func (t *LBTestingSuite) TestPartialLoadBalanceMaglev(c *check.C) {
	log.Printf("Start TestPartialLoadBalanceHashRing")
	t.testLoadBalance(c, lbPartialService, config.DefaultLoadBalancerMaglev)
}

//部分健康负载均衡测试l5
func (t *LBTestingSuite) TestPartialLoadBalancel5RingHash(c *check.C) {
	log.Printf("Start TestPartialLoadBalanceHashRing")
	t.testLoadBalance(c, lbPartialService, config.DefaultLoadBalancerL5CST)
}

//负载均衡测试
func (t *LBTestingSuite) TestAllFailLoadBalanceWR(c *check.C) {
	log.Printf("Start TestAllFailLoadBalanceWeightRandom")
	t.testLoadBalance(c, lbAllFailService, config.DefaultLoadBalancerWR)
}

//负载均衡测试
func (t *LBTestingSuite) TestAllFailLoadBalanceRing(c *check.C) {
	log.Printf("Start TestAllFailLoadBalanceHashRing")
	t.testLoadBalance(c, lbAllFailService, config.DefaultLoadBalancerRingHash)
}

//负载均衡测试
func (t *LBTestingSuite) TestAllFailLoadBalanceMaglev(c *check.C) {
	log.Printf("Start TestAllFailLoadBalanceHashRing")
	t.testLoadBalance(c, lbAllFailService, config.DefaultLoadBalancerMaglev)
}

//负载均衡测试
func (t *LBTestingSuite) TestAllFailLoadBalanceL5RingHash(c *check.C) {
	log.Printf("Start TestAllFailLoadBalanceHashRing")
	t.testLoadBalance(c, lbAllFailService, config.DefaultLoadBalancerL5CST)
}

//测试直接通过负载均衡插件来挑选实例
func (t *LBTestingSuite) TestDirectLoadBalance(c *check.C) {
	log.Printf("Start TestDirectLoadBalance")
	defer util.DeleteDir(util.BackupDir)
	cfg, err := config.LoadConfigurationByFile("testdata/consumer.yaml")
	c.Assert(err, check.IsNil)
	sdkCtx, err := api.InitContextByConfig(cfg)
	defer sdkCtx.Destroy()
	c.Assert(err, check.IsNil)
	loadBalancer, err := sdkCtx.GetPlugins().GetPlugin(common.TypeLoadBalancer, config.DefaultLoadBalancerWR)
	c.Assert(err, check.IsNil)
	lbPlug := loadBalancer.(loadbalancer.LoadBalancer)
	registry, err := sdkCtx.GetPlugins().GetPlugin(common.TypeLocalRegistry, "inmemory")
	c.Assert(err, check.IsNil)
	regPlug := registry.(localregistry.LocalRegistry)
	consumerAPI := api.NewConsumerAPIByContext(sdkCtx)
	defer consumerAPI.Destroy()
	request := &api.GetInstancesRequest{}
	request.FlowID = 1111
	request.Namespace = lbNamespace
	request.Service = lbService
	request.Timeout = model.ToDurationPtr(5 * time.Second)
	resp, err := consumerAPI.GetInstances(request)
	c.Assert(err, check.IsNil)
	t.genInstanceWeights(resp)
	svcInsts := regPlug.GetInstances(&model.ServiceKey{
		Service:   lbService,
		Namespace: lbNamespace,
	}, false, false)
	t.genInstanceWeights(svcInsts)
	for i := 0; i < 100000; i++ {
		inst, err := lbPlug.ChooseInstance(&loadbalancer.Criteria{
			Cluster: model.NewCluster(svcInsts.GetServiceClusters(), nil)}, svcInsts)
		c.Assert(err, check.IsNil)
		c, ok := t.idInstanceCalls[instanceKey{
			Host: inst.GetHost(),
			Port: inst.GetPort(),
		}]
		if !ok {
			t.idInstanceCalls[instanceKey{
				Host: inst.GetHost(),
				Port: inst.GetPort(),
			}] = 1
		} else {
			t.idInstanceCalls[instanceKey{
				Host: inst.GetHost(),
				Port: inst.GetPort(),
			}] = c + 1
		}
	}
	totalDiff := calDiff(t.idInstanceWeights, t.idInstanceCalls, instanceKey{})
	fmt.Printf("total diff is %v\n", totalDiff)
	c.Assert(totalDiff < 0.025, check.Equals, true)
}

//测试RingHash负载均衡是否可以每次都返回相同节点
func (t *LBTestingSuite) TestForeverNodeRingHash(c *check.C) {
	log.Printf("Start TestForeverNodeRingHash")
	t.testForeverNodeForHash(c, lbService, config.DefaultLoadBalancerRingHash)
	t.testForeverNodeForHashSameContext(c, lbService, config.DefaultLoadBalancerRingHash)
}

//测试RingHash负载均衡是否可以每次都返回相同节点
func (t *LBTestingSuite) TestForeverNodeMaglev(c *check.C) {
	log.Printf("Start TestForeverNodeMaglev")
	t.testForeverNodeForHash(c, lbService, config.DefaultLoadBalancerMaglev)
	t.testForeverNodeForHashSameContext(c, lbService, config.DefaultLoadBalancerMaglev)
}

//测试Hash负载均衡是否可以每次都返回相同节点
func (t *LBTestingSuite) TestForeverNodeHash(c *check.C) {
	log.Printf("Start TestForeverNodeHash")
	t.testForeverNodeForHash(c, lbService, config.DefaultLoadBalancerHash)
	t.testForeverNodeForHashSameContext(c, lbService, config.DefaultLoadBalancerHash)
}

//测试l5ringHash负载均衡是否可以每次都返回相同节点
func (t *LBTestingSuite) TestForeverNodeL5RingHash(c *check.C) {
	log.Printf("Start TestForeverNodeMaglev")
	t.testForeverNodeForHash(c, lbService, config.DefaultLoadBalancerL5CST)
	t.testForeverNodeForHashSameContext(c, lbService, config.DefaultLoadBalancerL5CST)
}

//测试一致性hash是否可以每次都返回相同节点
func (t *LBTestingSuite) testForeverNodeForHash(c *check.C, service string, lbType string) {
	log.Printf("TestForeverNodeHash for different context, lbType %s", lbType)
	var addr string
	t.buildNodeWeights(c, service)
	for i := 0; i < 10; i++ {
		addr = t.testForeverNodeForHashOne(c, service, lbType, addr, nil)
	}
}

//测试一致性hash是否可以每次都返回相同节点
func (t *LBTestingSuite) testForeverNodeForHashSameContext(c *check.C, service string, lbType string) {
	log.Printf("TestForeverNodeHash for same context, lbType %s", lbType)
	var addr string
	defer util.DeleteDir(util.BackupDir)
	cfg, err := config.LoadConfigurationByFile("testdata/consumer.yaml")
	c.Assert(err, check.IsNil)
	cfg.Consumer.Loadbalancer.Type = lbType
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()
	request := &api.GetInstancesRequest{}
	request.FlowID = 1111
	request.Namespace = lbNamespace
	request.Service = service
	request.Timeout = model.ToDurationPtr(5 * time.Second)
	resp, err := consumer.GetInstances(request)
	c.Assert(err, check.IsNil)
	t.genInstanceWeights(resp)
	for i := 0; i < 10; i++ {
		addr = t.testForeverNodeForHashOne(c, service, lbType, addr, consumer)
	}
}

//构建节点的权重信息
func (t *LBTestingSuite) buildNodeWeights(c *check.C, service string) {
	defer util.DeleteDir(util.BackupDir)
	cfg, err := config.LoadConfigurationByFile("testdata/consumer.yaml")
	c.Assert(err, check.IsNil)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()
	request := &api.GetInstancesRequest{}
	request.FlowID = 1111
	request.Namespace = lbNamespace
	request.Service = service
	request.Timeout = model.ToDurationPtr(5 * time.Second)
	resp, err := consumer.GetInstances(request)
	c.Assert(err, check.IsNil)
	t.genInstanceWeights(resp)
}

//执行单次负载均衡
func (t *LBTestingSuite) doLoadBalanceOnce(
	c *check.C, service string, lbType string, replicate int, consumer api.ConsumerAPI, vnode int) []string {
	if nil == consumer {
		cfg, err := config.LoadConfigurationByFile("testdata/consumer.yaml")
		c.Assert(err, check.IsNil)
		cfg.Consumer.Loadbalancer.Type = lbType
		cfg.Consumer.ServiceRouter.SetChain([]string{config.DefaultServiceRouterFilterOnly})
		if vnode > 0 {
			cfg.Consumer.Loadbalancer.SetPluginConfig(config.DefaultLoadBalancerRingHash, &ringhash.Config{
				VnodeCount: vnode,
			})
		}
		consumer, err = api.NewConsumerAPIByConfig(cfg)
		c.Assert(err, check.IsNil)
		defer func() {
			consumer.Destroy()
			util.DeleteDir(util.BackupDir)
		}()
	}
	oneRequest := &api.GetOneInstanceRequest{}
	oneRequest.FlowID = 1111
	oneRequest.Namespace = lbNamespace
	oneRequest.Service = service
	oneRequest.ReplicateCount = replicate
	oneRequest.Timeout = model.ToDurationPtr(2 * time.Second)
	oneRequest.HashKey = []byte("abcdefgh")
	instanceResp, err := consumer.GetOneInstance(oneRequest)
	c.Assert(err, check.IsNil)
	ids := make([]string, 0, len(instanceResp.Instances))
	for _, inst := range instanceResp.Instances {
		ids = append(ids, inst.GetId())
	}
	return ids
}

//单次运行
func (t *LBTestingSuite) testForeverNodeForHashOne(
	c *check.C, service string, lbType string, addr string, consumer api.ConsumerAPI) string {
	addrGot := t.doLoadBalanceOnce(c, service, lbType, 0, consumer, 0)
	if len(addr) == 0 {
		return addrGot[0]
	}
	c.Assert(addrGot[0], check.Equals, addr)
	return addrGot[0]
}

//套件名字
func (t *LBTestingSuite) GetName() string {
	return "LoadBalance"
}

//获取实例的权重信息并保存
func (t *LBTestingSuite) genInstanceWeights(response model.ServiceInstances) {
	t.idInstanceWeights = make(map[instanceKey]int)
	t.idInstanceCalls = make(map[instanceKey]int)
	var instances = response.GetInstances()
	var totalWeight = response.GetTotalWeight()
	instanceCount := len(instances)
	for i := 0; i < len(instances); i++ {
		t.idInstanceWeights[instanceKey{
			Host: instances[i].GetHost(),
			Port: instances[i].GetPort(),
		}] = int(instances[i].GetWeight())
	}
	fmt.Printf("instances count is %d, totalWeight is %d\n", instanceCount, totalWeight)
}

//测试获取备份节点
func (t *LBTestingSuite) TestReplicateNodeRingHash(c *check.C) {
	log.Printf("Start TestReplicateNodeRingHash")
	for i := 0; i < 1; i++ {
		func() {
			lbType := config.DefaultLoadBalancerRingHash
			service := lbService
			replicate := 2
			addrGot := t.doLoadBalanceOnce(c, service, lbType, replicate, nil, 10)
			c.Assert(len(addrGot), check.Equals, replicate+1)
			log.Printf("replicate test1, instances is %v", addrGot)
			targetInstId := addrGot[0]
			replicateInstId1 := addrGot[1]
			replicateInstId2 := addrGot[2]
			var anotherInstances = make([]*namingpb.Instance, 0, len(lbHealthyInstances)-1)
			for _, instance := range lbHealthyInstances {
				if instance.GetId().GetValue() == targetInstId {
					continue
				}
				anotherInstances = append(anotherInstances, instance)
			}
			svcKey := &model.ServiceKey{
				Namespace: lbHealthyService.GetNamespace().GetValue(),
				Service:   lbHealthyService.GetName().GetValue(),
			}
			t.mockServer.SetServiceInstances(svcKey, anotherInstances)
			defer t.mockServer.SetServiceInstances(svcKey, lbHealthyInstances)
			anotherAddr := t.doLoadBalanceOnce(c, service, lbType, 0, nil, 10)
			log.Printf("replicate test2, instances is %v", anotherAddr)
			anotherInstId := anotherAddr[0]
			c.Assert(anotherInstId, check.Equals, replicateInstId1)
			//比较下一个
			anotherInstances = make([]*namingpb.Instance, 0, len(lbHealthyInstances)-2)
			for _, instance := range lbHealthyInstances {
				if instance.GetId().GetValue() == targetInstId {
					continue
				}
				if instance.GetId().GetValue() == replicateInstId1 {
					continue
				}
				anotherInstances = append(anotherInstances, instance)
			}
			t.mockServer.SetServiceInstances(svcKey, anotherInstances)
			anotherAddr = t.doLoadBalanceOnce(c, service, lbType, 0, nil, 10)
			log.Printf("replicate test2, instances is %v", anotherAddr)
			anotherInstId = anotherAddr[0]
			c.Assert(anotherInstId, check.Equals, replicateInstId2)
		}()
	}
}

//测试用户选择负载均衡算法
func (t *LBTestingSuite) TestUserChooseLBAlgorithm(c *check.C) {
	log.Printf("Start TestUserChooseLBAlgorithm")
	cfg, err := config.LoadConfigurationByFile("testdata/consumer.yaml")
	c.Assert(err, check.IsNil)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()
	request := &api.GetOneInstanceRequest{}
	request.FlowID = 1111
	request.Namespace = lbNamespace
	request.Service = lbService
	request.Timeout = model.ToDurationPtr(5 * time.Second)
	request.HashKey = []byte("abc")

	// 不设置负载均衡算法，默认使用配置文件中的, 默认为weightedRandom
	var rspList []model.InstancesResponse
	for i := 0; i < 10; i++ {
		resp, err := consumer.GetOneInstance(request)
		c.Assert(err, check.IsNil)
		rspList = append(rspList, *resp)
	}
	allSame := true
	for _, v := range rspList {
		for _, v1 := range rspList {
			if v.GetInstances()[0].GetPort() != v1.GetInstances()[0].GetPort() {
				allSame = false
			}
		}
	}
	c.Assert(allSame, check.Equals, false)

	// choose ringHash
	request.LbPolicy = api.LBPolicyRingHash
	rspList = rspList[:0]
	for i := 0; i < 10; i++ {
		resp, err := consumer.GetOneInstance(request)
		fmt.Println(resp.GetInstances()[0].GetPort())
		c.Assert(err, check.IsNil)
		rspList = append(rspList, *resp)
	}
	allSame = true
	for _, v := range rspList {
		for _, v1 := range rspList {
			if v.GetInstances()[0].GetPort() != v1.GetInstances()[0].GetPort() {
				allSame = false
			}
		}
	}
	c.Assert(allSame, check.Equals, true)

}

//测试获取备份节点
//func (t *LBTestingSuite) TestReplicateNodeMaglev(c *check.C) {
//	log.Printf("Start TestReplicateNodeRingHash")
//	lbType := config.DefaultLoadBalancerMaglev
//	service := lbService
//	replicate := 2
//	addrGot := t.doLoadBalanceOnce(c, service, lbType, replicate, nil)
//	//c.Assert(len(addrGot), check.Equals, replicate+1)
//	targetInstId := addrGot[0]
//	fmt.Printf("target instance 1 is %s\n", targetInstId)
//	var anotherInstances = make([]*namingpb.Instance, 0, len(lbHealthyInstances)-1)
//	for _, instance := range lbHealthyInstances {
//		if instance.GetId().GetValue() == targetInstId {
//			continue
//		}
//		anotherInstances = append(anotherInstances, instance)
//	}
//	t.mockServer.setInstances(lbHealthyService, anotherInstances)
//	defer t.mockServer.setInstances(lbHealthyService, lbHealthyInstances)
//	anotherAddr := t.doLoadBalanceOnce(c, service, lbType, 0, nil)
//	anotherInstId := anotherAddr[0]
//	fmt.Printf("target instance 2 is %s\n", anotherInstId)
//}
