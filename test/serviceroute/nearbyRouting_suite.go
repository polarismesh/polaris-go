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
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net"
	"os"
	"strconv"
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
	srNamespace   = "srNS"
	srService     = "srSvc"
	srIPAddr      = "127.0.0.1"
	srPort        = commontest.NearbySuitServerPort // 需要跟配置文件一致(sr_nearby.yaml)
	srMonitorAddr = "127.0.0.1"
	srMonitorPort = 8010
)

// NearbyTestingSuite 路由API测试套
type NearbyTestingSuite struct {
	grpcServer   *grpc.Server
	grpcListener net.Listener
	serviceToken string
	mocksvr      mock.NamingServer
	testService  *service_manage.Service
}

// setupServer 构造server
func (t *NearbyTestingSuite) setupServer(ipAddr string, port int) (*grpc.Server, net.Listener, mock.NamingServer) {
	grpcOptions := make([]grpc.ServerOption, 0)
	maxStreams := 100000
	grpcOptions = append(grpcOptions, grpc.MaxConcurrentStreams(uint32(maxStreams)))

	// get the grpc server wired up
	grpc.EnableTracing = true
	var err error
	grpcServer := grpc.NewServer(grpcOptions...)

	mockServer := mock.NewNamingServer()
	token := mockServer.RegisterServerService(config.ServerDiscoverService)
	mockServer.RegisterServerInstance(ipAddr, port, config.ServerDiscoverService, token, true)
	mockServer.RegisterNamespace(&apimodel.Namespace{
		Name:    &wrappers.StringValue{Value: srNamespace},
		Comment: &wrappers.StringValue{Value: "for  service route test"},
		Owners:  &wrappers.StringValue{Value: "ServiceRoute"},
	})
	t.testService = &service_manage.Service{
		Name:      &wrappers.StringValue{Value: srService},
		Namespace: &wrappers.StringValue{Value: srNamespace},
		Token:     &wrappers.StringValue{Value: t.serviceToken},
	}
	mockServer.RegisterService(t.testService)

	service_manage.RegisterPolarisGRPCServer(grpcServer, mockServer)
	grpcListener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", ipAddr, port))
	if err != nil {
		log.Fatalf("error listening appserver %v", err)
	}
	log.Printf("appserver listening on %s:%d\n", ipAddr, port)
	return grpcServer, grpcListener, mockServer
}

// addInstance 构造instance
func (t *NearbyTestingSuite) addInstance(region, zone, campus string, health bool) {
	location := &apimodel.Location{
		Region: &wrappers.StringValue{Value: region},
		Zone:   &wrappers.StringValue{Value: zone},
		Campus: &wrappers.StringValue{Value: campus},
	}
	ins := &service_manage.Instance{
		Id:        &wrappers.StringValue{Value: uuid.New().String()},
		Service:   &wrappers.StringValue{Value: srService},
		Namespace: &wrappers.StringValue{Value: srNamespace},
		Host:      &wrappers.StringValue{Value: srIPAddr},
		Port:      &wrappers.UInt32Value{Value: uint32(srPort)},
		Weight:    &wrappers.UInt32Value{Value: uint32(rand.Intn(999) + 1)},
		Healthy:   &wrappers.BoolValue{Value: health},
		Location:  location}
	testService := &service_manage.Service{
		Name:      &wrappers.StringValue{Value: srService},
		Namespace: &wrappers.StringValue{Value: srNamespace},
		Token:     &wrappers.StringValue{Value: t.serviceToken},
	}
	t.mocksvr.RegisterServiceInstances(testService, []*service_manage.Instance{ins})
}

// addInstance 构造instance
func (t *NearbyTestingSuite) addInstanceV2(region, zone, campus string, health bool, isolate bool, weight uint32) {
	location := &apimodel.Location{
		Region: &wrappers.StringValue{Value: region},
		Zone:   &wrappers.StringValue{Value: zone},
		Campus: &wrappers.StringValue{Value: campus},
	}
	ins := &service_manage.Instance{
		Id:        &wrappers.StringValue{Value: uuid.New().String()},
		Service:   &wrappers.StringValue{Value: srService},
		Namespace: &wrappers.StringValue{Value: srNamespace},
		Host:      &wrappers.StringValue{Value: srIPAddr},
		Port:      &wrappers.UInt32Value{Value: uint32(srPort)},
		Weight:    &wrappers.UInt32Value{Value: weight},
		Isolate:   &wrappers.BoolValue{Value: isolate},
		Healthy:   &wrappers.BoolValue{Value: health},
		Location:  location}
	testService := &service_manage.Service{
		Name:      &wrappers.StringValue{Value: srService},
		Namespace: &wrappers.StringValue{Value: srNamespace},
		Token:     &wrappers.StringValue{Value: t.serviceToken},
	}
	t.mocksvr.RegisterServiceInstances(testService, []*service_manage.Instance{ins})
}

// 设置模拟桩服务器
func (t *NearbyTestingSuite) SetUpSuite(c *check.C) {
	util.DeleteDir(util.BackupDir)
	t.serviceToken = uuid.New().String()
	t.grpcServer, t.grpcListener, t.mocksvr = t.setupServer(srIPAddr, srPort)
	go func() {
		if err := t.grpcServer.Serve(t.grpcListener); err != nil {
			panic(err)
		}
	}()
	awaitServerReady(srIPAddr, srPort)
}

func awaitServerReady(ip string, port int) {
	for {
		conn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", ip, port))
		if err != nil {
			fmt.Printf("dial mock server failed, err: %v\n", err)
		} else {
			conn.Close()
			break
		}
	}
}

// SetUpSuite 结束测试套程序
func (t *NearbyTestingSuite) TearDownSuite(c *check.C) {
	t.grpcServer.GracefulStop()
	if util.DirExist(util.BackupDir) {
		os.RemoveAll(util.BackupDir)
	}
	util.InsertLog(t, c.GetTestLog())
}

// 返回默认的测试配置对象
func (t *NearbyTestingSuite) getDefaultTestConfiguration(c *check.C) config.Configuration {
	cfg, err := config.LoadConfigurationByFile("testdata/sr_nearby.yaml")
	c.Assert(err, check.IsNil)
	return cfg
}

// 测试strictnearby的效果
func (t *NearbyTestingSuite) TestStrictNearby(c *check.C) {
	log.Printf("Start to TestStrictNearby: ")
	defer util.DeleteDir(util.BackupDir)
	cfg := t.getDefaultTestConfiguration(c)
	cfg.GetConsumer().GetServiceRouter().GetNearbyConfig().SetStrictNearby(true)
	t.mocksvr.SetLocation("", "", "")
	_, err := api.NewConsumerAPIByConfig(cfg)
	log.Printf("expected err, %v", err)
	c.Assert(err, check.NotNil)
	t.mocksvr.SetLocation("A", "a", "0")
}

// 进行就近路由时，匹配到idc级别
func (t *NearbyTestingSuite) TestEnabledNearbyWithIDC(c *check.C) {
	log.Println("Start to TestEnabledNearbyWithIDC")
	t.testEnabledNearby(true, c)
}

// 进行就近路由匹配，以城市为最小级别
func (t *NearbyTestingSuite) TestEnabledNearbyWithoutIDC(c *check.C) {
	log.Println("Start to TestEnabledNearbyWithoutIDC")
	t.testEnabledNearby(false, c)
}

// 就近路由测试的流程，可以根据匹不匹配idc进行不同的测试
func (t *NearbyTestingSuite) testEnabledNearby(matchIDC bool, c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	t.mocksvr.SetServiceMetadata(t.serviceToken, model.NearbyMetadataEnable, "true")
	// cfg, err := config.LoadConfigurationByFile("testdata/sr_nearby.yaml")
	// enableStat := false
	// cfg.Global.StatReporter.Enable = &enableStat
	// c.Assert(err, check.IsNil)
	cfg := t.getDefaultTestConfiguration(c)
	cfg.GetGlobal().GetStatReporter().SetEnable(true)
	// sr_nearby.yaml的配置中的匹配级别王idc，如果不匹配到该级别，那么改为zone
	if !matchIDC {
		cfg.GetConsumer().GetServiceRouter().GetNearbyConfig().SetMatchLevel("zone")
		// cfg.GetConsumer().GetServiceRouter().SetPluginConfig(config.DefaultServiceRouterNearbyBased, map[string]interface{}{
		//	"matchLevel": "zone",
		// })
	}
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	t.addInstance("B", "b", "2", true)
	for i := 0; i < 10; i++ {
		t.addInstance("A", "a", strconv.Itoa(i), true)
	}

	// 这里要先sleep一下，让定时协程先跑起来，获取到客户端的地域信息
	time.Sleep(5 * time.Second)

	request := &api.GetInstancesRequest{}
	request.FlowID = 1111
	request.Namespace = srNamespace
	request.Service = srService
	request.Timeout = model.ToDurationPtr(5 * time.Second)
	request.SkipRouteFilter = false
	resp, err := consumer.GetInstances(request)
	c.Assert(err, check.IsNil)
	for _, inst := range resp.GetInstances() {
		c.Assert(inst.GetRegion(), check.Equals, "A")
		c.Assert(inst.GetZone(), check.Equals, "a")
		if matchIDC {
			c.Assert(inst.GetIDC(), check.Equals, "0")
		}
	}
	c.Assert(resp.Namespace, check.Equals, srNamespace)
	c.Assert(resp.Service, check.Equals, srService)

	t.mocksvr.ClearServiceInstances(t.testService)
	t.mocksvr.SetServiceMetadata(t.serviceToken, model.NearbyMetadataEnable, "false")
}

// recover percent: 0.2，开启降级，降级percent：100，matchLevel：zone，lowestMatchLevel：""
// A-a-0实例：3个不健康
// A-b-1实例：3个健康，6个不健康
// B-b-2实例：10个健康A-b-1实例
// 期望：返回3个健康A-b-1实例（降级到region，健康实例比例为3/(3+3+6)=0.25）
func (t *NearbyTestingSuite) TestCaseNB1(c *check.C) {
	log.Printf("Start to TestCase1: ")
	defer util.DeleteDir(util.BackupDir)
	t.mocksvr.SetServiceMetadata(t.serviceToken, model.NearbyMetadataEnable, "true")
	// cfg, err := config.LoadConfigurationByFile("testdata/sr_nearby.yaml")
	// c.Assert(err, check.IsNil)
	// cfg.GetGlobal().GetStatReporter().SetEnable(false)
	cfg := t.getDefaultTestConfiguration(c)
	cfg.GetConsumer().GetServiceRouter().SetEnableRecoverAll(true)
	cfg.GetConsumer().GetServiceRouter().SetPercentOfMinInstances(0.2)
	cfg.GetConsumer().GetLocalCache().SetServiceRefreshInterval(time.Second * 10)
	for i := 0; i < 3; i++ {
		t.addInstance("A", "a", "0", false)
		t.addInstance("A", "b", "1", true)
	}
	for i := 0; i < 6; i++ {
		t.addInstance("A", "b", "1", false)
	}
	for i := 0; i < 10; i++ {
		t.addInstance("B", "b", "2", true)
	}
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	loc := consumer.SDKContext().GetValueContext().GetCurrentLocation()
	c.Logf("NearbyTestingSuite.TestCaseNB1 : %#v", loc)

	request := t.getTestServiceReq()
	resp, err := consumer.GetInstances(request)

	c.Assert(err, check.IsNil)
	for _, inst := range resp.GetInstances() {
		c.Assert(inst.GetRegion(), check.Equals, "A")
		c.Assert(inst.GetZone(), check.Equals, "b")
		c.Assert(inst.GetCampus(), check.Equals, "1")
		c.Assert(inst.IsHealthy(), check.Equals, true)
	}
	c.Assert(resp.Namespace, check.Equals, srNamespace)
	c.Assert(resp.Service, check.Equals, srService)

	t.mocksvr.ClearServiceInstances(t.testService)
	t.mocksvr.SetServiceMetadata(t.serviceToken, model.NearbyMetadataEnable, "false")
}

// recover percent: 0.2，开启降级，降级percent：100，matchLevel：zone，lowestMatchLevel：""
// A-a-0实例：3个健康，1个不健康
// A-b-1实例：3个健康
// B-b-2实例：10个健康A-b-1实例
// 期望：返回3个健康A-a-0实例（不降级，健康实例比例为0.75）
func (t *NearbyTestingSuite) TestCase2(c *check.C) {
	log.Printf("Start to TestCase2: ")
	defer util.DeleteDir(util.BackupDir)
	t.mocksvr.SetServiceMetadata(t.serviceToken, model.NearbyMetadataEnable, "true")
	cfg := t.getDefaultTestConfiguration(c)
	cfg.GetConsumer().GetServiceRouter().SetEnableRecoverAll(true)
	cfg.GetConsumer().GetServiceRouter().SetPercentOfMinInstances(0.2)
	t.batchAddInstance(4, 3, 0, 0, 3, 3, 10, 10)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()
	request := t.getTestServiceReq()
	loc := consumer.SDKContext().GetValueContext().GetCurrentLocation()
	c.Logf("NearbyTestingSuite.TestCase2 : %#v", loc.GetLocation())
	resp, err := consumer.GetInstances(request)
	insData, _ := json.Marshal(resp.GetInstances())
	c.Logf("NearbyTestingSuite.TestCase2 instances : %s", string(insData))
	c.Assert(err, check.IsNil)
	for _, inst := range resp.GetInstances() {
		c.Assert(inst.GetRegion(), check.Equals, "A")
		c.Assert(inst.GetZone(), check.Equals, "a")
		c.Assert(inst.GetCampus(), check.Equals, "0")
		c.Assert(inst.IsHealthy(), check.Equals, true)
	}
	c.Assert(resp.Namespace, check.Equals, srNamespace)
	c.Assert(resp.Service, check.Equals, srService)

	t.mocksvr.ClearServiceInstances(t.testService)
	t.mocksvr.SetServiceMetadata(t.serviceToken, model.NearbyMetadataEnable, "false")
}

// recover percent: 0.2，开启降级，降级percent：100，matchLevel：zone，lowestMatchLevel：""
// A-a-0实例：4个不健康
// A-b-1实例：4个不健康
// B-b-2实例：4个健康
// 期望：返回4个健康B-b-2实例（降级到""，健康实例比例为0.333）
func (t *NearbyTestingSuite) TestCase3(c *check.C) {
	log.Printf("Start to TestCase3: ")
	defer util.DeleteDir(util.BackupDir)
	t.mocksvr.SetServiceMetadata(t.serviceToken, model.NearbyMetadataEnable, "true")
	cfg := t.getDefaultTestConfiguration(c)
	cfg.GetConsumer().GetServiceRouter().SetEnableRecoverAll(true)
	cfg.GetConsumer().GetServiceRouter().SetPercentOfMinInstances(0.2)
	t.batchAddInstance(4, 0, 0, 0, 4, 0, 4, 4)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	loc := consumer.SDKContext().GetValueContext().GetCurrentLocation()
	c.Logf("NearbyTestingSuite.TestCase3 : %#v", loc.GetLocation())

	request := t.getTestServiceReq()
	resp, err := consumer.GetInstances(request)
	c.Assert(err, check.IsNil)
	for _, inst := range resp.GetInstances() {
		c.Assert(inst.GetRegion(), check.Equals, "B")
		c.Assert(inst.GetZone(), check.Equals, "b")
		c.Assert(inst.GetCampus(), check.Equals, "2")
		c.Assert(inst.IsHealthy(), check.Equals, true)
	}
	c.Assert(resp.Namespace, check.Equals, srNamespace)
	c.Assert(resp.Service, check.Equals, srService)

	t.mocksvr.ClearServiceInstances(t.testService)
	t.mocksvr.SetServiceMetadata(t.serviceToken, model.NearbyMetadataEnable, "false")
}

// recover percent: 0.2，开启降级，降级percent：100，matchLevel：zone，lowestMatchLevel：""
// A-a-0实例：4个不健康
// A-b-1实例：4个不健康
// B-b-2实例：4个不健康
// 期望：返回4个不健康A-a-0实例（不降级，触发全死全活）
func (t *NearbyTestingSuite) TestCase4(c *check.C) {
	log.Printf("Start to TestCase4: ")
	defer util.DeleteDir(util.BackupDir)
	t.mocksvr.SetServiceMetadata(t.serviceToken, model.NearbyMetadataEnable, "true")
	// cfg, err := config.LoadConfigurationByFile("testdata/sr_nearby.yaml")
	// c.Assert(err, check.IsNil)
	// cfg.GetGlobal().GetStatReporter().SetEnable(false)
	cfg := t.getDefaultTestConfiguration(c)
	cfg.GetConsumer().GetServiceRouter().SetEnableRecoverAll(true)
	cfg.GetConsumer().GetServiceRouter().SetPercentOfMinInstances(0.2)
	t.batchAddInstance(4, 0, 0, 0, 4, 0, 4, 0)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	loc := consumer.SDKContext().GetValueContext().GetCurrentLocation()
	c.Logf("NearbyTestingSuite.TestCase4 : %#v", loc.GetLocation())

	request := t.getTestServiceReq()
	resp, err := consumer.GetInstances(request)
	c.Assert(err, check.IsNil)
	for _, inst := range resp.GetInstances() {
		c.Assert(inst.GetRegion(), check.Equals, "A")
		c.Assert(inst.GetZone(), check.Equals, "a")
		c.Assert(inst.GetCampus(), check.Equals, "0")
		c.Assert(inst.IsHealthy(), check.Equals, false)
	}
	c.Assert(resp.Namespace, check.Equals, srNamespace)
	c.Assert(resp.Service, check.Equals, srService)

	t.mocksvr.ClearServiceInstances(t.testService)
	t.mocksvr.SetServiceMetadata(t.serviceToken, model.NearbyMetadataEnable, "false")
}

// recover percent: 0.2，开启降级，降级percent：100，matchLevel：zone，lowestMatchLevel：""
// A-a-0实例：0个
// A-b-1实例：4个不健康
// B-b-2实例：4个不健康
// 期望：返回4个健康B-b-2实例（降级到region，触发全死全活）
func (t *NearbyTestingSuite) TestCase5(c *check.C) {
	log.Printf("Start to TestCase5: ")
	defer util.DeleteDir(util.BackupDir)
	t.mocksvr.SetServiceMetadata(t.serviceToken, model.NearbyMetadataEnable, "true")
	// cfg, err := config.LoadConfigurationByFile("testdata/sr_nearby.yaml")
	// c.Assert(err, check.IsNil)
	// cfg.GetGlobal().GetStatReporter().SetEnable(false)
	cfg := t.getDefaultTestConfiguration(c)
	cfg.GetConsumer().GetServiceRouter().SetEnableRecoverAll(true)
	cfg.GetConsumer().GetServiceRouter().SetPercentOfMinInstances(0.2)
	t.batchAddInstance(0, 0, 0, 0, 4, 0, 4, 0)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	loc := consumer.SDKContext().GetValueContext().GetCurrentLocation()
	c.Logf("NearbyTestingSuite.TestCase5 : %#v", loc.GetLocation())

	request := t.getTestServiceReq()
	resp, err := consumer.GetInstances(request)
	c.Assert(err, check.IsNil)
	for _, inst := range resp.GetInstances() {
		c.Assert(inst.GetRegion(), check.Equals, "A")
		c.Assert(inst.GetZone(), check.Equals, "b")
		c.Assert(inst.GetCampus(), check.Equals, "1")
		c.Assert(inst.IsHealthy(), check.Equals, false)
	}
	c.Assert(resp.Namespace, check.Equals, srNamespace)
	c.Assert(resp.Service, check.Equals, srService)

	t.mocksvr.ClearServiceInstances(t.testService)
	t.mocksvr.SetServiceMetadata(t.serviceToken, model.NearbyMetadataEnable, "false")
}

// recover percent: 0.2，开启降级，降级percent：100，matchLevel：campus，lowestMatchLevel：zone
// A-a-0实例：0个
// A-b-1实例：4个不健康
// B-b-2实例：4个不健康
// 期望：返回错误，在zone和campus两个级别之间没有足够的实例
func (t *NearbyTestingSuite) TestCase6(c *check.C) {
	log.Printf("Start to TestCase6: ")
	defer util.DeleteDir(util.BackupDir)
	t.mocksvr.SetServiceMetadata(t.serviceToken, model.NearbyMetadataEnable, "true")

	cfg := t.getDefaultTestConfiguration(c)
	cfg.GetConsumer().GetServiceRouter().SetEnableRecoverAll(true)
	cfg.GetConsumer().GetServiceRouter().SetPercentOfMinInstances(0.2)
	cfg.GetConsumer().GetServiceRouter().GetNearbyConfig().SetMatchLevel("campus")
	cfg.GetConsumer().GetServiceRouter().GetNearbyConfig().SetMaxMatchLevel("zone")

	t.batchAddInstance(0, 0, 0, 0, 4, 0, 4, 0)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()
	request := t.getTestServiceReq()
	for i := 0; i < 3; i++ {
		_, err = consumer.GetInstances(request)
		c.Assert(err, check.NotNil)
		log.Printf("expected err is %v", err)
	}
	t.mocksvr.ClearServiceInstances(t.testService)
	t.mocksvr.SetServiceMetadata(t.serviceToken, model.NearbyMetadataEnable, "false")
}

// recover percent: 0.2，不开启降级，matchLevel：zone
// A-a-0实例：4个不健康
// A-b-1实例：4个健康
// B-b-2实例：4个健康
// 期望：开启全死全活，返回4个不健康A-a-0实例（不降级，触发全死全活）；关闭全死全活，不返回实例
func (t *NearbyTestingSuite) TestCase7(c *check.C) {
	log.Printf("Start to TestCase7: ")
	defer util.DeleteDir(util.BackupDir)
	t.mocksvr.SetServiceMetadata(t.serviceToken, model.NearbyMetadataEnable, "true")
	cfg := t.getDefaultTestConfiguration(c)
	cfg.GetConsumer().GetServiceRouter().SetEnableRecoverAll(true)
	cfg.GetConsumer().GetServiceRouter().SetPercentOfMinInstances(0.2)
	cfg.GetConsumer().GetServiceRouter().GetNearbyConfig().SetMatchLevel("zone")
	cfg.GetConsumer().GetServiceRouter().GetNearbyConfig().SetEnableDegradeByUnhealthyPercent(false)
	t.batchAddInstance(4, 0, 0, 0, 4, 4, 4, 4)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	request := t.getTestServiceReq()
	resp, err := consumer.GetInstances(request)
	c.Assert(err, check.IsNil)
	for _, inst := range resp.GetInstances() {
		c.Assert(inst.GetRegion(), check.Equals, "A")
		c.Assert(inst.GetZone(), check.Equals, "a")
		c.Assert(inst.GetCampus(), check.Equals, "0")
		c.Assert(inst.IsHealthy(), check.Equals, false)
	}
	time.Sleep(2 * time.Second)
	consumer.Destroy()
	util.DeleteDir(util.BackupDir)
	cfg.GetConsumer().GetServiceRouter().SetEnableRecoverAll(false)
	consumer, err = api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()
	resp, err = consumer.GetInstances(request)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.Instances), check.Equals, 0)
	t.mocksvr.ClearServiceInstances(t.testService)
	t.mocksvr.SetServiceMetadata(t.serviceToken, model.NearbyMetadataEnable, "false")
}

// 测试由metadata控制就近路由的开启
func (t *NearbyTestingSuite) TestMetadataNearby(c *check.C) {
	log.Println("Start to TestMetadataNearby")
	defer util.DeleteDir(util.BackupDir)

	// 将metadata的值设为非法的"invalid"，在这种情况下，会按默认方式关闭就近路由
	t.mocksvr.SetServiceMetadata(t.serviceToken, model.NearbyMetadataEnable, "invalid")

	// 健康的不能匹配到城市的实例
	for i := 0; i < 5; i++ {
		t.addInstance("B", "b", "1", true)
	}

	// 不健康的不能匹配到城市的实例
	for i := 0; i < 3; i++ {
		t.addInstance("B", "b", "1", false)
	}

	// 可以匹配到城市级别的实例
	t.addInstance("A", "a", "1", true)

	cfg := t.getDefaultTestConfiguration(c)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	request := t.getTestServiceReq()
	resp, err := consumer.GetInstances(request)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.GetInstances()), check.Equals, 6)

	request.SkipRouteFilter = true
	resp, err = consumer.GetInstances(request)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.GetInstances()), check.Equals, 9)

	t.mocksvr.SetServiceMetadata(t.serviceToken, model.NearbyMetadataEnable, "true")
	time.Sleep(10 * time.Second)
	// 这里要先sleep一下，更新服务元数据,因为更新服务有0到10s随机时间，13s是个安全范围
	request.SkipRouteFilter = false
	resp, err = consumer.GetInstances(request)
	c.Assert(err, check.IsNil)
	// 在进行就近路由匹配后
	c.Assert(len(resp.GetInstances()), check.Equals, 1)
	t.mocksvr.ClearServiceInstances(t.testService)
}

// recover percent: 0.2，开启降级，降级percent：50，matchLevel：zone，lowestMatchLevel：""
// A-a-0实例：2个健康，2个不健康
// A-b-1实例：2个健康
// B-b-2实例：4个健康
// 期望：返回2个健康A-a-0实例和2个健康A-b-1实例（降级到region，不触发全死全活）
func (t *NearbyTestingSuite) TestCase8(c *check.C) {
	log.Printf("Start to TestCase8: ")
	defer util.DeleteDir(util.BackupDir)
	t.mocksvr.SetServiceMetadata(t.serviceToken, model.NearbyMetadataEnable, "true")
	cfg := t.getDefaultTestConfiguration(c)
	cfg.GetConsumer().GetServiceRouter().SetEnableRecoverAll(true)
	cfg.GetConsumer().GetServiceRouter().SetPercentOfMinInstances(0.2)
	cfg.GetConsumer().GetServiceRouter().GetNearbyConfig().SetUnhealthyPercentToDegrade(50)
	cfg.GetConsumer().GetServiceRouter().GetNearbyConfig().SetMatchLevel("zone")
	t.batchAddInstance(4, 2, 0, 0, 2, 2, 4, 4)
	consumer, err := api.NewConsumerAPIByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer consumer.Destroy()

	getAllReq := &api.GetAllInstancesRequest{}
	getAllReq.Namespace = srNamespace
	getAllReq.Service = srService
	respAll, err := consumer.GetAllInstances(getAllReq)
	log.Printf("len of respAll: %v", len(respAll.GetInstances()))

	request := t.getTestServiceReq()
	resp, err := consumer.GetInstances(request)
	c.Assert(err, check.IsNil)
	bzone := 0
	azone := 0
	log.Printf("len of resp: %v", len(resp.GetInstances()))

	for idx, inst := range resp.GetInstances() {
		log.Printf("inst %d, %s, %s, %s, %v", idx, inst.GetRegion(), inst.GetZone(), inst.GetCampus(), inst.IsHealthy())
		c.Assert(inst.GetRegion(), check.Equals, "A")
		c.Assert(inst.IsHealthy(), check.Equals, true)
		if inst.GetZone() == "b" {
			c.Assert(inst.GetCampus(), check.Equals, "1")
			bzone++
		}
		if inst.GetZone() == "a" {
			c.Assert(inst.GetCampus(), check.Equals, "0")
			azone++
		}
	}
	c.Assert(azone, check.Equals, 2)
	c.Assert(bzone, check.Equals, 2)
	c.Assert(resp.Namespace, check.Equals, srNamespace)
	c.Assert(resp.Service, check.Equals, srService)

	t.mocksvr.ClearServiceInstances(t.testService)
	t.mocksvr.SetServiceMetadata(t.serviceToken, model.NearbyMetadataEnable, "false")
}

func (t *NearbyTestingSuite) TestCase9(c *check.C) {
	fmt.Println("----------------------TestCase09")
	defer util.DeleteDir(util.BackupDir)
	cfg, err := config.LoadConfigurationByFile("testdata/sr_nearby.yaml")
	c.Assert(err, check.IsNil)
	sdkCtx, err := api.InitContextByConfig(cfg)
	c.Assert(err, check.IsNil)
	consumer := api.NewConsumerAPIByContext(sdkCtx)
	api.GetBaseLogger().SetLogLevel(0)
	defer consumer.Destroy()
	t.addInstance("A", "a", "0", true)
	t.addInstance("A", "a", "0", false)
	var getInstancesReq *api.GetOneInstanceRequest
	getInstancesReq = &api.GetOneInstanceRequest{}
	getInstancesReq.FlowID = 1
	getInstancesReq.Namespace = srNamespace
	getInstancesReq.Service = srService
	t.mocksvr.SetServiceMetadata(t.serviceToken, model.NearbyMetadataEnable, "true")
	resp, err := consumer.GetOneInstance(getInstancesReq)
	c.Assert(err, check.IsNil)
	targetInstance := resp.GetInstances()[0]

	// 熔断健康的实例
	var errCode int32
	errCode = 1
	for i := 0; i < 20; i++ {
		consumer.UpdateServiceCallResult(
			&api.ServiceCallResult{
				ServiceCallResult: *((&model.ServiceCallResult{
					CalledInstance: targetInstance,
					RetStatus:      model.RetFail,
					RetCode:        &errCode}).SetDelay(20 * time.Millisecond))})
	}
	time.Sleep(time.Second * 3)

	// 只能获取到熔断的实例
	for i := 0; i < 10; i++ {
		resp, err = consumer.GetOneInstance(getInstancesReq)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		c.Assert(resp.GetInstances()[0].GetPort(), check.Equals, targetInstance.GetPort())
	}
	t.mocksvr.ClearServiceInstances(t.testService)
	t.mocksvr.SetServiceMetadata(t.serviceToken, model.NearbyMetadataEnable, "false")
}

func (t *NearbyTestingSuite) TestCase10(c *check.C) {
	fmt.Println("----------------------TestCase10")
	defer util.DeleteDir(util.BackupDir)
	cfg, err := config.LoadConfigurationByFile("testdata/sr_nearby.yaml")
	c.Assert(err, check.IsNil)
	sdkCtx, err := api.InitContextByConfig(cfg)
	c.Assert(err, check.IsNil)
	consumer := api.NewConsumerAPIByContext(sdkCtx)
	api.GetBaseLogger().SetLogLevel(0)
	defer consumer.Destroy()
	t.addInstance("A", "a", "0", false)
	t.addInstance("A", "a", "0", false)
	var getInstancesReq = &api.GetOneInstanceRequest{}
	getInstancesReq.FlowID = 1
	getInstancesReq.Namespace = srNamespace
	getInstancesReq.Service = srService
	t.mocksvr.SetServiceMetadata(t.serviceToken, model.NearbyMetadataEnable, "true")
	resp, err := consumer.GetOneInstance(getInstancesReq)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.GetInstances()) > 0, check.Equals, true)
	t.mocksvr.ClearServiceInstances(t.testService)
	t.mocksvr.SetServiceMetadata(t.serviceToken, model.NearbyMetadataEnable, "false")
}

func (t *NearbyTestingSuite) TestCase11(c *check.C) {
	fmt.Println("----------------------TestCase11")
	defer util.DeleteDir(util.BackupDir)
	cfg, err := config.LoadConfigurationByFile("testdata/sr_nearby.yaml")
	c.Assert(err, check.IsNil)
	cfg.GetConsumer().GetCircuitBreaker().SetSleepWindow(time.Second * 20)
	c.Assert(err, check.IsNil)
	sdkCtx, err := api.InitContextByConfig(cfg)
	c.Assert(err, check.IsNil)
	consumer := api.NewConsumerAPIByContext(sdkCtx)
	api.GetBaseLogger().SetLogLevel(0)
	defer consumer.Destroy()
	t.addInstance("A", "a", "0", true)
	t.addInstance("A", "a", "0", true)
	t.addInstance("A", "a", "0", false)
	t.addInstanceV2("A", "a", "0", true, false, 0)
	t.addInstanceV2("A", "a", "0", true, true, 100)

	getAllReq := &api.GetAllInstancesRequest{}
	getAllReq.Namespace = srNamespace
	getAllReq.Service = srService
	t.mocksvr.SetServiceMetadata(t.serviceToken, model.NearbyMetadataEnable, "true")
	respAll, err := consumer.GetAllInstances(getAllReq)
	c.Assert(err, check.IsNil)
	c.Assert(len(respAll.GetInstances()), check.Equals, 5)

	getInstancesReq1 := &api.GetInstancesRequest{}
	getInstancesReq1.FlowID = 1
	getInstancesReq1.Namespace = srNamespace
	getInstancesReq1.Service = srService
	getInstancesReq1.SkipRouteFilter = true
	resp, err := consumer.GetInstances(getInstancesReq1)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.GetInstances()), check.Equals, 3)
	for _, v := range resp.GetInstances() {
		c.Assert(v.IsIsolated(), check.Equals, false)
		c.Assert(v.GetWeight() != 0, check.Equals, true)
	}

	getInstancesReq1.SkipRouteFilter = false
	resp, err = consumer.GetInstances(getInstancesReq1)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.GetInstances()), check.Equals, 2)
	for _, v := range resp.GetInstances() {
		c.Assert(v.IsHealthy(), check.Equals, true)
		c.Assert(v.IsIsolated(), check.Equals, false)
		c.Assert(v.GetWeight() != 0, check.Equals, true)
	}

	var getInstancesReq *api.GetOneInstanceRequest
	getInstancesReq = &api.GetOneInstanceRequest{}
	getInstancesReq.FlowID = 1
	getInstancesReq.Namespace = srNamespace
	getInstancesReq.Service = srService
	oneResp, err := consumer.GetOneInstance(getInstancesReq)
	c.Assert(err, check.IsNil)
	targetInstance := oneResp.GetInstance()
	// 熔断健康的实例
	fmt.Println("-------------circuitbreak on instance")
	var errCode int32
	errCode = 1
	for i := 0; i < 20; i++ {
		consumer.UpdateServiceCallResult(
			&api.ServiceCallResult{
				ServiceCallResult: *((&model.ServiceCallResult{
					CalledInstance: targetInstance,
					RetStatus:      model.RetFail,
					RetCode:        &errCode}).SetDelay(20 * time.Millisecond))})
	}
	fmt.Println("targetInstance ", targetInstance.GetId())
	time.Sleep(time.Second * 5)

	for i := 0; i < 100; i++ {
		resp1, err := consumer.GetOneInstance(getInstancesReq)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp1.GetInstances()), check.Equals, 1)
		v := resp1.GetInstances()[0]
		c.Assert(v.IsHealthy(), check.Equals, true)
		c.Assert(v.IsIsolated(), check.Equals, false)
		c.Assert(v.GetWeight() != 0, check.Equals, true)
		c.Assert(v.GetId() != targetInstance.GetId(), check.Equals, true)
		c.Assert(v.GetCircuitBreakerStatus(), check.IsNil)
	}

	getInstancesReq1.SkipRouteFilter = true
	resp, err = consumer.GetInstances(getInstancesReq1)
	c.Assert(err, check.IsNil)
	c.Assert(len(resp.GetInstances()), check.Equals, 3)
	for _, v := range resp.GetInstances() {
		c.Assert(v.IsIsolated(), check.Equals, false)
		c.Assert(v.GetWeight() != 0, check.Equals, true)
	}

	getInstancesReq1.SkipRouteFilter = false
	for i := 0; i < 100; i++ {
		resp, err = consumer.GetInstances(getInstancesReq1)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 1)
		for _, v := range resp.GetInstances() {
			c.Assert(v.IsHealthy(), check.Equals, true)
			c.Assert(v.IsIsolated(), check.Equals, false)
			c.Assert(v.GetWeight() != 0, check.Equals, true)
			c.Assert(v.GetId() != targetInstance.GetId(), check.Equals, true)
		}
	}

	getAllReq.Namespace = srNamespace
	getAllReq.Service = srService
	respAll, err = consumer.GetAllInstances(getAllReq)
	c.Assert(err, check.IsNil)
	c.Assert(len(respAll.GetInstances()), check.Equals, 5)

	fmt.Println("----------open to half open")
	time.Sleep(time.Second * 25)
	fmt.Println("----------half open to close")
	var okCode int32 = 0
	for i := 0; i < 20; i++ {
		consumer.UpdateServiceCallResult(
			&api.ServiceCallResult{
				ServiceCallResult: *((&model.ServiceCallResult{
					CalledInstance: targetInstance,
					RetStatus:      model.RetSuccess,
					RetCode:        &okCode}).SetDelay(20 * time.Millisecond))})
	}
	time.Sleep(time.Second * 5)
	getInstancesReq1.SkipRouteFilter = false
	for i := 0; i < 100; i++ {
		resp, err = consumer.GetInstances(getInstancesReq1)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.GetInstances()), check.Equals, 2)
		for _, v := range resp.GetInstances() {
			c.Assert(v.IsHealthy(), check.Equals, true)
			c.Assert(v.IsIsolated(), check.Equals, false)
			c.Assert(v.GetWeight() != 0, check.Equals, true)
		}
	}

	hasTarget := false
	getNum := 0
	for i := 0; i < 100; i++ {
		resp1, err := consumer.GetOneInstance(getInstancesReq)
		getNum++
		c.Assert(err, check.IsNil)
		v := resp1.GetInstances()[0]
		c.Assert(v.IsHealthy(), check.Equals, true)
		c.Assert(v.IsIsolated(), check.Equals, false)
		c.Assert(v.GetWeight() != 0, check.Equals, true)
		if v.GetId() != targetInstance.GetId() {
			hasTarget = true
			break
		}
	}
	log.Printf("getNum: %d", getNum)
	c.Assert(hasTarget, check.Equals, true)

	t.mocksvr.ClearServiceInstances(t.testService)
	t.mocksvr.SetServiceMetadata(t.serviceToken, model.NearbyMetadataEnable, "false")
}

// 返回一个*api.GetInstancesRequest
func (t *NearbyTestingSuite) getTestServiceReq() *api.GetInstancesRequest {
	request := &api.GetInstancesRequest{}
	request.FlowID = 1111
	request.Namespace = srNamespace
	request.Service = srService
	request.Timeout = model.ToDurationPtr(5 * time.Second)
	request.SkipRouteFilter = false
	return request
}

// type1：A-a-0实例
// type2：A-a-1实例
// type3：A-b-1实例
// type4：B-b-2实例
func (t *NearbyTestingSuite) batchAddInstance(type1, health1, type2, health2, type3, health3, type4, health4 int) {
	for i := 0; i < health1; i++ {
		t.addInstance("A", "a", "0", true)
	}
	for i := 0; i < type1-health1; i++ {
		t.addInstance("A", "a", "0", false)
	}
	for i := 0; i < health2; i++ {
		t.addInstance("A", "a", "1", true)
	}
	for i := 0; i < type2-health2; i++ {
		t.addInstance("A", "a", "1", false)
	}
	for i := 0; i < health3; i++ {
		t.addInstance("A", "b", "1", true)
	}
	for i := 0; i < type3-health3; i++ {
		t.addInstance("A", "b", "1", false)
	}
	for i := 0; i < health4; i++ {
		t.addInstance("B", "b", "2", true)
	}
	for i := 0; i < type4-health4; i++ {
		t.addInstance("B", "b", "2", false)
	}
}

// 套件名字
func (t *NearbyTestingSuite) GetName() string {
	return "ServiceRoute"
}
