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

package flow

import (
	"errors"
	"testing"
	"time"

	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

type fakeContractConnector struct {
	reportRequest *apiservice.ServiceContract
	getRequest    *apiservice.ServiceContract
	reportResult  *apiservice.ServiceContract
	getResult     *apiservice.ServiceContract
	reportErr     error
	getErr        error
	getErrs       []error
	getCalls      int
	getDelay      time.Duration
}

func (f *fakeContractConnector) ReportServiceContract(
	req *apiservice.ServiceContract, _ time.Duration,
) (*apiservice.ServiceContract, error) {
	f.reportRequest = req
	return f.reportResult, f.reportErr
}

func (f *fakeContractConnector) GetServiceContract(
	req *apiservice.ServiceContract, _ time.Duration,
) (*apiservice.ServiceContract, error) {
	f.getRequest = req
	f.getCalls++
	if f.getDelay > 0 {
		time.Sleep(f.getDelay)
	}
	if len(f.getErrs) > 0 {
		err := f.getErrs[0]
		f.getErrs = f.getErrs[1:]
		return nil, err
	}
	return f.getResult, f.getErr
}

func TestEngineReportsAndGetsServiceContract(t *testing.T) {
	connector := &fakeContractConnector{
		reportResult: &apiservice.ServiceContract{Id: "contract-id"},
		getResult: &apiservice.ServiceContract{
			Id:        "contract-id",
			Name:      "search-mcp",
			Type:      "search-mcp",
			Namespace: "Production",
			Service:   "search-mcp",
			Protocol:  string(model.MCPTransportStreamableHTTP),
			Version:   "1.0.0",
			Revision:  "revision-1",
			ServiceFeatures: []*apiservice.ServiceFeature{{
				Id: "tool-id", Type: apiservice.ServiceFeatureType_Service_Feature_Type_MCP_Tool,
				Name: "search", Description: "search", Content: `{"name":"search"}`,
			}},
			Interfaces: []*apiservice.InterfaceDescriptor{{
				Id: "interface-id", Method: "tools/list", Path: "/mcp",
			}},
		},
	}
	cfg := config.NewDefaultConfiguration([]string{"127.0.0.1:8091"})
	cfg.GetProvider().SetReportServiceContractEnable(true)
	engine := &Engine{configuration: cfg, contractConnector: connector}
	contract := &model.ServiceContract{
		GetServiceContractRequest: model.GetServiceContractRequest{
			Name:      "search-mcp",
			Namespace: "Production",
			Service:   "search-mcp",
			Protocol:  string(model.MCPTransportStreamableHTTP),
			Version:   "1.0.0",
		},
		Metadata: map[string]string{"owner": "team-a"},
		ServiceFeatures: []*model.ServiceFeature{{
			Type: model.ServiceFeatureTypeMCPTool, Name: "search",
			Status: model.ServiceFeatureEnabled, Content: `{"name":"search"}`,
		}},
		Interfaces: []*model.InterfaceDescriptor{{Method: "tools/list", Path: "/mcp"}},
	}
	reported, err := engine.SyncReportServiceContract(contract)
	require.NoError(t, err)
	require.NotNil(t, reported)
	require.NotNil(t, connector.reportRequest)
	assert.Equal(t, apiservice.ServiceFeatureType_Service_Feature_Type_MCP_Tool,
		connector.reportRequest.GetServiceFeatures()[0].GetType())
	assert.Equal(t, "tools/list", connector.reportRequest.GetInterfaces()[0].GetMethod())

	response, err := engine.SyncGetServiceContract(&contract.GetServiceContractRequest)
	require.NoError(t, err)
	require.Equal(t, model.EventServiceContract, response.GetType())
	fetched, ok := response.GetValue().(*model.ServiceContract)
	require.True(t, ok)
	assert.Equal(t, "revision-1", fetched.Revision)
	require.Len(t, fetched.ServiceFeatures, 1)
	assert.Equal(t, model.ServiceFeatureTypeMCPTool, fetched.ServiceFeatures[0].Type)
	require.Len(t, fetched.Interfaces, 1)
	assert.Equal(t, "tools/list", fetched.Interfaces[0].Method)
	assert.Equal(t, "search-mcp", connector.getRequest.GetName())
	assert.Equal(t, "search-mcp", connector.getRequest.GetType())
}

func TestEngineServiceContractDisabledAndIdentity(t *testing.T) {
	cfg := config.NewDefaultConfiguration([]string{"127.0.0.1:8091"})
	engine := &Engine{configuration: cfg}
	_, err := engine.SyncGetServiceContract(&model.GetServiceContractRequest{
		Namespace: "Production", Service: "search-mcp", Protocol: "http",
	})
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeServiceContractDisabled, model.GetErrorCodeFromError(err))

	cfg.GetProvider().SetReportServiceContractEnable(true)
	connector := &fakeContractConnector{
		getResult: &apiservice.ServiceContract{
			Namespace: "Other",
			Service:   "search-mcp",
			Name:      "search-mcp",
			Type:      "search-mcp",
			Protocol:  "http",
		},
	}
	engine.contractConnector = connector
	_, err = engine.SyncGetServiceContract(&model.GetServiceContractRequest{
		Namespace: "Production", Service: "search-mcp", Protocol: "http",
	})
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeInvalidResponse, model.GetErrorCodeFromError(err))

	connector.getErr = model.NewSDKError(model.ErrCodeServiceContractNotFound, errors.New("missing"), "missing")
	_, err = engine.SyncGetServiceContract(&model.GetServiceContractRequest{
		Namespace: "Production", Service: "search-mcp", Protocol: "http",
	})
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeServiceContractNotFound, model.GetErrorCodeFromError(err))
}

func TestEngineServiceContractPreservesServerNameAndType(t *testing.T) {
	cfg := config.NewDefaultConfiguration([]string{"127.0.0.1:8091"})
	cfg.GetProvider().SetReportServiceContractEnable(true)
	connector := &fakeContractConnector{
		getResult: &apiservice.ServiceContract{
			Namespace: "Production",
			Service:   "search-mcp",
			Name:      "custom-contract",
			Type:      "custom-type",
			Protocol:  "http",
			Version:   "1.0.0",
			Revision:  "revision-1",
		},
	}
	engine := &Engine{configuration: cfg, contractConnector: connector}
	response, err := engine.SyncGetServiceContract(&model.GetServiceContractRequest{
		Namespace: "Production",
		Service:   "search-mcp",
		Name:      "custom-contract",
		Type:      "custom-type",
		Protocol:  "http",
		Version:   "1.0.0",
	})
	require.NoError(t, err)
	contract, ok := response.GetValue().(*model.ServiceContract)
	require.True(t, ok)
	require.Equal(t, "custom-contract", contract.Name)
	require.Equal(t, "custom-type", contract.Type)

	connector.getResult.Type = "different-type"
	_, err = engine.SyncGetServiceContract(&model.GetServiceContractRequest{
		Namespace: "Production",
		Service:   "search-mcp",
		Name:      "custom-contract",
		Type:      "custom-type",
		Protocol:  "http",
	})
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeInvalidResponse, model.GetErrorCodeFromError(err))

	connector.getResult.Type = "custom-type"
	connector.getResult.Name = "different-contract"
	_, err = engine.SyncGetServiceContract(&model.GetServiceContractRequest{
		Namespace: "Production",
		Service:   "search-mcp",
		Name:      "custom-contract",
		Type:      "custom-type",
		Protocol:  "http",
	})
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeInvalidResponse, model.GetErrorCodeFromError(err))
}

func TestEngineServiceContractRetriesNetworkErrorsOnly(t *testing.T) {
	cfg := config.NewDefaultConfiguration([]string{"127.0.0.1:8091"})
	cfg.GetProvider().SetReportServiceContractEnable(true)
	cfg.GetGlobal().GetAPI().SetRetryInterval(time.Millisecond)
	connector := &fakeContractConnector{
		getErrs: []error{
			model.NewSDKError(model.ErrCodeNetworkError, errors.New("connection reset"), "connection reset"),
		},
		getResult: &apiservice.ServiceContract{
			Namespace: "Production",
			Service:   "search-mcp",
			Name:      "search-mcp",
			Type:      "search-mcp",
			Protocol:  "http",
			Revision:  "revision-1",
		},
	}
	engine := &Engine{configuration: cfg, contractConnector: connector}
	request := &model.GetServiceContractRequest{
		Namespace: "Production", Service: "search-mcp", Protocol: "http",
	}
	request.SetRetryCount(1)
	response, err := engine.SyncGetServiceContract(request)
	require.NoError(t, err)
	require.Equal(t, "revision-1", response.GetRevision())
	require.Equal(t, 2, connector.getCalls)

	connector.getCalls = 0
	connector.getErrs = []error{
		model.NewSDKError(model.ErrCodeInvalidResponse, nil, "wrong contract"),
	}
	request.SetRetryCount(1)
	_, err = engine.SyncGetServiceContract(request)
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeInvalidResponse, model.GetErrorCodeFromError(err))
	require.Equal(t, 1, connector.getCalls)
}

func TestEngineServiceContractRetrySleepUsesRemainingBudget(t *testing.T) {
	cfg := config.NewDefaultConfiguration([]string{"127.0.0.1:8091"})
	cfg.GetProvider().SetReportServiceContractEnable(true)
	cfg.GetGlobal().GetAPI().SetTimeout(500 * time.Millisecond)
	cfg.GetGlobal().GetAPI().SetRetryInterval(2 * time.Second)
	connector := &fakeContractConnector{
		getDelay: 400 * time.Millisecond,
		getErrs: []error{
			model.NewSDKError(model.ErrCodeNetworkError, errors.New("connection reset"), "connection reset"),
		},
	}
	engine := &Engine{configuration: cfg, contractConnector: connector}
	request := &model.GetServiceContractRequest{
		Namespace: "Production", Service: "search-mcp", Protocol: "http",
	}
	request.SetRetryCount(1)

	start := time.Now()
	_, err := engine.SyncGetServiceContract(request)
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeAPITimeoutError, model.GetErrorCodeFromError(err))
	assert.Equal(t, 1, connector.getCalls)
	assert.Less(t, time.Since(start), 700*time.Millisecond,
		"retry sleep must use the remaining request budget")
}
