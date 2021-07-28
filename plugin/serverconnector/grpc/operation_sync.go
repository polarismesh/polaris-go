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

package grpc

import (
	"context"
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/clock"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/pkg/network"
	connector "github.com/polarismesh/polaris-go/plugin/serverconnector/common"
	"github.com/golang/protobuf/jsonpb"
	"sync/atomic"
	"time"
)

//RegisterInstance 同步注册服务
func (g *Connector) RegisterInstance(req *model.InstanceRegisterRequest) (*model.InstanceRegisterResponse, error) {
	var err error
	if err = g.waitDiscoverReady(); nil != err {
		return nil, err
	}
	opKey := connector.OpKeyRegisterInstance
	startTime := clock.GetClock().Now()
	//获取server连接
	conn, err := g.connManager.GetConnection(opKey, config.DiscoverCluster)
	if nil != err {
		return nil, connector.NetworkError(g.connManager, conn, int32(model.ErrCodeConnectError), err, startTime,
			fmt.Sprintf("fail to get connection, opKey %s", opKey))
	}
	//释放server连接
	defer conn.Release(opKey)
	namingClient := namingpb.NewPolarisGRPCClient(network.ToGRPCConn(conn.Conn))
	reqID := connector.NextRegisterInstanceReqID()
	ctx, cancel := connector.CreateHeaderContextWithReqId(*req.Timeout, reqID)
	if cancel != nil {
		defer cancel()
	}
	reqProto := registerRequestToProto(req)
	//打印请求报文
	if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		reqJson, _ := (&jsonpb.Marshaler{}).MarshalToString(reqProto)
		log.GetBaseLogger().Debugf("request to send is %s, opKey %s, connID %s", reqJson, opKey, conn.ConnID)
	}
	pbResp, err := namingClient.RegisterInstance(ctx, reqProto)
	endTime := clock.GetClock().Now()
	if nil != err {
		return nil, connector.NetworkError(g.connManager, conn, int32(model.ErrorCodeRpcError), err, startTime,
			fmt.Sprintf("fail to registerInstance, request %s, "+
				"reason is fail to send request, reqID %s, server %s", *req, reqID, conn.ConnID))
	}
	//打印应答报文
	if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		respJson, _ := (&jsonpb.Marshaler{}).MarshalToString(pbResp)
		log.GetBaseLogger().Debugf("response recv is %s, opKey %s, connID %s", respJson, opKey, conn.ConnID)
	}
	serverCodeType := pb.ConvertServerErrorToRpcError(pbResp.GetCode().GetValue())
	//判断不同状态，对于已存在状态则不认为失败
	if namingpb.ExecuteSuccess != pbResp.GetCode().GetValue() &&
		namingpb.ExistedResource != pbResp.GetCode().GetValue() {
		errMsg := fmt.Sprintf(
			"fail to registerInstance, request %s, server code %d, reason %s, server %s",
			*req, pbResp.GetCode().GetValue(), pbResp.GetInfo().GetValue(), conn.ConnID)
		if serverCodeType == model.ErrCodeServerError {
			//当server发生了内部错误时，上报调用服务失败
			g.connManager.ReportFail(conn.ConnID, int32(model.ErrCodeServerError), endTime.Sub(startTime))
			return nil, model.NewSDKError(model.ErrCodeServerException, nil, errMsg)
		}
		g.connManager.ReportSuccess(conn.ConnID, int32(serverCodeType), endTime.Sub(startTime))
		return nil, model.NewSDKError(model.ErrCodeServerUserError, nil, errMsg)
	} else {
		g.connManager.ReportSuccess(conn.ConnID, int32(serverCodeType), endTime.Sub(startTime))
	}
	resp := &model.InstanceRegisterResponse{InstanceID: pbResp.GetInstance().GetId().GetValue(),
		Existed: namingpb.ExistedResource == pbResp.GetCode().GetValue()}
	return resp, nil
}

//DeregisterInstance 同步反注册服务
func (g *Connector) DeregisterInstance(req *model.InstanceDeRegisterRequest) error {
	var err error
	if err = g.waitDiscoverReady(); nil != err {
		return err
	}
	opKey := connector.OpKeyDeregisterInstance
	startTime := clock.GetClock().Now()
	//获取server连接
	conn, err := g.connManager.GetConnection(opKey, config.DiscoverCluster)
	if nil != err {
		return model.NewSDKError(model.ErrCodeNetworkError, err, "fail to get connection, opKey %s", opKey)
	}
	//释放server连接
	defer conn.Release(opKey)
	namingClient := namingpb.NewPolarisGRPCClient(network.ToGRPCConn(conn.Conn))
	reqID := connector.NextDeRegisterInstanceReqID()
	ctx, cancel := connector.CreateHeaderContextWithReqId(*req.Timeout, reqID)
	if cancel != nil {
		defer cancel()
	}
	reqProto := deregisterRequestToProto(req)
	//打印请求报文
	if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		reqJson, _ := (&jsonpb.Marshaler{}).MarshalToString(reqProto)
		log.GetBaseLogger().Debugf("request to send is %s, opKey %s, connID %s", reqJson, opKey, conn.ConnID)
	}
	pbResp, err := namingClient.DeregisterInstance(ctx, reqProto)
	endTime := clock.GetClock().Now()
	if nil != err {
		return connector.NetworkError(g.connManager, conn, int32(model.ErrorCodeRpcError), err, startTime,
			fmt.Sprintf("fail to deregisterInstance, request %s, "+
				"reason is fail to send request, reqID %s, server %s", *req, reqID, conn.ConnID))
	}
	//打印应答报文
	if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		respJson, _ := (&jsonpb.Marshaler{}).MarshalToString(pbResp)
		log.GetBaseLogger().Debugf("response recv is %s, opKey %s, connID %s", respJson, opKey, conn.ConnID)
	}
	serverCodeType := pb.ConvertServerErrorToRpcError(pbResp.GetCode().GetValue())
	//判断不同状态，对于不存在状态则不认为失败
	if namingpb.ExecuteSuccess != pbResp.GetCode().GetValue() &&
		namingpb.NotFoundResource != pbResp.GetCode().GetValue() {
		errMsg := fmt.Sprintf(
			"fail to deregisterInstance, request %s, server code %d, reason %s, server %s",
			*req, pbResp.GetCode().GetValue(), pbResp.GetInfo().GetValue(), conn.ConnID)
		if serverCodeType == model.ErrCodeServerError {
			//当server发生了内部错误时，上报调用服务失败
			g.connManager.ReportFail(conn.ConnID, int32(model.ErrCodeServerError), endTime.Sub(startTime))
			return model.NewSDKError(model.ErrCodeServerException, nil, errMsg)
		}
		g.connManager.ReportSuccess(conn.ConnID, int32(serverCodeType), endTime.Sub(startTime))
		return model.NewSDKError(model.ErrCodeServerUserError, nil, errMsg)
	} else {
		g.connManager.ReportSuccess(conn.ConnID, int32(serverCodeType), endTime.Sub(startTime))
	}
	return nil
}

//Heartbeat 心跳上报
func (g *Connector) Heartbeat(req *model.InstanceHeartbeatRequest) error {
	var err error
	if err = g.waitDiscoverReady(); nil != err {
		return err
	}
	opKey := connector.OpKeyInstanceHeartbeat
	startTime := clock.GetClock().Now()
	//获取心跳server连接
	conn, err := g.connManager.GetConnection(opKey, config.HealthCheckCluster)
	if nil != err {
		return model.NewSDKError(model.ErrCodeNetworkError, err, "fail to get connection, opKey %s", opKey)
	}
	//释放server连接
	defer conn.Release(opKey)
	namingClient := namingpb.NewPolarisGRPCClient(network.ToGRPCConn(conn.Conn))
	reqID := connector.NextHeartbeatReqID()
	ctx, cancel := connector.CreateHeaderContextWithReqId(*req.Timeout, reqID)
	if cancel != nil {
		defer cancel()
	}
	reqProto := heartbeatRequestToProto(req)
	//打印请求报文
	if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		reqJson, _ := (&jsonpb.Marshaler{}).MarshalToString(reqProto)
		log.GetBaseLogger().Debugf("request to send is %s, opKey %s, connID %s", reqJson, opKey, conn.ConnID)
	}
	pbResp, err := namingClient.Heartbeat(ctx, reqProto)
	endTime := clock.GetClock().Now()
	if nil != err {
		return connector.NetworkError(g.connManager, conn, int32(model.ErrorCodeRpcError), err, startTime,
			fmt.Sprintf("fail to heartbeat, request %s, reason is fail to send request, reqID %s, server %s",
				*req, reqID, conn.ConnID))
	}
	//打印应答报文
	if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		respJson, _ := (&jsonpb.Marshaler{}).MarshalToString(pbResp)
		log.GetBaseLogger().Debugf("response recv is %s, opKey %s, connID %s", respJson, opKey, conn.ConnID)
	}
	serverCodeType := pb.ConvertServerErrorToRpcError(pbResp.GetCode().GetValue())
	//判断不同状态
	if namingpb.ExecuteSuccess != pbResp.GetCode().GetValue() {
		errMsg := fmt.Sprintf(
			"fail to heartbeat, request %s, server error code is %d, error is %s, server %s",
			*req, pbResp.GetCode().GetValue(), pbResp.GetInfo().GetValue(), conn.ConnID)
		log.GetBaseLogger().Errorf(errMsg)
		if serverCodeType == model.ErrCodeServerError {
			//当server发生内部错误时，上报调用服务失败
			g.connManager.ReportFail(conn.ConnID, int32(model.ErrCodeServerError), endTime.Sub(startTime))
			return model.NewSDKError(model.ErrCodeServerException, nil, errMsg)
		}
		g.connManager.ReportSuccess(conn.ConnID, int32(serverCodeType), endTime.Sub(startTime))
		return model.NewSDKError(model.ErrCodeServerUserError, nil, errMsg)
	} else {
		g.connManager.ReportSuccess(conn.ConnID, int32(serverCodeType), endTime.Sub(startTime))
	}
	return nil
}

//等待discover就绪
func (g *Connector) waitDiscoverReady() error {
	ctx, cancel := context.WithTimeout(context.Background(), receiveConnInterval/2)
	defer cancel()
	for {
		select {
		case <-g.RunContext.Done():
			//connector已经销毁
			return model.NewSDKError(model.ErrCodeInvalidStateError, nil, "SDK context has destroyed")
		case <-ctx.Done():
			//超时
			return nil
		default:
			if g.connManager.IsReady() && atomic.CompareAndSwapUint32(&g.hasPrintedReady, 0, 1) {
				//准备就绪
				log.GetBaseLogger().Infof("%s, waitDiscover: discover service is ready", g.GetSDKContextID())
				return nil
			}
			time.Sleep(clock.TimeStep())
		}
	}
}

// 上报客户端信息
// 异常场景：当sdk已经退出过程中，则返回error
// 异常场景：当服务端不可用或者上报失败，则返回error，调用者需进行重试
func (g *Connector) ReportClient(req *model.ReportClientRequest) (*model.ReportClientResponse, error) {
	var err error
	if err = g.waitDiscoverReady(); nil != err {
		return nil, err
	}
	opKey := connector.OpKeyReportClient
	startTime := clock.GetClock().Now()
	//获取server连接
	conn, err := g.connManager.GetConnection(opKey, config.DiscoverCluster)
	if nil != err {
		return nil, model.NewSDKError(model.ErrCodeNetworkError, err, fmt.Sprintf("fail to get connection, opKey %s", opKey))
	}
	//释放server连接
	defer conn.Release(opKey)
	namingClient := namingpb.NewPolarisGRPCClient(network.ToGRPCConn(conn.Conn))
	reqID := connector.NextReportClientReqID()
	ctx, cancel := connector.CreateHeaderContextWithReqId(req.Timeout, reqID)
	if cancel != nil {
		defer cancel()
	}
	if len(req.Host) == 0 {
		//假如用户传入地址为空，则通过TCP连接的本地地址来进行设置
		req.Host = g.connManager.GetClientInfo().GetIPString()
	}
	reqProto := reportClientRequestToProto(req)
	//打印请求报文
	if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		reqJson, _ := (&jsonpb.Marshaler{}).MarshalToString(reqProto)
		log.GetBaseLogger().Debugf("request to send is %s, opKey %s, connID %s", reqJson, opKey, conn.ConnID)
	}
	pbResp, err := namingClient.ReportClient(ctx, reqProto)
	endTime := g.valueCtx.Now()
	if nil != err {
		return nil, connector.NetworkError(g.connManager, conn, int32(model.ErrorCodeRpcError), err, startTime,
			fmt.Sprintf("fail to send request, opKey %s, reqID %s, connID %s", opKey, reqID, conn.ConnID))
	}
	//打印应答报文
	if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		respJson, _ := (&jsonpb.Marshaler{}).MarshalToString(pbResp)
		log.GetBaseLogger().Debugf("response recv is %s, opKey %s, connID %s", respJson, opKey, conn.ConnID)
	}
	serverCodeType := pb.ConvertServerErrorToRpcError(pbResp.GetCode().GetValue())
	//判断不同状态
	if namingpb.ExecuteSuccess != pbResp.GetCode().GetValue() && namingpb.CMDBNotFindHost != pbResp.GetCode().GetValue() {
		errMsg := fmt.Sprintf("fail to reportClient, server error code is %d, error is %s, connID %s",
			pbResp.GetCode().GetValue(), pbResp.GetInfo().GetValue(), conn.ConnID)
		if serverCodeType == model.ErrCodeServerError {
			//当server发生内部错误时，上报调用服务失败
			g.connManager.ReportFail(conn.ConnID, int32(model.ErrCodeServerError), endTime.Sub(startTime))
			return nil, model.NewSDKError(model.ErrCodeServerException, nil, errMsg)
		}
		g.connManager.ReportSuccess(conn.ConnID, int32(serverCodeType), endTime.Sub(startTime))
		return nil, model.NewSDKError(model.ErrCodeServerUserError, nil, errMsg)
	} else {
		g.connManager.ReportSuccess(conn.ConnID, int32(serverCodeType), endTime.Sub(startTime))
	}
	//持久化本地信息
	if nil != req.PersistHandler {
		if err = req.PersistHandler(pbResp); nil != err {
			log.GetBaseLogger().Errorf("fail to persist client report response, err is %v", err)
		}
	}
	rsp := &model.ReportClientResponse{
		Mode:    model.RunMode(pbResp.GetClient().GetType()),
		Version: pbResp.GetClient().GetVersion().GetValue(),
		Region:  pbResp.GetClient().GetLocation().GetRegion().GetValue(),
		Zone:    pbResp.GetClient().GetLocation().GetZone().GetValue(),
		Campus:  pbResp.GetClient().GetLocation().GetCampus().GetValue(),
	}
	return rsp, nil
}
