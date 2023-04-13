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
	"google.golang.org/grpc"
	"gopkg.in/check.v1"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/test/mock"
	"github.com/polarismesh/specification/source/go/api/v1/service_manage"
)

const (
	// BackupDir is the backup directory for test
	BackupDir = "testdata/backup"
)

var logMap = make(map[string]string)

// InsertLog 将测试结果的日志存入logmap
func InsertLog(s NamingTestSuite, logContent string) {
	logMap[s.GetName()] = logContent
}

// FileExist 文件是否存在
func FileExist(path string) bool {
	_, err := os.Stat(path) // os.Stat获取文件信息
	if err != nil {
		return os.IsExist(err)
	}
	return true
}

// DirExist 目录是否存在
func DirExist(path string) bool {
	s, err := os.Stat(path)
	if err != nil {
		return false
	}
	return s.IsDir()
}

// DeleteDir 删除目录
func DeleteDir(path string) error {
	if DirExist(path) {
		err := os.RemoveAll(path)
		if err != nil {
			fmt.Printf("remove %s err %s\n", path, err)
		}
		return err
	}
	return fmt.Errorf("%s is not a directory", path)
}

// SameInstances 比较两个实例数组内容是否一致
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

// CopyDir 将src文件夹中的文件复制到dst文件夹中，不包括子目录
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

// GetGrpcServer 返回一个默认的grpc server
func GetGrpcServer() *grpc.Server {
	grpcOptions := make([]grpc.ServerOption, 0)
	maxStreams := 100000
	grpcOptions = append(grpcOptions, grpc.MaxConcurrentStreams(uint32(maxStreams)))
	return grpc.NewServer(grpcOptions...)
}

// BuildNamingService 创建一个namingpb service
func BuildNamingService(namespace string, service string, token string) *service_manage.Service {
	return &service_manage.Service{
		Name:      &wrappers.StringValue{Value: service},
		Namespace: &wrappers.StringValue{Value: namespace},
		Token:     &wrappers.StringValue{Value: token},
	}
}

// StartGrpcServer 启动grpc server监听
func StartGrpcServer(svr *grpc.Server, listener net.Listener) {
	go func() {
		svr.Serve(listener)
	}()
}

// RegisteredInstance 要注册的实例
type RegisteredInstance struct {
	IP      string
	Port    int
	Listen  bool
	Healthy bool
}

// SetupMockDiscover 初始化mock namingServer
func SetupMockDiscover(host string, port int) (*grpc.Server, net.Listener, mock.NamingServer) {
	grpcServer := grpc.NewServer()
	mockServer := mock.NewNamingServer()
	service_manage.RegisterPolarisGRPCServer(grpcServer, mockServer)
	grpcListener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		log.Fatal(fmt.Sprintf("error listening discoverServer: %v", err))
	}
	token := mockServer.RegisterServerService(config.ServerDiscoverService)
	mockServer.RegisterServerInstance(host, port, config.ServerDiscoverService, token, true)
	return grpcServer, grpcListener, mockServer
}

// SelectInstanceSpecificNum 获取某个特定的实例一定的次数
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
