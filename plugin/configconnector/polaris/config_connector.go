/*
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
 *  under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
 * CONDITIONS OF ANY KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations under the License.
 *
 */

package polaris

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/golang/protobuf/jsonpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/clock"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	configpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/pkg/network"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/configconnector"
	connector "github.com/polarismesh/polaris-go/plugin/serverconnector/common"
)

const (
	// 接收线程获取连接的间隔.
	receiveConnInterval = 1 * time.Second
)

// Connector 使用GRPC协议对接.
type Connector struct {
	*plugin.PluginBase
	*common.RunContext
	// 插件级配置
	cfg                   *networkConfig
	connManager           network.ConnectionManager
	connectionIdleTimeout time.Duration
	valueCtx              model.ValueContext
	// 有没有打印过connManager ready的信息，用于避免重复打印
	hasPrintedReady uint32
}

// Type 插件类型.
func (c *Connector) Type() common.Type {
	return common.TypeConfigConnector
}

// Name 插件名，一个类型下插件名唯一.
func (c *Connector) Name() string {
	return "polaris"
}

// Init 初始化插件.
func (c *Connector) Init(ctx *plugin.InitContext) error {
	c.RunContext = common.NewRunContext()
	c.PluginBase = plugin.NewPluginBase(ctx)
	cfgValue := ctx.Config.GetConfigFile().GetConfigConnectorConfig().GetPluginConfig(c.Name())
	if cfgValue != nil {
		c.cfg = cfgValue.(*networkConfig)
	}
	connManager, err := network.NewConfigConnectionManager(ctx.Config, ctx.ValueCtx)
	if err != nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidConfig, err, "fail to create config connectionManager")
	}
	c.connManager = connManager
	c.connectionIdleTimeout = ctx.Config.GetGlobal().GetServerConnector().GetConnectionIdleTimeout()
	c.valueCtx = ctx.ValueCtx
	protocol := ctx.Config.GetConfigFile().GetConfigConnectorConfig().GetProtocol()
	if protocol == c.Name() {
		log.GetBaseLogger().Infof("set %s plugin as connectionCreator", c.Name())
		c.connManager.SetConnCreator(c)
	}
	return nil
}

// Destroy 销毁插件，可用于释放资源.
func (c *Connector) Destroy() error {
	if nil != c.RunContext {
		_ = c.RunContext.Destroy()
	}
	if nil != c.connManager {
		c.connManager.Destroy()
	}
	return nil
}

// GetConfigFile Get config file.
func (c *Connector) GetConfigFile(configFile *configconnector.ConfigFile) (*configconnector.ConfigFileResponse, error) {
	var err error
	if err = c.waitDiscoverReady(); err != nil {
		return nil, err
	}
	opKey := connector.OpKeyGetConfigFile
	startTime := clock.GetClock().Now()
	// 获取server连接
	conn, err := c.connManager.GetConnection(opKey, config.ConfigCluster)
	if err != nil {
		return nil, connector.NetworkError(c.connManager, conn, int32(model.ErrCodeConnectError), err, startTime,
			fmt.Sprintf("fail to get connection, opKey %s", opKey))
	}
	// 释放server连接
	defer conn.Release(opKey)
	configClient := configpb.NewPolarisConfigGRPCClient(network.ToGRPCConn(conn.Conn))
	reqID := connector.NextRegisterInstanceReqID()
	ctx, cancel := connector.CreateHeaderContextWithReqId(0, reqID)
	if cancel != nil {
		defer cancel()
	}
	// 打印请求报文
	info := transferToClientConfigFileInfo(configFile)
	if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		reqJson, _ := (&jsonpb.Marshaler{}).MarshalToString(info)
		log.GetBaseLogger().Debugf("request to send is %s, opKey %s, connID %s", reqJson, opKey, conn.ConnID)
	}
	pbResp, err := configClient.GetConfigFile(ctx, info)
	return c.handleResponse(info.String(), reqID, opKey, pbResp, err, conn, startTime)
}

// WatchConfigFiles Watch config files.
func (c *Connector) WatchConfigFiles(configFileList []*configconnector.ConfigFile) (*configconnector.ConfigFileResponse, error) {
	var err error
	if err = c.waitDiscoverReady(); err != nil {
		return nil, err
	}
	opKey := connector.OpKeyWatchConfigFiles
	startTime := clock.GetClock().Now()
	// 获取server连接
	conn, err := c.connManager.GetConnection(opKey, config.ConfigCluster)
	if err != nil {
		return nil, connector.NetworkError(c.connManager, conn, int32(model.ErrCodeConnectError), err, startTime,
			fmt.Sprintf("fail to get connection, opKey %s", opKey))
	}
	// 释放server连接
	defer conn.Release(opKey)
	configClient := configpb.NewPolarisConfigGRPCClient(network.ToGRPCConn(conn.Conn))
	reqID := connector.NextWatchConfigFilesReqID()
	ctx, cancel := connector.CreateHeaderContextWithReqId(0, reqID)
	if cancel != nil {
		defer cancel()
	}
	// 构造request
	var configFileInfoList []*configpb.ClientConfigFileInfo
	for _, c := range configFileList {
		configFileInfoList = append(configFileInfoList, transferToClientConfigFileInfo(c))
	}
	request := &configpb.ClientWatchConfigFileRequest{WatchFiles: configFileInfoList}
	// 打印请求报文
	if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		reqJson, _ := (&jsonpb.Marshaler{}).MarshalToString(request)
		log.GetBaseLogger().Debugf("request to send is %s, opKey %s, connID %s", reqJson, opKey, conn.ConnID)
	}
	pbResp, err := configClient.WatchConfigFiles(ctx, request)
	return c.handleResponse(request.String(), reqID, opKey, pbResp, err, conn, startTime)
}

// IsEnable .插件开关.
func (c *Connector) IsEnable(cfg config.Configuration) bool {
	return cfg.GetGlobal().GetSystem().GetMode() != model.ModeWithAgent
}

// 等待discover就绪.
func (c *Connector) waitDiscoverReady() error {
	ctx, cancel := context.WithTimeout(context.Background(), receiveConnInterval/2)
	defer cancel()
	for {
		select {
		case <-c.RunContext.Done():
			// connector已经销毁
			return model.NewSDKError(model.ErrCodeInvalidStateError, nil, "SDK context has destroyed")
		case <-ctx.Done():
			// 超时
			return nil
		default:
			if c.connManager.IsReady() && atomic.CompareAndSwapUint32(&c.hasPrintedReady, 0, 1) {
				// 准备就绪
				log.GetBaseLogger().Infof("%s, waitDiscover: config service is ready", c.GetSDKContextID())
				return nil
			}
			time.Sleep(clock.TimeStep())
		}
	}
}

func (c *Connector) handleResponse(request string, reqID string, opKey string, response *configpb.ConfigClientResponse,
	err error, conn *network.Connection, startTime time.Time,
) (*configconnector.ConfigFileResponse, error) {
	endTime := clock.GetClock().Now()
	if err != nil {
		return nil, connector.NetworkError(c.connManager, conn, int32(model.ErrorCodeRpcError), err, startTime,
			fmt.Sprintf("fail to %s, request %s, "+
				"reason is fail to send request, reqID %s, server %s", opKey, request, reqID, conn.ConnID))
	}
	// 打印应答报文
	if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		respJson, _ := (&jsonpb.Marshaler{}).MarshalToString(response)
		log.GetBaseLogger().Debugf("response recv is %s, opKey %s, connID %s", respJson, opKey, conn.ConnID)
	}
	serverCodeType := pb.ConvertServerErrorToRpcError(response.GetCode().GetValue())
	code := response.GetCode().GetValue()
	// 预期code，正常响应
	if code == configpb.ExecuteSuccess || code == configpb.NotFoundResource || code == configpb.DataNoChange {
		c.connManager.ReportSuccess(conn.ConnID, int32(serverCodeType), endTime.Sub(startTime))
		return &configconnector.ConfigFileResponse{
			Code:       response.GetCode().GetValue(),
			Message:    response.GetInfo().GetValue(),
			ConfigFile: transferFromClientConfigFileInfo(response.GetConfigFile()),
		}, nil
	}
	// 当server发生了内部错误时，上报调用服务失败
	errMsg := fmt.Sprintf(
		"fail to %s, request %s, server code %d, reason %s, server %s", opKey,
		request, response.GetCode().GetValue(), response.GetInfo().GetValue(), conn.ConnID)
	c.connManager.ReportFail(conn.ConnID, int32(model.ErrCodeServerError), endTime.Sub(startTime))
	return nil, model.NewSDKError(model.ErrCodeServerException, nil, errMsg)
}

func transferToClientConfigFileInfo(configFile *configconnector.ConfigFile) *configpb.ClientConfigFileInfo {
	return &configpb.ClientConfigFileInfo{
		Namespace: wrapperspb.String(configFile.Namespace),
		Group:     wrapperspb.String(configFile.GetFileGroup()),
		FileName:  wrapperspb.String(configFile.GetFileName()),
		Version:   wrapperspb.UInt64(configFile.GetVersion()),
	}
}

func transferFromClientConfigFileInfo(configFileInfo *configpb.ClientConfigFileInfo) *configconnector.ConfigFile {
	return &configconnector.ConfigFile{
		Namespace: configFileInfo.GetNamespace().GetValue(),
		FileGroup: configFileInfo.GetGroup().GetValue(),
		FileName:  configFileInfo.GetFileName().GetValue(),
		Content:   configFileInfo.GetContent().GetValue(),
		Version:   configFileInfo.GetVersion().GetValue(),
		Md5:       configFileInfo.GetMd5().GetValue(),
	}
}

// init 注册插件信息.
func init() {
	plugin.RegisterConfigurablePlugin(&Connector{}, &networkConfig{})
}
