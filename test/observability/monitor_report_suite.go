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

package observability

import (
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"sort"
	"time"

	"github.com/golang/protobuf/jsonpb"
	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/ptypes/wrappers"
	"github.com/google/uuid"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/test/mock"
	"github.com/polarismesh/polaris-go/test/util"
	"google.golang.org/grpc"
	"gopkg.in/check.v1"
)

//consumerAPI各种方法的调用次数
var (
	//同步获取一个实例的成功失败数
	GetOneInstanceSuccessNum = 0
	GetOneInstanceFailNum    = 0

	//同步获取实例的成功失败数
	GetInstancesSuccessNum = 0
	GetInstancesFailNum    = 0

	//同步获取路由规则
	GetRouteRuleSuccessNum = 0
	GetRouteRuleFailNum    = 0

	//上报服务调用的次数
	ServiceCallSuccessNum = 0
	ServiceCallFailNum    = 0

	//获取网格调用次数
	GetMeshSuccessNum = 0
	GetMeshFailNum    = 0
)

//providerAPI各种方法调用次数
var (
	//注册实例的成功失败数
	RegisterSuccessNum = 0
	RegisterFailNum    = 0

	//反注册实例的成功失败数
	DeregisterSuccessNum = 0
	DeregisterFailNum    = 0

	//心跳的成功失败数
	HeartbeatSuccessNum = 0
	HeartbeatFailNum    = 0

	//限流的成功失败数
	GetQuotaSuccessNum = 0
	GetQuotaFailNum    = 0
)

const (
	//polaris-server的IP端口
	discoverIp   = "127.0.0.1"
	discoverPort = 8180

	//上报的服务调用
	calledSvc = "calledSvc"
	calledNs  = "calledNs"

	changedSvc = "changedSvc"
	changedNs  = "changedNs"

	recoverAllSvc = "recoverAllSvc"
	recoverAllNs  = "recoverAllNs"

	//用于测试的限流规则的文件路径
	rateLimitRulesPath = "testdata/ratelimit_rule/rate_limit_total.json"
	rateLimitRule2Path = "testdata/ratelimit_rule/rate_limit_rule2.json"
	//rateLimit里面两个rule的id
	rateLimitRule1Id = "rule1"
	rateLimitRule2Id = "rule2"
)

//上报插件测试套件
type MonitorReportSuite struct {
	mockServer mock.NamingServer
	//SERVER
	grpcServer *grpc.Server
	//MONITOR
	discoverLisenter      net.Listener
	discoverToken         string
	serviceToken          string
	instanceId            string
	instPort              uint32
	instHost              string
	changeSvcToken        string
	changeSvcRevision     string
	changeSvcRouting      *namingpb.Routing
	changeRoutingRevision string

	recoverAllService *namingpb.Service
}

//初始化套件
func (m *MonitorReportSuite) SetUpSuite(c *check.C) {
	m.initStatNum()

	// get the grpc server wired up
	grpc.EnableTracing = true

	var err error

	m.grpcServer = util.GetGrpcServer()
	m.discoverToken = uuid.New().String()

	m.mockServer = mock.NewNamingServer()
	m.mockServer.RegisterServerServices(discoverIp, discoverPort)

	m.mockServer.RegisterRouteRule(util.BuildNamingService(config.ServerNamespace, config.ServerMonitorService, ""),
		m.mockServer.BuildRouteRule(config.ServerNamespace, config.ServerMonitorService))

	m.serviceToken = uuid.New().String()
	testService := util.BuildNamingService(calledNs, calledSvc, m.serviceToken)
	m.mockServer.RegisterService(testService)
	m.mockServer.GenTestInstances(testService, 3)
	m.mockServer.GenInstancesWithStatus(testService, 2, mock.IsolatedStatus, 2048)
	m.mockServer.GenInstancesWithStatus(testService, 1, mock.UnhealthyStatus, 4096)
	//获取一个被调服务实例的信息，用于后面调用providerApi
	svcKey := &model.ServiceKey{Namespace: testService.GetNamespace().GetValue(),
		Service: testService.GetName().GetValue()}
	m.instanceId = m.mockServer.GetServiceInstances(svcKey)[0].GetId().GetValue()
	m.instHost = m.mockServer.GetServiceInstances(svcKey)[0].GetHost().GetValue()
	m.instPort = m.mockServer.GetServiceInstances(svcKey)[0].GetPort().GetValue()

	m.changeSvcToken = uuid.New().String()
	m.changeSvcRevision = uuid.New().String()
	m.changeRoutingRevision = uuid.New().String()
	m.setupChangeSvc()

	m.mockServer.RegisterNamespace(&namingpb.Namespace{
		Name:  &wrappers.StringValue{Value: recoverAllNs},
		Token: &wrappers.StringValue{Value: uuid.New().String()},
	})
	m.recoverAllService = util.BuildNamingService(recoverAllNs, recoverAllSvc, uuid.New().String())
	m.mockServer.RegisterService(m.recoverAllService)
	m.mockServer.GenInstancesWithStatus(m.recoverAllService, 2, mock.UnhealthyStatus, 2048)

	namingpb.RegisterPolarisGRPCServer(m.grpcServer, m.mockServer)
	m.discoverLisenter, err = net.Listen("tcp", fmt.Sprintf("%s:%d", discoverIp, discoverPort))
	if nil != err {
		log.Fatal(fmt.Sprintf("error listening appserver %v", err))
	}
	log.Printf("appserver listening on %s:%d\n", discoverIp, discoverPort)
	util.StartGrpcServer(m.grpcServer, m.discoverLisenter)

}

//设置changeSvc
func (m MonitorReportSuite) setupChangeSvc() {
	changeService := util.BuildNamingService(changedNs, changedSvc, m.changeSvcToken)
	m.mockServer.RegisterNamespace(&namingpb.Namespace{
		Name:  &wrappers.StringValue{Value: changedNs},
		Token: &wrappers.StringValue{Value: m.changeSvcToken},
	})
	changeSvcKey := model.ServiceKey{Namespace: changedNs, Service: changedSvc}
	m.mockServer.RegisterService(changeService)
	m.mockServer.GenTestInstances(changeService, 4)
	m.mockServer.SetInstanceStatus(changeSvcKey, 0, true, false, 50)
	m.mockServer.SetInstanceStatus(changeSvcKey, 1, false, false, 50)
	m.mockServer.SetInstanceStatus(changeSvcKey, 2, false, true, 50)
	m.mockServer.SetInstanceStatus(changeSvcKey, 3, false, false, 50)
	m.mockServer.SetServiceRevision(m.changeSvcToken, m.changeSvcRevision, model.ServiceEventKey{
		ServiceKey: changeSvcKey,
		Type:       model.EventInstances,
	})

	m.changeSvcRouting = &namingpb.Routing{
		Service:   &wrappers.StringValue{Value: changedSvc},
		Namespace: &wrappers.StringValue{Value: changedNs},
		Inbounds:  nil,
		Outbounds: []*namingpb.Route{
			{
				//指定源服务为任意服务, 否则因为没有sourceServiceInfo会匹配不了
				Sources: []*namingpb.Source{
					{Service: &wrappers.StringValue{Value: "*"}, Namespace: &wrappers.StringValue{Value: "*"}},
				},
				//根据不同逻辑set来进行目标服务分区路由
				Destinations: []*namingpb.Destination{
					{
						Metadata: nil,
						Priority: &wrappers.UInt32Value{Value: 1},
						Weight:   &wrappers.UInt32Value{Value: 100},
					},
				},
			},
		},
		Ctime:        &wrappers.StringValue{Value: time.Now().String()},
		Mtime:        &wrappers.StringValue{Value: time.Now().String()},
		Revision:     &wrappers.StringValue{Value: m.changeRoutingRevision},
		ServiceToken: &wrappers.StringValue{Value: m.changeSvcToken},
	}
	m.mockServer.InsertRouting(model.ServiceKey{Namespace: changedNs, Service: changedSvc}, m.changeSvcRouting)

}

//关闭测试套件
func (m *MonitorReportSuite) TearDownSuite(c *check.C) {
	m.grpcServer.Stop()
	util.DeleteDir(util.BackupDir)
}

//产生随机的方法调用次数
func (m *MonitorReportSuite) initStatNum() {
	GetOneInstanceSuccessNum = rand.Intn(20) + 1
	GetOneInstanceFailNum = rand.Intn(20) + 1

	GetInstancesSuccessNum = rand.Intn(20) + 1
	GetInstancesFailNum = rand.Intn(20) + 1

	GetRouteRuleSuccessNum = rand.Intn(20) + 1
	GetRouteRuleFailNum = rand.Intn(20) + 1

	ServiceCallSuccessNum = rand.Intn(20) + 2
	ServiceCallFailNum = rand.Intn(20) + 1

	RegisterSuccessNum = rand.Intn(20) + 1
	RegisterFailNum = rand.Intn(20) + 1

	DeregisterSuccessNum = rand.Intn(20) + 1
	DeregisterFailNum = rand.Intn(20) + 1

	HeartbeatSuccessNum = rand.Intn(20) + 1
	HeartbeatFailNum = rand.Intn(20) + 1

	GetQuotaSuccessNum = rand.Intn(20) + 1
	GetQuotaFailNum = rand.Intn(20) + 1

	GetMeshSuccessNum = rand.Intn(20) + 1
	GetMeshFailNum = rand.Intn(20) + 1

}

//测试consumerAPI方法的上报
func (m *MonitorReportSuite) TestMonitorReportConsumer(c *check.C) {
	log.Printf("Start TestMonitorReportConsumer")
	consumer, err := api.NewConsumerAPIByFile("testdata/monitor.yaml")
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()
	defer util.DeleteDir(util.BackupDir)

	//请求一个实例的请求（错误的）
	request := &api.GetOneInstanceRequest{}
	request.FlowID = 1111

	//预热获取monitor
	request.Namespace = "Polaris"
	request.Service = "polaris.monitor"
	_, err = consumer.GetOneInstance(request)
	//不会发生路由错误
	c.Assert(err, check.IsNil)
	log.Printf("expected getting monitor error %v", err)

	//测试getOneInstance
	m.checkGetOneInstance(c, consumer)

	//测试网格规则获取
	m.checkGetMeshConfig(c, consumer)

	//获取路由请求
	routeRequest := &api.GetServiceRuleRequest{}
	routeRequest.FlowID = 1112
	//命名空间和服务名有误
	routeRequest.Namespace = calledNs + "err"
	routeRequest.Service = calledSvc + "err"

	for i := 0; i < GetRouteRuleFailNum; i++ {
		_, er := consumer.GetRouteRule(routeRequest)
		c.Assert(er, check.NotNil)
	}

	//恢复正确
	routeRequest.Namespace = calledNs
	routeRequest.Service = calledSvc

	for i := 0; i < GetRouteRuleSuccessNum; i++ {
		_, er := consumer.GetRouteRule(routeRequest)
		c.Assert(er, check.IsNil)
	}

	//测试获取所有实例和上报
	m.checkGetInstancesAndReport(c, consumer)
	m.checkInitCalleeService(c, consumer)

	log.Printf("TestMonitorReportConsumer waiting 40s to upload stat\n")
	time.Sleep(50 * time.Second)
}

//测试consumer的getOneInstance
func (m *MonitorReportSuite) checkGetOneInstance(c *check.C, consumer api.ConsumerAPI) {
	request := &api.GetOneInstanceRequest{}
	request.FlowID = 1111

	//恢复正确命名空间和服务名
	request.Namespace = calledNs
	request.Service = calledSvc

	m.mockServer.SetReturnException(true)
	_, err := consumer.GetOneInstance(request)
	c.Assert(err, check.NotNil)
	log.Printf("expected err %v", err)
	m.mockServer.SetReturnException(false)

	for i := 0; i < GetOneInstanceSuccessNum; i++ {
		_, er := consumer.GetOneInstance(request)
		c.Assert(er, check.IsNil)
	}

	//命名空间和服务名有误
	request.Namespace = calledNs + "err"
	request.Service = calledSvc + "err"

	for i := 0; i < GetOneInstanceFailNum; i++ {
		_, err = consumer.GetOneInstance(request)
		c.Assert(err, check.NotNil)
	}
}

//测试网格获取
func (m *MonitorReportSuite) checkGetMeshConfig(c *check.C, consumer api.ConsumerAPI) {
	//添加辅助服务
	testbus := "ExistBusiness"
	consumerNamespace := "testns"

	//添加server网格规则
	m.mockServer.RegisterMeshConfig(&namingpb.Service{
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
	m.mockServer.RegisterService(testService)

	request := &api.GetMeshConfigRequest{}
	request.FlowID = 1111
	request.Namespace = consumerNamespace
	request.MeshId = testbus
	request.MeshType = api.MeshVirtualService

	//resp, err := consumer.GetMeshConfig(request)

	m.mockServer.SetReturnException(true)
	_, err := consumer.GetMeshConfig(request)
	c.Assert(err, check.NotNil)
	log.Printf("expected err %v", err)
	m.mockServer.SetReturnException(false)

	for i := 0; i < GetMeshSuccessNum; i++ {
		_, err := consumer.GetMeshConfig(request)
		c.Assert(err, check.IsNil)
	}

	//命名空间和服务名有误
	request.Namespace = calledNs + "err2"
	request.Business = calledSvc + "err2"
	m.mockServer.SetNotRegisterAssistant(true)
	for i := 0; i < GetMeshFailNum; i++ {
		_, err = consumer.GetMeshConfig(request)
		c.Assert(err, check.NotNil)
		log.Println("GetMeshConfig", err)
	}
	m.mockServer.SetNotRegisterAssistant(false)
}

//测试获取所有实例和上报调用结果
func (m *MonitorReportSuite) checkGetInstancesAndReport(c *check.C, consumer api.ConsumerAPI) {
	//获取所有实例请求
	requests := &api.GetInstancesRequest{}
	requests.FlowID = 1113
	requests.Namespace = calledNs
	requests.Service = calledSvc

	var calledInstance model.Instance

	for i := 0; i < GetInstancesSuccessNum; i++ {
		resp, er := consumer.GetInstances(requests)
		c.Assert(er, check.IsNil)
		calledInstance = resp.GetInstances()[0]
	}

	//有误的命名空间和服务名
	requests.Service = calledSvc + "err"
	requests.Namespace = calledNs + "err"

	for i := 0; i < GetInstancesFailNum; i++ {
		_, er := consumer.GetInstances(requests)
		c.Assert(er, check.NotNil)
	}

	//上报的服务调用结果
	result := &api.ServiceCallResult{}
	result.CalledInstance = calledInstance
	result.Delay = model.ToDurationPtr(50 * time.Millisecond)
	result.RetStatus = model.RetFail
	result.SetRetCode(-1)
	for i := 0; i < ServiceCallFailNum; i++ {
		er := consumer.UpdateServiceCallResult(result)
		c.Assert(er, check.IsNil)
	}

	result.SetRetCode(1)
	//将结果改为成功
	result.RetStatus = model.RetSuccess
	//先上传一个记录
	er := consumer.UpdateServiceCallResult(result)
	c.Assert(er, check.IsNil)
	//等待上报周期过去
	time.Sleep(50 * time.Second)
	//再次恢复上报，测试跨周期上报服务调用
	for i := 1; i < ServiceCallSuccessNum; i++ {
		er := consumer.UpdateServiceCallResult(result)
		c.Assert(er, check.IsNil)
	}
}

func (m *MonitorReportSuite) checkInitCalleeService(c *check.C, consumer api.ConsumerAPI) {
	req := api.InitCalleeServiceRequest{}
	t1 := time.Second * 1
	req.Timeout = &t1
	req.Namespace = calledNs
	req.Service = calledSvc
	err := consumer.InitCalleeService(&req)
	c.Assert(err, check.IsNil)
}

//实例状态修改类型
type changeStatus struct {
	healthy bool
	isolate bool
	weight  uint32
}

//对changeSvc实例状态进行修改的表
var instChanges = [][]changeStatus{
	{
		{true, true, 100},
		{true, false, 200},
		{false, false, 400},
	},
	{
		{true, true, 100},
		{false, false, 200},
	},
	{
		{true, false, 100},
		{true, false, 200},
	},
	{
		{true, false, 100},
		{false, true, 200},
	},
}

//测试上报缓存信息的statReporter
func (m *MonitorReportSuite) TestReportCacheInfo(c *check.C) {
	log.Printf("Start TestReportCacheInfo, changeSvcToken: %s", m.changeSvcToken)
	defer util.DeleteDir(util.BackupDir)
	//获取所有实例请求
	svcRequests := &api.GetInstancesRequest{}
	svcRequests.FlowID = 1113
	svcRequests.Namespace = changedNs
	svcRequests.Service = changedSvc

	routingRequests := &api.GetServiceRuleRequest{}
	routingRequests.FlowID = 1134
	routingRequests.Namespace = changedNs
	routingRequests.Service = changedSvc

	revisionsOfSvc := []string{uuid.New().String(), uuid.New().String(), uuid.New().String()}

	revisionsOfRouting := []string{uuid.New().String(), uuid.New().String(), uuid.New().String()}

	configuration, err := getCacheInfoConfiguration()
	c.Assert(err, check.IsNil)

	m.mockServer.SetPrintDiscoverReturn(true)
	configuration.Consumer.LocalCache.ServiceRefreshInterval = model.ToDurationPtr(100 * time.Millisecond)
	consumer, err := api.NewConsumerAPIByConfig(configuration)
	c.Assert(err, check.IsNil)
	_, err = consumer.GetInstances(svcRequests)
	c.Assert(err, check.IsNil)
	_, err = consumer.GetRouteRule(routingRequests)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()
	fmt.Printf("init revisionsOfSvc: %s, len: %d\n", revisionsOfSvc, len(revisionsOfSvc))
	setInstHealthy := false
	setInstIsolate := true
	setInstWeight := uint32(100)
	for i := 0; i < 3; i++ {
		svcKey := model.ServiceKey{Namespace: changedNs, Service: changedSvc}
		m.setRevisionOfInstance(i, c)
		m.mockServer.SetServiceRevision(m.changeSvcToken, revisionsOfSvc[i], model.ServiceEventKey{
			ServiceKey: svcKey,
			Type:       model.EventInstances,
		})
		setInstHealthy = !setInstHealthy
		setInstIsolate = !setInstIsolate
		setInstWeight = 300 - setInstWeight
		m.mockServer.SetServiceRevision(m.changeSvcToken, revisionsOfRouting[i], model.ServiceEventKey{
			ServiceKey: svcKey,
			Type:       model.EventRouting,
		})
		log.Printf("sleeping 5s for updating cache")
		time.Sleep(5 * time.Second)
	}
	m.mockServer.DeregisterService(changedNs, changedSvc)
	revisionsOfSvc = append(revisionsOfSvc, m.changeSvcRevision, "")
	fmt.Printf("after append, len of revisionsOfSvc: %d\n", len(revisionsOfSvc))
	revisionsOfRouting = append(revisionsOfRouting, m.changeRoutingRevision, "")
	sort.Sort(sort.StringSlice(revisionsOfSvc))
	sort.Sort(sort.StringSlice(revisionsOfRouting))
	log.Printf("all svc revisions changed: %v", revisionsOfSvc)
	log.Printf("all routing revisions changed: %v", revisionsOfRouting)
	log.Printf("sleeping 15s for reporting cache")
	time.Sleep(15 * time.Second)
	var svcRevisionsReport []string
	var routingRevisionsReport []string

	c.Assert(len(svcRevisionsReport), check.Equals, len(routingRevisionsReport))
	c.Assert(len(revisionsOfRouting), check.Equals, len(revisionsOfSvc))
	fmt.Printf("len of revisionsOfSvc: %d\n", len(revisionsOfSvc))
	c.Assert(len(revisionsOfSvc), check.Equals, len(svcRevisionsReport))
	sort.Sort(sort.StringSlice(svcRevisionsReport))
	fmt.Printf("expect instances %s\n", revisionsOfSvc)
	fmt.Printf("obtain instances %s\n", svcRevisionsReport)
	sort.Sort(sort.StringSlice(routingRevisionsReport))
	fmt.Printf("expect routing %s\n", revisionsOfRouting)
	fmt.Printf("obtain routing %s\n", routingRevisionsReport)

	for i := 0; i < len(revisionsOfSvc); i++ {
		c.Assert(svcRevisionsReport[i], check.Equals, revisionsOfSvc[i])
		c.Assert(routingRevisionsReport[i], check.Equals, revisionsOfRouting[i])
	}
}

//获取测试cacheInfo时使用的配置
func getCacheInfoConfiguration() (*config.ConfigurationImpl, error) {
	configuration, err := config.LoadConfigurationByFile("testdata/monitor.yaml")
	if err != nil {
		return nil, err
	}
	return configuration, nil
}

//设置changedSvc服务实例状态
func (m *MonitorReportSuite) setRevisionOfInstance(loopCount int, c *check.C) {
	svcKey := model.ServiceKey{
		Namespace: changedNs,
		Service:   changedSvc,
	}
	for i := 0; i < 4; i++ {
		if loopCount < len(instChanges[i]) {
			err := m.mockServer.SetInstanceStatus(svcKey, i, instChanges[i][loopCount].healthy,
				instChanges[i][loopCount].isolate, instChanges[i][loopCount].weight)
			c.Assert(err, check.IsNil)
		}
	}
}

//检测两个[]string是否相等
func (m *MonitorReportSuite) checkStringArrayEqual(arr1 []string, arr2 []string, c *check.C) {
	c.Assert(len(arr1), check.Equals, len(arr2))
	for idx, e1 := range arr1 {
		c.Assert(e1, check.Equals, arr2[idx])
	}
}

//从文件中读取rateLimit的定义
func readRateLimitRuleFromFile(path string, singleRule bool) (proto.Message, error) {
	buf, err := ioutil.ReadFile(path)
	if nil != err {
		return nil, err
	}
	if singleRule {
		rule := &namingpb.Rule{}
		if err = jsonpb.UnmarshalString(string(buf), rule); nil != err {
			return nil, err
		}
		return rule, err
	}
	rateLimit := &namingpb.RateLimit{}
	if err = jsonpb.UnmarshalString(string(buf), rateLimit); nil != err {
		return nil, err
	}
	return rateLimit, err
}

//设置限流规则里面的单独一个规则
func (m *MonitorReportSuite) setSingleRateLimitRule(rules *namingpb.RateLimit, rule *namingpb.Rule,
	singleRules map[string]string, totalRevision string) {
	var exists bool
	for idx, singleRule := range rules.GetRules() {
		if singleRule.GetId().GetValue() == rule.GetId().GetValue() {
			rules.Rules[idx] = rule
			exists = true
		}
	}
	if !exists {
		rules.Rules = append(rules.Rules, rule)
	}
	m.setRateLimitRuleRevisions(rules, singleRules, totalRevision)
}

//设置限流规则的revision
func (m *MonitorReportSuite) setRateLimitRuleRevisions(rule *namingpb.RateLimit,
	singleRules map[string]string, totalRevision string) {
	log.Printf("set revisions: total: %v, single: %v", totalRevision, singleRules)
	rule.Revision = &wrappers.StringValue{Value: totalRevision}
	for _, rule := range rule.GetRules() {
		revision, ok := singleRules[rule.GetId().GetValue()]
		if ok {
			rule.Revision = &wrappers.StringValue{Value: revision}
		}
	}
}

//测试全死全活上报
func (m *MonitorReportSuite) TestRecoverAllReport(c *check.C) {
	log.Printf("TestRecoverAllReport")
	defer util.DeleteDir(util.BackupDir)
	configuration, err := config.LoadConfigurationByFile("testdata/monitor.yaml")
	c.Assert(err, check.IsNil)
	configuration.Global.StatReporter.Chain = []string{config.DefaultCacheReporter}
	configuration.Consumer.LocalCache.ServiceRefreshInterval = model.ToDurationPtr(10 * time.Millisecond)
	percent := 0.5
	configuration.Consumer.ServiceRouter.PercentOfMinInstances = &percent

	svcRequests := &api.GetInstancesRequest{}
	svcRequests.FlowID = 1114
	svcRequests.SkipRouteFilter = false
	svcRequests.Namespace = recoverAllNs
	svcRequests.Service = recoverAllSvc
	consumer, err := api.NewConsumerAPIByConfig(configuration)
	c.Assert(err, check.IsNil)
	//这时请求的服务都是不健康实例，触发全死全活
	_, err = consumer.GetInstances(svcRequests)
	c.Assert(err, check.IsNil)
	_, err = consumer.GetInstances(svcRequests)
	c.Assert(err, check.IsNil)
	m.mockServer.GenTestInstances(m.recoverAllService, 3)
	time.Sleep(5 * time.Second)
	//这次请求时生成了健康的实例，关闭全死全活
	_, err = consumer.GetInstances(svcRequests)
	c.Assert(err, check.IsNil)
	time.Sleep(6 * time.Second)
}

////检测插件接口统计信息
//func (m *MonitorReportSuite) checkPluginStat(c *check.C) {
//	pluginStats := m.monitorServer.GetPluginStat()
//	c.Assert(len(pluginStats) > 0, check.Equals, true)
//	for _, p := range pluginStats {
//		for _, r := range p.Results {
//			c.Assert(r.TotalRequestsPerMinute > 0, check.Equals, true)
//		}
//	}
//}

//检测当前添加的错误码不会返回ErrCodeUnknown
func (m *MonitorReportSuite) TestErrorCodeUnknown(c *check.C) {
	log.Printf("Start TestErrorCodeUnknown")
	code := model.ErrCodeUnknown + 1
	for i := 0; i < model.ErrCodeCount-2; i++ {
		codeStr := model.ErrCodeToString(code)
		log.Printf("errCode: %s, %d", codeStr, int(code))
		c.Assert(codeStr != "ErrCodeUnknown", check.Equals, true)
		code++
	}
	//确保不会出现panic
	for i := 0; i < int(model.ErrCodeCount); i++ {
		model.ErrCodeFromIndex(i)
	}
}

//检测当connectionManager不ready的时候，是否会跳过第一次reportConfig
//现在通过检测日志可以看出是不是跳过了第一次reportConfig，还无法进行自动判断
func (m *MonitorReportSuite) TestConfigNotReady(c *check.C) {
	log.Printf("Start TestConfigNotReady")
	m.mockServer.SetFirstNoReturn(model.ServiceEventKey{
		ServiceKey: model.ServiceKey{
			Namespace: "Polaris",
			Service:   "polaris.discover",
		},
		Type: model.EventInstances,
	})
	consumer, err := api.NewConsumerAPIByFile("testdata/monitor.yaml")
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()
	m.mockServer.UnsetFirstNoReturn(model.ServiceEventKey{
		ServiceKey: model.ServiceKey{
			Namespace: "Polaris",
			Service:   "polaris.discover",
		},
		Type: model.EventInstances,
	})
	time.Sleep(1 * time.Second)
}
