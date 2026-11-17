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
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	// MCPMetadataKeyServerName 是 MCP Server 逻辑名 metadata key。
	MCPMetadataKeyServerName = "mcp-server-name"
	// MCPMetadataKeyProtocolVersion 是 MCP 协议版本 metadata key。
	MCPMetadataKeyProtocolVersion = "mcp-protocol-version"
	// MCPMetadataKeyEndpointPath 是 MCP endpoint path metadata key。
	MCPMetadataKeyEndpointPath = "mcp-endpoint-path"

	// MCPDefaultSSEPath 是 SSE transport 的默认 endpoint。
	MCPDefaultSSEPath = "/sse"
	// MCPDefaultStreamableHTTPPath 是 Streamable HTTP 的默认 endpoint。
	MCPDefaultStreamableHTTPPath = "/mcp"

	// MaxMCPProtocolVersions 是逗号分隔协议版本的数量上限。
	MaxMCPProtocolVersions = 32
	// MaxMCPServerNameRunes 是 MCP Server/Service 名上限。
	MaxMCPServerNameRunes = 255
)

// MCPTransport 是 MCP 实例 transport/protocol 值。
type MCPTransport string

const (
	// MCPTransportStreamableHTTP 表示 Streamable HTTP。
	MCPTransportStreamableHTTP MCPTransport = "mcp-streamable-http"
	// MCPTransportSSE 表示 SSE。
	MCPTransportSSE MCPTransport = "mcp-sse"
	// MCPTransportStdio 表示本地 stdio。
	MCPTransportStdio MCPTransport = "mcp-stdio"
)

// IsNetwork 返回 transport 是否需要网络实例。
func (t MCPTransport) IsNetwork() bool {
	return t == MCPTransportStreamableHTTP || t == MCPTransportSSE
}

// Valid 返回是否为受支持的 MCP transport。
func (t MCPTransport) Valid() bool {
	return t.IsNetwork() || t == MCPTransportStdio
}

// MCPServerMetadataRequest 描述需要生成 metadata 的 MCP Server。
type MCPServerMetadataRequest struct {
	ServerName      string
	ProtocolVersion string
	Transport       MCPTransport
	EndpointPath    string
}

// MCPServerValidationRequest 描述需要校验的 MCP Server 和实例 metadata。
type MCPServerValidationRequest struct {
	Namespace       string
	Service         string
	ServerName      string
	ProtocolVersion string
	Transport       MCPTransport
	EndpointPath    string
	Metadata        map[string]string
}

// BuildMCPServerMetadata 构建并校验标准 MCP 实例 metadata。
func BuildMCPServerMetadata(req *MCPServerMetadataRequest) (map[string]string, error) {
	if req == nil {
		return nil, NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"MCPServerMetadataRequest can not be nil")
	}
	serverName, protocolVersion, endpoint, err := normalizeMCPServer(req)
	if err != nil {
		return nil, err
	}
	metadata := map[string]string{
		MCPMetadataKeyServerName:      serverName,
		MCPMetadataKeyProtocolVersion: protocolVersion,
	}
	if endpoint != "" {
		metadata[MCPMetadataKeyEndpointPath] = endpoint
	}
	if err = validateStringMap(metadata, MaxInstanceMetadataEntries, "MCP instance"); err != nil {
		return nil, err
	}
	return metadata, nil
}

// ValidateMCPServerMetadata 校验 MCP Server 标识、transport 和实例 metadata。
func ValidateMCPServerMetadata(req *MCPServerValidationRequest) error {
	if req == nil {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"MCPServerValidationRequest can not be nil")
	}
	if req.Namespace == "" || req.Service == "" {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"MCPServerValidationRequest namespace and service must not be empty")
	}
	if req.ServerName != "" && req.ServerName != req.Service {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"MCP server name %q must equal service %q", req.ServerName, req.Service)
	}
	expected, err := BuildMCPServerMetadata(&MCPServerMetadataRequest{
		ServerName:      req.Service,
		ProtocolVersion: req.ProtocolVersion,
		Transport:       req.Transport,
		EndpointPath:    req.EndpointPath,
	})
	if err != nil {
		return err
	}
	if req.Metadata == nil {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"MCP instance metadata can not be nil")
	}
	for key, value := range expected {
		if req.Metadata[key] != value {
			return NewSDKError(ErrCodeAPIInvalidArgument, nil,
				"MCP instance metadata %s=%q does not match expected %q",
				key, req.Metadata[key], value)
		}
	}
	if req.Transport == MCPTransportStdio {
		if _, ok := req.Metadata[MCPMetadataKeyEndpointPath]; ok {
			return NewSDKError(ErrCodeAPIInvalidArgument, nil,
				"mcp-stdio must not expose a network endpoint path")
		}
	}
	return validateStringMap(req.Metadata, MaxInstanceMetadataEntries, "MCP instance")
}

func normalizeMCPServer(req *MCPServerMetadataRequest) (string, string, string, error) {
	serverName := strings.TrimSpace(req.ServerName)
	if serverName == "" || serverName != req.ServerName {
		return "", "", "", NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"MCP server name must be non-empty and must not contain surrounding whitespace")
	}
	if utf8.RuneCountInString(serverName) > MaxMCPServerNameRunes {
		return "", "", "", NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"MCP server name is too long")
	}
	if strings.ContainsAny(serverName, "/\\:@\x00\r\n") {
		return "", "", "", NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"MCP server name contains reserved characters")
	}
	if !req.Transport.Valid() {
		return "", "", "", NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"MCP transport %q must be %s, %s, or %s", req.Transport,
			MCPTransportStreamableHTTP, MCPTransportSSE, MCPTransportStdio)
	}
	protocolVersion, err := normalizeMCPProtocolVersions(req.ProtocolVersion)
	if err != nil {
		return "", "", "", err
	}
	if req.Transport == MCPTransportStdio && req.EndpointPath != "" {
		return "", "", "", NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"mcp-stdio must not configure an endpoint path")
	}
	endpoint, err := normalizeMCPPath(req.EndpointPath)
	if err != nil {
		return "", "", "", err
	}
	if endpoint == "" {
		switch req.Transport {
		case MCPTransportSSE:
			endpoint = MCPDefaultSSEPath
		case MCPTransportStreamableHTTP:
			endpoint = MCPDefaultStreamableHTTPPath
		}
	}
	return serverName, protocolVersion, endpoint, nil
}

func normalizeMCPProtocolVersions(value string) (string, error) {
	if value == "" || strings.TrimSpace(value) != value {
		return "", NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"MCP protocol version must be non-empty and must not contain surrounding whitespace")
	}
	parts := strings.Split(value, ",")
	if len(parts) == 0 || len(parts) > MaxMCPProtocolVersions {
		return "", NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"MCP protocol versions must contain at most %d values", MaxMCPProtocolVersions)
	}
	seen := make(map[string]struct{}, len(parts))
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		version := strings.TrimSpace(part)
		if version == "" || strings.ContainsAny(version, "\x00\r\n") {
			return "", NewSDKError(ErrCodeAPIInvalidArgument, nil,
				"MCP protocol versions contain an empty or invalid value")
		}
		if utf8.RuneCountInString(version) > MaxMetadataKeyRunes {
			return "", NewSDKError(ErrCodeAPIInvalidArgument, nil,
				"MCP protocol version is too long")
		}
		if _, ok := seen[version]; ok {
			continue
		}
		seen[version] = struct{}{}
		normalized = append(normalized, version)
	}
	result := strings.Join(normalized, ",")
	if utf8.RuneCountInString(result) > MaxMetadataValueRunes {
		return "", NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"MCP protocol versions are too long")
	}
	return result, nil
}

func normalizeMCPPath(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if strings.TrimSpace(value) != value || !strings.HasPrefix(value, "/") ||
		strings.HasPrefix(value, "//") || strings.ContainsAny(value, "?#\x00\r\n") {
		return "", fmt.Errorf("MCP endpoint path %q must be an absolute path without query or fragment", value)
	}
	parsed, err := url.ParseRequestURI(value)
	canonical := path.Clean(value)
	if value != "/" && strings.HasSuffix(value, "/") {
		canonical += "/"
	}
	if err != nil || parsed.Path != value || canonical != value {
		return "", fmt.Errorf("MCP endpoint path %q must be canonical", value)
	}
	return value, nil
}

func validateStringMap(values map[string]string, maxEntries int, owner string) error {
	if len(values) > maxEntries {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"%s metadata has %d entries, maximum is %d", owner, len(values), maxEntries)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if key == "" || utf8.RuneCountInString(key) > MaxMetadataKeyRunes {
			return NewSDKError(ErrCodeAPIInvalidArgument, nil,
				"%s metadata key %q is invalid", owner, key)
		}
		if strings.ContainsAny(key, "\x00\r\n") {
			return NewSDKError(ErrCodeAPIInvalidArgument, nil,
				"%s metadata key %q contains control characters", owner, key)
		}
		if utf8.RuneCountInString(values[key]) > MaxMetadataValueRunes {
			return NewSDKError(ErrCodeAPIInvalidArgument, nil,
				"%s metadata value for %q is too long", owner, key)
		}
		if strings.ContainsRune(values[key], '\x00') {
			return NewSDKError(ErrCodeAPIInvalidArgument, nil,
				"%s metadata value for %q contains NUL", owner, key)
		}
	}
	return nil
}

func cloneStringMapForMCP(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	result := make(map[string]string, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}
