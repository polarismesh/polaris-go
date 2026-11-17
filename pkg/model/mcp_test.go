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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildMCPServerMetadata(t *testing.T) {
	tests := []struct {
		name    string
		request MCPServerMetadataRequest
		path    string
		wantErr string
	}{
		{
			name: "streamable_default",
			request: MCPServerMetadataRequest{
				ServerName: "search-mcp", ProtocolVersion: "2025-06-18, 2025-03-26,2025-06-18",
				Transport: MCPTransportStreamableHTTP,
			},
			path: MCPDefaultStreamableHTTPPath,
		},
		{
			name: "sse_default",
			request: MCPServerMetadataRequest{
				ServerName: "search-mcp", ProtocolVersion: "2025-03-26",
				Transport: MCPTransportSSE,
			},
			path: MCPDefaultSSEPath,
		},
		{
			name: "stdio",
			request: MCPServerMetadataRequest{
				ServerName: "local-mcp", ProtocolVersion: "2025-06-18",
				Transport: MCPTransportStdio,
			},
		},
		{
			name: "unknown_transport",
			request: MCPServerMetadataRequest{
				ServerName: "bad", ProtocolVersion: "2025-06-18", Transport: "websocket",
			},
			wantErr: "transport",
		},
		{
			name: "stdio_endpoint",
			request: MCPServerMetadataRequest{
				ServerName: "bad", ProtocolVersion: "2025-06-18",
				Transport: MCPTransportStdio, EndpointPath: "/mcp",
			},
			wantErr: "stdio",
		},
		{
			name: "relative_endpoint",
			request: MCPServerMetadataRequest{
				ServerName: "bad", ProtocolVersion: "2025-06-18",
				Transport: MCPTransportStreamableHTTP, EndpointPath: "mcp",
			},
			wantErr: "absolute path",
		},
		{
			name: "query_endpoint",
			request: MCPServerMetadataRequest{
				ServerName: "bad", ProtocolVersion: "2025-06-18",
				Transport: MCPTransportStreamableHTTP, EndpointPath: "/mcp?x=1",
			},
			wantErr: "query",
		},
		{
			name: "empty_protocol",
			request: MCPServerMetadataRequest{
				ServerName: "bad", Transport: MCPTransportStreamableHTTP,
			},
			wantErr: "protocol version",
		},
		{
			name: "reserved_server_name",
			request: MCPServerMetadataRequest{
				ServerName: "bad/name", ProtocolVersion: "2025-06-18", Transport: MCPTransportStreamableHTTP,
			},
			wantErr: "reserved",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metadata, err := BuildMCPServerMetadata(&test.request)
			if test.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.request.ServerName, metadata[MCPMetadataKeyServerName])
			if test.path == "" {
				_, ok := metadata[MCPMetadataKeyEndpointPath]
				assert.False(t, ok)
			} else {
				assert.Equal(t, test.path, metadata[MCPMetadataKeyEndpointPath])
			}
		})
	}
	metadata, err := BuildMCPServerMetadata(&MCPServerMetadataRequest{
		ServerName: "search-mcp", ProtocolVersion: "2025-06-18, 2025-03-26,2025-06-18",
		Transport: MCPTransportStreamableHTTP,
	})
	require.NoError(t, err)
	assert.Equal(t, "2025-06-18,2025-03-26", metadata[MCPMetadataKeyProtocolVersion])
}

func TestValidateMCPServerMetadata(t *testing.T) {
	metadata, err := BuildMCPServerMetadata(&MCPServerMetadataRequest{
		ServerName: "search-mcp", ProtocolVersion: "2025-06-18",
		Transport: MCPTransportSSE, EndpointPath: "/events/",
	})
	require.NoError(t, err)
	request := &MCPServerValidationRequest{
		Namespace: "Production", Service: "search-mcp", ServerName: "search-mcp",
		ProtocolVersion: "2025-06-18", Transport: MCPTransportSSE,
		EndpointPath: "/events/", Metadata: metadata,
	}
	request.Metadata["custom"] = "allowed"
	require.NoError(t, ValidateMCPServerMetadata(request))

	request.Metadata[MCPMetadataKeyProtocolVersion] = "2024-11-05"
	require.Error(t, ValidateMCPServerMetadata(request))

	request.Metadata = metadata
	request.ServerName = "other-mcp"
	require.Error(t, ValidateMCPServerMetadata(request))
}

func TestMCPProtocolVersionLimit(t *testing.T) {
	versions := make([]string, MaxMCPProtocolVersions+1)
	for index := range versions {
		versions[index] = strings.TrimSpace("v") + strings.TrimSpace(strings.Repeat(" ", 0)) +
			string(rune('0'+index%10))
	}
	_, err := BuildMCPServerMetadata(&MCPServerMetadataRequest{
		ServerName: "search-mcp", ProtocolVersion: strings.Join(versions, ","),
		Transport: MCPTransportStreamableHTTP,
	})
	require.Error(t, err)
}
