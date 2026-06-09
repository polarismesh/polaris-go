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

package model

import (
	"strings"
	"testing"

	"github.com/polarismesh/specification/source/go/api/v1/fault_tolerance"
	"github.com/stretchr/testify/assert"
)

// TestNewMethodResourceWithAPI_Basic 测试场景：完整四元组构造接口级熔断资源
// 前置条件：传入合法 service/caller 与 protocol/method/path
// 预期结果：成功创建 MethodResource，Level=METHOD，字段值与入参一致
func TestNewMethodResourceWithAPI_Basic(t *testing.T) {
	svc := &ServiceKey{Namespace: "Production", Service: "order-service"}
	caller := &ServiceKey{Namespace: "Production", Service: "trade-service"}
	res, err := NewMethodResourceWithAPI(svc, caller, "http", "POST", "/api/v1/orders")

	assert.NoError(t, err)
	assert.NotNil(t, res)
	assert.Equal(t, fault_tolerance.Level_METHOD, res.GetLevel())
	assert.Equal(t, "http", res.Protocol)
	assert.Equal(t, "POST", res.Method)
	assert.Equal(t, "/api/v1/orders", res.Path)
	assert.Equal(t, svc, res.GetService())
	assert.Equal(t, caller, res.GetCallerService())
}

// TestNewMethodResourceWithAPI_NormalizeEmpty 测试场景：protocol/method 空串规范化
// 前置条件：protocol 与 method 传空字符串
// 预期结果：自动转换为通配符 "*"，与 polaris-java 行为一致
func TestNewMethodResourceWithAPI_NormalizeEmpty(t *testing.T) {
	svc := &ServiceKey{Namespace: "default", Service: "svc"}
	res, err := NewMethodResourceWithAPI(svc, nil, "", "", "/path")

	assert.NoError(t, err)
	assert.Equal(t, "*", res.Protocol)
	assert.Equal(t, "*", res.Method)
	assert.Equal(t, "/path", res.Path)
}

// TestNewMethodResourceWithAPI_EmptyPath 测试场景：path 为空时返回错误
// 前置条件：path 传空字符串
// 预期结果：返回 "path can not be empty" 错误，资源为 nil
func TestNewMethodResourceWithAPI_EmptyPath(t *testing.T) {
	svc := &ServiceKey{Namespace: "default", Service: "svc"}
	res, err := NewMethodResourceWithAPI(svc, nil, "http", "GET", "")

	assert.Error(t, err)
	assert.Nil(t, res)
	assert.Contains(t, err.Error(), "path can not be empty")
}

// TestNewMethodResource_Compat 测试场景：旧 NewMethodResource 兼容性
// 前置条件：调用旧构造，method 参数携带接口路径
// 预期结果：method 参数被保存到 Path 字段，Protocol/Method 自动设为通配符 "*"
func TestNewMethodResource_Compat(t *testing.T) {
	svc := &ServiceKey{Namespace: "default", Service: "svc"}
	res, err := NewMethodResource(svc, nil, "/legacy/path")

	assert.NoError(t, err)
	assert.Equal(t, "*", res.Protocol)
	assert.Equal(t, "*", res.Method)
	assert.Equal(t, "/legacy/path", res.Path)
}

// TestNewMethodResource_EmptyMethod 测试场景：旧构造传空字符串
// 前置条件：method 参数为空
// 预期结果：返回错误，与未传 path 行为一致
func TestNewMethodResource_EmptyMethod(t *testing.T) {
	svc := &ServiceKey{Namespace: "default", Service: "svc"}
	res, err := NewMethodResource(svc, nil, "")

	assert.Error(t, err)
	assert.Nil(t, res)
}

// TestMethodResource_StringContainsAllFields 测试场景：String() 包含完整四元组
// 前置条件：构造一个带完整 protocol/method/path 的 MethodResource
// 预期结果：String() 输出中包含 protocol/method/path/service 各字段
// 这是 counters bucket key 唯一性的基础保证
func TestMethodResource_StringContainsAllFields(t *testing.T) {
	svc := &ServiceKey{Namespace: "ns1", Service: "svc1"}
	res, err := NewMethodResourceWithAPI(svc, nil, "grpc", "*", "/pkg.S/M")
	assert.NoError(t, err)

	str := res.String()
	assert.True(t, strings.Contains(str, "protocol=grpc"), "missing protocol in %s", str)
	assert.True(t, strings.Contains(str, "method=*"), "missing method in %s", str)
	assert.True(t, strings.Contains(str, "path=/pkg.S/M"), "missing path in %s", str)
	assert.True(t, strings.Contains(str, "ns1"), "missing namespace in %s", str)
	assert.True(t, strings.Contains(str, "svc1"), "missing service name in %s", str)
}

// TestMethodResource_StringUniqueAcrossApis 测试场景：不同 API 的 String() 不同
// 前置条件：同一 service 下构造多个 protocol/method/path 各不相同的资源
// 预期结果：每个资源的 String() 输出都唯一，确保 counters bucket map key 不冲突
func TestMethodResource_StringUniqueAcrossApis(t *testing.T) {
	svc := &ServiceKey{Namespace: "ns", Service: "svc"}
	cases := []struct {
		protocol string
		method   string
		path     string
	}{
		{"http", "GET", "/a"},
		{"http", "POST", "/a"},
		{"http", "GET", "/b"},
		{"grpc", "*", "/a"},
	}
	seen := make(map[string]bool)
	for _, c := range cases {
		res, err := NewMethodResourceWithAPI(svc, nil, c.protocol, c.method, c.path)
		assert.NoError(t, err)
		key := res.String()
		assert.False(t, seen[key], "duplicate key %s for case %+v", key, c)
		seen[key] = true
	}
	assert.Equal(t, len(cases), len(seen))
}

// TestRequestContext_NewFieldsZeroValue 测试场景：RequestContext 新增字段默认零值
// 前置条件：构造一个仅填 Caller/Callee 的 RequestContext
// 预期结果：Protocol/HTTPMethod/Path 均为空串，保留向后兼容
func TestRequestContext_NewFieldsZeroValue(t *testing.T) {
	caller := &ServiceKey{Namespace: "ns", Service: "caller"}
	callee := &ServiceKey{Namespace: "ns", Service: "callee"}
	reqCtx := &RequestContext{Caller: caller, Callee: callee}

	assert.Equal(t, "", reqCtx.Protocol)
	assert.Equal(t, "", reqCtx.HTTPMethod)
	assert.Equal(t, "", reqCtx.Path)
	assert.Equal(t, "", reqCtx.Method)
}
