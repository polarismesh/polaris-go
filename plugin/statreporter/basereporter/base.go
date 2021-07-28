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

package basereporter

import (
	"context"
	sysconfig "github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/flow/data"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/network"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/localregistry"
	monitorpb "github.com/polarismesh/polaris-go/plugin/statreporter/pb/v1"
	"time"
)

const (
	opReportStat      = "ReportStat"
	MinReportInterval = 1 * time.Second
)

//一个与monitor连接的stream，用于发送和接收某种统计数据
type ClientStream struct {
	ConnectFunc CreateClientStreamFunc
	cancelFunc  context.CancelFunc
	Stream      CloseAbleStream
	conn        *network.Connection
	IPString    string
}

//关闭clientStream
func (cs *ClientStream) DestroyStream() error {
	if cs.Stream != nil {
		cs.Stream.CloseSend()
		cs.Stream = nil
	}
	if cs.cancelFunc != nil {
		cs.cancelFunc()
		cs.cancelFunc = nil
	}
	if cs.conn != nil {
		cs.conn.Release(opReportStat)
		cs.conn = nil
	}
	return nil
}

//创建一个clientStream
func (cs *ClientStream) CreateStream(conn *network.Connection) error {
	var err error
	cs.conn = conn
	client := monitorpb.NewGrpcAPIClient(network.ToGRPCConn(conn.Conn))
	cs.Stream, cs.cancelFunc, err = cs.ConnectFunc(client)
	if err != nil {
		if cs.cancelFunc != nil {
			cs.cancelFunc()
			cs.cancelFunc = nil
		}
		return err
	}
	return nil
}

//连接monitor的reporter的基类，具备一些大多数reporter需要的字段和连接管理方法
type Reporter struct {
	*plugin.PluginBase
	*common.RunContext
	SDKToken      model.SDKToken
	ClientStreams []*ClientStream
	ConnManager   network.ConnectionManager

	//插件工厂
	Plugins plugin.Supplier
	//本地缓存插件
	Registry localregistry.LocalRegistry
}

type CloseAbleStream interface {
	CloseSend() error
}

//根据与monitor的连接创建clientStream
type CreateClientStreamFunc func(conn monitorpb.GrpcAPIClient) (client CloseAbleStream,
	cancelFunc context.CancelFunc, err error)

//获取一个baseReporter
func InitBaseReporter(ctx *plugin.InitContext, streamFunc []CreateClientStreamFunc) (*Reporter, error) {
	result := &Reporter{
		PluginBase:  plugin.NewPluginBase(ctx),
		RunContext:  common.NewRunContext(),
		SDKToken:    model.SDKToken{},
		ConnManager: ctx.ConnManager,
		Plugins:     ctx.Plugins,
		Registry:    nil,
	}
	result.ClientStreams = make([]*ClientStream, len(streamFunc))
	for idx, f := range streamFunc {
		result.ClientStreams[idx] = &ClientStream{
			ConnectFunc: f,
			cancelFunc:  nil,
			Stream:      nil,
		}
	}
	var err error
	result.Registry, err = data.GetRegistry(ctx.Config, ctx.Plugins)
	if err != nil {
		return nil, err
	}
	t, _ := ctx.ValueCtx.GetValue(model.ContextKeyToken)
	result.SDKToken = t.(model.SDKToken)
	return result, nil
}

//连接monitor，并且创建对应的clientStream
func (s *Reporter) CreateStreamWithIndex(idx int) error {
	var err error
	if err != nil {
		return err
	}
	var conn *network.Connection
	conn, err = s.ConnManager.GetConnection(opReportStat, sysconfig.MonitorCluster)
	if err != nil {
		return err
	}
	err = s.ClientStreams[idx].CreateStream(conn)
	if err != nil {
		return s.ClientStreams[idx].DestroyStream()
	}
	return nil
}

//断开与monitor的连接并且销毁所有clientStream
func (s *Reporter) DestroyStreamWithIndex(idx int) error {
	cs := s.ClientStreams[idx]
	cs.DestroyStream()
	return nil
}

//获取一个与monitor的连接
func (s *Reporter) GetMonitorConnection() (*network.Connection, error) {
	conn, err := s.ConnManager.GetConnection(opReportStat, sysconfig.MonitorCluster)
	return conn, err
}

//释放一个与monitor的连接
func (s *Reporter) ReleaseMonitorConnection(conn *network.Connection) {
	conn.Release(opReportStat)
}

//获取对应的clientStream用于发送和接收数据
func (s *Reporter) GetClientStream(idx int) CloseAbleStream {
	return s.ClientStreams[idx].Stream
}

func (s *Reporter) GetClientStreamServer(idx int) network.ConnID {
	return s.ClientStreams[idx].conn.ConnID
}

//获取客户端的ip地址
func (s *Reporter) GetIPString() string {
	return s.ConnManager.GetClientInfo().GetIPString()
}
