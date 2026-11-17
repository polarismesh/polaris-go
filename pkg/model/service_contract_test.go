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

package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateServiceContract(t *testing.T) {
	contract := &ServiceContract{
		GetServiceContractRequest: GetServiceContractRequest{
			Namespace: "Production",
			Service:   "search-mcp",
			Name:      "search-mcp",
			Protocol:  string(MCPTransportStreamableHTTP),
			Version:   "1.0.0",
		},
		ServiceFeatures: []*ServiceFeature{
			{
				Type: ServiceFeatureTypeMCPTool, Name: "search", Status: ServiceFeatureEnabled,
				Content: `{"name":"search"}`,
			},
			{
				Type: ServiceFeatureTypeMCPResource, Name: "index", Status: ServiceFeatureDisabled,
				Content: `{"name":"index"}`,
			},
			{
				Type: ServiceFeatureTypeMCPPrompt, Name: "summarize", Status: ServiceFeatureEnabled,
				Content: `{"name":"summarize"}`,
			},
		},
		Interfaces: []*InterfaceDescriptor{
			{Method: "tools/list", Path: "/mcp"},
			{Method: "resources/list", Path: "/mcp"},
		},
	}
	require.NoError(t, contract.Validate())

	tests := []struct {
		name   string
		mutate func(*ServiceContract)
	}{
		{
			name: "invalid feature type",
			mutate: func(contract *ServiceContract) {
				contract.ServiceFeatures[0].Type = 999
			},
		},
		{
			name: "invalid feature status",
			mutate: func(contract *ServiceContract) {
				contract.ServiceFeatures[0].Status = 99
			},
		},
		{
			name: "invalid feature JSON",
			mutate: func(contract *ServiceContract) {
				contract.ServiceFeatures[0].Content = "{"
			},
		},
		{
			name: "missing interface method",
			mutate: func(contract *ServiceContract) {
				contract.Interfaces[0].Method = ""
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copied := CloneServiceContract(contract)
			test.mutate(copied)
			require.Error(t, copied.Validate())
			require.NoError(t, contract.Validate())
		})
	}
}

func TestServiceContractRequestValidation(t *testing.T) {
	request := &GetServiceContractRequest{Namespace: "Production", Service: "search-mcp"}
	require.NoError(t, request.Validate())
	request.Name = "search-mcp"
	request.Protocol = "http"
	request.Version = "1.0.0"
	require.NoError(t, request.Validate())

	request.Namespace = ""
	require.Error(t, request.Validate())
	request.Namespace = "Production"
	request.Timeout = ToDurationPtr(-1)
	require.Error(t, request.Validate())
	request.Timeout = nil
	request.RetryCount = new(int)
	*request.RetryCount = -1
	require.Error(t, request.Validate())
}

func TestCloneServiceContractIsDeep(t *testing.T) {
	original := &ServiceContract{
		GetServiceContractRequest: GetServiceContractRequest{
			Namespace: "Production", Service: "search-mcp", Name: "search-mcp", Protocol: "http",
		},
		ServiceFeatures: []*ServiceFeature{{
			Type: ServiceFeatureTypeMCPTool, Name: "search", Status: ServiceFeatureEnabled,
		}},
		Interfaces: []*InterfaceDescriptor{{Method: "tools/list"}},
		Metadata:   map[string]string{"owner": "team"},
	}
	cloned := CloneServiceContract(original)
	cloned.ServiceFeatures[0].Name = "changed"
	cloned.Interfaces[0].Method = "changed"
	cloned.Metadata["owner"] = "changed"
	assert.Equal(t, "search", original.ServiceFeatures[0].Name)
	assert.Equal(t, "tools/list", original.Interfaces[0].Method)
	assert.Equal(t, "team", original.Metadata["owner"])
}
