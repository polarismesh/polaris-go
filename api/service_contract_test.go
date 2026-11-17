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

package api

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

type serviceContractServer struct {
	apiservice.UnimplementedPolarisServiceContractGRPCServer
	mu       sync.Mutex
	contract *apiservice.ServiceContract
	getCode  apimodel.Code
}

type permissionDeniedContractServer struct {
	apiservice.UnimplementedPolarisServiceContractGRPCServer
	reportCalls int32
	getCalls    int32
}

func (s *permissionDeniedContractServer) ReportServiceContract(
	context.Context, *apiservice.ServiceContract,
) (*apiservice.Response, error) {
	atomic.AddInt32(&s.reportCalls, 1)
	return nil, status.Error(codes.PermissionDenied, "token denied")
}

func (s *permissionDeniedContractServer) GetServiceContract(
	context.Context, *apiservice.ServiceContract,
) (*apiservice.Response, error) {
	atomic.AddInt32(&s.getCalls, 1)
	return nil, status.Error(codes.PermissionDenied, "token denied")
}

func (s *serviceContractServer) ReportServiceContract(
	_ context.Context, req *apiservice.ServiceContract,
) (*apiservice.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.contract = proto.Clone(req).(*apiservice.ServiceContract)
	s.contract.Id = "contract-id"
	s.contract.Revision = "revision-1"
	return &apiservice.Response{
		Code:            wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
		Info:            wrapperspb.String("success"),
		ServiceContract: &apiservice.ServiceContract{Id: "contract-id"},
	}, nil
}

func (s *serviceContractServer) GetServiceContract(
	_ context.Context, _ *apiservice.ServiceContract,
) (*apiservice.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getCode != apimodel.Code_ExecuteSuccess {
		return &apiservice.Response{
			Code: wrapperspb.UInt32(uint32(s.getCode)),
			Info: wrapperspb.String("not found"),
		}, nil
	}
	return &apiservice.Response{
		Code:            wrapperspb.UInt32(uint32(apimodel.Code_ExecuteSuccess)),
		Info:            wrapperspb.String("success"),
		ServiceContract: proto.Clone(s.contract).(*apiservice.ServiceContract),
	}, nil
}

func TestProviderAndConsumerServiceContract(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("local gRPC listener is unavailable: %v", err)
	}
	server := grpc.NewServer()
	contractServer := &serviceContractServer{getCode: apimodel.Code_ExecuteSuccess}
	apiservice.RegisterPolarisServiceContractGRPCServer(server, contractServer)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	cfg := config.NewDefaultConfiguration([]string{listener.Addr().String()})
	cfg.GetGlobal().GetAPI().SetTimeout(2 * time.Second)
	cfg.GetProvider().SetReportServiceContractEnable(true)
	sdkContext, err := InitContextByConfig(cfg)
	require.NoError(t, err)
	defer sdkContext.Destroy()
	provider := NewProviderAPIByContext(sdkContext)
	consumer := NewConsumerAPIByContext(sdkContext)

	_, err = provider.ReportServiceContract(&ReportServiceContractRequest{
		ServiceContract: &model.ServiceContract{
			GetServiceContractRequest: model.GetServiceContractRequest{
				Name:      "search-mcp",
				Namespace: "Production",
				Service:   "search-mcp",
				Protocol:  string(model.MCPTransportStreamableHTTP),
				Version:   "1.0.0",
			},
			ServiceFeatures: []*model.ServiceFeature{{
				Type: model.ServiceFeatureTypeMCPTool, Name: "search",
				Status:  model.ServiceFeatureEnabled,
				Content: `{"name":"search","description":"Search documents"}`,
			}},
			Interfaces: []*model.InterfaceDescriptor{{Method: "tools/list", Path: "/mcp"}},
		},
	})
	require.NoError(t, err)

	response, err := consumer.GetServiceContract(&GetServiceContractRequest{
		GetServiceContractRequest: model.GetServiceContractRequest{
			Namespace: "Production", Service: "search-mcp", Name: "search-mcp",
			Protocol: string(model.MCPTransportStreamableHTTP), Version: "1.0.0",
		},
	})
	require.NoError(t, err)
	require.Equal(t, model.EventServiceContract, response.GetType())
	got, ok := response.GetValue().(*model.ServiceContract)
	require.True(t, ok)
	assert.Equal(t, "revision-1", got.Revision)
	require.Len(t, got.ServiceFeatures, 1)
	assert.Equal(t, model.ServiceFeatureTypeMCPTool, got.ServiceFeatures[0].Type)
	require.Len(t, got.Interfaces, 1)
	assert.Equal(t, "tools/list", got.Interfaces[0].Method)

	contractServer.mu.Lock()
	contractServer.getCode = apimodel.Code_NotFoundResource
	contractServer.mu.Unlock()
	_, err = consumer.GetServiceContract(&GetServiceContractRequest{
		GetServiceContractRequest: model.GetServiceContractRequest{
			Namespace: "Production", Service: "missing-mcp",
			Protocol: string(model.MCPTransportStreamableHTTP),
		},
	})
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeServiceContractNotFound, model.GetErrorCodeFromError(err))
}

func TestServiceContractDisabledByDefault(t *testing.T) {
	cfg := config.NewDefaultConfiguration([]string{"127.0.0.1:1"})
	require.False(t, cfg.GetProvider().IsReportServiceContractEnable())
	sdkContext, err := InitContextByConfig(cfg)
	require.NoError(t, err)
	defer sdkContext.Destroy()

	_, err = NewProviderAPIByContext(sdkContext).ReportServiceContract(&ReportServiceContractRequest{
		ServiceContract: &model.ServiceContract{
			GetServiceContractRequest: model.GetServiceContractRequest{
				Namespace: "Production", Service: "search-mcp", Protocol: "http",
			},
		},
	})
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeServiceContractDisabled, model.GetErrorCodeFromError(err))

	_, err = NewConsumerAPIByContext(sdkContext).GetServiceContract(&GetServiceContractRequest{
		GetServiceContractRequest: model.GetServiceContractRequest{
			Namespace: "Production", Service: "search-mcp",
		},
	})
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeServiceContractDisabled, model.GetErrorCodeFromError(err))
}

func TestServiceContractPermissionDeniedIsNotRetried(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("local gRPC listener is unavailable in this environment: %v", err)
	}
	server := grpc.NewServer()
	contractServer := &permissionDeniedContractServer{}
	apiservice.RegisterPolarisServiceContractGRPCServer(server, contractServer)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	cfg := config.NewDefaultConfiguration([]string{listener.Addr().String()})
	cfg.GetGlobal().GetAPI().SetTimeout(2 * time.Second)
	cfg.GetGlobal().GetAPI().SetMaxRetryTimes(2)
	cfg.GetGlobal().GetAPI().SetRetryInterval(time.Second)
	cfg.GetProvider().SetReportServiceContractEnable(true)
	sdkContext, err := InitContextByConfig(cfg)
	require.NoError(t, err)
	defer sdkContext.Destroy()

	_, err = NewProviderAPIByContext(sdkContext).ReportServiceContract(&ReportServiceContractRequest{
		ServiceContract: &model.ServiceContract{
			GetServiceContractRequest: model.GetServiceContractRequest{
				Namespace: "Production", Service: "search-mcp", Protocol: "http",
			},
		},
	})
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeUnauthorized, model.GetErrorCodeFromError(err))
	assert.Equal(t, int32(1), atomic.LoadInt32(&contractServer.reportCalls))

	_, err = NewConsumerAPIByContext(sdkContext).GetServiceContract(&GetServiceContractRequest{
		GetServiceContractRequest: model.GetServiceContractRequest{
			Namespace: "Production", Service: "search-mcp", Protocol: "http",
		},
	})
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeUnauthorized, model.GetErrorCodeFromError(err))
	assert.Equal(t, int32(1), atomic.LoadInt32(&contractServer.getCalls))
}

func TestGetServiceContractNilRequestReturnsError(t *testing.T) {
	consumer := NewConsumerAPIByContext(&unsupportedSDKContext{engine: &unsupportedEngine{}})
	_, err := consumer.GetServiceContract(nil)
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeAPIInvalidArgument, model.GetErrorCodeFromError(err))
}
