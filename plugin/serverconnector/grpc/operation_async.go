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
 * specific language governing permissions and limitations under thhe License.
 */

package grpc

import (
	"context"
	"github.com/polarismesh/polaris-go/pkg/log"
	connector "github.com/polarismesh/polaris-go/plugin/serverconnector/common"
	"time"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/pkg/network"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/serverconnector"
)

const (
	//接收线程获取连接的间隔
	receiveConnInterval = 1 * time.Second
	//GRPC协议名
	protocolGrpc = "grpc"
)

//Connector cl5服务端代理，使用GRPC协议对接
type Connector struct {
	*plugin.PluginBase
	*common.RunContext
	//插件级配置
	cfg                   *networkConfig
	connManager           network.ConnectionManager
	connectionIdleTimeout time.Duration
	valueCtx              model.ValueContext
	asyncRLimitConnector  *AsyncRateLimitConnector
	discoverConnector     *connector.DiscoverConnector
	//有没有打印过connManager ready的信息，用于避免重复打印
	hasPrintedReady uint32
}

//Type 插件类型
func (g *Connector) Type() common.Type {
	return common.TypeServerConnector
}

//Name 插件名，一个类型下插件名唯一
func (g *Connector) Name() string {
	return protocolGrpc
}

//Init 初始化插件
func (g *Connector) Init(ctx *plugin.InitContext) error {
	g.RunContext = common.NewRunContext()
	g.PluginBase = plugin.NewPluginBase(ctx)
	cfgValue := ctx.Config.GetGlobal().GetServerConnector().GetPluginConfig(g.Name())
	if cfgValue != nil {
		g.cfg = cfgValue.(*networkConfig)
	}
	g.connManager = ctx.ConnManager
	g.connectionIdleTimeout = ctx.Config.GetGlobal().GetServerConnector().GetConnectionIdleTimeout()
	g.valueCtx = ctx.ValueCtx
	g.asyncRLimitConnector = NewAsyncRateLimitConnector(g.valueCtx, ctx.ConnManager.GetClientInfo(), ctx.Config)
	protocol := ctx.Config.GetGlobal().GetServerConnector().GetProtocol()
	if protocol == g.Name() {
		log.GetBaseLogger().Infof("set %s plugin as connectionCreator", g.Name())
		g.connManager.SetConnCreator(g)
	}
	g.discoverConnector = &connector.DiscoverConnector{}
	g.discoverConnector.ServiceConnector = g.PluginBase
	g.discoverConnector.Init(ctx, g.createDiscoverClient)
	return nil
}

//启动插件
func (g *Connector) Start() error {
	g.discoverConnector.StartUpdateRoutines()
	return nil
}

//获取连接管理器
func (g *Connector) GetConnectionManager() network.ConnectionManager {
	return g.connManager
}

//创建服务发现客户端
func (g *Connector) createDiscoverClient(reqID string,
	connection *network.Connection, timeout time.Duration) (connector.DiscoverClient, context.CancelFunc, error) {
	//创建namingClient对象
	client := namingpb.NewPolarisGRPCClient(network.ToGRPCConn(connection.Conn))
	outgoingCtx, cancel := connector.CreateHeaderContextWithReqId(timeout, reqID)
	discoverClient, err := client.Discover(outgoingCtx)
	return discoverClient, cancel, err
}

//Destroy 销毁插件，可用于释放资源
func (g *Connector) Destroy() error {
	g.RunContext.Destroy()
	g.discoverConnector.Destroy()
	g.connManager.Destroy()
	return nil
}

// enable
func (g *Connector) IsEnable(cfg config.Configuration) bool {
	if cfg.GetGlobal().GetSystem().GetMode() == model.ModeWithAgent {
		return false
	} else {
		return true
	}
}

//RegisterServiceHandler 注册服务监听器
//异常场景：当key不合法或者sdk已经退出过程中，则返回error
func (g *Connector) RegisterServiceHandler(svcEventHandler *serverconnector.ServiceEventHandler) error {
	return g.discoverConnector.RegisterServiceHandler(svcEventHandler)
}

//DeRegisterEventHandler 反注册事件监听器
//异常场景：当sdk已经退出过程中，则返回error
func (g *Connector) DeRegisterServiceHandler(key *model.ServiceEventKey) error {
	return g.discoverConnector.DeRegisterServiceHandler(key)
}

// 更新服务端地址
// 异常场景：当地址列表为空，或者地址全部连接失败，则返回error，调用者需进行重试
func (g *Connector) UpdateServers(key *model.ServiceEventKey) error {
	return g.discoverConnector.UpdateServers(key)
}

func (g *Connector) GetAsyncRateLimitConnector() serverconnector.AsyncRateLimitConnector {
	return g.asyncRLimitConnector
}

//init 注册插件信息
func init() {
	plugin.RegisterConfigurablePlugin(&Connector{}, &networkConfig{})
}
