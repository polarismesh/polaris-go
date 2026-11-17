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

package polaris

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/network"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/skillconnector"
)

// Connector 使用 gRPC 对接独立 PolarisSkill 服务。
type Connector struct {
	*plugin.PluginBase
	cfg             *networkConfig
	connManager     network.ConnectionManager
	token           string
	requestTimeout  time.Duration
	downloadTimeout time.Duration
	logCtx          *log.ContextLogger
}

// Type 返回插件类型。
func (c *Connector) Type() common.Type {
	return common.TypeSkillConnector
}

// Name 返回插件名。
func (c *Connector) Name() string {
	return "polaris"
}

// Init 初始化插件。
func (c *Connector) Init(ctx *plugin.InitContext) error {
	c.PluginBase = plugin.NewPluginBase(ctx)
	c.logCtx = ctx.ValueCtx.GetContextLogger()
	cfgValue := ctx.Config.GetSkill().GetPluginConfig(c.Name())
	if cfgValue != nil {
		c.cfg = cfgValue.(*networkConfig)
	}
	if c.cfg == nil {
		c.cfg = &networkConfig{}
		c.cfg.SetDefault()
	}
	c.token = ctx.Config.GetSkill().GetToken()
	c.requestTimeout = ctx.Config.GetSkill().GetMessageTimeout()
	c.downloadTimeout = ctx.Config.GetSkill().GetDownloadTimeout()
	manager, err := network.NewSkillConnectionManager(ctx.Config, ctx.ValueCtx)
	if err != nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidConfig, err, "fail to create skill connection manager")
	}
	c.connManager = manager
	c.connManager.SetConnCreator(c)
	return nil
}

// Start 启动插件。
func (c *Connector) Start() error {
	return nil
}

// Destroy 销毁插件。
func (c *Connector) Destroy() error {
	if c.connManager != nil {
		c.connManager.Destroy()
	}
	return nil
}

// IsEnable 返回 Skill Registry 是否启用。
func (c *Connector) IsEnable(cfg config.Configuration) bool {
	return cfg.GetSkill().IsEnable()
}

// CreateConnection 创建 gRPC 连接。
func (c *Connector) CreateConnection(
	address string, timeout time.Duration, clientInfo *network.ClientInfo,
) (network.ClosableConn, error) {
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(c.cfg.MaxCallRecvMsgSize)),
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	conn, err := grpc.DialContext(ctx, address, opts...)
	if err != nil {
		return nil, err
	}
	c.logCtx.GetNetworkLogger().Debugf(
		"created skill connection to %s with maxCallRecvSize %d", address, c.cfg.MaxCallRecvMsgSize)
	return conn, nil
}

func init() {
	plugin.RegisterConfigurablePlugin(&Connector{}, &networkConfig{})
}

var _ skillconnector.SkillConnector = (*Connector)(nil)
