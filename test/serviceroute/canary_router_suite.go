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

package serviceroute

import (
	"fmt"
	"log"
	"net"
	"os/user"
	"time"

	"github.com/golang/protobuf/ptypes/wrappers"
	"github.com/google/uuid"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/test/util"
	"google.golang.org/grpc"
	"gopkg.in/check.v1"
)

const (
	//测试的默认命名空间
	canaryNamespace = "dstCanaryNs"
	//测试的默认服务名
	canaryService = "dstCanarySvc"
	//测试服务器的默认地址
	canaryIPAddress = "127.0.0.1"
	//测试服务器的端口
	canaryPort = 8118
	//测试monitor的地址
	canaryMonitorIPAddr = "127.0.0.1"
	//测试monitor的端口
	canaryMonitorPort = 8119
)

//元数据过滤路由插件测试用例
type CanaryTestingSuite struct {
	grpcServer   *grpc.Server
	grpcMonitor  *grpc.Server
	grpcListener net.Listener
	serviceToken string
	testService  *namingpb.Service
}

//套件名字
func (t *CanaryTestingSuite) GetName() string {
	return "CanaryTestingSuite"
}

//SetUpSuite 启动测试套程序
func (t *CanaryTestingSuite) SetUpSuite(c *check.C) {
	fmt.Println("----------------SetUpSuite")
	grpcOptions := make([]grpc.ServerOption, 0)
	maxStreams := 100000
	grpcOptions = append(grpcOptions, grpc.MaxConcurrentStreams(uint32(maxStreams)))

	// get the grpc server wired up
	grpc.EnableTracing = true

	ipAddr := canaryIPAddress
	shopPort := canaryPort
	var err error
	t.grpcServer = grpc.NewServer(grpcOptions...)
	t.serviceToken = uuid.New().String()
	t.testService = &namingpb.Service{
		Name:      &wrappers.StringValue{Value: canaryService},
		Namespace: &wrappers.StringValue{Value: canaryNamespace},
		Token:     &wrappers.StringValue{Value: t.serviceToken},
	}
	//t.mockServer.GenTestInstances(t.testService, normalInstances)
	//t.mockServer.GenTestInstancesWithMeta(t.testService, addMetaCount, map[string]string{addMetaKey: addMetaValue})

	t.grpcListener, err = net.Listen("tcp", fmt.Sprintf("%s:%d", ipAddr, shopPort))
	if nil != err {
		log.Fatal(fmt.Sprintf("error listening appserver %v", err))
	}
	log.Printf("appserver listening on %s:%d\n", ipAddr, shopPort)
	go func() {
		t.grpcServer.Serve(t.grpcListener)
	}()
	if err != nil {
		log.Fatalf("fail to setup monitor, err %v", err)
	}
}

//SetUpSuite 结束测试套程序
func (t *CanaryTestingSuite) TearDownSuite(c *check.C) {
	t.grpcServer.Stop()
	t.grpcMonitor.Stop()
	util.InsertLog(t, c.GetTestLog())
}

//正常逻辑测试
func (t *CanaryTestingSuite) TestCanaryNormal01(c *check.C) {
	DeleteBackUpDir()
	fmt.Println("-----------TestCanaryNormal01")
	cfg := config.NewDefaultConfiguration(
		[]string{fmt.Sprintf("%s:%d", canaryIPAddress, canaryPort)})
	cfg.GetConsumer().GetServiceRouter().SetChain([]string{config.DefaultServiceRouterCanary})
	cfg.GetGlobal().GetStatReporter().SetChain([]string{config.DefaultServiceRouteReporter})
	cfg.GetGlobal().GetStatReporter().SetEnable(true)

	cfg.GetConsumer().GetLocalCache().SetStartUseFileCache(false)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	getAllReq := &api.GetAllInstancesRequest{}
	getAllReq.Namespace = canaryNamespace
	getAllReq.Service = canaryService
	respAll, err := consumer.GetAllInstances(getAllReq)
	c.Assert(err, check.IsNil)
	c.Assert(len(respAll.GetInstances()), check.Equals, 6)
	var v1Inst model.Instance
	for _, v := range respAll.GetInstances() {
		metaData := v.GetMetadata()
		if metaData != nil {
			if mv, ok := metaData[model.CanaryMetaKey]; ok {
				if mv == "useV1" {
					v1Inst = v
					break
				}
			}
		}
	}
	fmt.Println("----v1Inst", v1Inst)
	var getInstancesReq *api.GetOneInstanceRequest
	getInstancesReq = &api.GetOneInstanceRequest{}
	getInstancesReq.FlowID = 1
	getInstancesReq.Namespace = canaryNamespace
	getInstancesReq.Service = canaryService
	getInstancesReq.Metadata = make(map[string]string)
	getInstancesReq.Canary = "useV1"
	for i := 0; i < 100; i++ {
		resp, err := consumer.GetOneInstance(getInstancesReq)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		instance := resp.GetInstances()[0]
		c.Assert(instance.GetId(), check.Equals, v1Inst.GetId())
	}

	getInstancesReq1 := &api.GetInstancesRequest{}
	getInstancesReq1.FlowID = 1
	getInstancesReq1.Namespace = canaryNamespace
	getInstancesReq1.Service = canaryService
	resp, err := consumer.GetInstances(getInstancesReq1)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.GetInstances()), check.Equals, 3)
	for _, v := range resp.GetInstances() {
		c.Assert(v.IsIsolated(), check.Equals, false)
		c.Assert(v.GetWeight() != 0, check.Equals, true)
		c.Assert(v.GetId() != v1Inst.GetId(), check.Equals, true)
	}
	getInstancesReq1.Metadata = make(map[string]string)
	getInstancesReq1.Canary = "useV1"
	resp, err = consumer.GetInstances(getInstancesReq1)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.GetInstances()), check.Equals, 1)
	for _, v := range resp.GetInstances() {
		c.Assert(v.IsIsolated(), check.Equals, false)
		c.Assert(v.GetWeight() != 0, check.Equals, true)
		c.Assert(v.GetId() == v1Inst.GetId(), check.Equals, true)
	}
	time.Sleep(2 * time.Second)

}

// 正常逻辑不带金丝雀标签
func (t *CanaryTestingSuite) TestCanaryNormal02(c *check.C) {
	DeleteBackUpDir()
	cfg := config.NewDefaultConfiguration(
		[]string{fmt.Sprintf("%s:%d", canaryIPAddress, canaryPort)})
	cfg.GetConsumer().GetServiceRouter().SetChain([]string{config.DefaultServiceRouterCanary})
	cfg.GetGlobal().GetStatReporter().SetChain([]string{config.DefaultServiceRouteReporter})
	cfg.GetGlobal().GetStatReporter().SetEnable(true)
	cfg.GetConsumer().GetLocalCache().SetStartUseFileCache(false)

	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	getAllReq := &api.GetAllInstancesRequest{}
	getAllReq.Namespace = canaryNamespace
	getAllReq.Service = canaryService
	respAll, err := consumer.GetAllInstances(getAllReq)
	c.Assert(err, check.IsNil)
	c.Assert(len(respAll.GetInstances()), check.Equals, 6)
	var v1Inst model.Instance
	for _, v := range respAll.GetInstances() {
		metaData := v.GetMetadata()
		if metaData != nil {
			if mv, ok := metaData[model.CanaryMetaKey]; ok {
				if mv == "useV1" {
					v1Inst = v
					break
				}
			}
		}
	}
	fmt.Println("----v1Inst", v1Inst)
	var getInstancesReq *api.GetOneInstanceRequest
	getInstancesReq = &api.GetOneInstanceRequest{}
	getInstancesReq.FlowID = 1
	getInstancesReq.Namespace = canaryNamespace
	getInstancesReq.Service = canaryService
	getInstancesReq.Metadata = make(map[string]string)
	for i := 0; i < 100; i++ {
		resp, err := consumer.GetOneInstance(getInstancesReq)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		instance := resp.GetInstances()[0]
		c.Assert(instance.GetId() != v1Inst.GetId(), check.Equals, true)
	}
	time.Sleep(2 * time.Second)
	//测试monitor接收的数据对不对
}

// 服务不启用金丝雀
func (t *CanaryTestingSuite) TestCanaryNormal03(c *check.C) {
	DeleteBackUpDir()
	cfg := config.NewDefaultConfiguration(
		[]string{fmt.Sprintf("%s:%d", canaryIPAddress, canaryPort)})
	cfg.GetConsumer().GetServiceRouter().SetChain([]string{config.DefaultServiceRouterCanary})
	cfg.GetGlobal().GetStatReporter().SetChain([]string{config.DefaultServiceRouteReporter})
	cfg.GetGlobal().GetStatReporter().SetEnable(true)
	cfg.GetConsumer().GetLocalCache().SetStartUseFileCache(false)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	getInstancesReq1 := &api.GetInstancesRequest{}
	getInstancesReq1.FlowID = 1
	getInstancesReq1.Namespace = canaryNamespace
	getInstancesReq1.Service = canaryService
	resp, err := consumer.GetInstances(getInstancesReq1)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.GetInstances()), check.Equals, 6)
	for _, v := range resp.GetInstances() {
		c.Assert(v.IsIsolated(), check.Equals, false)
		c.Assert(v.GetWeight() != 0, check.Equals, true)
	}

	getInstancesReq1.Metadata = make(map[string]string)
	getInstancesReq1.Canary = "useV1"
	resp, err = consumer.GetInstances(getInstancesReq1)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.GetInstances()), check.Equals, 6)
	for _, v := range resp.GetInstances() {
		c.Assert(v.IsIsolated(), check.Equals, false)
		c.Assert(v.GetWeight() != 0, check.Equals, true)
	}
}

func CircuitBreakerInstance(instance model.Instance, consumer api.ConsumerAPI, c *check.C) {
	var errCode int32
	errCode = 1
	for i := 0; i < 20; i++ {
		err := consumer.UpdateServiceCallResult(
			&api.ServiceCallResult{
				ServiceCallResult: *((&model.ServiceCallResult{
					CalledInstance: instance,
					RetStatus:      model.RetFail,
					RetCode:        &errCode}).SetDelay(20 * time.Millisecond))})
		c.Assert(err, check.IsNil)
	}
}

func CloseCbInstance(instance model.Instance, consumer api.ConsumerAPI, c *check.C) {
	var errCode int32
	errCode = 1
	for i := 0; i < 10; i++ {
		//util.SelectInstanceSpecificNum(c, consumer, instance, 1, 200)
		instance.GetCircuitBreakerStatus().Allocate()
		err := consumer.UpdateServiceCallResult(
			&api.ServiceCallResult{
				ServiceCallResult: *((&model.ServiceCallResult{
					CalledInstance: instance,
					RetStatus:      model.RetSuccess,
					RetCode:        &errCode}).SetDelay(20 * time.Millisecond))})
		c.Assert(err, check.IsNil)
	}
}

func CloseCbInstances(namespace, service string, consumer api.ConsumerAPI, c *check.C, maxTimes int) {
	request := &api.GetOneInstanceRequest{}
	request.FlowID = 1111
	request.Namespace = namespace
	request.Service = service
	request.Timeout = model.ToDurationPtr(2 * time.Second)
	var errCode int32
	errCode = 1
	for i := 0; i < maxTimes; i++ {
		resp, err := consumer.GetOneInstance(request)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.Instances), check.Equals, 1)
		err = consumer.UpdateServiceCallResult(
			&api.ServiceCallResult{
				ServiceCallResult: *((&model.ServiceCallResult{
					CalledInstance: resp.Instances[0],
					RetStatus:      model.RetSuccess,
					RetCode:        &errCode}).SetDelay(20 * time.Millisecond))})
		c.Assert(err, check.IsNil)
	}
}

func CheckInstanceHasCanaryMeta(instance model.Instance, canaryValue string) int {
	metaData := instance.GetMetadata()
	if metaData == nil {
		return NormalInstance
	}
	for k, v := range metaData {
		if k == model.CanaryMetaKey {
			if v == canaryValue {
				return CanaryInstance
			} else {
				return OtherCanaryInstance
			}
		}
	}
	return NormalInstance
}

const (
	NormalInstance      = 1
	CanaryInstance      = 2
	OtherCanaryInstance = 3
)

func SplitInstances(consumer api.ConsumerAPI, canaryVal string) map[int][]model.Instance {
	getAllReq := &api.GetAllInstancesRequest{}
	getAllReq.Namespace = canaryNamespace
	getAllReq.Service = canaryService
	respAll, _ := consumer.GetAllInstances(getAllReq)
	RetMap := make(map[int][]model.Instance)
	for _, inst := range respAll.GetInstances() {
		insType := CheckInstanceHasCanaryMeta(inst, canaryVal)
		RetMap[insType] = append(RetMap[insType], inst)
	}
	return RetMap
}

func checkGetInstancesByCanaryType(consumer api.ConsumerAPI, instSize int, CType int, c *check.C) {
	getInstancesReq1 := &api.GetInstancesRequest{}
	getInstancesReq1.FlowID = 1
	getInstancesReq1.Namespace = canaryNamespace
	getInstancesReq1.Service = canaryService
	if CType == CanaryInstance {
		getInstancesReq1.Canary = "useV1"
	}
	resp, err := consumer.GetInstances(getInstancesReq1)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.GetInstances()), check.Equals, instSize)
	for _, v := range resp.GetInstances() {
		c.Assert(v.IsIsolated(), check.Equals, false)
		c.Assert(v.GetWeight() != 0, check.Equals, true)
		c.Assert(CheckInstanceHasCanaryMeta(v, "useV1"), check.Equals, CType)
	}
}

// 异常测试， 服务启用金丝雀路由，有两个目标金丝雀实例， 1个正常实例， 1个其他版本金丝实例
// 先一个目标金丝雀实例熔断   -- 只能获取到可用的一个金丝雀实例
// 两个目标金丝雀实例熔断     -- 获取正常实例
// 正常实例熔断             -- 获取到 其他版本金丝实例
// 其他版本金丝实例熔断      -- 获取到金丝雀实例
func (t *CanaryTestingSuite) TestCanaryException01(c *check.C) {
	DeleteBackUpDir()
	fmt.Println("-----------TestCanaryException01")
	cfg := config.NewDefaultConfiguration(
		[]string{fmt.Sprintf("%s:%d", canaryIPAddress, canaryPort)})
	cfg.GetConsumer().GetServiceRouter().SetChain([]string{config.DefaultServiceRouterCanary})
	cfg.GetConsumer().GetCircuitBreaker().SetSleepWindow(time.Second * 20)
	cfg.GetConsumer().GetCircuitBreaker().SetCheckPeriod(time.Second * 3)
	cfg.GetConsumer().GetCircuitBreaker().GetErrorCountConfig().SetMetricStatTimeWindow(time.Second * 5)
	cfg.GetConsumer().GetCircuitBreaker().GetErrorRateConfig().SetMetricStatTimeWindow(time.Second * 5)
	cfg.GetGlobal().GetStatReporter().SetChain([]string{config.DefaultServiceRouteReporter})
	cfg.GetGlobal().GetStatReporter().SetEnable(true)
	cfg.GetConsumer().GetLocalCache().SetStartUseFileCache(false)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	instMap := SplitInstances(consumer, "useV1")
	_ = instMap

	getInstancesReq1 := &api.GetInstancesRequest{}
	getInstancesReq1.FlowID = 1
	getInstancesReq1.Namespace = canaryNamespace
	getInstancesReq1.Service = canaryService
	getInstancesReq1.Canary = "useV1"
	resp, err := consumer.GetInstances(getInstancesReq1)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.GetInstances()), check.Equals, 2)
	for _, v := range resp.GetInstances() {
		c.Assert(v.IsIsolated(), check.Equals, false)
		c.Assert(v.GetWeight() != 0, check.Equals, true)
		isCanary := false
		if v.GetMetadata() != nil {
			if v1, ok := v.GetMetadata()[model.CanaryMetaKey]; ok {
				if v1 == "useV1" {
					isCanary = true
				}
			}
		}
		c.Assert(isCanary, check.Equals, true)
	}
	var tarIns1 = resp.GetInstances()[0]
	var tarIns2 = resp.GetInstances()[1]
	CircuitBreakerInstance(tarIns1, consumer, c)
	time.Sleep(time.Second * 5)
	c.Assert(tarIns1.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Open)
	var getInstancesReq *api.GetOneInstanceRequest
	getInstancesReq = &api.GetOneInstanceRequest{}
	getInstancesReq.FlowID = 1
	getInstancesReq.Namespace = canaryNamespace
	getInstancesReq.Service = canaryService
	getInstancesReq.Metadata = make(map[string]string)
	getInstancesReq.Canary = "useV1"

	for i := 0; i < 100; i++ {
		resp, err := consumer.GetOneInstance(getInstancesReq)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		instance := resp.GetInstances()[0]
		c.Assert(instance.GetId(), check.Equals, tarIns2.GetId())
	}
	CircuitBreakerInstance(tarIns2, consumer, c)
	time.Sleep(time.Second * 5)
	c.Assert(tarIns2.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Open)
	//测试monitor接收的数据对不对

	for i := 0; i < 100; i++ {
		resp, err := consumer.GetOneInstance(getInstancesReq)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		instance := resp.GetInstances()[0]
		c.Assert(CheckInstanceHasCanaryMeta(instance, "useV1"), check.Equals, NormalInstance)
	}

	for _, v := range instMap[NormalInstance] {
		CircuitBreakerInstance(v, consumer, c)
	}
	time.Sleep(time.Second * 5)

	for _, v := range instMap[NormalInstance] {
		c.Assert(v.GetCircuitBreakerStatus(), check.NotNil)
		c.Assert(v.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Open)
	}

	for i := 0; i < 100; i++ {
		resp, err := consumer.GetOneInstance(getInstancesReq)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		instance := resp.GetInstances()[0]
		c.Assert(CheckInstanceHasCanaryMeta(instance, "useV1"), check.Equals, OtherCanaryInstance)
	}

	for _, v := range instMap[OtherCanaryInstance] {
		CircuitBreakerInstance(v, consumer, c)
	}
	time.Sleep(time.Second * 2)

	time.Sleep(time.Second * 5)
	for _, v := range instMap[OtherCanaryInstance] {
		c.Assert(v.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Open)
	}

	for i := 0; i < 100; i++ {
		resp, err := consumer.GetOneInstance(getInstancesReq)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		instance := resp.GetInstances()[0]
		c.Assert(CheckInstanceHasCanaryMeta(instance, "useV1"), check.Equals, CanaryInstance)
	}

	time.Sleep(time.Second * 25)

	log.Printf("len(instMap[CanaryInstance]): %v", len(instMap[CanaryInstance]))
	for _, v := range instMap[CanaryInstance] {
		CloseCbInstance(v, consumer, c)
	}
	//CloseCbInstances(canaryNamespace, canaryService, consumer, c, 2000)
	time.Sleep(time.Second * 3)
	for _, v := range instMap[CanaryInstance] {
		c.Assert(v.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Close)
	}
	for i := 0; i < 3; i++ {
		checkGetInstancesByCanaryType(consumer, 2, CanaryInstance, c)
	}
}

// 异常测试， 服务启用金丝雀路由，测试不带金丝雀标签, 有1个目标金丝雀实例， 2个正常实例， 1个其他版本金丝实例
// 先一个目标正常实例熔断   -- 只能获取到可用的一个正常实例
// 两个目标正常实例熔断     -- 获取正常带金丝雀标签实例（共2个）
// 带金丝雀标签实例熔断             -- 获取到正常实例
func (t *CanaryTestingSuite) TestCanaryException02(c *check.C) {
	defer util.DeleteDir("/Users/angevil/polaris/backup")
	cfg := config.NewDefaultConfiguration(
		[]string{fmt.Sprintf("%s:%d", canaryIPAddress, canaryPort)})
	cfg.GetConsumer().GetServiceRouter().SetChain([]string{config.DefaultServiceRouterCanary})
	cfg.GetConsumer().GetCircuitBreaker().SetSleepWindow(time.Second * 20)
	cfg.GetConsumer().GetCircuitBreaker().SetCheckPeriod(time.Second * 3)
	cfg.GetConsumer().GetCircuitBreaker().GetErrorCountConfig().SetMetricStatTimeWindow(time.Second * 5)
	cfg.GetConsumer().GetCircuitBreaker().GetErrorRateConfig().SetMetricStatTimeWindow(time.Second * 5)
	cfg.GetGlobal().GetStatReporter().SetChain([]string{config.DefaultServiceRouteReporter})
	cfg.GetGlobal().GetStatReporter().SetEnable(true)
	cfg.GetConsumer().GetLocalCache().SetStartUseFileCache(false)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	instMap := SplitInstances(consumer, "useV1")
	_ = instMap

	getInstancesReq1 := &api.GetInstancesRequest{}
	getInstancesReq1.FlowID = 1
	getInstancesReq1.Namespace = canaryNamespace
	getInstancesReq1.Service = canaryService
	getInstancesReq1.Metadata = make(map[string]string)
	getInstancesReq1.Metadata[model.CanaryMetaKey] = ""
	resp, err := consumer.GetInstances(getInstancesReq1)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.GetInstances()), check.Equals, 2)
	for _, v := range resp.GetInstances() {
		c.Assert(CheckInstanceHasCanaryMeta(v, "useV1"), check.Equals, NormalInstance)
	}
	var tarIns1 model.Instance = resp.GetInstances()[0]
	var tarIns2 model.Instance = resp.GetInstances()[1]
	CircuitBreakerInstance(tarIns1, consumer, c)
	time.Sleep(time.Second * 5)
	c.Assert(tarIns1.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Open)
	var getInstancesReq *api.GetOneInstanceRequest
	getInstancesReq = &api.GetOneInstanceRequest{}
	getInstancesReq.FlowID = 1
	getInstancesReq.Namespace = canaryNamespace
	getInstancesReq.Service = canaryService
	getInstancesReq.Metadata = make(map[string]string)
	getInstancesReq.Canary = ""
	for i := 0; i < 100; i++ {
		resp, err := consumer.GetOneInstance(getInstancesReq)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		instance := resp.GetInstances()[0]
		c.Assert(instance.GetId(), check.Equals, tarIns2.GetId())
	}
	CircuitBreakerInstance(tarIns2, consumer, c)
	time.Sleep(time.Second * 5)
	c.Assert(tarIns2.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Open)
	//测试monitor接收的数据对不对

	for i := 0; i < 100; i++ {
		resp, err := consumer.GetOneInstance(getInstancesReq)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		instance := resp.GetInstances()[0]
		insType := CheckInstanceHasCanaryMeta(instance, "useV1")
		c.Assert(insType == CanaryInstance || insType == OtherCanaryInstance, check.Equals, true)
	}

	for _, v := range instMap[CanaryInstance] {
		CircuitBreakerInstance(v, consumer, c)
	}
	for _, v := range instMap[OtherCanaryInstance] {
		CircuitBreakerInstance(v, consumer, c)
	}
	time.Sleep(time.Second * 5)
	for _, v := range instMap[CanaryInstance] {
		c.Assert(v.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Open)
	}
	for _, v := range instMap[OtherCanaryInstance] {
		c.Assert(v.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Open)
	}

	for i := 0; i < 100; i++ {
		resp, err := consumer.GetOneInstance(getInstancesReq)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		instance := resp.GetInstances()[0]
		c.Assert(CheckInstanceHasCanaryMeta(instance, "useV1"), check.Equals, NormalInstance)
	}
	time.Sleep(2 * time.Second)

}

// 异常测试， 服务启用金丝雀路由，测试带金丝雀标签, 有1个目标金丝雀实例， 1个正常实例
// 获取到金丝雀实例
// 金丝雀实例熔断， 获取到正常实例
// 金丝雀实例恢复， 获取到金丝雀实例
func (t *CanaryTestingSuite) TestCanaryException03(c *check.C) {
	DeleteBackUpDir()
	cfg := config.NewDefaultConfiguration(
		[]string{fmt.Sprintf("%s:%d", canaryIPAddress, canaryPort)})
	cfg.GetConsumer().GetServiceRouter().SetChain([]string{config.DefaultServiceRouterCanary})
	cfg.GetConsumer().GetCircuitBreaker().SetSleepWindow(time.Second * 20)
	cfg.GetConsumer().GetCircuitBreaker().SetCheckPeriod(time.Second * 3)
	cfg.GetConsumer().GetCircuitBreaker().GetErrorCountConfig().SetMetricStatTimeWindow(time.Second * 5)
	cfg.GetConsumer().GetCircuitBreaker().GetErrorRateConfig().SetMetricStatTimeWindow(time.Second * 5)
	cfg.GetGlobal().GetStatReporter().SetChain([]string{config.DefaultServiceRouteReporter})
	cfg.GetGlobal().GetStatReporter().SetEnable(true)
	cfg.GetConsumer().GetLocalCache().SetStartUseFileCache(false)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	instMap := SplitInstances(consumer, "useV1")
	_ = instMap

	var tarIns model.Instance
	var getInstancesReq *api.GetOneInstanceRequest
	getInstancesReq = &api.GetOneInstanceRequest{}
	getInstancesReq.FlowID = 1
	getInstancesReq.Namespace = canaryNamespace
	getInstancesReq.Service = canaryService
	getInstancesReq.Metadata = make(map[string]string)
	getInstancesReq.Canary = "useV1"
	for i := 0; i < 100; i++ {
		resp, err := consumer.GetOneInstance(getInstancesReq)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		instance := resp.GetInstances()[0]
		c.Assert(CheckInstanceHasCanaryMeta(instance, "useV1"), check.Equals, CanaryInstance)
		tarIns = instance
	}
	CircuitBreakerInstance(tarIns, consumer, c)
	time.Sleep(time.Second * 5)
	c.Assert(tarIns.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Open)
	//测试monitor接收的数据对不对

	for i := 0; i < 100; i++ {
		resp, err := consumer.GetOneInstance(getInstancesReq)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		instance := resp.GetInstances()[0]
		insType := CheckInstanceHasCanaryMeta(instance, "useV1")
		c.Assert(insType, check.Equals, NormalInstance)
	}
	time.Sleep(time.Second * 22)
	CloseCbInstance(tarIns, consumer, c)
	time.Sleep(time.Second * 2)

	time.Sleep(time.Second * 5)
	for i := 0; i < 100; i++ {
		resp, err := consumer.GetOneInstance(getInstancesReq)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		instance := resp.GetInstances()[0]
		c.Assert(CheckInstanceHasCanaryMeta(instance, "useV1"), check.Equals, CanaryInstance)
	}
	time.Sleep(2 * time.Second)
	//测试monitor接收的数据对不对

}

// 异常测试， 服务启用金丝雀路由，测试不带带金丝雀标签, 有1个目标金丝雀实例， 1个正常实例
// 获取到正常实例
// 正常实例熔断， 获取到金丝雀实例
// 正常实例恢复， 获取到正常实例
func (t *CanaryTestingSuite) TestCanaryException04(c *check.C) {
	DeleteBackUpDir()
	cfg := config.NewDefaultConfiguration(
		[]string{fmt.Sprintf("%s:%d", canaryIPAddress, canaryPort)})
	cfg.GetConsumer().GetServiceRouter().SetChain([]string{config.DefaultServiceRouterCanary})
	cfg.GetConsumer().GetCircuitBreaker().SetSleepWindow(time.Second * 20)
	cfg.GetConsumer().GetCircuitBreaker().SetCheckPeriod(time.Second * 3)
	cfg.GetConsumer().GetCircuitBreaker().GetErrorCountConfig().SetMetricStatTimeWindow(time.Second * 5)
	cfg.GetConsumer().GetCircuitBreaker().GetErrorRateConfig().SetMetricStatTimeWindow(time.Second * 5)
	cfg.GetGlobal().GetStatReporter().SetChain([]string{config.DefaultServiceRouteReporter})
	cfg.GetGlobal().GetStatReporter().SetEnable(true)
	cfg.GetConsumer().GetLocalCache().SetStartUseFileCache(false)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	instMap := SplitInstances(consumer, "useV1")
	_ = instMap

	var tarIns model.Instance
	var getInstancesReq *api.GetOneInstanceRequest
	getInstancesReq = &api.GetOneInstanceRequest{}
	getInstancesReq.FlowID = 1
	getInstancesReq.Namespace = canaryNamespace
	getInstancesReq.Service = canaryService
	for i := 0; i < 100; i++ {
		resp, err := consumer.GetOneInstance(getInstancesReq)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		instance := resp.GetInstances()[0]
		c.Assert(CheckInstanceHasCanaryMeta(instance, "useV1"), check.Equals, NormalInstance)
		tarIns = instance
	}
	CircuitBreakerInstance(tarIns, consumer, c)
	time.Sleep(time.Second * 5)
	c.Assert(tarIns.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Open)

	for i := 0; i < 100; i++ {
		resp, err := consumer.GetOneInstance(getInstancesReq)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		instance := resp.GetInstances()[0]
		insType := CheckInstanceHasCanaryMeta(instance, "useV1")
		c.Assert(insType, check.Equals, CanaryInstance)
	}
	time.Sleep(time.Second * 22)
	CloseCbInstance(tarIns, consumer, c)
	time.Sleep(time.Second * 2)

	time.Sleep(time.Second * 5)
	for i := 0; i < 100; i++ {
		resp, err := consumer.GetOneInstance(getInstancesReq)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		instance := resp.GetInstances()[0]
		c.Assert(CheckInstanceHasCanaryMeta(instance, "useV1"), check.Equals, NormalInstance)
	}
	time.Sleep(2 * time.Second)

}

// 和SetDivision一起使用
// set路由过滤后有金丝雀实例
func (t *CanaryTestingSuite) TestCanaryNormal04(c *check.C) {
	DeleteBackUpDir()
	inst1MetaMap := make(map[string]string)
	inst1MetaMap[model.CanaryMetaKey] = "isCanary"
	inst1MetaMap[internalSetEnableKey] = setEnable
	inst1MetaMap[internalSetNameKey] = "set1"

	inst2MetaMap := make(map[string]string)
	inst2MetaMap[model.CanaryMetaKey] = "AnotherCanary"
	inst2MetaMap[internalSetEnableKey] = setEnable
	inst2MetaMap[internalSetNameKey] = "set1"

	inst3MetaMap := make(map[string]string)
	inst3MetaMap[internalSetEnableKey] = setEnable
	inst3MetaMap[internalSetNameKey] = "set1"
	cfg := config.NewDefaultConfiguration(
		[]string{fmt.Sprintf("%s:%d", canaryIPAddress, canaryPort)})
	cfg.GetConsumer().GetServiceRouter().SetChain([]string{config.DefaultServiceRouterSetDivision,
		config.DefaultServiceRouterCanary})
	cfg.GetGlobal().GetStatReporter().SetChain([]string{config.DefaultServiceRouteReporter})
	cfg.GetGlobal().GetStatReporter().SetEnable(true)
	cfg.GetConsumer().GetLocalCache().SetStartUseFileCache(false)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	//测试不带金丝雀标签
	var getOneInstanceReq *api.GetOneInstanceRequest
	getOneInstanceReq = &api.GetOneInstanceRequest{}
	getOneInstanceReq.FlowID = 1
	getOneInstanceReq.Namespace = canaryNamespace
	getOneInstanceReq.Service = canaryService
	getOneInstanceReq.SourceService = &model.ServiceInfo{
		Namespace: canaryNamespace,
		Service:   canaryService,
		Metadata:  map[string]string{internalSetNameKey: "set1"},
	}
	for i := 0; i < 10; i++ {
		resp, err := consumer.GetOneInstance(getOneInstanceReq)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		instance := resp.GetInstances()[0]
		c.Assert(CheckInstanceHasCanaryMeta(instance, "isCanary"), check.Equals, NormalInstance)
	}
	getInstancesReq1 := &api.GetInstancesRequest{}
	getInstancesReq1.FlowID = 1
	getInstancesReq1.Namespace = canaryNamespace
	getInstancesReq1.Service = canaryService
	getInstancesReq1.SourceService = &model.ServiceInfo{
		Namespace: canaryNamespace,
		Service:   canaryService,
		Metadata:  map[string]string{internalSetNameKey: "set1"},
	}
	resp, err := consumer.GetInstances(getInstancesReq1)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.GetInstances()), check.Equals, 2)
	for _, v := range resp.GetInstances() {
		c.Assert(v.IsIsolated(), check.Equals, false)
		c.Assert(v.GetWeight() != 0, check.Equals, true)
		fmt.Println(v.GetId(), v.GetMetadata())
		c.Assert(CheckInstanceHasCanaryMeta(v, "isCanary"), check.Equals, NormalInstance)
	}

	//测试带金丝雀标签
	getOneInstanceReq.Canary = "isCanary"
	for i := 0; i < 10; i++ {
		resp, err := consumer.GetOneInstance(getOneInstanceReq)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		instance := resp.GetInstances()[0]
		c.Assert(CheckInstanceHasCanaryMeta(instance, "isCanary"), check.Equals, CanaryInstance)
	}

	getInstancesReq1.Canary = "isCanary"
	resp, err = consumer.GetInstances(getInstancesReq1)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.GetInstances()), check.Equals, 2)
	for _, v := range resp.GetInstances() {
		c.Assert(v.IsIsolated(), check.Equals, false)
		c.Assert(v.GetWeight() != 0, check.Equals, true)
		c.Assert(CheckInstanceHasCanaryMeta(v, "isCanary"), check.Equals, CanaryInstance)
	}
	time.Sleep(2 * time.Second)

}

func (t *CanaryTestingSuite) addInstance(region, zone, campus string, health bool, metaData map[string]string) {
	location := &namingpb.Location{
		Region: &wrappers.StringValue{Value: region},
		Zone:   &wrappers.StringValue{Value: zone},
		Campus: &wrappers.StringValue{Value: campus},
	}
	ins := &namingpb.Instance{
		Id:        &wrappers.StringValue{Value: uuid.New().String()},
		Service:   &wrappers.StringValue{Value: canaryService},
		Namespace: &wrappers.StringValue{Value: canaryNamespace},
		Host:      &wrappers.StringValue{Value: srIPAddr},
		Port:      &wrappers.UInt32Value{Value: uint32(srPort)},
		Weight:    &wrappers.UInt32Value{Value: uint32(100)},
		Healthy:   &wrappers.BoolValue{Value: health},
		Location:  location}

	if metaData != nil {
		ins.Metadata = metaData
	}
}

// 和nearbyRouter一起使用
func (t *CanaryTestingSuite) TestCanaryNormal05(c *check.C) {
	DeleteBackUpDir()
	inst1MetaMap := make(map[string]string)
	inst1MetaMap[model.CanaryMetaKey] = "isCanary"
	t.addInstance("A", "a", "1", true, inst1MetaMap)
	t.addInstance("A", "a", "1", true, inst1MetaMap)
	t.addInstance("A", "a", "1", true, nil)
	t.addInstance("A", "a", "1", true, nil)
	t.addInstance("A", "a", "1", true, nil)

	cfg := config.NewDefaultConfiguration(
		[]string{fmt.Sprintf("%s:%d", canaryIPAddress, canaryPort)})
	cfg.GetConsumer().GetServiceRouter().SetChain([]string{config.DefaultServiceRouterNearbyBased,
		config.DefaultServiceRouterCanary})
	cfg.GetGlobal().GetStatReporter().SetChain([]string{config.DefaultServiceRouteReporter})
	cfg.GetGlobal().GetStatReporter().SetEnable(true)
	cfg.GetConsumer().GetLocalCache().SetStartUseFileCache(false)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	var getOneInstanceReq *api.GetOneInstanceRequest
	getOneInstanceReq = &api.GetOneInstanceRequest{}
	getOneInstanceReq.FlowID = 1
	getOneInstanceReq.Namespace = canaryNamespace
	getOneInstanceReq.Service = canaryService

	//测试不带金丝雀标签
	for i := 0; i < 100; i++ {
		resp, err := consumer.GetOneInstance(getOneInstanceReq)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		instance := resp.GetInstances()[0]
		c.Assert(CheckInstanceHasCanaryMeta(instance, "isCanary"), check.Equals, NormalInstance)
	}
	getInstancesReq1 := &api.GetInstancesRequest{}
	getInstancesReq1.FlowID = 1
	getInstancesReq1.Namespace = canaryNamespace
	getInstancesReq1.Service = canaryService
	resp, err := consumer.GetInstances(getInstancesReq1)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.GetInstances()), check.Equals, 3)
	for _, v := range resp.GetInstances() {
		c.Assert(v.IsIsolated(), check.Equals, false)
		c.Assert(v.GetWeight() != 0, check.Equals, true)
		c.Assert(CheckInstanceHasCanaryMeta(v, "isCanary"), check.Equals, NormalInstance)
	}

	//测试带金丝雀标签
	getOneInstanceReq.Canary = "isCanary"
	for i := 0; i < 100; i++ {
		resp, err := consumer.GetOneInstance(getOneInstanceReq)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		instance := resp.GetInstances()[0]
		c.Assert(CheckInstanceHasCanaryMeta(instance, "isCanary"), check.Equals, CanaryInstance)
	}
	getInstancesReq1.Canary = "isCanary"
	resp, err = consumer.GetInstances(getInstancesReq1)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.GetInstances()), check.Equals, 2)
	for _, v := range resp.GetInstances() {
		c.Assert(v.IsIsolated(), check.Equals, false)
		c.Assert(v.GetWeight() != 0, check.Equals, true)
		c.Assert(CheckInstanceHasCanaryMeta(v, "isCanary"), check.Equals, CanaryInstance)
	}
	time.Sleep(2 * time.Second)

}

func DeleteBackUpDir() {
	user, err := user.Current()
	if err != nil {
		log.Fatalf(err.Error())
	}
	homeDir := user.HomeDir
	fmt.Printf("Home Directory: %s\n", homeDir)
	var filePath string
	filePath = fmt.Sprintf("%s/polaris/backup", homeDir)
	util.DeleteDir(filePath)
}
