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
	"google.golang.org/grpc"
	"gopkg.in/check.v1"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/pkg/version"
	"github.com/polarismesh/polaris-go/plugin/statreporter/tencent/monitor"
	monitorpb "github.com/polarismesh/polaris-go/plugin/statreporter/tencent/pb/v1"
	"github.com/polarismesh/polaris-go/plugin/statreporter/tencent/serviceinfo"
	"github.com/polarismesh/polaris-go/test/mock"
	"github.com/polarismesh/polaris-go/test/util"
)

// consumerAPI各种方法的调用次数
var (
	// GetOneInstanceSuccessNum 同步获取一个实例的成功失败数
	GetOneInstanceSuccessNum = 0
	GetOneInstanceFailNum    = 0

	// GetInstancesSuccessNum 同步获取实例的成功失败数
	GetInstancesSuccessNum = 0
	GetInstancesFailNum    = 0

	// GetRouteRuleSuccessNum 同步获取路由规则
	GetRouteRuleSuccessNum = 0
	GetRouteRuleFailNum    = 0

	// ServiceCallSuccessNum 上报服务调用的次数
	ServiceCallSuccessNum = 0
	ServiceCallFailNum    = 0
)

// providerAPI各种方法调用次数
var (
	// RegisterSuccessNum 注册实例的成功失败数
	RegisterSuccessNum = 0
	RegisterFailNum    = 0

	// DeregisterSuccessNum 反注册实例的成功失败数
	DeregisterSuccessNum = 0
	DeregisterFailNum    = 0

	// HeartbeatSuccessNum 心跳的成功失败数
	HeartbeatSuccessNum = 0
	HeartbeatFailNum    = 0

	// GetQuotaSuccessNum 限流的成功失败数
	GetQuotaSuccessNum = 0
	GetQuotaFailNum    = 0
)

const (
	// polaris-server的IP端口
	discoverIP   = "127.0.0.1"
	discoverPort = 8180

	// 上报的服务调用
	calledSvc = "calledSvc"
	calledNs  = "calledNs"

	changedSvc = "changedSvc"
	changedNs  = "changedNs"

	recoverAllSvc = "recoverAllSvc"
	recoverAllNs  = "recoverAllNs"

	// 用于测试的限流规则的文件路径
	rateLimitRulesPath = "testdata/ratelimit_rule/rate_limit_total.json"
	rateLimitRule2Path = "testdata/ratelimit_rule/rate_limit_rule2.json"
	// rateLimit里面两个rule的id
	rateLimitRule1Id = "rule1"
	rateLimitRule2Id = "rule2"
)

// MonitorReportSuite 上报插件测试套件
type MonitorReportSuite struct {
	mockServer    mock.NamingServer
	monitorServer mock.MonitorServer
	// SERVER
	grpcServer *grpc.Server
	// MONITOR
	grpcMonitor           *grpc.Server
	discoverLisenter      net.Listener
	monitorListener       net.Listener
	discoverToken         string
	monitorToken          string
	serviceToken          string
	instanceID            string
	instPort              uint32
	instHost              string
	changeSvcToken        string
	changeSvcRevision     string
	changeSvcRouting      *namingpb.Routing
	changeRoutingRevision string

	recoverAllService *namingpb.Service
}

// SetUpSuite 初始化套件
func (m *MonitorReportSuite) SetUpSuite(c *check.C) {
	m.initStatNum()

	// get the grpc server wired up
	grpc.EnableTracing = true

	var err error

	m.grpcServer = util.GetGrpcServer()
	m.discoverToken = uuid.New().String()
	m.grpcMonitor = util.GetGrpcServer()

	m.monitorServer = mock.NewMonitorServer()

	m.mockServer = mock.NewNamingServer()
	m.mockServer.RegisterServerServices(discoverIP, discoverPort)

	m.monitorToken = m.mockServer.RegisterServerService(config.ServerMonitorService)
	m.mockServer.RegisterServerInstance(
		mock.MonitorIP, mock.MonitorPort, config.ServerMonitorService, m.monitorToken, true)
	m.mockServer.RegisterRouteRule(util.BuildNamingService(config.ServerNamespace, config.ServerMonitorService, ""),
		m.mockServer.BuildRouteRule(config.ServerNamespace, config.ServerMonitorService))

	m.mockServer.RegisterNamespace(&namingpb.Namespace{
		Name:    &wrappers.StringValue{Value: calledNs},
		Comment: &wrappers.StringValue{Value: "for service call stat upload"},
		Owners:  &wrappers.StringValue{Value: "monitor_reporter"},
		Token:   &wrappers.StringValue{Value: m.monitorToken},
	})

	m.serviceToken = uuid.New().String()
	testService := util.BuildNamingService(calledNs, calledSvc, m.serviceToken)
	m.mockServer.RegisterService(testService)
	m.mockServer.GenTestInstances(testService, 3)
	m.mockServer.GenInstancesWithStatus(testService, 2, mock.IsolatedStatus, 2048)
	m.mockServer.GenInstancesWithStatus(testService, 1, mock.UnhealthyStatus, 4096)
	// 获取一个被调服务实例的信息，用于后面调用providerApi
	svcKey := &model.ServiceKey{Namespace: testService.GetNamespace().GetValue(),
		Service: testService.GetName().GetValue()}
	m.instanceID = m.mockServer.GetServiceInstances(svcKey)[0].GetId().GetValue()
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
	m.discoverLisenter, err = net.Listen("tcp", fmt.Sprintf("%s:%d", discoverIP, discoverPort))
	if err != nil {
		log.Fatal(fmt.Sprintf("error listening appserver %v", err))
	}
	log.Printf("appserver listening on %s:%d\n", discoverIP, discoverPort)
	util.StartGrpcServer(m.grpcServer, m.discoverLisenter)

	monitorpb.RegisterGrpcAPIServer(m.grpcMonitor, m.monitorServer)
	m.monitorListener, err = net.Listen("tcp", fmt.Sprintf("%s:%d", mock.MonitorIP, mock.MonitorPort))
	if err != nil {
		log.Fatal(fmt.Sprintf("error listening monitor %v", err))
	}
	log.Printf("appserver listening on %s:%d\n", mock.MonitorIP, mock.MonitorPort)
	util.StartGrpcServer(m.grpcMonitor, m.monitorListener)
}

// 设置changeSvc
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
				// 指定源服务为任意服务, 否则因为没有sourceServiceInfo会匹配不了
				Sources: []*namingpb.Source{
					{Service: &wrappers.StringValue{Value: "*"}, Namespace: &wrappers.StringValue{Value: "*"}},
				},
				// 根据不同逻辑set来进行目标服务分区路由
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

// TearDownSuite 关闭测试套件
func (m *MonitorReportSuite) TearDownSuite(c *check.C) {
	m.grpcMonitor.Stop()
	m.grpcServer.Stop()
	util.DeleteDir(util.BackupDir)
}

// 产生随机的方法调用次数
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

}

// TestMonitorReportConsumer 测试consumerAPI方法的上报
func (m *MonitorReportSuite) TestMonitorReportConsumer(c *check.C) {
	m.monitorServer.SetSdkStat(nil)
	m.monitorServer.SetSvcStat(nil)
	log.Printf("Start TestMonitorReportConsumer")
	consumer, err := api.NewConsumerAPIByFile("testdata/monitor.yaml")
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()
	defer util.DeleteDir(util.BackupDir)

	// 请求一个实例的请求（错误的）
	request := &api.GetOneInstanceRequest{}
	request.FlowID = 1111

	// 预热获取monitor
	request.Namespace = "Polaris"
	request.Service = "polaris.monitor"
	_, err = consumer.GetOneInstance(request)
	// 不会发生路由错误
	c.Assert(err, check.IsNil)
	log.Printf("expected getting monitor error %v", err)

	// 测试getOneInstance
	m.checkGetOneInstance(c, consumer)

	// 获取路由请求
	routeRequest := &api.GetServiceRuleRequest{}
	routeRequest.FlowID = 1112
	// 命名空间和服务名有误
	routeRequest.Namespace = calledNs + "err"
	routeRequest.Service = calledSvc + "err"

	for i := 0; i < GetRouteRuleFailNum; i++ {
		_, er := consumer.GetRouteRule(routeRequest)
		c.Assert(er, check.NotNil)
	}

	// 恢复正确
	routeRequest.Namespace = calledNs
	routeRequest.Service = calledSvc

	for i := 0; i < GetRouteRuleSuccessNum; i++ {
		_, er := consumer.GetRouteRule(routeRequest)
		c.Assert(er, check.IsNil)
	}

	// 测试获取所有实例和上报
	m.checkGetInstancesAndReport(c, consumer)
	m.checkInitCalleeService(c, consumer)

	log.Printf("TestMonitorReportConsumer waiting 40s to upload stat\n")
	time.Sleep(50 * time.Second)
	sdkStat := m.monitorServer.GetSdkStat()
	svcStat := m.monitorServer.GetSvcStat()
	m.checkConsumerStat(sdkStat, svcStat, c)
	// 检测配置信息有没有上传成功
	sdkCfg := m.monitorServer.GetSdkCfg()
	m.monitorServer.SetSdkCfg(nil)
	c.Assert(len(sdkCfg) > 0, check.Equals, true)
	m.monitorServer.SetPluginStat(nil)
}

// 测试consumer的getOneInstance
func (m *MonitorReportSuite) checkGetOneInstance(c *check.C, consumer api.ConsumerAPI) {
	request := &api.GetOneInstanceRequest{}
	request.FlowID = 1111

	// 恢复正确命名空间和服务名
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

	// 命名空间和服务名有误
	request.Namespace = calledNs + "err"
	request.Service = calledSvc + "err"

	for i := 0; i < GetOneInstanceFailNum; i++ {
		_, err = consumer.GetOneInstance(request)
		c.Assert(err, check.NotNil)
	}
}

// 测试获取所有实例和上报调用结果
func (m *MonitorReportSuite) checkGetInstancesAndReport(c *check.C, consumer api.ConsumerAPI) {
	// 获取所有实例请求
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

	// 有误的命名空间和服务名
	requests.Service = calledSvc + "err"
	requests.Namespace = calledNs + "err"

	for i := 0; i < GetInstancesFailNum; i++ {
		_, er := consumer.GetInstances(requests)
		c.Assert(er, check.NotNil)
	}

	// 上报的服务调用结果
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
	// 将结果改为成功
	result.RetStatus = model.RetSuccess
	// 先上传一个记录
	er := consumer.UpdateServiceCallResult(result)
	c.Assert(er, check.IsNil)
	// 等待上报周期过去
	time.Sleep(50 * time.Second)
	// 再次恢复上报，测试跨周期上报服务调用
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

// 检查consumerAPi调用统计
func (m *MonitorReportSuite) checkConsumerStat(sdkStat []*monitorpb.SDKAPIStatistics,
	svcStat []*monitorpb.ServiceStatistics, c *check.C) {
	uploadGetInstancesSuccessNum := 0
	uploadGetInstancesFailNum := 0
	uploadGetOneInstanceSuccessNum := 0
	uploadGetOneInstanceFailNum := 0
	uploadGetRouteRuleSuccessNum := 0
	uploadGetRouteRuleFailNum := 0
	uploadServiceCallResultSuccessNum := 0
	uploadServiceCallResultFailNum := 0
	// mesh
	//uploadGetMeshSuccessNum := 0
	//uploadGetMeshFailNum := 0

	execptionFailNum := 0

	uploadGetInitCalleeSuccessNum := 0
	uploadGetInitCalleeFailNum := 0

	for _, s := range sdkStat {
		c.Assert(s.GetKey().GetClientVersion().GetValue() == version.Version, check.Equals, true)
		c.Assert(s.GetKey().GetClientType().GetValue() == version.ClientType, check.Equals, true)
		c.Assert(s.GetKey().GetClientHost().GetValue() == discoverIP, check.Equals, true)
		switch s.GetKey().GetSdkApi().GetValue() {
		case model.ApiGetInstances.String():
			// 因为获取系统服务时也会使用同步api，
			// 所以成功失败数应该大于等于上报服务调用信息次数
			if s.GetKey().GetSuccess().GetValue() {
				uploadGetInstancesSuccessNum += int(s.GetValue().GetTotalRequestPerMinute().GetValue())
			} else {
				uploadGetInstancesFailNum += int(s.GetValue().GetTotalRequestPerMinute().GetValue())
				if s.GetKey().GetResult() == monitorpb.APIResultType_PolarisFail {
					execptionFailNum++
				}
				// c.Assert(s.GetKey().GetResult().String(), check.Equals, monitorpb.APIResultType_UserFail.String())
			}
		case model.ApiGetOneInstance.String():
			// 因为获取系统服务时也会使用同步api，
			// 所以成功失败数应该大于等于上报服务调用信息次数
			if s.GetKey().GetSuccess().GetValue() {
				uploadGetOneInstanceSuccessNum += int(s.GetValue().GetTotalRequestPerMinute().GetValue())
			} else {
				uploadGetOneInstanceFailNum += int(s.GetValue().GetTotalRequestPerMinute().GetValue())
				if s.GetKey().GetResult() == monitorpb.APIResultType_PolarisFail {
					execptionFailNum++
				}
				// c.Assert(s.GetKey().GetResult().String(), check.Equals, monitorpb.APIResultType_UserFail.String())
			}
		case model.ApiGetRouteRule.String():
			if s.GetKey().GetSuccess().GetValue() {
				uploadGetRouteRuleSuccessNum += int(s.GetValue().GetTotalRequestPerMinute().GetValue())
			} else {
				uploadGetRouteRuleFailNum += int(s.GetValue().GetTotalRequestPerMinute().GetValue())
				c.Assert(s.GetKey().GetResult().String(), check.Equals, monitorpb.APIResultType_UserFail.String())
			}
		case model.ApiUpdateServiceCallResult.String():
			if s.GetKey().GetSuccess().GetValue() {
				uploadServiceCallResultSuccessNum += int(s.GetValue().GetTotalRequestPerMinute().GetValue())
			} else {
				uploadServiceCallResultFailNum += int(s.GetValue().GetTotalRequestPerMinute().GetValue())
				c.Assert(s.GetKey().GetResult().String(), check.Equals, monitorpb.APIResultType_UserFail.String())
			}
		case model.ApiInitCalleeServices.String():
			if s.GetKey().GetSuccess().GetValue() {
				uploadGetInitCalleeSuccessNum += int(s.GetValue().GetTotalRequestPerMinute().GetValue())
			} else {
				uploadGetInitCalleeFailNum += int(s.GetValue().GetTotalRequestPerMinute().GetValue())
				c.Assert(s.GetKey().GetResult().String(), check.Equals, monitorpb.APIResultType_UserFail.String())
			}
		}

	}
	c.Assert(uploadGetInstancesSuccessNum >= GetInstancesSuccessNum, check.Equals, true)
	c.Assert(uploadGetInstancesFailNum >= 3*GetInstancesFailNum, check.Equals, true)
	c.Assert(uploadGetOneInstanceSuccessNum >= GetOneInstanceSuccessNum, check.Equals, true)
	log.Printf("uploadGetOneInstanceFailNum: %d, GetOneInstanceFailNum: %d", uploadGetOneInstanceFailNum, GetOneInstanceFailNum)
	c.Assert(uploadGetOneInstanceFailNum >= 3*GetOneInstanceFailNum, check.Equals, true)
	c.Assert(execptionFailNum, check.Equals, 1)
	c.Assert(uploadGetRouteRuleSuccessNum >= GetRouteRuleSuccessNum, check.Equals, true)
	c.Assert(uploadGetRouteRuleFailNum >= GetRouteRuleFailNum, check.Equals, true)
	fmt.Println("===================uploadGetInitCalleeSuccessNum", uploadGetInitCalleeSuccessNum)
	c.Assert(uploadGetInitCalleeSuccessNum >= uploadGetInitCalleeFailNum, check.Equals, true)

	hasCalledSvcStat := false
	var svcSuccessNum uint32
	for _, s := range svcStat {
		if s.GetKey().GetNamespace().GetValue() == "Polaris" {
			continue
		}
		hasCalledSvcStat = true
		c.Assert(s.GetKey().GetNamespace().GetValue() == calledNs, check.Equals, true)
		c.Assert(s.GetKey().GetService().GetValue() == calledSvc, check.Equals, true)
		if s.GetKey().GetSuccess().GetValue() {
			svcSuccessNum += s.GetValue().GetTotalRequestPerMinute().GetValue()
			c.Assert(s.GetKey().ResCode, check.Equals, int32(1))
		} else {
			c.Assert(s.GetValue().GetTotalRequestPerMinute().GetValue() == uint32(ServiceCallFailNum),
				check.Equals, true)
			c.Assert(s.GetKey().ResCode, check.Equals, int32(-1))
		}
	}
	log.Printf("received svcSuccessNum %d, expected ServiceCallSuccessNum %d", svcSuccessNum, ServiceCallSuccessNum)
	c.Assert(svcSuccessNum, check.Equals, uint32(ServiceCallSuccessNum))
	c.Assert(hasCalledSvcStat, check.Equals, true)
}

// TestMonitorReportProvider 测试providerapi方法上报统计情况
func (m *MonitorReportSuite) TestMonitorReportProvider(c *check.C) {
	m.monitorServer.SetSdkStat(nil)
	log.Printf("Start TestMonitorReportProvider")
	provider, err := api.NewProviderAPIByFile("testdata/monitor.yaml")
	c.Assert(err, check.IsNil)
	defer provider.Destroy()
	defer util.DeleteDir(util.BackupDir)

	log.Printf("TestMonitorReportProvider waiting 30s for system services ready\n")
	// 等待一段时间，让系统服务就绪
	time.Sleep(30 * time.Second)

	registerReq := &api.InstanceRegisterRequest{}
	registerReq.Namespace = calledNs
	registerReq.Service = calledSvc
	registerReq.ServiceToken = m.serviceToken + "err"
	registerReq.Host = discoverIP
	registerReq.Port = 78

	for i := 0; i < RegisterFailNum; i++ {
		_, err := provider.Register(registerReq)
		c.Assert(err, check.NotNil)
	}

	registerReq.ServiceToken = m.serviceToken
	for i := 0; i < RegisterSuccessNum; i++ {
		_, err := provider.Register(registerReq)
		c.Assert(err, check.IsNil)
	}

	deregReq := &api.InstanceDeRegisterRequest{}
	deregReq.Namespace = calledNs
	deregReq.Service = calledSvc
	deregReq.ServiceToken = m.serviceToken + "err"
	deregReq.Host = discoverIP
	deregReq.Port = 56

	for i := 0; i < DeregisterFailNum; i++ {
		err := provider.Deregister(deregReq)
		c.Assert(err, check.NotNil)
	}

	deregReq.ServiceToken = m.serviceToken
	for i := 0; i < DeregisterSuccessNum; i++ {
		err := provider.Deregister(deregReq)
		c.Assert(err, check.IsNil)
	}

	heartBeatReq := &api.InstanceHeartbeatRequest{}
	heartBeatReq.Namespace = calledNs
	heartBeatReq.Service = calledSvc
	heartBeatReq.Port = int(m.instPort)
	heartBeatReq.Host = m.instHost
	heartBeatReq.ServiceToken = m.serviceToken + "err"
	heartBeatReq.InstanceID = m.instanceID

	for i := 0; i < HeartbeatFailNum; i++ {
		err := provider.Heartbeat(heartBeatReq)
		c.Assert(err, check.NotNil)
	}

	heartBeatReq.ServiceToken = m.serviceToken
	for i := 0; i < HeartbeatSuccessNum; i++ {
		err := provider.Heartbeat(heartBeatReq)
		c.Assert(err, check.IsNil)
	}

	log.Printf("TestMonitorReportProvider waiting 40s to upload stat\n")
	time.Sleep(50 * time.Second)
	sdkStat := m.monitorServer.GetSdkStat()
	m.checkProviderStat(sdkStat, c)
	// 检测有没有上传配置信息成功
	sdkCfg := m.monitorServer.GetSdkCfg()
	m.monitorServer.SetSdkCfg(nil)
	c.Assert(len(sdkCfg) > 0, check.Equals, true)
	m.monitorServer.SetPluginStat(nil)
}

// TestMonitorReportLimitAPI 测试limitAPI方法上报统计情况
func (m *MonitorReportSuite) TestMonitorReportLimitAPI(c *check.C) {
	m.monitorServer.SetSdkStat(nil)
	log.Printf("Start TestMonitorReportLimitAPI")
	limit, err := api.NewLimitAPIByFile("testdata/monitor.yaml")
	c.Assert(err, check.IsNil)
	defer limit.Destroy()
	defer util.DeleteDir(util.BackupDir)

	limitReq := api.NewQuotaRequest()
	limitReq.SetNamespace(calledNs)
	limitReq.SetService(calledSvc)
	// 不需要触发限流，只需要获取到限流规则即可
	limitReq.SetLabels(map[string]string{"noKey": "noValue"})

	// 检查上报的错误码个数
	for i := 0; i < GetQuotaSuccessNum; i++ {
		_, err = limit.GetQuota(limitReq)
		fmt.Printf("GetQuota err is %v\n", err)
		c.Assert(err, check.IsNil)
	}

	limitReq.SetNamespace(calledNs + "err")
	for i := 0; i < GetQuotaFailNum; i++ {
		_, err = limit.GetQuota(limitReq)
		c.Assert(err, check.NotNil)
		log.Printf("expected err %v", err)
	}
	log.Printf("TestMonitorReportLimitAPI waiting 70s to upload stat\n")
	time.Sleep(70 * time.Second)
	sdkStat := m.monitorServer.GetSdkStat()
	m.checkLimitAPIStat(sdkStat, c)
	// 检测配置信息有没有上传成功
	sdkCfg := m.monitorServer.GetSdkCfg()
	m.monitorServer.SetSdkCfg(nil)
	c.Assert(len(sdkCfg) > 0, check.Equals, true)
	m.monitorServer.SetPluginStat(nil)
	m.monitorServer.SetCacheReport(nil)
}

// 检查limitAPI方法调用统计
func (m *MonitorReportSuite) checkLimitAPIStat(sdkStat []*monitorpb.SDKAPIStatistics, c *check.C) {
	uploadGetQuotaSuccessNum := 0
	uploadGetQuotaFailNum := 0

	for _, s := range sdkStat {
		c.Assert(s.GetKey().GetClientVersion().GetValue() == version.Version, check.Equals, true)
		c.Assert(s.GetKey().GetClientType().GetValue() == version.ClientType, check.Equals, true)
		c.Assert(s.GetKey().GetClientHost().GetValue() == discoverIP, check.Equals, true)
		switch s.GetKey().GetSdkApi().GetValue() {
		case model.ApiGetQuota.String():
			if s.GetKey().GetSuccess().GetValue() {
				uploadGetQuotaSuccessNum += int(s.GetValue().GetTotalRequestPerMinute().GetValue())
			} else {
				uploadGetQuotaFailNum += int(s.GetValue().GetTotalRequestPerMinute().GetValue())
			}
		}
	}
	log.Printf("GetQuotaFailNum: %d, uploadGetQuotaFailNum: %d", GetQuotaFailNum, uploadGetQuotaFailNum)
	log.Printf("GetQuotaSuccessNum: %d, uploadGetQuotaSuccessNum: %d", GetQuotaSuccessNum, uploadGetQuotaSuccessNum)
	// 一次调用会有2个错误码，一个是规则获取失败的错误码，一个是整体接口的错误码
	c.Assert(uploadGetQuotaFailNum, check.Equals, 2*GetQuotaFailNum)
	c.Assert(uploadGetQuotaSuccessNum, check.Equals, GetQuotaSuccessNum)
}

// 检测providerAPI调用统计
func (m *MonitorReportSuite) checkProviderStat(sdkStat []*monitorpb.SDKAPIStatistics, c *check.C) {
	providerNum := 0
	uploadRegisterFail := 0
	uploadRegisterSucc := 0
	uploadDeregisterFail := 0
	uploadDeregisterSucc := 0
	uploadHeartbeatFail := 0
	uploadHeartbeatSucc := 0
	for _, s := range sdkStat {
		// 统计信息的版本、类型、host要对的上
		c.Assert(s.GetKey().GetClientVersion().GetValue() == version.Version, check.Equals, true)
		c.Assert(s.GetKey().GetClientType().GetValue() == version.ClientType, check.Equals, true)
		c.Assert(s.GetKey().GetClientHost().GetValue() == discoverIP, check.Equals, true)
		switch s.GetKey().GetSdkApi().GetValue() {
		case model.ApiRegister.String():
			if s.GetKey().GetSuccess().GetValue() {
				uploadRegisterSucc += int(s.GetValue().GetTotalRequestPerMinute().GetValue())
			} else {
				uploadRegisterFail += int(s.GetValue().GetTotalRequestPerMinute().GetValue())
				c.Assert(s.GetKey().GetResult().String(), check.Equals, monitorpb.APIResultType_UserFail.String())
			}
			providerNum++
		case model.ApiDeregister.String():
			if s.GetKey().GetSuccess().GetValue() {
				uploadDeregisterSucc += int(s.GetValue().GetTotalRequestPerMinute().GetValue())
			} else {
				uploadDeregisterFail += int(s.GetValue().GetTotalRequestPerMinute().GetValue())
				c.Assert(s.GetKey().GetResult().String(), check.Equals, monitorpb.APIResultType_UserFail.String())
			}
			providerNum++
		case model.ApiHeartbeat.String():
			if s.GetKey().GetSuccess().GetValue() {
				uploadHeartbeatSucc += int(s.GetValue().GetTotalRequestPerMinute().GetValue())
			} else {
				uploadHeartbeatFail += int(s.GetValue().GetTotalRequestPerMinute().GetValue())
				c.Assert(s.GetKey().GetResult().String(), check.Equals, monitorpb.APIResultType_UserFail.String())
			}
			providerNum++
		}
	}
	fmt.Printf("providernum %d\n", providerNum)
	// 必须每种统计都有
	// c.Assert(providerNum >= 6, check.Equals, true)
	c.Assert(uploadRegisterSucc, check.Equals, RegisterSuccessNum)
	c.Assert(uploadRegisterFail, check.Equals, RegisterFailNum)
	c.Assert(uploadDeregisterSucc, check.Equals, DeregisterSuccessNum)
	c.Assert(uploadDeregisterFail, check.Equals, DeregisterFailNum)
	c.Assert(uploadHeartbeatSucc, check.Equals, HeartbeatSuccessNum)
	c.Assert(uploadHeartbeatFail, check.Equals, HeartbeatFailNum)
}

// 实例状态修改类型
type changeStatus struct {
	healthy bool
	isolate bool
	weight  uint32
}

// 对changeSvc实例状态进行修改的表
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

// TestReportCacheInfo 测试上报缓存信息的statReporter
func (m *MonitorReportSuite) TestReportCacheInfo(c *check.C) {
	log.Printf("Start TestReportCacheInfo, changeSvcToken: %s", m.changeSvcToken)
	defer util.DeleteDir(util.BackupDir)
	// 获取所有实例请求
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
	sort.Strings(revisionsOfSvc)
	sort.Strings(revisionsOfRouting)
	// sort.Sort(sort.StringSlice(revisionsOfSvc))
	// sort.Sort(sort.StringSlice(revisionsOfRouting))
	log.Printf("all svc revisions changed: %v", revisionsOfSvc)
	log.Printf("all routing revisions changed: %v", revisionsOfRouting)
	log.Printf("sleeping 15s for reporting cache")
	time.Sleep(15 * time.Second)
	svcCaches := m.monitorServer.GetCacheReport(model.ServiceKey{Namespace: changedNs, Service: changedSvc})
	var svcRevisionsReport []string
	var routingRevisionsReport []string
	for _, sc := range svcCaches {
		for _, r := range sc.InstancesHistory.Revision {
			svcRevisionsReport = append(svcRevisionsReport, r.Revision)
		}
		for _, r := range sc.RoutingHistory.Revision {
			routingRevisionsReport = append(routingRevisionsReport, r.Revision)
		}
	}
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
	m.monitorServer.SetPluginStat(nil)
	m.monitorServer.SetCacheReport(nil)
}

// 获取测试cacheInfo时使用的配置
func getCacheInfoConfiguration() (*config.ConfigurationImpl, error) {
	configuration, err := config.LoadConfigurationByFile("testdata/monitor.yaml")
	if err != nil {
		return nil, err
	}
	configuration.GetGlobal().GetStatReporter().SetPluginConfig("serviceCache",
		&serviceinfo.Config{ReportInterval: model.ToDurationPtr(10 * time.Second)})
	configuration.GetGlobal().GetStatReporter().SetPluginConfig("stat2Monitor", &monitor.Config{
		MetricsReportWindow: model.ToDurationPtr(10 * time.Minute),
		MetricsNumBuckets:   6,
	})
	return configuration, nil
}

// 检测实例信息变更情况是否符合预期
func (m *MonitorReportSuite) checkInstanceChangeInfo(info []*monitorpb.ServiceInfo, c *check.C) {
	var addInstances []*monitorpb.ChangeInstance
	var deleteInstances []*monitorpb.ChangeInstance
	var modifiedInstances []*monitorpb.ChangeInstance
	for _, sc := range info {
		for _, rev := range sc.InstancesHistory.Revision {
			c.Assert(rev.InstanceChange, check.NotNil)
			if rev.InstanceChange.AddedInstances != nil {
				addInstances = append(addInstances, rev.InstanceChange.AddedInstances...)
				c.Assert(rev.InstanceChange.NewCount, check.Equals, rev.InstanceChange.OldCount+4)
			}
			if rev.InstanceChange.DeletedInstances != nil {
				deleteInstances = append(deleteInstances, rev.InstanceChange.DeletedInstances...)
				c.Assert(rev.InstanceChange.NewCount+4, check.Equals, rev.InstanceChange.OldCount)
			}
			if rev.InstanceChange.ModifiedInstances != nil {
				modifiedInstances = append(modifiedInstances, rev.InstanceChange.ModifiedInstances...)
			}
		}
	}
	// statusSet := make(map[monitorpb.ModifiedInstanceInstanceStatusChange]bool)
	c.Assert(len(modifiedInstances), check.Equals, 9)
	// for _, mod := range modifiedInstances {
	//	weightChange := mod.GetWeightChange()
	//	c.Assert(weightChange.NewWeight, check.Equals, weightChange.OldWeight * 2)
	//	statusSet[mod.GetStatusChange()] = true
	// }
	// c.Assert(len(statusSet), check.Equals, 9)
}

// 设置changedSvc服务实例状态
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

// TestRateLimitRuleRevisionReport 测试限流规则的版本号上报
func (m *MonitorReportSuite) TestRateLimitRuleRevisionReport(c *check.C) {
	log.Printf("Start TestRateLimitRuleRevisionReport")
	defer util.DeleteDir(util.BackupDir)
	defer m.monitorServer.SetCacheReport(nil)

	svcPB := util.BuildNamingService(calledNs, calledSvc, m.serviceToken)

	revisionsOfRateLimit := []string{uuid.New().String(), uuid.New().String(), uuid.New().String(), uuid.New().String()}

	revisionsOfRateLimit1 := []string{uuid.New().String(), uuid.New().String()}

	revisionsOfRateLimit2 := []string{uuid.New().String(), ""}

	originAllRulesMsg, err := readRateLimitRuleFromFile(rateLimitRulesPath, false)
	c.Assert(err, check.IsNil)
	originAllRules := originAllRulesMsg.(*namingpb.RateLimit)

	originRule2Msg, err := readRateLimitRuleFromFile(rateLimitRule2Path, true)
	c.Assert(err, check.IsNil)
	originRule2 := originRule2Msg.(*namingpb.Rule)

	// 设置规则的初始revision，一开始的rateLimit只有一个rule
	m.setRateLimitRuleRevisions(originAllRules, map[string]string{rateLimitRule1Id: revisionsOfRateLimit1[0]},
		revisionsOfRateLimit[0])

	// 注册初始的rateLimit
	m.mockServer.RegisterRateLimitRule(svcPB, proto.Clone(originAllRules).(*namingpb.RateLimit))

	configuration, err := config.LoadConfigurationByFile("testdata/monitor.yaml")
	c.Assert(err, check.IsNil)
	configuration.GetGlobal().GetStatReporter().SetPluginConfig("serviceCache",
		&serviceinfo.Config{ReportInterval: model.ToDurationPtr(15 * time.Second)})
	configuration.GetGlobal().GetStatReporter().SetPluginConfig("stat2Monitor", &monitor.Config{
		MetricsReportWindow: model.ToDurationPtr(10 * time.Minute),
		MetricsNumBuckets:   6,
	})
	// m.mockServer.SetPrintDiscoverReturn(true)
	configuration.Consumer.LocalCache.ServiceRefreshInterval = model.ToDurationPtr(100 * time.Millisecond)
	limitAPI, err := api.NewLimitAPIByConfig(configuration)
	c.Assert(err, check.IsNil)
	defer limitAPI.Destroy()
	limitReq := api.NewQuotaRequest()
	limitReq.SetNamespace(calledNs)
	limitReq.SetService(calledSvc)
	// 不需要触发限流，只需要获取到限流规则即可
	limitReq.SetLabels(map[string]string{"noKey": "noValue"})
	_, err = limitAPI.GetQuota(limitReq)
	// 往rateLimit添加第二个rule
	m.setSingleRateLimitRule(originAllRules, originRule2, map[string]string{rateLimitRule2Id: revisionsOfRateLimit2[0]},
		revisionsOfRateLimit[1])
	m.mockServer.RegisterRateLimitRule(svcPB, proto.Clone(originAllRules).(*namingpb.RateLimit))
	time.Sleep(4 * time.Second)
	m.setRateLimitRuleRevisions(originAllRules, map[string]string{rateLimitRule1Id: revisionsOfRateLimit1[1]},
		revisionsOfRateLimit[2])
	m.mockServer.RegisterRateLimitRule(svcPB, proto.Clone(originAllRules).(*namingpb.RateLimit))
	time.Sleep(4 * time.Second)
	newRules := []*namingpb.Rule{originAllRules.Rules[0]}
	originAllRules.Rules = newRules
	m.setRateLimitRuleRevisions(originAllRules, nil, revisionsOfRateLimit[3])
	m.mockServer.RegisterRateLimitRule(svcPB, proto.Clone(originAllRules).(*namingpb.RateLimit))
	time.Sleep(4 * time.Second)
	log.Printf("sleep 5s to wait rateLimit cache report")
	time.Sleep(5 * time.Second)

	svcCaches := m.monitorServer.GetCacheReport(model.ServiceKey{Namespace: calledNs, Service: calledSvc})
	var totalRevisions, rule1Revisions, rule2Revisions []string
	// 从monitor获取到的cache上报中提取限流规则的版本号变化
	for _, sc := range svcCaches {
		for _, totalRevision := range sc.GetRateLimitHistory().GetRevision() {
			totalRevisions = append(totalRevisions, totalRevision.GetRevision())
		}
		for _, ruleRevision := range sc.GetSingleRateLimitHistories() {
			if ruleRevision.RuleId == rateLimitRule1Id {
				for _, revision := range ruleRevision.GetRevision() {
					rule1Revisions = append(rule1Revisions, revision.Revision)
				}
			}
			if ruleRevision.RuleId == rateLimitRule2Id {
				for _, revision := range ruleRevision.GetRevision() {
					rule2Revisions = append(rule2Revisions, revision.Revision)
				}
			}
		}
	}
	log.Printf("revisionsOfRateLimit: %v, totalRevisions: %v", revisionsOfRateLimit, totalRevisions)
	m.checkStringArrayEqual(revisionsOfRateLimit, totalRevisions, c)
	log.Printf("revisionsOfRateLimit1: %v, rule1Revisions: %v", revisionsOfRateLimit1, rule1Revisions)
	m.checkStringArrayEqual(revisionsOfRateLimit1, rule1Revisions, c)
	log.Printf("revisionsOfRateLimit2: %v, rule2Revisions: %v", revisionsOfRateLimit2, rule2Revisions)
	m.checkStringArrayEqual(revisionsOfRateLimit2, rule2Revisions, c)
}

// 检测两个[]string是否相等
func (m *MonitorReportSuite) checkStringArrayEqual(arr1 []string, arr2 []string, c *check.C) {
	c.Assert(len(arr1), check.Equals, len(arr2))
	for idx, e1 := range arr1 {
		c.Assert(e1, check.Equals, arr2[idx])
	}
}

// 从文件中读取rateLimit的定义
func readRateLimitRuleFromFile(path string, singleRule bool) (proto.Message, error) {
	buf, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if singleRule {
		rule := &namingpb.Rule{}
		if err = jsonpb.UnmarshalString(string(buf), rule); err != nil {
			return nil, err
		}
		return rule, err
	}
	rateLimit := &namingpb.RateLimit{}
	if err = jsonpb.UnmarshalString(string(buf), rateLimit); err != nil {
		return nil, err
	}
	return rateLimit, err
}

// 设置限流规则里面的单独一个规则
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

// 设置限流规则的revision
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

// TestRecoverAllReport 测试全死全活上报
func (m *MonitorReportSuite) TestRecoverAllReport(c *check.C) {
	log.Printf("TestRecoverAllReport")
	defer util.DeleteDir(util.BackupDir)
	configuration, err := config.LoadConfigurationByFile("testdata/monitor.yaml")
	c.Assert(err, check.IsNil)
	configuration.GetGlobal().GetStatReporter().SetPluginConfig("serviceCache",
		&serviceinfo.Config{ReportInterval: model.ToDurationPtr(5 * time.Second)})
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
	// 这时请求的服务都是不健康实例，触发全死全活
	_, err = consumer.GetInstances(svcRequests)
	c.Assert(err, check.IsNil)
	_, err = consumer.GetInstances(svcRequests)
	c.Assert(err, check.IsNil)
	m.mockServer.GenTestInstances(m.recoverAllService, 3)
	time.Sleep(5 * time.Second)
	// 这次请求时生成了健康的实例，关闭全死全活
	_, err = consumer.GetInstances(svcRequests)
	c.Assert(err, check.IsNil)
	time.Sleep(6 * time.Second)
	records := m.monitorServer.GetCircuitBreakStatus(model.ServiceKey{
		Namespace: recoverAllNs,
		Service:   recoverAllSvc,
	})
	var recovers []monitorpb.RecoverAllStatus
	for _, r := range records {
		for _, c := range r.RecoverAll {
			recovers = append(recovers, c.Change)
		}
	}
	// 应该有两个记录，一次开启，一次关闭全死全后
	c.Assert(len(recovers), check.Equals, 2)
	m.monitorServer.SetPluginStat(nil)
}

// //检测插件接口统计信息
// func (m *MonitorReportSuite) checkPluginStat(c *check.C) {
//	pluginStats := m.monitorServer.GetPluginStat()
//	c.Assert(len(pluginStats) > 0, check.Equals, true)
//	for _, p := range pluginStats {
//		for _, r := range p.Results {
//			c.Assert(r.TotalRequestsPerMinute > 0, check.Equals, true)
//		}
//	}
// }

// TestUpdateServiceCallReport 检查是否有 UpdateServiceCallReport 的monitor上报
func (m *MonitorReportSuite) TestUpdateServiceCallReport(c *check.C) {
	m.monitorServer.SetSdkStat(nil)
	m.monitorServer.SetSvcStat(nil)
	log.Printf("Start TestUpdateServiceCallReport")
	consumer, err := api.NewConsumerAPIByFile("testdata/monitor.yaml")
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()
	defer util.DeleteDir(util.BackupDir)

	request := &api.GetOneInstanceRequest{}
	request.FlowID = 1111

	// 预热获取monitor
	request.Namespace = "Polaris"
	request.Service = "polaris.monitor"
	resp, err := consumer.GetOneInstance(request)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.GetInstances()) > 0, check.Equals, true)
	targetInstance := resp.GetInstances()[0]

	// 请求一个实例的请求（错误的）
	svcCallResult := &api.ServiceCallResult{}
	// 设置被调的实例信息
	svcCallResult.SetCalledInstance(targetInstance)
	// 设置服务调用结果，枚举，成功或者失败
	svcCallResult.SetRetStatus(api.RetSuccess)
	// 设置服务调用返回码
	svcCallResult.SetRetCode(0)
	// 设置服务调用时延信息
	svcCallResult.SetDelay(100)

	err = consumer.UpdateServiceCallResult(svcCallResult)
	// 不会发生路由错误
	c.Assert(err, check.IsNil)

	log.Printf("TestUpdateServiceCallReport waiting 40s to upload stat\n")
	time.Sleep(50 * time.Second)
	sdkStat := m.monitorServer.GetSdkStat()
	has := false
	for _, s := range sdkStat {
		if s.GetKey().GetSdkApi().GetValue() == model.ApiUpdateServiceCallResult.String() {
			has = true
		}
	}
	c.Assert(has, check.Equals, true)
	c.Assert(len(sdkStat) >= 2, check.Equals, true)
}

// TestErrorCodeUnknown 检测当前添加的错误码不会返回ErrCodeUnknown
func (m *MonitorReportSuite) TestErrorCodeUnknown(c *check.C) {
	log.Printf("Start TestErrorCodeUnknown")
	code := model.ErrCodeUnknown + 1
	for i := 0; i < model.ErrCodeCount-2; i++ {
		codeStr := model.ErrCodeToString(code)
		log.Printf("errCode: %s, %d", codeStr, int(code))
		c.Assert(codeStr != "ErrCodeUnknown", check.Equals, true)
		code++
	}
	// 确保不会出现panic
	for i := 0; i < int(model.ErrCodeCount); i++ {
		model.ErrCodeFromIndex(i)
	}
}

// TestConfigNotReady 检测当connectionManager不ready的时候，是否会跳过第一次reportConfig
// 现在通过检测日志可以看出是不是跳过了第一次reportConfig，还无法进行自动判断
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
