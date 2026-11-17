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

package grpc

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/polarismesh/polaris-go/pkg/clock"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/network"
	connector "github.com/polarismesh/polaris-go/plugin/serverconnector/common"
)

// ReportServiceContract 创建或更新 Service Contract。
func (g *Connector) ReportServiceContract(
	req *apiservice.ServiceContract, timeout time.Duration,
) (*apiservice.ServiceContract, error) {
	if req == nil {
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"ServiceContract can not be nil")
	}
	if timeout <= 0 {
		timeout = config.DefaultAPIInvokeTimeout
	}
	start := clock.GetClock().Now()
	requestCtx, requestCancel := context.WithTimeout(context.Background(), timeout)
	defer requestCancel()
	if err := g.waitDiscoverReadyContext(requestCtx); err != nil {
		return nil, err
	}
	conn, err := g.getServiceContractConnection(requestCtx, start)
	if err != nil {
		return nil, err
	}
	defer conn.Release(connector.OpKeyServiceContract)
	remaining, err := remainingContextTimeout(requestCtx)
	if err != nil {
		return nil, err
	}
	rpcCtx, rpcCancel := connector.CreateHeadersContext(remaining,
		connector.AppendAuthHeader(g.token),
		connector.AppendHeaderWithReqId(connector.NextServiceContractReqID()))
	defer rpcCancel()
	client := apiservice.NewPolarisServiceContractGRPCClient(network.ToGRPCConn(conn.Conn))
	resp, err := client.ReportServiceContract(rpcCtx, req)
	end := clock.GetClock().Now()
	if err != nil {
		return nil, g.serviceContractRPCError(conn, err, start,
			fmt.Sprintf("fail to report ServiceContract %s/%s", req.GetNamespace(), req.GetService()))
	}
	if resp == nil {
		g.connManager.ReportFail(conn.ConnID, int32(model.ErrCodeInvalidResponse), end.Sub(start))
		return nil, model.NewSDKError(model.ErrCodeInvalidResponse, nil,
			"ReportServiceContract returned an empty response")
	}
	if resp.GetCode().GetValue() != uint32(apimodel.Code_ExecuteSuccess) {
		g.connManager.ReportSuccess(conn.ConnID, int32(model.ErrCodeServerUserError), end.Sub(start))
		return nil, model.NewSDKErrorWithServerInfo(model.ErrCodeServerUserError, nil,
			resp.GetCode().GetValue(), resp.GetInfo().GetValue(), "ReportServiceContract returned an error")
	}
	g.connManager.ReportSuccess(conn.ConnID, int32(model.ErrCodeSuccess), end.Sub(start))
	if resp.GetServiceContract() == nil {
		return &apiservice.ServiceContract{}, nil
	}
	return proto.Clone(resp.GetServiceContract()).(*apiservice.ServiceContract), nil
}

// GetServiceContract 查询 Service Contract。
func (g *Connector) GetServiceContract(
	req *apiservice.ServiceContract, timeout time.Duration,
) (*apiservice.ServiceContract, error) {
	if req == nil {
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"ServiceContract can not be nil")
	}
	if timeout <= 0 {
		timeout = config.DefaultAPIInvokeTimeout
	}
	start := clock.GetClock().Now()
	requestCtx, requestCancel := context.WithTimeout(context.Background(), timeout)
	defer requestCancel()
	if err := g.waitDiscoverReadyContext(requestCtx); err != nil {
		return nil, err
	}
	conn, err := g.getServiceContractConnection(requestCtx, start)
	if err != nil {
		return nil, err
	}
	defer conn.Release(connector.OpKeyServiceContract)
	remaining, err := remainingContextTimeout(requestCtx)
	if err != nil {
		return nil, err
	}
	rpcCtx, rpcCancel := connector.CreateHeadersContext(remaining,
		connector.AppendAuthHeader(g.token),
		connector.AppendHeaderWithReqId(connector.NextServiceContractReqID()))
	defer rpcCancel()
	client := apiservice.NewPolarisServiceContractGRPCClient(network.ToGRPCConn(conn.Conn))
	resp, err := client.GetServiceContract(rpcCtx, req)
	end := clock.GetClock().Now()
	if err != nil {
		return nil, g.serviceContractRPCError(conn, err, start,
			fmt.Sprintf("fail to get ServiceContract %s/%s", req.GetNamespace(), req.GetService()))
	}
	if resp == nil {
		g.connManager.ReportFail(conn.ConnID, int32(model.ErrCodeInvalidResponse), end.Sub(start))
		return nil, model.NewSDKError(model.ErrCodeInvalidResponse, nil,
			"GetServiceContract returned an empty response")
	}
	if resp.GetCode().GetValue() == uint32(apimodel.Code_NotFoundResource) {
		g.connManager.ReportSuccess(conn.ConnID, int32(model.ErrCodeServiceContractNotFound), end.Sub(start))
		return nil, model.NewSDKErrorWithServerInfo(model.ErrCodeServiceContractNotFound, nil,
			resp.GetCode().GetValue(), resp.GetInfo().GetValue(), "ServiceContract was not found")
	}
	if resp.GetCode().GetValue() != uint32(apimodel.Code_ExecuteSuccess) {
		g.connManager.ReportSuccess(conn.ConnID, int32(model.ErrCodeServerUserError), end.Sub(start))
		return nil, model.NewSDKErrorWithServerInfo(model.ErrCodeServerUserError, nil,
			resp.GetCode().GetValue(), resp.GetInfo().GetValue(), "GetServiceContract returned an error")
	}
	if resp.GetServiceContract() == nil {
		g.connManager.ReportFail(conn.ConnID, int32(model.ErrCodeInvalidResponse), end.Sub(start))
		return nil, model.NewSDKError(model.ErrCodeInvalidResponse, nil,
			"GetServiceContract response has no service contract")
	}
	g.connManager.ReportSuccess(conn.ConnID, int32(model.ErrCodeSuccess), end.Sub(start))
	return proto.Clone(resp.GetServiceContract()).(*apiservice.ServiceContract), nil
}

func (g *Connector) waitDiscoverReadyContext(ctx context.Context) error {
	ticker := time.NewTicker(clock.TimeStep())
	defer ticker.Stop()
	for {
		select {
		case <-g.Done():
			return model.NewSDKError(model.ErrCodeInvalidStateError, nil, "SDK context has destroyed")
		case <-ctx.Done():
			return model.NewSDKError(model.ErrCodeAPITimeoutError, ctx.Err(),
				"Service Connector was not ready before the request timeout")
		case <-ticker.C:
			if g.connManager.IsReady() {
				if atomic.CompareAndSwapUint32(&g.hasPrintedReady, 0, 1) && g.logCtx != nil {
					g.logCtx.GetBaseLogger().Infof(
						"%s, waitDiscover: discover service is ready", g.GetSDKContextID())
				}
				return nil
			}
		}
	}
}

type serviceContractConnectionResult struct {
	conn *network.Connection
	err  error
}

func (g *Connector) getServiceContractConnection(
	ctx context.Context, start time.Time,
) (*network.Connection, error) {
	resultCh := make(chan serviceContractConnectionResult, 1)
	go func() {
		conn, err := g.connManager.GetConnection(connector.OpKeyServiceContract, config.DiscoverCluster)
		resultCh <- serviceContractConnectionResult{conn: conn, err: err}
	}()
	select {
	case result := <-resultCh:
		if result.err != nil {
			return nil, connector.NetworkError(g.connManager, nil, int32(model.ErrCodeConnectError),
				result.err, start, "fail to get ServiceContract connection")
		}
		return result.conn, nil
	case <-ctx.Done():
		go func() {
			result := <-resultCh
			if result.conn != nil {
				result.conn.Release(connector.OpKeyServiceContract)
			}
		}()
		return nil, model.NewSDKError(model.ErrCodeAPITimeoutError, ctx.Err(),
			"Service Contract request timed out while acquiring a connection")
	}
}

func remainingContextTimeout(ctx context.Context) (time.Duration, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0, model.NewSDKError(model.ErrCodeInternalError, nil,
			"Service Contract request has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0, model.NewSDKError(model.ErrCodeAPITimeoutError, nil,
			"Service Contract request exceeded timeout")
	}
	return remaining, nil
}

func (g *Connector) serviceContractRPCError(
	conn *network.Connection, err error, start time.Time, message string,
) model.SDKError {
	if _, ok := status.FromError(err); !ok {
		return connector.NetworkError(g.connManager, conn, int32(model.ErrorCodeRpcError),
			err, start, message)
	}
	switch status.Code(err) {
	case codes.Canceled, codes.DeadlineExceeded, codes.Unavailable:
		return connector.NetworkError(g.connManager, conn, int32(model.ErrorCodeRpcError),
			err, start, message)
	case codes.PermissionDenied, codes.Unauthenticated:
		return model.NewSDKError(model.ErrCodeUnauthorized, err, message)
	case codes.InvalidArgument, codes.OutOfRange, codes.FailedPrecondition:
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, err, message)
	case codes.NotFound:
		return model.NewSDKError(model.ErrCodeServiceContractNotFound, err, message)
	default:
		return model.NewSDKError(model.ErrCodeServerException, err, message)
	}
}
