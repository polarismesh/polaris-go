// Tencent is pleased to support the open source community by making polaris-go available.
//
// Copyright (C) 2021 Tencent. All rights reserved.
//
// Licensed under the BSD 3-Clause License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// https://opensource.org/licenses/BSD-3-Clause
//
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package grpc

import (
	"context"
	"fmt"
	"sync"

	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/network"
	"github.com/polarismesh/polaris-go/pkg/plugin/serverconnector"
	connector "github.com/polarismesh/polaris-go/plugin/serverconnector/common"
)

// WatchClientEvents 建立 WatchClientEvents 双向流。
// 复用 ReportClient 的 discover 集群连接获取模式，但双向流不立即释放连接——
// 连接由返回的 ClientEventStream 长期持有，调用方调用 Close 时释放。
// ctx 暂未使用，流的生命周期由返回的 ClientEventStream.Close 控制。
func (g *Connector) WatchClientEvents() (serverconnector.ClientEventStream, error) {
	if err := g.waitDiscoverReady(); err != nil {
		return nil, err
	}
	opKey := connector.OpKeyWatchClientEvents
	// 获取 discover 集群连接（与 ReportClient 同集群，服务端按 clientID 绑定 stream）
	conn, err := g.connManager.GetConnection(opKey, config.DiscoverCluster)
	if err != nil {
		return nil, model.NewSDKError(model.ErrCodeNetworkError, err,
			fmt.Sprintf("fail to get connection, opKey %s", opKey))
	}
	namingClient := apiservice.NewPolarisGRPCClient(network.ToGRPCConn(conn.Conn))
	// timeout=0 时 CreateHeadersContext 用 context.Background（不设 deadline），适合长期持有的双向流；
	// 再包一层 WithCancel 供 Close 时中断阻塞的 Recv
	baseCtx, _ := connector.CreateHeadersContext(0, connector.AppendAuthHeader(g.token))
	grpcCtx, cancel := context.WithCancel(baseCtx)
	stream, err := namingClient.WatchClientEvents(grpcCtx)
	if err != nil {
		conn.Release(opKey)
		cancel()
		return nil, model.NewSDKError(model.ErrCodeNetworkError, err,
			fmt.Sprintf("fail to open watch client events stream, opKey %s", opKey))
	}
	return &clientEventStream{
		PolarisGRPC_WatchClientEventsClient: stream,
		conn:                                conn,
		opKey:                               opKey,
		cancel:                              cancel,
	}, nil
}

// clientEventStream 封装 WatchClientEvents 双向流，嵌入 gRPC 生成的流接口复用 Send/Recv/CloseSend，
// 额外持有底层连接与取消函数，Close 时统一释放。
type clientEventStream struct {
	apiservice.PolarisGRPC_WatchClientEventsClient
	conn      *network.Connection
	opKey     string
	cancel    context.CancelFunc
	closeOnce sync.Once
	closeErr  error
}

// Close 关闭双向流并释放底层连接与上下文。
// 先 CloseSend 通知服务端发送完毕，再取消上下文中断可能阻塞的 Recv，最后归还连接。
// 使用 sync.Once 保证幂等（watcher 关闭与重连路径都会调用，避免重复 Release 连接）；
// 首次调用记录 CloseSend 的错误并在后续调用中重复返回，便于调用方观测关流异常。
func (s *clientEventStream) Close() error {
	s.closeOnce.Do(func() {
		s.closeErr = s.CloseSend()
		if s.cancel != nil {
			s.cancel()
		}
		s.conn.Release(s.opKey)
	})
	return s.closeErr
}
