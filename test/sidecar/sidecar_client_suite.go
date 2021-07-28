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
package sidecar

import (
	_ "fmt"
	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/plugin/serverconnector/sidecar/client"
	"github.com/golang/protobuf/ptypes/wrappers"
	"gopkg.in/check.v1"
)


// sidecar逻辑测试
type SideCarClientSuit struct {
	connector *client.Connector
}

// SetUpSuite
func (s *SideCarClientSuit) SetUpSuite(c *check.C) {
	s.connector = new(client.Connector)
	s.connector.GetSyncConnFunc = GetSideCarMockConn
}

// TearDownSuite
func (s *SideCarClientSuit) TearDownSuite(c *check.C) {

}

// 测试注册实例
func (s *SideCarClientSuit) TestRegisterInstance(c *check.C) {
	req := model.InstanceRegisterRequest{
		Service: "angevil_test",
		Namespace: "Test",
		Host: "127.0.0.1",
		Port: 12345,
	}
	rsp, err := s.connector.RegisterInstance(&req)
	c.Assert(err, check.IsNil)
	c.Assert(rsp, check.NotNil)
	return
}

// 测试注册实例
func (s *SideCarClientSuit) TestRegisterInstance01(c *check.C) {
	req := model.InstanceRegisterRequest{
		Service:   "angevil_test",
		Namespace: "Test",
		Host:      ShouldErrHost,
		Port:      12345,
	}
	rsp, err := s.connector.RegisterInstance(&req)
	c.Assert(err, check.NotNil)
	c.Assert(rsp, check.IsNil)
	return
}

// 测试反注册
func (s *SideCarClientSuit) TestDeRegisterInstance(c *check.C)  {
	req := model.InstanceDeRegisterRequest{
		Service:      "angevil_test",
		ServiceToken: "test1111",
		Namespace:    "Test",
		InstanceID:   "xxxxxxxx",
	}
	err := s.connector.DeregisterInstance(&req)
	c.Assert(err, check.IsNil)
}

// 测试心跳
func (s *SideCarClientSuit) TestHeatBeat(c *check.C)  {
	req := model.InstanceHeartbeatRequest{
		Service:      "angevil_test",
		ServiceToken: "test1111",
		Namespace:    "Test",
		InstanceID:   "xxxxxxxx",
		Host: "127.0.0.1",
		Port: 12345,
	}
	err := s.connector.Heartbeat(&req)
	c.Assert(err, check.IsNil)
}

// 测试GetInstances
func (s *SideCarClientSuit) TestGetInstances(c *check.C)  {
	req := namingpb.DiscoverRequest{
		Type:                 namingpb.DiscoverRequest_INSTANCE,
		Service:              &namingpb.Service{
			Name:                 &wrappers.StringValue{Value: "angevil_test"},
			Namespace:            &wrappers.StringValue{Value: "Test"},
		},
	}
	rsp, err := s.connector.SyncGetResourceReq(&req)
	c.Assert(err, check.IsNil)
	c.Assert(rsp.Instances[0].Host.GetValue(), check.Equals, "127.0.0.1")
}

// 测试ReportClient
func (s *SideCarClientSuit) TestReportClient(c *check.C)  {
	req := model.ReportClientRequest{
		Host: "127.0.0.1",
		Version: "10",
	}
	rsp, err := s.connector.ReportClient(&req)
	c.Assert(err, check.IsNil)
	_ = rsp
}

/*
func (s *SideCarClientSuit) TestRegisterHandler(c *check.C)  {
	svcEventHandler := serverconnector.ServiceEventHandler {
		ServiceEventKey: &model.ServiceEventKey{
			ServiceKey: model.ServiceKey{
				Namespace: "Test",
				Service:   "angevil_tst",
			},
		},
	}
	err := s.connector.RegisterServiceHandler(&svcEventHandler)
	c.Assert(err, check.IsNil)
}
*/
