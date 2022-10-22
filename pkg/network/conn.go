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

// Package network provides network related functions.
package network

import (
	"sync"
	"sync/atomic"

	"google.golang.org/grpc"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
)

const (
	// ConnectionShort 短连接模式
	ConnectionShort int = iota
	// ConnectionLong 长连接模式
	ConnectionLong
)

var (
	// DefaultServerServiceToConnectionControl 不同系统服务使用不同的连接持有模式
	DefaultServerServiceToConnectionControl = map[config.ClusterType]int{
		config.DiscoverCluster:    ConnectionLong,
		config.ConfigCluster:      ConnectionShort,
		config.HealthCheckCluster: ConnectionShort,
		config.MonitorCluster:     ConnectionShort,
	}
)

// ClosableConn you can close the connection object
type ClosableConn interface {
	// Close 关闭并释放
	Close() error
}

// Connection GRPC connection object containing a unique connection ID
type Connection struct {
	ConnID
	// Conn connect the entity of grpc connection
	Conn ClosableConn
	// closed whether to shut down
	closed bool
	// reference number
	ref int32
	// Is the destruction started? 0 not started, 1 started
	lazyDestroy uint32
	// to apply for the lock
	mutex sync.Mutex
}

// IsAvailableConnection whether the connection is available
func IsAvailableConnection(conn *Connection) bool {
	if nil == conn {
		return false
	}
	return atomic.LoadUint32(&conn.lazyDestroy) == 0
}

// acquire Try to occupy the connection, ref+1, return true on success, false otherwise
func (c *Connection) acquire(opKey string) bool {
	if atomic.LoadUint32(&c.lazyDestroy) == 1 {
		return false
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if atomic.LoadUint32(&c.lazyDestroy) == 1 {
		return false
	}
	curRef := atomic.AddInt32(&c.ref, 1)
	log.GetNetworkLogger().Tracef("connection %v: acquired, curRef is %d", c.ConnID, curRef)
	return true
}

// closeConnection close the connection
func (c *Connection) closeConnection(force bool) bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	curRef := atomic.LoadInt32(&c.ref)
	if (force || curRef <= 0) && !c.closed {
		c.closed = true
		_ = c.Conn.Close()
		log.GetNetworkLogger().Infof("connection %v: close, curRef is %d", c.ConnID, curRef)
	}
	return c.closed
}

// ForceClose forcibly close the connection
func (c *Connection) ForceClose() bool {
	return c.closeConnection(true)
}

// lazyClose lazy collection
func (c *Connection) lazyClose(force bool) {
	atomic.StoreUint32(&c.lazyDestroy, 1)
	curRef := atomic.LoadInt32(&c.ref)
	log.GetNetworkLogger().Tracef("connection %v: lazyClose, curRef is %d", c.ConnID, curRef)
	if force || curRef <= 0 {
		// Set the status to disallow the connection to be allocated
		c.closeConnection(force)
		return
	}
}

// Release the connection release
func (c *Connection) Release(opKey string) {
	nextValue := atomic.AddInt32(&c.ref, -1)
	log.GetNetworkLogger().Tracef(
		"connection %s: pending to release for op %s, curRef is %d", c.ConnID, opKey, nextValue)
	var closed bool
	if nextValue <= 0 && atomic.LoadUint32(&c.lazyDestroy) == 1 {
		// 可以释放
		closed = c.closeConnection(false)
	}
	if closed {
		return
	}
	// lazy closing short connections
	if DefaultServerServiceToConnectionControl[c.ConnID.Service.ClusterType] == ConnectionShort &&
		IsAvailableConnection(c) {
		c.lazyClose(false)
	}
}

// ToGRPCConn convert to a grpc connection
func ToGRPCConn(conn ClosableConn) *grpc.ClientConn {
	return conn.(*grpc.ClientConn)
}
