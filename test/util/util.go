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

package util

import (
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/golang/protobuf/ptypes/wrappers"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"gopkg.in/check.v1"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	monitorpb "github.com/polarismesh/polaris-go/plugin/statreporter/pb/v1"
	"github.com/polarismesh/polaris-go/test/mock"
)

const (
	BackupDir = "testdata/backup"
)

var logMap = make(map[string]string)

// 将测试结果的日志存入logmap
func InsertLog(s NamingTestSuite, logContent string) {
	logMap[s.GetName()] = logContent
}

// 文件是否存在
func FileExist(path string) bool {
	_, err := os.Stat(path) // os.Stat获取文件信息
	if err != nil {
		if os.IsExist(err) {
			return true
		}
		return false
	}
	return true
}

// 目录是否存在
func DirExist(path string) bool {
	s, err := os.Stat(path)
	if err != nil {
		return false
	}
	return s.IsDir()
}

// 删除目录
func DeleteDir(path string) error {
	if DirExist(path) {
		err := os.RemoveAll(path)
		if nil != err {
			fmt.Printf("remove %s err %s\n", path, err)
		}
		return err
	}
	return fmt.Errorf("%s is not a directory", path)
}

// 比较两个实例数组内容是否一致
func SameInstances(origInst, newInst []model.Instance) bool {
	if len(newInst) != len(origInst) {
		fmt.Printf("Num of orig, %v, Num of new, %v\n", len(origInst), len(newInst))
		return false
	}
	newInstMap := make(map[string]model.Instance, len(newInst))
	for _, inst := range newInst {
		newInstMap[inst.GetId()] = inst
	}
	for _, inst := range origInst {
		newInst, ok := newInstMap[inst.GetId()]
		if !ok {
			fmt.Printf("instance %s not exists in new\n", inst.GetId())
			return false
		}
		if newInst.GetPort() != inst.GetPort() {
			fmt.Printf(
				"instance %s port %v not matched in new %v", inst.GetId(), inst.GetPort(), newInst.GetPort())
			return false
		}
		if newInst.GetHost() != inst.GetHost() {
			fmt.Printf(
				"instance %s host %s not matched in new %s", inst.GetId(), inst.GetHost(), newInst.GetHost())
			return false
		}
		if newInst.GetWeight() != inst.GetWeight() {
			fmt.Printf(
				"instance %s weight %v not matched in new %v",
				inst.GetId(), inst.GetWeight(), newInst.GetWeight())
			return false
		}
	}
	return true
}

// 将src文件夹中的文件复制到dst文件夹中，不包括子目录
func CopyDir(src string, dst string) error {
	if !DirExist(src) {
		return fmt.Errorf("fail to copy directory, src dir %s not exist", src)
	}
	if !DirExist(dst) {
		err := os.MkdirAll(dst, 0744)
		if err != nil {
			return err
		}
	}
	// 获取文件或目录相关信息
	fileInfoList, err := ioutil.ReadDir(src)
	if err != nil {
		return nil
	}
	for _, f := range fileInfoList {
		srcFileName := filepath.Join(src, f.Name())
		finfo, err := os.Stat(srcFileName)
		if err != nil {
			return err
		}
		if finfo.IsDir() {
			continue
		}
		dstFileName := filepath.Join(dst, f.Name())
		in, err := ioutil.ReadFile(srcFileName)
		if err != nil {
			return err
		}
		err = ioutil.WriteFile(dstFileName, in, 0644)
		if err != nil {
			return err
		}
	}
	return nil
}

// 返回一个默认的grpc server
func GetGrpcServer() *grpc.Server {
	grpcOptions := make([]grpc.ServerOption, 0)
	maxStreams := 100000
	grpcOptions = append(grpcOptions, grpc.MaxConcurrentStreams(uint32(maxStreams)))
	return grpc.NewServer(grpcOptions...)
}

// 创建一个namingpb service
func BuildNamingService(namespace string, service string, token string) *namingpb.Service {
	return &namingpb.Service{
		Name:      &wrappers.StringValue{Value: service},
		Namespace: &wrappers.StringValue{Value: namespace},
		Token:     &wrappers.StringValue{Value: token},
	}
}

// 启动grpc server监听
func StartGrpcServer(svr *grpc.Server, listener net.Listener) {
	go func() {
		svr.Serve(listener)
	}()
}

// 要注册的实例
type RegisteredInstance struct {
	IP      string
	Port    int
	Listen  bool
	Healthy bool
}

// 注册并启动一个mock monitor
func SetupMonitor(mockServer mock.NamingServer, svcKey model.ServiceKey, instances RegisteredInstance) (mock.MonitorServer, *grpc.Server, string, error) {
	monitorToken := uuid.New().String()
	monitorService := BuildNamingService(svcKey.Namespace, svcKey.Service, monitorToken)
	mockServer.RegisterService(monitorService)
	mockServer.RegisterRouteRule(monitorService, mockServer.BuildRouteRule(svcKey.Namespace, svcKey.Service))
	mockServer.RegisterServerInstance(instances.IP, instances.Port, svcKey.Service, monitorToken, instances.Healthy)
	monitorServer := mock.NewMonitorServer()
	grpcMonitor := GetGrpcServer()
	monitorpb.RegisterGrpcAPIServer(grpcMonitor, monitorServer)
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", instances.IP, instances.Port))
	if err != nil {
		return nil, nil, "", err
	}
	log.Printf("monitor server lietening on: %s", listener.Addr().String())
	StartGrpcServer(grpcMonitor, listener)
	return monitorServer, grpcMonitor, monitorToken, nil
}

// 初始化mock namingServer
func SetupMockDiscover(host string, port int) (*grpc.Server, net.Listener, mock.NamingServer) {
	grpcServer := grpc.NewServer()
	mockServer := mock.NewNamingServer()
	namingpb.RegisterPolarisGRPCServer(grpcServer, mockServer)
	grpcListener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
	if nil != err {
		log.Fatal(fmt.Sprintf("error listening discoverServer: %v", err))
	}
	token := mockServer.RegisterServerService(config.ServerDiscoverService)
	mockServer.RegisterServerInstance(host, port, config.ServerDiscoverService, token, true)
	return grpcServer, grpcListener, mockServer
}

// 获取某个特定的实例一定的次数
func SelectInstanceSpecificNum(c *check.C, consumerAPI api.ConsumerAPI, targetIns model.Instance, selectNum, maxTimes int) {
	allReq := &api.GetAllInstancesRequest{}
	allReq.Service = targetIns.GetService()
	allReq.Namespace = targetIns.GetNamespace()
	_, err := consumerAPI.GetAllInstances(allReq)
	c.Assert(err, check.Equals, nil)
	// log.Printf("all instances: %v", resp.GetInstances())
	request := &api.GetOneInstanceRequest{}
	request.Namespace = targetIns.GetNamespace()
	request.Service = targetIns.GetService()
	request.Timeout = model.ToDurationPtr(2 * time.Second)
	var selected int
	for i := 0; i < maxTimes; i++ {
		resp, err := consumerAPI.GetOneInstance(request)
		c.Assert(err, check.IsNil)
		c.Assert(len(resp.Instances), check.Equals, 1)
		if resp.Instances[0].GetId() == targetIns.GetId() {
			selected++
			if selectNum == selected {
				break
			}
		}
	}
	// log.Printf("cbstatus of targetInstance: %+v\n", targetIns.GetCircuitBreakerStatus())
	c.Assert(selected, check.Equals, selectNum)
}
