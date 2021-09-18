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
	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/ptypes/wrappers"
	"github.com/google/uuid"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	monitorpb "github.com/polarismesh/polaris-go/plugin/statreporter/pb/v1"
	"github.com/polarismesh/polaris-go/plugin/statreporter/serviceinfo"
	"github.com/polarismesh/polaris-go/test/mock"
	"github.com/polarismesh/polaris-go/test/util"
	"google.golang.org/grpc"
	"gopkg.in/check.v1"
	log2 "log"
	"net"
	"os"
	"sort"
	"sync"
	"time"
)

const (
	cbNS   = "cbns"
	cbSVC  = "cbsvc"
	cbIP   = "127.0.0.1"
	cbPORT = 8088
)

//熔断测试套件
type CircuitBreakSuite struct {
	grpcServer      *grpc.Server
	grpcListener    net.Listener
	serviceToken    string
	testService     *namingpb.Service
	mockServer      mock.NamingServer
	monitorServer   mock.MonitorServer
	monitorToken    string
	grpcMonitor     *grpc.Server
	monitorListener net.Listener
}

//初始化套件
func (t *CircuitBreakSuite) SetUpSuite(c *check.C) {
	grpcOptions := make([]grpc.ServerOption, 0)
	maxStreams := 100000
	grpcOptions = append(grpcOptions, grpc.MaxConcurrentStreams(uint32(maxStreams)))
	grpc.EnableTracing = true

	var err error
	t.grpcServer = grpc.NewServer(grpcOptions...)
	t.grpcMonitor = grpc.NewServer(grpcOptions...)
	t.serviceToken = uuid.New().String()
	t.mockServer = mock.NewNamingServer()
	//注册系统服务
	t.mockServer.RegisterServerServices(cbIP, cbPORT)

	t.monitorServer = mock.NewMonitorServer()

	t.monitorToken = t.mockServer.RegisterServerService(config.ServerMonitorService)
	t.mockServer.RegisterServerInstance(
		mock.MonitorIp, mock.MonitorPort, config.ServerMonitorService, t.monitorToken, true)
	t.mockServer.RegisterRouteRule(&namingpb.Service{
		Name:      &wrappers.StringValue{Value: config.ServerMonitorService},
		Namespace: &wrappers.StringValue{Value: config.ServerNamespace}},
		t.mockServer.BuildRouteRule(config.ServerNamespace, config.ServerMonitorService))

	t.mockServer.RegisterNamespace(&namingpb.Namespace{
		Name:    &wrappers.StringValue{Value: cbNS},
		Comment: &wrappers.StringValue{Value: "for circuiebreak test"},
		Owners:  &wrappers.StringValue{Value: "Circuitbreaker"},
	})
	t.testService = &namingpb.Service{
		Name:      &wrappers.StringValue{Value: cbSVC},
		Namespace: &wrappers.StringValue{Value: cbNS},
		Token:     &wrappers.StringValue{Value: t.serviceToken},
	}
	t.mockServer.RegisterService(t.testService)
	t.mockServer.GenTestInstances(t.testService, 50)

	namingpb.RegisterPolarisGRPCServer(t.grpcServer, t.mockServer)
	monitorpb.RegisterGrpcAPIServer(t.grpcMonitor, t.monitorServer)
	t.monitorListener, err = net.Listen("tcp", fmt.Sprintf("%s:%d", mock.MonitorIp, mock.MonitorPort))
	if nil != err {
		log2.Fatal(fmt.Sprintf("error listening monitor %v", err))
	}
	log2.Printf("moniator server listening on %s:%d\n", mock.MonitorIp, mock.MonitorPort)
	go func() {
		t.grpcMonitor.Serve(t.monitorListener)
	}()

	t.grpcListener, err = net.Listen("tcp", fmt.Sprintf("%s:%d", cbIP, cbPORT))
	c.Assert(err, check.IsNil)
	log2.Printf("appserver listening on %s:%d\n", cbIP, cbPORT)
	go func() {
		t.grpcServer.Serve(t.grpcListener)
	}()
}

//套件名字
func (t *CircuitBreakSuite) GetName() string {
	return "CircuitBreak"
}

//销毁套件
func (t *CircuitBreakSuite) TearDownSuite(c *check.C) {
	t.grpcServer.Stop()
	t.grpcMonitor.Stop()
	if util.DirExist(util.BackupDir) {
		os.RemoveAll(util.BackupDir)
	}
	util.InsertLog(t, c.GetTestLog())
}

//测试利用err_Count熔断器熔断实例
func (t *CircuitBreakSuite) TestErrCount(c *check.C) {
	t.mockServer.SetPrintDiscoverReturn(true)
	t.testCircuitBreakByInstance(c, "errorCount", true)
}

//测试单实例连续错误数熔断
func (t *CircuitBreakSuite) testErrCountByInstance(
	c *check.C, targetInstance model.Instance, consumerAPI api.ConsumerAPI) {
	log.GetBaseLogger().Debugf(
		"The instance to ciucuitbreak by errcount: %s%d", targetInstance.GetHost(), targetInstance.GetPort())
	//按目前的配置，连续十次上报一个实例出错，
	//那么就会触发err_count熔断器将其变为熔断开启状态
	var failCode int32 = 1
	for i := 0; i < 10; i++ {
		consumerAPI.UpdateServiceCallResult(
			&api.ServiceCallResult{
				ServiceCallResult: *((&model.ServiceCallResult{
					CalledInstance: targetInstance,
					RetStatus:      model.RetFail,
					RetCode:        &failCode}).SetDelay(20 * time.Millisecond))})
	}
	time.Sleep(2 * time.Second)
	t.openToHalfOpen(targetInstance, 20*time.Second, c, consumerAPI)
	t.halfOpenToOpen(targetInstance, consumerAPI, c)
	t.openToHalfOpen(targetInstance, 20*time.Second, c, consumerAPI)
	t.halfOpenToClose(targetInstance, consumerAPI, c)
	time.Sleep(13 * time.Second)
	t.checkCircuitBreakReport(c, targetInstance)
	t.monitorServer.SetCircuitBreakCache(nil)
}

//测试利用err_rate熔断器熔断实例
func (t *CircuitBreakSuite) TestErrRate(c *check.C) {
	t.testCircuitBreakByInstance(c, "errorRate", true)
}

func (t *CircuitBreakSuite) testCircuitBreakByInstance(c *check.C, cbWay string, oneInstance bool) {
	defer util.DeleteDir(util.BackupDir)
	cfg, err := config.LoadConfigurationByFile("testdata/circuitbreaker.yaml")
	c.Assert(err, check.IsNil)
	cfg.Global.StatReporter.Chain = []string{config.DefaultCacheReporter}
	err = cfg.GetGlobal().GetStatReporter().SetPluginConfig(config.DefaultCacheReporter,
		&serviceinfo.Config{ReportInterval: model.ToDurationPtr(10 * time.Second)})
	c.Assert(err, check.IsNil)
	sdkCtx, err := api.InitContextByConfig(cfg)
	c.Assert(err, check.IsNil)
	consumerAPI := api.NewConsumerAPIByContext(sdkCtx)
	defer consumerAPI.Destroy()
	var targetInstance model.Instance
	var resp *model.InstancesResponse
	//随机获取一个实例，并将这个实例作为熔断的目标
	if oneInstance {
		request := &api.GetOneInstanceRequest{}
		request.FlowID = 1111
		request.Namespace = cbNS
		request.Service = cbSVC
		request.Timeout = model.ToDurationPtr(2 * time.Second)
		resp, err = consumerAPI.GetOneInstance(request)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.Instances), check.Equals, 1)
	} else {
		request := &api.GetInstancesRequest{}
		request.FlowID = 1111
		request.Namespace = cbNS
		request.Service = cbSVC
		request.Timeout = model.ToDurationPtr(2 * time.Second)
		resp, err = consumerAPI.GetInstances(request)
		c.Assert(err, check.IsNil)
	}
	targetInstance = resp.Instances[0]

	c.Assert(cbWay == "errorCount" || cbWay == "errorRate", check.Equals, true)

	switch cbWay {
	case "errorCount":
		t.testErrCountByInstance(c, targetInstance, consumerAPI)
	case "errorRate":
		t.testErrRateByInstance(c, targetInstance, consumerAPI)
	}
}

//测试单实例错误率熔断
func (t *CircuitBreakSuite) testErrRateByInstance(
	c *check.C, targetInstance model.Instance, consumerAPI api.ConsumerAPI) {
	log.GetBaseLogger().Debugf(
		"The instance to ciucuitbreak by errRate: %s%d", targetInstance.GetHost(), targetInstance.GetPort())
	var failCode int32 = 1
	var successCode int32 = 0
	for i := 0; i < 6; i++ {
		consumerAPI.UpdateServiceCallResult(
			&api.ServiceCallResult{
				ServiceCallResult: *((&model.ServiceCallResult{
					CalledInstance: targetInstance,
					RetStatus:      model.RetSuccess,
					RetCode:        &successCode}).SetDelay(20 * time.Millisecond))})
		consumerAPI.UpdateServiceCallResult(
			&api.ServiceCallResult{
				ServiceCallResult: *((&model.ServiceCallResult{
					CalledInstance: targetInstance,
					RetStatus:      model.RetFail,
					RetCode:        &failCode}).SetDelay(20 * time.Millisecond))})
	}
	time.Sleep(2 * time.Second)

	t.openToHalfOpen(targetInstance, 60*time.Second, c, consumerAPI)
	t.halfOpenToOpen(targetInstance, consumerAPI, c)
	t.openToHalfOpen(targetInstance, 60*time.Second, c, consumerAPI)
	t.halfOpenToClose(targetInstance, consumerAPI, c)
	time.Sleep(13 * time.Second)
	t.checkCircuitBreakReport(c, targetInstance)
	t.monitorServer.SetCircuitBreakCache(nil)
}

func CheckInstanceAvailable(c *check.C, consumerAPI api.ConsumerAPI, targetIns model.Instance,
	available bool, namespace string, service string) {
	request := &api.GetOneInstanceRequest{}
	request.FlowID = 1111
	request.Namespace = namespace
	request.Service = service
	request.Timeout = model.ToDurationPtr(2 * time.Second)
	hasTargetInst := false
	for i := 0; i < 2000; i++ {
		resp, err := consumerAPI.GetOneInstance(request)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.Instances), check.Equals, 1)
		if resp.Instances[0].GetId() == targetIns.GetId() {
			hasTargetInst = true
			break
		}
	}
	if available {
		c.Assert(hasTargetInst, check.Equals, true)
	} else {
		c.Assert(hasTargetInst, check.Equals, false)
	}

	request1 := &api.GetInstancesRequest{}
	request1.FlowID = 1111
	request1.Namespace = namespace
	request1.Service = service
	request1.Timeout = model.ToDurationPtr(2 * time.Second)
	resp1, err := consumerAPI.GetInstances(request1)
	c.Assert(err, check.IsNil)
	hasTargetInst = false
	for _, ins := range resp1.GetInstances() {
		if ins.GetId() == targetIns.GetId() {
			hasTargetInst = true
			break
		}
	}
	if available {
		c.Assert(hasTargetInst, check.Equals, true)
	} else {
		c.Assert(hasTargetInst, check.Equals, false)
	}
}

//熔断到半开
func (t *CircuitBreakSuite) openToHalfOpen(openInstance model.Instance, cbDuration time.Duration, c *check.C,
	consumerAPI api.ConsumerAPI) {
	c.Assert(openInstance.GetCircuitBreakerStatus(), check.NotNil)
	c.Assert(openInstance.GetCircuitBreakerStatus().GetStatus() == model.Open, check.Equals, true)
	CheckInstanceAvailable(c, consumerAPI, openInstance, false, cbNS, cbSVC)

	openTime := openInstance.GetCircuitBreakerStatus().GetStartTime()
	halfOpenTime := openTime.Add(cbDuration)
	fmt.Printf("To be halfopen in %v\n", halfOpenTime)
	time.Sleep(halfOpenTime.Sub(time.Now()) + 2*time.Second)
	c.Assert(openInstance.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.HalfOpen)
	//CheckInstanceAvailable(c, consumerAPI, openInstance, true, cbNS, cbSVC)
}

//半开到熔断
func (t *CircuitBreakSuite) halfOpenToOpen(openInstance model.Instance, consumerAPI api.ConsumerAPI, c *check.C) {
	failCode := int32(1)
	log.GetBaseLogger().Debugf("start report fail for open instance %s", openInstance.GetId())
	log2.Printf("instance id: %s, circuit break status: %v", openInstance.GetId(), openInstance.GetCircuitBreakerStatus())
	for i := 0; i < 2; i++ {
		consumerAPI.UpdateServiceCallResult(&api.ServiceCallResult{ServiceCallResult: model.ServiceCallResult{
			CalledInstance: openInstance,
			RetStatus:      model.RetFail,
			RetCode:        &failCode,
			Delay:          model.ToDurationPtr(1 * time.Second)}})
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(10 * time.Second)
	log2.Printf("circuit break status: %v", openInstance.GetCircuitBreakerStatus())
	time.Sleep(500 * time.Millisecond)
	successCode := int32(0)
	consumerAPI.UpdateServiceCallResult(&api.ServiceCallResult{ServiceCallResult: model.ServiceCallResult{
		CalledInstance: openInstance,
		RetStatus:      model.RetSuccess,
		RetCode:        &successCode,
		Delay:          model.ToDurationPtr(1 * time.Second)}})
	time.Sleep(2 * time.Second)
	//localValues = regPlug.GetInstanceLocalValue(openInstance.GetId())
	c.Assert(openInstance.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Open)
	//c.Assert(localValues, check.NotNil)
	CheckInstanceAvailable(c, consumerAPI, openInstance, false, cbNS, cbSVC)
}

//半开到关闭
func (t *CircuitBreakSuite) halfOpenToClose(openInstance model.Instance, consumerAPI api.ConsumerAPI, c *check.C) {
	successCode := int32(0)
	for i := 0; i < 3; i++ {
		log2.Printf("report success for instance %s", openInstance.GetId())
		util.SelectInstanceSpecificNum(c, consumerAPI, openInstance, 1, 2000)
		consumerAPI.UpdateServiceCallResult(&api.ServiceCallResult{ServiceCallResult: model.ServiceCallResult{
			CalledInstance: openInstance,
			RetStatus:      model.RetSuccess,
			RetCode:        &successCode,
			Delay:          model.ToDurationPtr(1 * time.Second)}})
	}
	time.Sleep(500 * time.Millisecond)
	time.Sleep(10 * time.Second)
	c.Assert(openInstance.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Close)
	CheckInstanceAvailable(c, consumerAPI, openInstance, true, cbNS, cbSVC)
}

//熔断变化数组
type changeArray []*monitorpb.CircuitbreakChange

//熔断比较
func (ca changeArray) Less(i, j int) bool {
	return ca[i].ChangeSeq < ca[j].ChangeSeq
}

//熔断交换
func (ca changeArray) Swap(i, j int) {
	ca[i], ca[j] = ca[j], ca[i]
}

//熔断状态数量
func (ca changeArray) Len() int {
	return len(ca)
}

//检查熔断状态的上报是否正确
func (t *CircuitBreakSuite) checkCircuitBreakReport(c *check.C, inst model.Instance) {
	uploadStatus := t.monitorServer.GetCircuitBreakStatus(model.ServiceKey{
		Namespace: inst.GetNamespace(),
		Service:   inst.GetService(),
	})
	var statusChange []*monitorpb.CircuitbreakChange
	for _, us := range uploadStatus {
		for _, ch := range us.InstanceCircuitbreak {
			c.Assert(ch.GetIp(), check.Equals, inst.GetHost())
			c.Assert(ch.GetPort(), check.Equals, inst.GetPort())
			for _, cc := range ch.Changes {
				statusChange = append(statusChange, cc)
			}
		}
	}
	c.Assert(len(statusChange), check.Equals, 5)
	sort.Sort(changeArray(statusChange))
	c.Assert(statusChange[0].Change, check.Equals, monitorpb.StatusChange_CloseToOpen)
	c.Assert(statusChange[1].Change, check.Equals, monitorpb.StatusChange_OpenToHalfOpen)
	c.Assert(statusChange[2].Change, check.Equals, monitorpb.StatusChange_HalfOpenToOpen)
	c.Assert(statusChange[3].Change, check.Equals, monitorpb.StatusChange_OpenToHalfOpen)
	c.Assert(statusChange[4].Change, check.Equals, monitorpb.StatusChange_HalfOpenToClose)
}

//通过默认配置来进行熔断测试
func (t *CircuitBreakSuite) testCircuitBreakByDefault(skipRouter bool, c *check.C) {
	fmt.Printf("TestCircuitBreakByDefault(skipRouter is %v) started\n", skipRouter)
	defer util.DeleteDir(util.BackupDir)
	cfg := config.NewDefaultConfiguration([]string{fmt.Sprintf("%s:%d", cbIP, cbPORT)})
	//enableStat := false
	period := 10 * time.Second
	//cfg.Global.StatReporter.Enable = &enableStat
	cfg.Consumer.LocalCache.PersistDir = "testdata/backup"
	cfg.Consumer.CircuitBreaker.CheckPeriod = &period
	var err error
	var consumerAPI api.ConsumerAPI
	consumerAPI, err = api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumerAPI.Destroy()
	//等待直到完成首次地域信息拉取
	time.Sleep(1 * time.Second)
	//随机获取一个实例，并将这个实例作为熔断的目标
	request := &api.GetInstancesRequest{}
	request.FlowID = 1111
	request.Namespace = cbNS
	request.Service = cbSVC
	request.SkipRouteFilter = skipRouter
	var response *model.InstancesResponse
	response, err = consumerAPI.GetInstances(request)
	c.Assert(err, check.IsNil)
	c.Assert(len(response.Instances) > 0, check.Equals, true)
	fmt.Printf("GetInstances: first count is %d\n", len(response.Instances))
	firstInstance := response.Instances[0]
	var addressToLimit = fmt.Sprintf("%s:%d", firstInstance.GetHost(), firstInstance.GetPort())
	//运行30s
	deadline := time.Now().Add(45 * time.Second)
	var firstCount = len(response.Instances)
	//检查是否发生了熔断
	var hasLimited = false
	//检查是否进行了熔断恢复
	var hasResume = false
	for {
		if time.Now().After(deadline) {
			break
		}
		if len(response.Instances) == firstCount-1 && !hasLimited {
			fmt.Printf("instance has been limited for service %s::%s\n", request.Namespace, request.Service)
			hasLimited = true
		}
		if len(response.Instances) == firstCount && hasLimited && !hasResume {
			fmt.Printf("instance has been resumed for service %s::%s\n", request.Namespace, request.Service)
			hasResume = true
		}
		for _, instance := range response.Instances {
			address := fmt.Sprintf("%s:%d", instance.GetHost(), instance.GetPort())
			var callResult = &api.ServiceCallResult{}
			if address == addressToLimit && !hasLimited {
				//fmt.Printf("report fail for %s:%d\n", instance.GetHost(), instance.GetPort())
				callResult.RetStatus = model.RetFail
				callResult.Delay = model.ToDurationPtr(1000 * time.Millisecond)
				callResult.RetCode = proto.Int(-1)
				callResult.CalledInstance = instance
			} else {
				//fmt.Printf("report success for %s:%d\n", instance.GetHost(), instance.GetPort())
				callResult.RetStatus = model.RetSuccess
				callResult.Delay = model.ToDurationPtr(10 * time.Millisecond)
				callResult.RetCode = proto.Int(0)
				callResult.CalledInstance = instance
			}
			err = consumerAPI.UpdateServiceCallResult(callResult)
			c.Assert(err, check.IsNil)
		}
		time.Sleep(50 * time.Millisecond)
		response, err = consumerAPI.GetInstances(request)
		c.Assert(err, check.IsNil)
		//fmt.Printf("GetInstances: count is %d\n", len(response.Instances))
	}
	c.Assert(hasLimited, check.Equals, true)
	c.Assert(hasResume, check.Equals, true)
	fmt.Printf("TestCircuitBreakByDefault(skipRouter is %v) terminated\n", skipRouter)
}

//通过默认配置来进行熔断测试
func (t *CircuitBreakSuite) TestCircuitBreakByDefault(c *check.C) {
	t.testCircuitBreakByDefault(false, c)
}

//测试通过获取多个实例接口分配的实例也可以进行连续错误数熔断半开状态转换
func (t *CircuitBreakSuite) TestErrCountByGetInstances(c *check.C) {
	t.testCircuitBreakByInstance(c, "errorCount", false)
}

//测试通过获取多个实例接口分配的实例也可以进行错误率熔断半开状态转换
func (t *CircuitBreakSuite) TestErrRateByGetInstances(c *check.C) {
	t.testCircuitBreakByInstance(c, "errorRate", false)
}

//连续错误熔断的阈值 测试
func (t *CircuitBreakSuite) TestErrCountTriggerOpenThreshold(c *check.C) {
	fmt.Println("--TestErrCountTriggerOpenNum")
	defer util.DeleteDir(util.BackupDir)
	cfg := config.NewDefaultConfiguration([]string{fmt.Sprintf("%s:%d", cbIP, cbPORT)})
	//enableStat := false
	period := 2 * time.Second
	//cfg.Global.StatReporter.Enable = &enableStat
	cfg.Consumer.LocalCache.PersistDir = "testdata/backup"
	cfg.Consumer.CircuitBreaker.CheckPeriod = &period
	cfg.Consumer.GetCircuitBreaker().GetErrorCountConfig().SetContinuousErrorThreshold(20)
	cfg.Consumer.GetCircuitBreaker().GetErrorRateConfig().SetErrorRatePercent(100)

	var err error
	var consumerAPI api.ConsumerAPI
	consumerAPI, err = api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumerAPI.Destroy()
	//等待直到完成首次地域信息拉取
	time.Sleep(time.Second * 1)

	//随机获取一个实例，并将这个实例作为熔断的目标
	request := &api.GetInstancesRequest{}
	request.FlowID = 1111
	request.Namespace = cbNS
	request.Service = cbSVC
	request.SkipRouteFilter = false
	var response *model.InstancesResponse
	response, err = consumerAPI.GetInstances(request)
	c.Assert(err, check.IsNil)
	c.Assert(len(response.Instances) > 0, check.Equals, true)
	fmt.Printf("GetInstances: first count is %d\n", len(response.Instances))
	targetIns := response.Instances[0]

	var callResult = &api.ServiceCallResult{}
	callResult.RetStatus = model.RetFail
	callResult.Delay = model.ToDurationPtr(1000 * time.Millisecond)
	callResult.RetCode = proto.Int(-1)
	callResult.CalledInstance = targetIns

	var callResultOk = &api.ServiceCallResult{}
	callResultOk.RetStatus = model.RetSuccess
	callResultOk.Delay = model.ToDurationPtr(1000 * time.Millisecond)
	callResultOk.RetCode = proto.Int(-1)
	callResultOk.CalledInstance = targetIns

	for i := 0; i < 10; i++ {
		err := consumerAPI.UpdateServiceCallResult(callResult)
		c.Assert(err, check.IsNil)
		for j := 0; j < 3; j++ {
			err := consumerAPI.UpdateServiceCallResult(callResultOk)
			c.Assert(err, check.IsNil)
		}
	}
	time.Sleep(time.Second * 5)
	if targetIns.GetCircuitBreakerStatus() != nil {
		c.Assert(targetIns.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Close)
	}
	CheckInstanceAvailable(c, consumerAPI, targetIns, true, cbNS, cbSVC)

	for i := 0; i < 20; i++ {
		err := consumerAPI.UpdateServiceCallResult(callResult)
		c.Assert(err, check.IsNil)
	}
	time.Sleep(time.Second * 5)
	c.Assert(targetIns.GetCircuitBreakerStatus(), check.NotNil)
	c.Assert(targetIns.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Open)
	CheckInstanceAvailable(c, consumerAPI, targetIns, false, cbNS, cbSVC)
}

//触发错误率熔断的阈值 测试
func (t *CircuitBreakSuite) TestErrRateTriggerOpenThreshold(c *check.C) {
	fmt.Println("--TestErrRateTriggerOpenThreshold")
	defer util.DeleteDir(util.BackupDir)
	cfg := config.NewDefaultConfiguration([]string{fmt.Sprintf("%s:%d", cbIP, cbPORT)})
	//enableStat := false
	period := 2 * time.Second
	//cfg.Global.StatReporter.Enable = &enableStat
	cfg.Consumer.LocalCache.PersistDir = "testdata/backup"
	cfg.Consumer.CircuitBreaker.CheckPeriod = &period
	cfg.Consumer.GetCircuitBreaker().GetErrorCountConfig().SetContinuousErrorThreshold(20)
	cfg.Consumer.GetCircuitBreaker().GetErrorRateConfig().SetErrorRatePercent(60)

	var err error
	var consumerAPI api.ConsumerAPI
	consumerAPI, err = api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumerAPI.Destroy()
	//等待直到完成首次地域信息拉取
	time.Sleep(time.Second * 1)

	//随机获取一个实例，并将这个实例作为熔断的目标
	request := &api.GetInstancesRequest{}
	request.FlowID = 1111
	request.Namespace = cbNS
	request.Service = cbSVC
	request.SkipRouteFilter = false
	var response *model.InstancesResponse
	response, err = consumerAPI.GetInstances(request)
	c.Assert(err, check.IsNil)
	c.Assert(len(response.Instances) > 0, check.Equals, true)
	fmt.Printf("GetInstances: first count is %d\n", len(response.Instances))
	targetIns := response.Instances[0]

	var callResult = &api.ServiceCallResult{}
	callResult.RetStatus = model.RetFail
	callResult.Delay = model.ToDurationPtr(1000 * time.Millisecond)
	callResult.RetCode = proto.Int(-1)
	callResult.CalledInstance = targetIns

	var callResultOk = &api.ServiceCallResult{}
	callResultOk.RetStatus = model.RetSuccess
	callResultOk.Delay = model.ToDurationPtr(1000 * time.Millisecond)
	callResultOk.RetCode = proto.Int(-1)
	callResultOk.CalledInstance = targetIns

	for i := 0; i < 10; i++ {
		err := consumerAPI.UpdateServiceCallResult(callResult)
		c.Assert(err, check.IsNil)

		err = consumerAPI.UpdateServiceCallResult(callResultOk)
		c.Assert(err, check.IsNil)
	}
	time.Sleep(time.Second * 5)
	if targetIns.GetCircuitBreakerStatus() != nil {
		c.Assert(targetIns.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Close)
	}
	CheckInstanceAvailable(c, consumerAPI, targetIns, true, cbNS, cbSVC)

	for i := 0; i < 10; i++ {
		err := consumerAPI.UpdateServiceCallResult(callResult)
		c.Assert(err, check.IsNil)
	}
	time.Sleep(time.Second * 5)
	c.Assert(targetIns.GetCircuitBreakerStatus(), check.NotNil)
	c.Assert(targetIns.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Open)
	CheckInstanceAvailable(c, consumerAPI, targetIns, false, cbNS, cbSVC)
}

//熔断sleepWindow测试, 不启用探测
//测试半开后最大可以获取实例的次数
//熔断器半开后恢复所需成功探测数测试
func (t *CircuitBreakSuite) TestSleepWindow(c *check.C) {
	fmt.Println("--TestSleepWindow")
	defer util.DeleteDir(util.BackupDir)
	cfg := config.NewDefaultConfiguration([]string{fmt.Sprintf("%s:%d", cbIP, cbPORT)})
	//enableStat := false
	period := 1 * time.Second
	//cfg.Global.StatReporter.Enable = &enableStat
	cfg.Consumer.LocalCache.PersistDir = "testdata/backup"
	cfg.Consumer.CircuitBreaker.CheckPeriod = &period
	cfg.Consumer.GetCircuitBreaker().GetErrorCountConfig().SetContinuousErrorThreshold(10)
	cfg.Consumer.GetCircuitBreaker().GetErrorCountConfig().SetMetricStatTimeWindow(time.Second * 10)
	cfg.Consumer.GetCircuitBreaker().GetErrorRateConfig().SetErrorRatePercent(100)
	cfg.Consumer.GetCircuitBreaker().SetSleepWindow(time.Second * 10)
	cfg.Consumer.GetCircuitBreaker().SetRequestCountAfterHalfOpen(3)
	cfg.Consumer.GetCircuitBreaker().SetSuccessCountAfterHalfOpen(2)

	var err error
	var consumerAPI api.ConsumerAPI
	consumerAPI, err = api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumerAPI.Destroy()
	//等待直到完成首次地域信息拉取
	time.Sleep(time.Second * 1)

	//随机获取一个实例，并将这个实例作为熔断的目标
	request := &api.GetInstancesRequest{}
	request.FlowID = 1111
	request.Namespace = cbNS
	request.Service = cbSVC
	request.SkipRouteFilter = false
	var response *model.InstancesResponse
	response, err = consumerAPI.GetInstances(request)
	c.Assert(err, check.IsNil)
	c.Assert(len(response.Instances) > 0, check.Equals, true)
	fmt.Printf("GetInstances: first count is %d\n", len(response.Instances))
	targetIns := response.Instances[0]

	var callResult = &api.ServiceCallResult{}
	callResult.RetStatus = model.RetFail
	callResult.Delay = model.ToDurationPtr(1000 * time.Millisecond)
	callResult.RetCode = proto.Int(-1)
	callResult.CalledInstance = targetIns
	for i := 0; i < 15; i++ {
		err := consumerAPI.UpdateServiceCallResult(callResult)
		c.Assert(err, check.IsNil)
	}
	time.Sleep(time.Second * 2)
	c.Assert(targetIns.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Open)
	CheckInstanceAvailable(c, consumerAPI, targetIns, false, cbNS, cbSVC)
	time.Sleep(time.Second * 5)
	c.Assert(targetIns.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Open)
	CheckInstanceAvailable(c, consumerAPI, targetIns, false, cbNS, cbSVC)
	time.Sleep(time.Second * 6)
	c.Assert(targetIns.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.HalfOpen)
	CheckInstanceAvailable(c, consumerAPI, targetIns, true, cbNS, cbSVC)

	//测试半开后最大可以获取实例的次数
	request1 := &api.GetOneInstanceRequest{}
	request1.FlowID = 1111
	request1.Namespace = cbNS
	request1.Service = cbSVC
	useNum := 0
	for i := 0; i < 10000; i++ {
		response, err = consumerAPI.GetOneInstance(request1)
		c.Assert(err, check.IsNil)
		c.Assert(len(response.Instances) > 0, check.Equals, true)
		if response.Instances[0].GetId() == targetIns.GetId() {
			useNum++
		}
	}
	log2.Printf("useNum: %d", useNum)
	c.Assert(useNum <= 2, check.Equals, true)

	//熔断器半开后恢复所需成功探测数测试
	var callResultOk = &api.ServiceCallResult{}
	callResultOk.RetStatus = model.RetSuccess
	callResultOk.Delay = model.ToDurationPtr(10 * time.Millisecond)
	callResultOk.RetCode = proto.Int(0)
	callResultOk.CalledInstance = targetIns
	err = consumerAPI.UpdateServiceCallResult(callResultOk)
	c.Assert(err, check.IsNil)
	time.Sleep(time.Second * 2)
	c.Assert(targetIns.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.HalfOpen)

	for i := 0; i < 2; i++ {
		err = consumerAPI.UpdateServiceCallResult(callResultOk)
		c.Assert(err, check.IsNil)
	}
	time.Sleep(time.Second * 2)
	fmt.Println("targetId  ", targetIns.GetId())
	c.Assert(targetIns.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Close)
	CheckInstanceAvailable(c, consumerAPI, targetIns, true, cbNS, cbSVC)
}

func (t *CircuitBreakSuite) addInstance(srService string, srNamespace string, srIPAddr string, srPort uint32, health bool) {
	location := &namingpb.Location{
		Region: &wrappers.StringValue{Value: "A"},
		Zone:   &wrappers.StringValue{Value: "a"},
		Campus: &wrappers.StringValue{Value: "1"},
	}
	ins := &namingpb.Instance{
		Id:        &wrappers.StringValue{Value: uuid.New().String()},
		Service:   &wrappers.StringValue{Value: srService},
		Namespace: &wrappers.StringValue{Value: srNamespace},
		Host:      &wrappers.StringValue{Value: srIPAddr},
		Port:      &wrappers.UInt32Value{Value: uint32(srPort)},
		Weight:    &wrappers.UInt32Value{Value: 100},
		Healthy:   &wrappers.BoolValue{Value: health},
		Location:  location}
	testService := &namingpb.Service{
		Name:      &wrappers.StringValue{Value: srService},
		Namespace: &wrappers.StringValue{Value: srNamespace},
		Token:     &wrappers.StringValue{Value: t.serviceToken},
	}
	t.mockServer.RegisterServiceInstances(testService, []*namingpb.Instance{ins})
}

//全部熔断后测试
//全部熔断后优先获取熔断实例
//全部不健康,可以触发全死全活
func (t *CircuitBreakSuite) TestAllCircuitBreaker(c *check.C) {
	fmt.Println("--TestAllCircuitBreaker")
	defer util.DeleteDir(util.BackupDir)
	cfg := config.NewDefaultConfiguration([]string{fmt.Sprintf("%s:%d", cbIP, cbPORT)})
	//enableStat := false
	period := 1 * time.Second
	//cfg.Global.StatReporter.Enable = &enableStat
	cfg.Consumer.LocalCache.PersistDir = "testdata/backup"
	cfg.Consumer.CircuitBreaker.CheckPeriod = &period
	cfg.Consumer.GetCircuitBreaker().GetErrorCountConfig().SetContinuousErrorThreshold(10)

	var err error
	var consumerAPI api.ConsumerAPI
	consumerAPI, err = api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumerAPI.Destroy()
	//等待直到完成首次地域信息拉取
	time.Sleep(time.Second * 1)

	t.testService = &namingpb.Service{
		Name:      &wrappers.StringValue{Value: "cbTest1"},
		Namespace: &wrappers.StringValue{Value: cbNS},
		Token:     &wrappers.StringValue{Value: t.serviceToken},
	}
	t.mockServer.RegisterService(t.testService)
	t.addInstance("cbTest1", cbNS, "127.0.0.1", 1234, true)
	t.addInstance("cbTest1", cbNS, "127.0.0.1", 1235, false)

	defer t.mockServer.ClearServiceInstances(t.testService)
	request1 := &api.GetOneInstanceRequest{}
	request1.FlowID = 1111
	request1.Namespace = cbNS
	request1.Service = "cbTest1"
	response, err := consumerAPI.GetOneInstance(request1)
	c.Assert(err, check.IsNil)
	c.Assert(len(response.Instances), check.Equals, 1)
	targetIns := response.GetInstances()[0]

	//熔断
	var callResult = &api.ServiceCallResult{}
	callResult.RetStatus = model.RetFail
	callResult.Delay = model.ToDurationPtr(1000 * time.Millisecond)
	callResult.RetCode = proto.Int(-1)
	callResult.CalledInstance = targetIns
	for i := 0; i < 15; i++ {
		err := consumerAPI.UpdateServiceCallResult(callResult)
		c.Assert(err, check.IsNil)
	}
	time.Sleep(time.Second * 2)
	c.Assert(targetIns.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Open)
	for i := 0; i < 10; i++ {
		response, err := consumerAPI.GetOneInstance(request1)
		c.Assert(err, check.IsNil)
		c.Assert(len(response.Instances), check.Equals, 1)
		ins := response.Instances[0]
		c.Assert(ins.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Open)
	}
	request2 := &api.GetInstancesRequest{}
	request2.FlowID = 1111
	request2.Namespace = cbNS
	request2.Service = "cbTest1"
	response, err = consumerAPI.GetInstances(request2)
	c.Assert(err, check.IsNil)
	c.Assert(len(response.Instances), check.Equals, 1)
	c.Assert(response.Instances[0].GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Open)

	t.mockServer.UpdateServerInstanceHealthy(cbNS, "cbTest1", targetIns.GetId(), false)
	time.Sleep(time.Second * 3)
	response, err = consumerAPI.GetInstances(request2)
	c.Assert(err, check.IsNil)
	c.Assert(len(response.Instances), check.Equals, 2)
	for _, ins := range response.GetInstances() {
		c.Assert(ins.IsHealthy(), check.Equals, false)
	}
}

//半开后低频率请求测试
func (t *CircuitBreakSuite) TestHalfOpenSlow(c *check.C) {
	fmt.Println("--TestHalfOpenSlow")
	defer util.DeleteDir(util.BackupDir)
	cfg := config.NewDefaultConfiguration([]string{fmt.Sprintf("%s:%d", cbIP, cbPORT)})
	//enableStat := false
	period := 1 * time.Second
	//cfg.Global.StatReporter.Enable = &enableStat
	cfg.Consumer.LocalCache.PersistDir = "testdata/backup"
	cfg.Consumer.CircuitBreaker.CheckPeriod = &period
	cfg.Consumer.GetCircuitBreaker().GetErrorCountConfig().SetContinuousErrorThreshold(10)
	cfg.Consumer.GetCircuitBreaker().GetErrorCountConfig().SetMetricStatTimeWindow(time.Second * 10)
	cfg.Consumer.GetCircuitBreaker().GetErrorRateConfig().SetErrorRatePercent(100)
	cfg.Consumer.GetCircuitBreaker().SetSleepWindow(time.Second * 10)
	cfg.Consumer.GetCircuitBreaker().SetRequestCountAfterHalfOpen(150)
	cfg.Consumer.GetCircuitBreaker().SetSuccessCountAfterHalfOpen(150)
	cfg.Consumer.GetCircuitBreaker().SetRecoverWindow(time.Second * 20)

	var err error
	var consumerAPI api.ConsumerAPI
	consumerAPI, err = api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumerAPI.Destroy()
	//等待直到完成首次地域信息拉取
	time.Sleep(time.Second * 1)

	//随机获取一个实例，并将这个实例作为熔断的目标
	request := &api.GetInstancesRequest{}
	request.FlowID = 1111
	request.Namespace = cbNS
	request.Service = cbSVC
	request.SkipRouteFilter = false
	var response *model.InstancesResponse
	response, err = consumerAPI.GetInstances(request)
	c.Assert(err, check.IsNil)
	c.Assert(len(response.Instances) > 0, check.Equals, true)
	fmt.Printf("GetInstances: first count is %d\n", len(response.Instances))
	targetIns := response.Instances[0]

	//熔断
	var callResult = &api.ServiceCallResult{}
	callResult.RetStatus = model.RetFail
	callResult.Delay = model.ToDurationPtr(1000 * time.Millisecond)
	callResult.RetCode = proto.Int(-1)
	callResult.CalledInstance = targetIns
	for i := 0; i < 15; i++ {
		err := consumerAPI.UpdateServiceCallResult(callResult)
		c.Assert(err, check.IsNil)
	}
	time.Sleep(time.Second * 2)
	c.Assert(targetIns.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Open)
	CheckInstanceAvailable(c, consumerAPI, targetIns, false, cbNS, cbSVC)
	time.Sleep(time.Second * 11)
	c.Assert(targetIns.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.HalfOpen)
	callResult.RetStatus = model.RetSuccess
	CheckInstanceAvailable(c, consumerAPI, targetIns, true, cbNS, cbSVC)
	consumerAPI.UpdateServiceCallResult(callResult)

	for cnt := 0; cnt < 4; cnt++ {
		for i := 0; i < 30; i++ {
			util.SelectInstanceSpecificNum(c, consumerAPI, targetIns, 1, 2000)
			err := consumerAPI.UpdateServiceCallResult(callResult)
			c.Assert(err, check.IsNil)
		}
		time.Sleep(time.Second * 2)
		c.Assert(targetIns.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.HalfOpen)
		//CheckInstanceAvailable(c, consumerAPI, targetIns, true, cbNS, cbSVC)
	}
	for i := 0; i < 29; i++ {
		//log2.Printf("i: %d, cbStatus: %v", i, targetIns.GetCircuitBreakerStatus())
		util.SelectInstanceSpecificNum(c, consumerAPI, targetIns, 1, 2000)
		err := consumerAPI.UpdateServiceCallResult(callResult)
		c.Assert(err, check.IsNil)
	}
	time.Sleep(time.Second * 2)
	util.SelectInstanceSpecificNum(c, consumerAPI, targetIns, 1, 2000)
	c.Assert(targetIns.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Close)
	CheckInstanceAvailable(c, consumerAPI, targetIns, true, cbNS, cbSVC)
}

const (
	setUnhealthy  = 1
	setIsolate    = 2
	setWeightZero = 3
)

func (t *CircuitBreakSuite) WhenOpenToHalfOpenChangToUnavailable(c *check.C, flag int) {
	defer util.DeleteDir(util.BackupDir)
	cfg := config.NewDefaultConfiguration([]string{fmt.Sprintf("%s:%d", cbIP, cbPORT)})
	//enableStat := false
	period := 1 * time.Second
	//cfg.Global.StatReporter.Enable = &enableStat
	cfg.Consumer.LocalCache.PersistDir = "testdata/backup"
	cfg.Consumer.CircuitBreaker.CheckPeriod = &period
	cfg.Consumer.GetCircuitBreaker().GetErrorCountConfig().SetContinuousErrorThreshold(10)
	cfg.Consumer.GetCircuitBreaker().GetErrorCountConfig().SetMetricStatTimeWindow(time.Second * 10)
	cfg.Consumer.GetCircuitBreaker().GetErrorRateConfig().SetErrorRatePercent(100)
	cfg.Consumer.GetCircuitBreaker().SetSleepWindow(time.Second * 10)
	cfg.Consumer.GetCircuitBreaker().SetRequestCountAfterHalfOpen(15)
	cfg.Consumer.GetCircuitBreaker().SetSuccessCountAfterHalfOpen(10)
	cfg.Consumer.GetCircuitBreaker().SetRecoverWindow(time.Second * 20)

	var err error
	var consumerAPI api.ConsumerAPI
	consumerAPI, err = api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumerAPI.Destroy()
	//等待直到完成首次地域信息拉取
	time.Sleep(time.Second * 1)

	//随机获取一个实例，并将这个实例作为熔断的目标
	request := &api.GetInstancesRequest{}
	request.FlowID = 1111
	request.Namespace = cbNS
	request.Service = cbSVC
	request.SkipRouteFilter = false
	var response *model.InstancesResponse
	response, err = consumerAPI.GetInstances(request)
	c.Assert(err, check.IsNil)
	c.Assert(len(response.Instances) > 0, check.Equals, true)
	fmt.Printf("GetInstances: first count is %d\n", len(response.Instances))
	targetIns := response.Instances[0]

	//熔断
	var callResult = &api.ServiceCallResult{}
	callResult.RetStatus = model.RetFail
	callResult.Delay = model.ToDurationPtr(1000 * time.Millisecond)
	callResult.RetCode = proto.Int(-1)
	callResult.CalledInstance = targetIns
	for i := 0; i < 15; i++ {
		err := consumerAPI.UpdateServiceCallResult(callResult)
		c.Assert(err, check.IsNil)
	}
	time.Sleep(time.Second * 2)
	c.Assert(targetIns.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Open)
	CheckInstanceAvailable(c, consumerAPI, targetIns, false, cbNS, cbSVC)

	for i := 0; i < 20; i++ {
		err := consumerAPI.UpdateServiceCallResult(callResult)
		c.Assert(err, check.IsNil)
		if i == 10 {
			if flag == setUnhealthy {
				t.mockServer.UpdateServerInstanceHealthy(cbNS, cbSVC, targetIns.GetId(), false)
			} else if flag == setIsolate {
				t.mockServer.UpdateServerInstanceIsolate(cbNS, cbSVC, targetIns.GetId(), true)
			} else if flag == setWeightZero {
				t.mockServer.UpdateServerInstanceWeight(cbNS, cbSVC, targetIns.GetId(), 0)
			}
		}
	}
	time.Sleep(time.Second * 11)
	fmt.Println(targetIns.GetCircuitBreakerStatus().GetStatus())
	CheckInstanceAvailable(c, consumerAPI, targetIns, false, cbNS, cbSVC)
	time.Sleep(time.Second * 2)
}

func (t *CircuitBreakSuite) TestWhenOpenToHalfOpenChangToUnhealthy(c *check.C) {
	fmt.Println("---TestWhenOpenToHalfOpenChangToUnhealthy")
	t.WhenOpenToHalfOpenChangToUnavailable(c, setUnhealthy)
}

func (t *CircuitBreakSuite) TestWhenOpenToHalfOpenChangToIsolate(c *check.C) {
	fmt.Println("---TestWhenOpenToHalfOpenChangToIsolate")
	t.WhenOpenToHalfOpenChangToUnavailable(c, setIsolate)
}

func (t *CircuitBreakSuite) TestWhenOpenToHalfOpenChangToWeightZero(c *check.C) {
	fmt.Println("---TestWhenOpenToHalfOpenChangToWeightZero")
	t.WhenOpenToHalfOpenChangToUnavailable(c, setWeightZero)
}

//测试实例转化为半开之后，不会陷入不能分配配额的情况
func (t *CircuitBreakSuite) testHalfOpenMustChange(c *check.C, consumer api.ConsumerAPI, cb string, success, fail int) {
	defer util.DeleteDir(util.BackupDir)
	//随机获取一个实例，并将这个实例作为熔断的目标
	request := &api.GetOneInstanceRequest{}
	request.FlowID = 1111
	request.Namespace = cbNS
	request.Service = cbSVC
	response, err := consumer.GetOneInstance(request)
	c.Assert(err, check.IsNil)
	c.Assert(len(response.Instances) > 0, check.Equals, true)
	targetIns := response.Instances[0]

	//熔断
	var callResult = &api.ServiceCallResult{}
	callResult.RetStatus = model.RetFail
	callResult.Delay = model.ToDurationPtr(1000 * time.Millisecond)
	callResult.RetCode = proto.Int(-1)
	callResult.CalledInstance = targetIns
	t.reportCallStatus(c, consumer, targetIns, 0, model.RetSuccess, 100*time.Millisecond, success)
	t.reportCallStatus(c, consumer, targetIns, 1, model.RetFail, 100*time.Millisecond, fail)
	// 转为熔断状态
	time.Sleep(time.Second * 2)
	c.Assert(targetIns.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Open)
	c.Assert(targetIns.GetCircuitBreakerStatus().GetCircuitBreaker(), check.Equals, cb)
	log2.Printf("cbstatus of targetInstance: %+v\n", targetIns.GetCircuitBreakerStatus())

	// 转为半开状态
	time.Sleep(time.Second * 10)
	c.Assert(targetIns.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.HalfOpen)

	log.GetDetectLogger().Infof("start to circuit break %s when half open", targetIns.GetId())
	//多线程并发上报失败，导致重新熔断
	wg := &sync.WaitGroup{}
	wg.Add(3)
	for i := 0; i < 3; i++ {
		go t.reportErrorWhenHalfOpen(c, consumer, targetIns, wg)
	}
	wg.Wait()
	time.Sleep(time.Second * 2)
	c.Assert(targetIns.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Open)
	c.Assert(targetIns.GetCircuitBreakerStatus().GetCircuitBreaker(), check.Equals, cb)
	log2.Printf("cbstatus of targetInstance: %+v\n", targetIns.GetCircuitBreakerStatus())
	log.GetDetectLogger().Infof("end circuit break %s when half open", targetIns.GetId())

	// 转为半开状态
	time.Sleep(time.Second * 10)
	c.Assert(targetIns.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.HalfOpen)

	//用完配额
	util.SelectInstanceSpecificNum(c, consumer, targetIns, 10, 2000)
	//但是成功数少于配置要求
	t.reportCallStatus(c, consumer, targetIns, 0, model.RetSuccess, 100*time.Millisecond, 9)
	time.Sleep(12 * time.Second)
	//重新熔断
	c.Assert(targetIns.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Open)

	// 转为半开状态，并分配9次调用
	time.Sleep(time.Second * 11)
	c.Assert(targetIns.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.HalfOpen)
	util.SelectInstanceSpecificNum(c, consumer, targetIns, 9, 2000)
	t.reportCallStatus(c, consumer, targetIns, 0, model.RetSuccess, 100*time.Millisecond, 9)
	//跳过recover时间，继续分配请求
	time.Sleep(20 * time.Second)
	util.SelectInstanceSpecificNum(c, consumer, targetIns, 1, 2000)
	t.reportCallStatus(c, consumer, targetIns, 0, model.RetSuccess, 100*time.Millisecond, 1)
	//恢复正常
	time.Sleep(2 * time.Second)
	util.SelectInstanceSpecificNum(c, consumer, targetIns, 10, 2000)
}

func (t *CircuitBreakSuite) reportErrorWhenHalfOpen(c *check.C, consumer api.ConsumerAPI, targetIns model.Instance, wg *sync.WaitGroup) {
	t.reportCallStatus(c, consumer, targetIns, -1, model.RetFail, 100*time.Millisecond, 1)
	wg.Done()
}

func (t *CircuitBreakSuite) TestHalfOpenMustChangeErrorCount(c *check.C) {
	fmt.Println("--TestHalfOpenMustChangeErrorCount")
	defer util.DeleteDir(util.BackupDir)
	cfg := config.NewDefaultConfiguration([]string{fmt.Sprintf("%s:%d", cbIP, cbPORT)})
	//enableStat := false
	period := 1 * time.Second
	//cfg.Global.StatReporter.Enable = &enableStat
	cfg.Consumer.LocalCache.PersistDir = "testdata/backup"
	cfg.Consumer.CircuitBreaker.CheckPeriod = &period
	cfg.Consumer.GetCircuitBreaker().GetErrorCountConfig().SetContinuousErrorThreshold(10)
	cfg.Consumer.GetCircuitBreaker().GetErrorCountConfig().SetMetricStatTimeWindow(time.Second * 10)
	cfg.Consumer.GetCircuitBreaker().SetSleepWindow(time.Second * 10)
	cfg.Consumer.GetCircuitBreaker().SetRequestCountAfterHalfOpen(10)
	cfg.Consumer.GetCircuitBreaker().SetSuccessCountAfterHalfOpen(10)
	cfg.Consumer.GetCircuitBreaker().SetRecoverWindow(time.Second * 10)
	cfg.Consumer.GetCircuitBreaker().GetErrorRateConfig().SetErrorRatePercent(100)

	var err error
	var consumerAPI api.ConsumerAPI
	consumerAPI, err = api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumerAPI.Destroy()
	//等待直到完成首次地域信息拉取
	time.Sleep(time.Second * 1)
	t.testHalfOpenMustChange(c, consumerAPI, "errorCount", 0, 15)
}

func (t *CircuitBreakSuite) TestHalfOpenMustChangeErrorRate(c *check.C) {
	fmt.Println("--TestHalfOpenMustChangeErrorRate")
	defer util.DeleteDir(util.BackupDir)
	cfg := config.NewDefaultConfiguration([]string{fmt.Sprintf("%s:%d", cbIP, cbPORT)})
	//enableStat := false
	period := 1 * time.Second
	//cfg.Global.StatReporter.Enable = &enableStat
	cfg.Consumer.LocalCache.PersistDir = "testdata/backup"
	cfg.Consumer.CircuitBreaker.CheckPeriod = &period
	cfg.Consumer.GetCircuitBreaker().GetErrorRateConfig().SetRequestVolumeThreshold(10)
	cfg.Consumer.GetCircuitBreaker().GetErrorRateConfig().SetMetricStatTimeWindow(time.Second * 10)
	cfg.Consumer.GetCircuitBreaker().SetSleepWindow(time.Second * 10)
	cfg.Consumer.GetCircuitBreaker().SetRequestCountAfterHalfOpen(10)
	cfg.Consumer.GetCircuitBreaker().SetSuccessCountAfterHalfOpen(10)
	cfg.Consumer.GetCircuitBreaker().SetRecoverWindow(time.Second * 10)
	cfg.Consumer.GetCircuitBreaker().GetErrorRateConfig().SetErrorRatePercent(80)
	var err error
	var consumerAPI api.ConsumerAPI
	consumerAPI, err = api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumerAPI.Destroy()
	//等待直到完成首次地域信息拉取
	time.Sleep(time.Second * 1)
	t.testHalfOpenMustChange(c, consumerAPI, "errorRate", 2, 9)
}

func (t *CircuitBreakSuite) TestAllHalfOpenReturn(c *check.C) {
	fmt.Println("--TestAllHalfOpenReturn")
	defer util.DeleteDir(util.BackupDir)
	cfg := config.NewDefaultConfiguration([]string{fmt.Sprintf("%s:%d", cbIP, cbPORT)})
	//enableStat := false
	period := 1 * time.Second
	//cfg.Global.StatReporter.Enable = &enableStat
	cfg.Consumer.LocalCache.PersistDir = "testdata/backup"
	cfg.Consumer.CircuitBreaker.CheckPeriod = &period
	cfg.Consumer.GetCircuitBreaker().GetErrorRateConfig().SetRequestVolumeThreshold(10)
	cfg.Consumer.GetCircuitBreaker().GetErrorRateConfig().SetMetricStatTimeWindow(time.Second * 10)
	cfg.Consumer.GetCircuitBreaker().SetSleepWindow(time.Second * 10)
	cfg.Consumer.GetCircuitBreaker().SetRequestCountAfterHalfOpen(10)
	cfg.Consumer.GetCircuitBreaker().SetSuccessCountAfterHalfOpen(10)
	cfg.Consumer.GetCircuitBreaker().SetRecoverWindow(time.Second * 10)
	cfg.Consumer.GetCircuitBreaker().GetErrorRateConfig().SetErrorRatePercent(80)

	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	request := &api.GetAllInstancesRequest{}
	request.FlowID = 1111
	request.Namespace = cbNS
	request.Service = cbSVC
	response, err := consumer.GetAllInstances(request)
	c.Assert(err, check.IsNil)
	c.Assert(len(response.Instances) > 0, check.Equals, true)
	allInstances := response.Instances
	for _, inst := range allInstances {
		t.reportCallStatus(c, consumer, inst, -1, model.RetFail, 100*time.Millisecond, 11)
	}
	time.Sleep(1 * time.Second)
	for _, inst := range allInstances {
		c.Assert(inst.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Open)
	}
	time.Sleep(11 * time.Second)
	for _, inst := range allInstances {
		c.Assert(inst.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.HalfOpen)
		for i := 0; i < 10; i++ {
			//用完所有配额
			inst.GetCircuitBreakerStatus().Allocate()
		}
	}
	for i := 0; i < 100; i++ {
		oneReq := &api.GetOneInstanceRequest{}
		oneReq.Namespace = cbNS
		oneReq.Service = cbSVC
		resp, err := consumer.GetOneInstance(oneReq)
		c.Assert(err, check.IsNil)
		inst := resp.Instances[0]
		c.Assert(inst.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.HalfOpen)
		c.Assert(inst.GetCircuitBreakerStatus().IsAvailable(), check.Equals, false)
	}
}

func (t *CircuitBreakSuite) reportCallStatus(c *check.C, consumerAPI api.ConsumerAPI, targetIns model.Instance, code int, success model.RetStatus, cost time.Duration, times int) {
	var callResult = &api.ServiceCallResult{}
	callResult.RetStatus = success
	callResult.Delay = model.ToDurationPtr(cost)
	callResult.RetCode = proto.Int(code)
	callResult.CalledInstance = targetIns
	for i := 0; i < times; i++ {
		err := consumerAPI.UpdateServiceCallResult(callResult)
		c.Assert(err, check.IsNil)
	}
}
