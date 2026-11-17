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
	"errors"
	"testing"
	"time"

	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/network"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

type slowContractConnectionManager struct {
	network.ConnectionManager
	done chan struct{}
}

func (m *slowContractConnectionManager) IsReady() bool {
	return true
}

func (m *slowContractConnectionManager) GetConnection(
	string, config.ClusterType,
) (*network.Connection, error) {
	defer close(m.done)
	time.Sleep(150 * time.Millisecond)
	return nil, errors.New("dial timeout")
}

func TestServiceContractBudgetIncludesConnection(t *testing.T) {
	manager := &slowContractConnectionManager{done: make(chan struct{})}
	connector := &Connector{
		RunContext:      common.NewRunContext(),
		connManager:     manager,
		hasPrintedReady: 1,
	}
	start := time.Now()
	_, err := connector.GetServiceContract(&apiservice.ServiceContract{
		Namespace: "Production",
		Service:   "search-mcp",
	}, 10*time.Millisecond)
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeAPITimeoutError, model.GetErrorCodeFromError(err))
	assert.Less(t, time.Since(start), 100*time.Millisecond,
		"connection acquisition must honor the request deadline")
	select {
	case <-manager.done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("connection acquisition goroutine did not finish")
	}
}

func TestServiceContractRPCErrorClassification(t *testing.T) {
	connector := &Connector{}
	tests := []struct {
		code model.ErrCode
		err  error
	}{
		{model.ErrCodeUnauthorized, status.Error(codes.PermissionDenied, "denied")},
		{model.ErrCodeUnauthorized, status.Error(codes.Unauthenticated, "unauthenticated")},
		{model.ErrCodeAPIInvalidArgument, status.Error(codes.InvalidArgument, "invalid")},
		{model.ErrCodeServiceContractNotFound, status.Error(codes.NotFound, "missing")},
		{model.ErrCodeServerException, status.Error(codes.Internal, "internal")},
		{model.ErrCodeNetworkError, status.Error(codes.Unavailable, "unavailable")},
		{model.ErrCodeNetworkError, errors.New("connection reset")},
	}
	for _, test := range tests {
		sdkErr := connector.serviceContractRPCError(nil, test.err, time.Now(), "classification test")
		require.Error(t, sdkErr)
		assert.Equal(t, test.code, model.GetErrorCodeFromError(sdkErr), "error: %v", test.err)
	}
}
