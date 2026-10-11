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

package startup

import (
	"testing"
	"time"

	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/serverconnector"
)

// unimplStream 模拟旧版服务端：Send 成功（gRPC stream 缓冲），
// 但第一次 Recv 返回 Unimplemented——复现线上"established 后立即 Unimplemented"的时序。
type unimplStream struct{ closed bool }

func (s *unimplStream) Send(*apiservice.ClientEvent) error { return nil }
func (s *unimplStream) Recv() (*apiservice.ClientEvent, error) {
	return nil, status.Error(codes.Unimplemented, "unknown method WatchClientEvents for service v1.PolarisGRPC")
}
func (s *unimplStream) Close() error { s.closed = true; return nil }

// stubConnector 仅实现 WatchClientEvents，其余方法 panic（测试不触达）。
// 通过嵌入 serverconnector.ServerConnector 接口避免逐一实现。
type stubConnector struct {
	serverconnector.ServerConnector
	stream serverconnector.ClientEventStream
}

func (c *stubConnector) WatchClientEvents() (serverconnector.ClientEventStream, error) {
	return c.stream, nil
}

// TestRunLoop_UnimplementedStopsReconnect 回归线上 bug：
// Send 成功 + Recv 返回 Unimplemented 时，watcher 必须停止重连而非无限循环刷日志。
func TestRunLoop_UnimplementedStopsReconnect(t *testing.T) {
	stream := &unimplStream{}
	w := NewClientEventWatcher(&stubConnector{stream: stream}, "c1", nil, nil)

	// Start 会进入 runLoop；若 bug 未修复会无限重连（每秒一次），无法停止
	w.Start()

	// 最多等 3s，watcher 应已因 Unimplemented 退出（done channel 关闭）
	deadline := time.After(3 * time.Second)
	select {
	case <-w.done:
		// watcher 已退出 ✅
	case <-deadline:
		t.Fatal("watcher 未在 3s 内因 Unimplemented 停止，可能仍在无限重连")
	}
	assert.True(t, w.isClosed() == false, "Unimplemented 退出不应置 closeCh")
	// 确认 stream 被 Close 释放
	assert.True(t, stream.closed, "退出前应 Close stream 释放连接")
}

// TestRunLoop_UnimplementedFromConnectPath connectAndWatch 直接返回 Unimplemented 也应停止。
func TestRunLoop_UnimplementedFromConnectPath(t *testing.T) {
	// 用一个 WatchClientEvents 直接返回 Unimplemented 错误的 connector
	w := NewClientEventWatcher(&failingConnector{
		err: status.Error(codes.Unimplemented, "unknown method"),
	}, "c1", nil, nil)
	w.Start()
	select {
	case <-w.done:
	case <-time.After(3 * time.Second):
		t.Fatal("connect 路径 Unimplemented 也应停止")
	}
}

// failingConnector 的 WatchClientEvents 直接返回错误（建流阶段失败）。
type failingConnector struct {
	serverconnector.ServerConnector
	err error
}

func (c *failingConnector) WatchClientEvents() (serverconnector.ClientEventStream, error) {
	return nil, c.err
}

// 占位：确保 model 包被引用（stubConnector 嵌入接口需要）
var _ = model.ErrCodeNetworkError
