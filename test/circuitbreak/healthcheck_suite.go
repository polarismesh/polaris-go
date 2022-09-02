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
	"bytes"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/golang/protobuf/ptypes/wrappers"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"gopkg.in/check.v1"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/local"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/plugin/healthcheck/utils"
	"github.com/polarismesh/polaris-go/test/mock"
	"github.com/polarismesh/polaris-go/test/util"
)

const (
	// 测试的默认命名空间
	detectNamespace = "testod"
	// 测试的默认服务名
	detectService = "svc1"
	// 测试服务器的默认地址
	detectIPAdress = "127.0.0.1"
	// 测试服务器的端口
	detectPort = 8008
)

// HealthCheckTestingSuite 消费者API测试套
type HealthCheckTestingSuite struct {
	grpcServer   *grpc.Server
	grpcListener net.Listener
	serviceToken string
}

// GetName 套件名字
func (t *HealthCheckTestingSuite) GetName() string {
	return "HealthCheckSuite"
}

// SetUpSuite 启动测试套程序
func (t *HealthCheckTestingSuite) SetUpSuite(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	grpcOptions := make([]grpc.ServerOption, 0)
	maxStreams := 100000
	grpcOptions = append(grpcOptions, grpc.MaxConcurrentStreams(uint32(maxStreams)))

	// get the grpc server wired up
	grpc.EnableTracing = true

	ipAddr := detectIPAdress
	shopPort := detectPort
	var err error
	t.grpcServer = grpc.NewServer(grpcOptions...)
	t.serviceToken = uuid.New().String()
	mockServer := mock.NewNamingServer()
	token := mockServer.RegisterServerService(config.ServerDiscoverService)
	mockServer.RegisterServerInstance(ipAddr, shopPort, config.ServerDiscoverService, token, true)
	mockServer.RegisterNamespace(&namingpb.Namespace{
		Name:    &wrappers.StringValue{Value: detectNamespace},
		Comment: &wrappers.StringValue{Value: "for healthCheck api test"},
		Owners:  &wrappers.StringValue{Value: "healthCheck"},
	})
	testService := &namingpb.Service{
		Name:      &wrappers.StringValue{Value: detectService},
		Namespace: &wrappers.StringValue{Value: detectNamespace},
		Token:     &wrappers.StringValue{Value: t.serviceToken},
	}
	mockServer.RegisterService(testService)
	mockServer.GenTestInstancesWithHostPort(testService, 100, "127.0.0.1", 1024)
	namingpb.RegisterPolarisGRPCServer(t.grpcServer, mockServer)
	t.grpcListener, err = net.Listen("tcp", fmt.Sprintf("%s:%d", ipAddr, shopPort))
	if err != nil {
		log.Fatal(fmt.Sprintf("error listening appserver %v", err))
	}
	log.Printf("appserver listening on %s:%d\n", ipAddr, shopPort)
	go func() {
		t.grpcServer.Serve(t.grpcListener)
	}()
}

// TearDownSuite 结束测试套程序
func (t *HealthCheckTestingSuite) TearDownSuite(c *check.C) {
	t.grpcServer.Stop()
	if util.DirExist(util.BackupDir) {
		os.RemoveAll(util.BackupDir)
	}
	util.InsertLog(t, c.GetTestLog())
}

// TestTCPDetection 测试TCP健康探测
func (t *HealthCheckTestingSuite) TestTCPDetection(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	log.Printf("start to TestTCPDetection")
	Logic(c, startTCPServer, nil, 0, true)
}

// TestFailTCPDetection 测试TCP探测失败
func (t *HealthCheckTestingSuite) TestFailTCPDetection(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	log.Printf("start to TestTCPDetectionFail")
	Logic(c, startTCPServer, nil, 0, false)
}

// TestHTTPDetection 测试HTTP健康探测
func (t *HealthCheckTestingSuite) TestHTTPDetection(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	log.Printf("start to TestHTTPDetection")
	Logic(c, nil, startHTTPServer, 3, true)
}

// TestFailHTTPDetection 测试UDP探测失败
func (t *HealthCheckTestingSuite) TestFailHTTPDetection(c *check.C) {
	defer util.DeleteDir(util.BackupDir)
	log.Printf("start to TestFailHTTPDetection")
	Logic(c, nil, startHTTPServer, 0, false)
}

// Logic 执行业务逻辑测试
func Logic(c *check.C,
	tcpServer func(string, int, []byte) error, httpServer func(string, int, int) error, index int, checkFlag bool) {
	cfg, err := config.LoadConfigurationByFile("testdata/healthcheck.yaml")
	c.Assert(err, check.IsNil)
	if nil == httpServer && nil != tcpServer {
		cfg.Consumer.HealthCheck.Chain = []string{"tcp"}
	}
	if nil != httpServer && nil == tcpServer {
		cfg.Consumer.HealthCheck.Chain = []string{"http"}
	}
	sdkCtx, err := api.InitContextByConfig(cfg)
	c.Assert(err, check.IsNil)
	defer sdkCtx.Destroy()
	consumerAPI := api.NewConsumerAPIByContext(sdkCtx)
	// 随机获取一个实例，并将这个实例作为熔断的目标
	request := &api.GetInstancesRequest{}
	request.Namespace = detectNamespace
	request.Service = detectService
	request.Timeout = model.ToDurationPtr(2 * time.Second)
	resp, err := consumerAPI.GetInstances(request)
	c.Assert(err, check.IsNil)
	if len(resp.Instances) < 3 {
		c.Assert(len(resp.Instances), check.Equals, 3)
	}
	cbID := resp.Instances[index].GetId()
	log.Printf("The instance to ciucuitbreak by errcount: %v", cbID)
	// 开始没有健康探测状态
	respInstance := resp.Instances[index]
	localInstanceValue := respInstance.(local.InstanceLocalValue)
	c.Assert(localInstanceValue.GetActiveDetectStatus(), check.IsNil)
	var failCode int32 = 1
	for i := 0; i < 10; i++ {
		consumerAPI.UpdateServiceCallResult(&api.ServiceCallResult{ServiceCallResult: model.ServiceCallResult{
			CalledInstance: resp.Instances[index],
			RetStatus:      model.RetFail,
			RetCode:        &failCode,
			Delay:          request.Timeout}})
	}
	// 2s 之后，被熔断
	time.Sleep(2 * time.Second)
	c.Assert(respInstance.GetCircuitBreakerStatus(), check.NotNil)
	c.Assert(respInstance.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.Open)
	// openTime := localValues.GetCircuitBreakerStatus().GetStartTime()
	CheckInstanceAvailable(c, consumerAPI, respInstance, false, detectNamespace, detectService)

	if checkFlag {
		go func() {
			// 起tcp服务
			address := utils.GetAddressByInstance(resp.Instances[index])
			log.Printf("Start  Server:%s", address)
			if tcpServer != nil {
				err = tcpServer(address, 10, []byte{0x00, 0x00, 0x43, 0x21})
			} else if httpServer != nil {
				err = httpServer(address, 10, 200)
			}
			c.Assert(err, check.IsNil)
		}()
	}
	// 5s 之后，探测为Health
	time.Sleep(5 * time.Second)
	if checkFlag {
		c.Assert(localInstanceValue.GetActiveDetectStatus(), check.NotNil)
		c.Assert(localInstanceValue.GetActiveDetectStatus().GetStatus(), check.Equals, model.Healthy)
		// 熔断状态修改为HalfOpen
		c.Assert(respInstance.GetCircuitBreakerStatus().GetStatus(), check.Equals, model.HalfOpen)
		CheckInstanceAvailable(c, consumerAPI, respInstance, true, detectNamespace, detectService)
	} else {
		c.Assert(localInstanceValue.GetActiveDetectStatus(), check.NotNil)
		c.Assert(localInstanceValue.GetActiveDetectStatus().GetStatus(), check.Equals, model.Dead)
		// 熔断状态修改为HalfOpen
		c.Assert(respInstance.GetCircuitBreakerStatus().GetStatus() != model.HalfOpen, check.Equals, true)
		CheckInstanceAvailable(c, consumerAPI, respInstance, false, detectNamespace, detectService)
	}
}

// startTCPServer 起一个TCP服务
func startTCPServer(address string, sTime int, retByte []byte) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		fmt.Println("Error listening", err.Error())
		return err // 终止程序
	}
	defer listener.Close()
	// 监听并接受来自客户端的连接
	for {
		task := func() error {
			conn, err := listener.Accept()
			if err != nil {
				fmt.Println("Error accepting", err.Error())
				return err // 终止程序
			}
			defer conn.Close()
			go func(conn net.Conn) {
				buf := make([]byte, 4)
				recvBuf := bytes.Buffer{}
				for {
					var n int
					n, err = conn.Read(buf[0:])
					if n > 0 {
						recvBuf.Write(buf[0:n])
						if recvBuf.Len() >= 4 {
							time.Sleep(time.Duration(sTime) * time.Millisecond)
							conn.Write(retByte)
							break
						}
					}
				}
			}(conn)
			return nil
		}
		if err := task(); err != nil {
			return err
		}
	}
}

// startHttpServer 启动一个Http服务
func startHTTPServer(address string, sTime int, statusCode int) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Duration(sTime) * time.Millisecond)
		log.Printf("receive health http detection")
		w.WriteHeader(statusCode)
	})
	log.Printf("httpserver ready, addr %s", address)
	err := http.ListenAndServe(address, mux)
	if err != nil {
		log.Printf("httpserver err %v", err)
	}
	return err
}
