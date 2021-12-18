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

package client

// A client implementation.

import (
	"sync/atomic"
	"time"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	connector "github.com/polarismesh/polaris-go/plugin/serverconnector/common"
	"github.com/polarismesh/polaris-go/plugin/serverconnector/sidecar/dns"
	_ "github.com/polarismesh/polaris-go/plugin/serverconnector/sidecar/dns"

	"github.com/polarismesh/polaris-go/pkg/plugin"
)

const (
	dnsTimeout time.Duration = 2 * time.Second
	UDPSize    uint16        = 65535

	protocolSidecar = "sidecar"
)

type GetNewConnFunc func() Conn

// SideCar connector
type Connector struct {
	*plugin.PluginBase
	Timeout      time.Duration
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration

	Conn *AsyncConn // 用于异步发送

	discoverConnector *connector.DiscoverConnector

	DnsID uint32

	SideCarIp   string
	SideCarPort int

	GetSyncConnFunc GetNewConnFunc
}

func getSyncConn() Conn {
	conn := new(ConnBase)
	return conn
}

// 返回结果记录结构
type RspData struct {
	RRArr         []dns.RR
	DetailErrInfo *dns.DetailErrInfoRR
	OpCode        int
	RCode         int
}

// 返回Name
func (c *Connector) Name() string {
	return protocolSidecar
}

// 初始化
func (c *Connector) Init(ctx *plugin.InitContext) {
	c.PluginBase = plugin.NewPluginBase(ctx)
	cfgValue := ctx.Config.GetGlobal().GetServerConnector().GetPluginConfig(c.Name())

	_ = cfgValue

	c.DnsID = 0
	c.discoverConnector = &connector.DiscoverConnector{}
	c.discoverConnector.Init(ctx, nil)
	c.discoverConnector.ServiceConnector = c.PluginBase
	c.GetSyncConnFunc = getSyncConn
}

// enable
func (g *Connector) IsEnable(cfg config.Configuration) bool {
	if cfg.GetGlobal().GetSystem().GetMode() == model.ModeWithAgent {
		return true
	} else {
		return false
	}
}

// 原子增加DnsID
func (c *Connector) getDnsMsgId() uint16 {
	// need lock or use atomic
	t := atomic.AddUint32(&c.DnsID, 1)
	dnsId := t >> 16
	return uint16(dnsId)
}

// 异步发送
func (c *Connector) Send(request *namingpb.DiscoverRequest) error {
	dnsMsg, err := convertDiscoverRequestToDnsMsg(request, c.getDnsMsgId())
	if err != nil {
		log.GetBaseLogger().Errorf("%s", err)
		return err
	}

	err = c.Conn.pushToWriteChanWrapper(dnsMsg)
	return err
}

// 异步接收
func (c *Connector) Recv() (*namingpb.DiscoverResponse, error) {
	select {
	case rspData := <-c.Conn.ReadChan:
		if rspData.OpCode != dns.OpCodePolarisGetResource {
			return nil, nil
		}
		rsp, err := convertRspDataToDiscoverResponse(rspData)
		if err != nil {
			return nil, err
		}
		return rsp, nil
	default:
		return nil, nil
	}
}

// dial timeout
func (c *Connector) dialTimeout() time.Duration {
	if c.Timeout != 0 {
		return c.Timeout
	}
	if c.DialTimeout != 0 {
		return c.DialTimeout
	}
	return dnsTimeout
}

// read timeout
func (c *Connector) readTimeout() time.Duration {
	if c.ReadTimeout != 0 {
		return c.ReadTimeout
	}
	return dnsTimeout
}

// write timeout
func (c *Connector) writeTimeout() time.Duration {
	if c.WriteTimeout != 0 {
		return c.WriteTimeout
	}
	return dnsTimeout
}

// getTimeoutForRequest
func (c *Connector) getTimeoutForRequest(timeout time.Duration) time.Duration {
	var requestTimeout time.Duration
	if c.Timeout != 0 {
		requestTimeout = c.Timeout
	} else {
		requestTimeout = timeout
	}
	return requestTimeout
}
