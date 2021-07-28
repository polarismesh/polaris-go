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
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"google.golang.org/grpc"
	"sync"
	"sync/atomic"
)

const (
	//短连接模式
	ConnectionShort int = iota
	//长连接模式
	ConnectionLong
)

var (
	//不同系统服务使用不同的连接持有模式
	DefaultServerServiceToConnectionControl = map[config.ClusterType]int{
		config.DiscoverCluster:    ConnectionLong,
		config.HealthCheckCluster: ConnectionShort,
		config.MonitorCluster:     ConnectionShort,
	}
)

//可关闭连接对象
type ClosableConn interface {
	//关闭并释放
	Close() error
}

//GRPC连接对象，含唯一连接ID
type Connection struct {
	ConnID
	//连接实体
	Conn ClosableConn
	//是否关闭
	closed bool
	//引用数
	ref int32
	//是否已经开始销毁，0未开始，1开始
	lazyDestroy uint32
	//申请锁
	mutex sync.Mutex
}

//连接是否可用
func IsAvailableConnection(conn *Connection) bool {
	if nil == conn {
		return false
	}
	return atomic.LoadUint32(&conn.lazyDestroy) == 0
}

//尝试占据连接，ref+1, 占据成功返回true，否则返回false
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

//关闭连接
func (c *Connection) closeConnection(force bool) bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	curRef := atomic.LoadInt32(&c.ref)
	if (force || curRef <= 0) && !c.closed {
		c.closed = true
		c.Conn.Close()
		log.GetNetworkLogger().Infof("connection %v: close, curRef is %d", c.ConnID, curRef)
	}
	return c.closed
}

func (c *Connection) ForceClose() bool {
	return c.closeConnection(true)
}

//懒回收
func (c *Connection) lazyClose(force bool) {
	atomic.StoreUint32(&c.lazyDestroy, 1)
	curRef := atomic.LoadInt32(&c.ref)
	log.GetNetworkLogger().Tracef("connection %v: lazyClose, curRef is %d", c.ConnID, curRef)
	if force || curRef <= 0 {
		//设置状态，不允许该连接再继续分配
		c.closeConnection(force)
		return
	}
}

//释放连接
func (c *Connection) Release(opKey string) {
	nextValue := atomic.AddInt32(&c.ref, -1)
	log.GetNetworkLogger().Tracef(
		"connection %s: pending to release for op %s, curRef is %d", c.ConnID, opKey, nextValue)
	var closed bool
	if nextValue <= 0 && atomic.LoadUint32(&c.lazyDestroy) == 1 {
		//可以释放
		closed = c.closeConnection(false)
	}
	if closed {
		return
	}
	//懒关闭短连接
	if DefaultServerServiceToConnectionControl[c.ConnID.Service.ClusterType] == ConnectionShort &&
		IsAvailableConnection(c) {
		c.lazyClose(false)
	}
}

//转换成GRPC连接
func ToGRPCConn(conn ClosableConn) *grpc.ClientConn {
	return conn.(*grpc.ClientConn)
}
