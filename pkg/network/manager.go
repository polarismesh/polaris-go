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
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/google/uuid"
	"github.com/modern-go/reflect2"
	"sync/atomic"
	"time"
)

//连接标识
type ConnID struct {
	//唯一ID
	ID uint32
	//所属服务
	Service config.ClusterService
	//目标server地址
	Address string
	//所属的实例信息
	instance model.Instance
}

//TOString方法
func (c ConnID) String() string {
	return fmt.Sprintf("{ID: %d, Address: %s}", c.ID, c.Address)
}

//当前客户端相关信息
type ClientInfo struct {
	IP      atomic.Value
	HashKey atomic.Value
}

//获取IP值
func (c *ClientInfo) GetIPString() string {
	ipValue := c.IP.Load()
	if reflect2.IsNil(ipValue) {
		return ""
	}
	return ipValue.(string)
}

//获取Hash值
func (c *ClientInfo) GetHashKey() []byte {
	hashKeyValue := c.HashKey.Load()
	if reflect2.IsNil(hashKeyValue) {
		return uuid.New().NodeID()
	}
	return hashKeyValue.([]byte)
}

//通用的连接管理器
type ConnectionManager interface {

	//设置当前协议的连接创建器
	SetConnCreator(creator ConnCreator)

	//销毁并释放连接管理器
	Destroy()

	//获取并占用连接
	GetConnection(opKey string, clusterType config.ClusterType) (*Connection, error)

	//通过传入一致性hashKey的方式获取链接
	GetConnectionByHashKey(opKey string, clusterType config.ClusterType, hashKey []byte) (*Connection, error)

	//报告连接故障
	ReportConnectionDown(connID ConnID)

	//上报接口调用成功
	ReportSuccess(connID ConnID, retCode int32, timeout time.Duration)

	//上报接口调用失败
	ReportFail(connID ConnID, retCode int32, timeout time.Duration)

	//更新服务地址
	UpdateServers(svcEventKey model.ServiceEventKey)

	//获取当前客户端信息
	GetClientInfo() *ClientInfo

	//discover服务是否已经就绪
	IsReady() bool

	//计算hash Key对应的实例
	GetHashExpectedInstance(clusterType config.ClusterType, hash []byte) (string, model.Instance, error)

	//直接通过addr连接，慎使用
	ConnectByAddr(clusterType config.ClusterType, addr string, instance model.Instance) (*Connection, error)
}
