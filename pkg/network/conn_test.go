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

package network

import (
	"testing"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
)

// fakeClosableConn 仅用于测试，不实际持有任何 grpc 资源。
type fakeClosableConn struct {
	closed bool
}

func (f *fakeClosableConn) Close() error {
	f.closed = true
	return nil
}

// noopLogger 在测试中替代未初始化的全局日志。
type noopLogger struct{}

func (noopLogger) Tracef(string, ...interface{}) {}
func (noopLogger) Debugf(string, ...interface{}) {}
func (noopLogger) Infof(string, ...interface{})  {}
func (noopLogger) Warnf(string, ...interface{})  {}
func (noopLogger) Errorf(string, ...interface{}) {}
func (noopLogger) Fatalf(string, ...interface{}) {}
func (noopLogger) IsLevelEnabled(int) bool       { return false }
func (noopLogger) SetLogLevel(int) error         { return nil }

var _ log.Logger = noopLogger{}

func newTestConn(clusterType config.ClusterType) *Connection {
	return &Connection{
		Conn: &fakeClosableConn{},
		ConnID: ConnID{
			ID: 1,
			Service: config.ClusterService{
				ClusterType: clusterType,
			},
			Address: "127.0.0.1:8091",
		},
		logNetwork: noopLogger{},
	}
}

// TestRelease_BuiltinClusterShouldNotLazyClose 防回归：BuiltinCluster 不在
// DefaultServerServiceToConnectionControl 中，Release 时不应被当成短连接 lazyClose。
// 这是 lossless register 与 ReportClient 共用同一条 builtin 连接时引发
// "grpc: the client connection is closing" 报错的根因。
func TestRelease_BuiltinClusterShouldNotLazyClose(t *testing.T) {
	conn := newTestConn(config.BuiltinCluster)
	if !conn.acquire("test") {
		t.Fatalf("acquire should succeed on a fresh connection")
	}
	conn.Release("test")
	if !IsAvailableConnection(conn) {
		t.Fatalf("BuiltinCluster connection must remain available after Release; got lazyDestroy=1")
	}
}

// TestRelease_DiscoverClusterIsLongConnection long 连接也不应在 Release 时 lazyClose。
func TestRelease_DiscoverClusterIsLongConnection(t *testing.T) {
	conn := newTestConn(config.DiscoverCluster)
	if !conn.acquire("test") {
		t.Fatalf("acquire should succeed on a fresh connection")
	}
	conn.Release("test")
	if !IsAvailableConnection(conn) {
		t.Fatalf("DiscoverCluster (long connection) must remain available after Release")
	}
}

// TestRelease_HealthCheckClusterIsShortConnection short 连接 Release 之后必须被 lazyClose。
// 这是历史行为，确保改动没有打翻它。
func TestRelease_HealthCheckClusterIsShortConnection(t *testing.T) {
	conn := newTestConn(config.HealthCheckCluster)
	if !conn.acquire("test") {
		t.Fatalf("acquire should succeed on a fresh connection")
	}
	conn.Release("test")
	if IsAvailableConnection(conn) {
		t.Fatalf("HealthCheckCluster (short connection) must be lazyClose'd after Release")
	}
}

// TestRelease_UnknownClusterTypeShouldNotLazyClose 任意未注册到
// DefaultServerServiceToConnectionControl 的 ClusterType 也不应被默认当成短连接。
// 防止再次因 map 缺键的零值（ConnectionShort=0）导致与本次相同的隐式 bug 复发。
func TestRelease_UnknownClusterTypeShouldNotLazyClose(t *testing.T) {
	conn := newTestConn(config.ClusterType("not-registered-in-control-map"))
	if !conn.acquire("test") {
		t.Fatalf("acquire should succeed on a fresh connection")
	}
	conn.Release("test")
	if !IsAvailableConnection(conn) {
		t.Fatalf("unknown ClusterType must remain available after Release; map miss must not imply short connection")
	}
}

// TestRelease_ShortConnectionFinalReleaseClosesUnderlying 端到端覆盖短连接的引用
// 计数 + lazyClose 协同链路：在 ref>0 期间只标记 lazyDestroy、不关闭底层 TCP；
// 最终 Release 让 closeConnection 真正落到底层 Close()。
func TestRelease_ShortConnectionFinalReleaseClosesUnderlying(t *testing.T) {
	conn := newTestConn(config.HealthCheckCluster)
	if !conn.acquire("a") {
		t.Fatalf("first acquire should succeed")
	}
	if !conn.acquire("b") {
		t.Fatalf("second acquire should succeed")
	}
	fake := conn.Conn.(*fakeClosableConn)

	conn.Release("a") // ref: 2 -> 1，触发 lazyClose 但不关 TCP
	if fake.closed {
		t.Fatalf("underlying conn must not be closed while ref>0")
	}
	if IsAvailableConnection(conn) {
		t.Fatalf("short connection should be lazyDestroy'd after first Release, even with ref>0")
	}

	conn.Release("b") // ref: 1 -> 0 + lazyDestroy=1 -> closeConnection
	if !fake.closed {
		t.Fatalf("underlying conn must be closed after final Release")
	}
}
