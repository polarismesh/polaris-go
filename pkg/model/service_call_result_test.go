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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestServiceCallResult_SetTimestamp 测试场景：SetTimestamp 链式设置时间戳，GetTimestamp 取回原值。
// 前置条件：构造空 ServiceCallResult。
// 预期结果：SetTimestamp 返回自身指针，GetTimestamp 返回设置的值。
func TestServiceCallResult_SetTimestamp(t *testing.T) {
	r := &ServiceCallResult{}
	now := time.Date(2026, 7, 17, 15, 30, 0, 0, time.UTC)
	got := r.SetTimestamp(now)
	assert.Same(t, r, got, "SetTimestamp should return receiver for chaining")
	assert.Equal(t, now, r.GetTimestamp())
	// 默认零值
	assert.True(t, (&ServiceCallResult{}).GetTimestamp().IsZero())
}

// TestServiceCallResult_SetCalledIP 测试场景：SetCalledIP 链式设置主调 IP，复用 CalledIP 字段。
// 前置条件：构造空 ServiceCallResult。
// 预期结果：SetCalledIP 返回自身指针，CalledIP 字段被正确设置。
func TestServiceCallResult_SetCalledIP(t *testing.T) {
	r := &ServiceCallResult{}
	got := r.SetCalledIP("10.0.1.5")
	assert.Same(t, r, got, "SetCalledIP should return receiver for chaining")
	assert.Equal(t, "10.0.1.5", r.CalledIP)
}

// TestServiceCallResult_SetCallerService 测试场景：SetCallerService 复用 SourceService 字段，
// GetCallerService/GetCallerNamespace 能正确读取。
// 前置条件：构造空 ServiceCallResult。
// 预期结果：SetCallerService 返回自身指针；设置后 GetCallerService/GetCallerNamespace 返回主调信息；
// 未设置时两者返回空串。
func TestServiceCallResult_SetCallerService(t *testing.T) {
	r := &ServiceCallResult{}
	info := &ServiceInfo{Namespace: "Production", Service: "order-service"}
	got := r.SetCallerService(info)
	assert.Same(t, r, got, "SetCallerService should return receiver for chaining")
	assert.Same(t, info, r.SourceService)
	assert.Equal(t, "order-service", r.GetCallerService())
	assert.Equal(t, "Production", r.GetCallerNamespace())

	// 未设置 SourceService 时返回空串，不 panic
	empty := &ServiceCallResult{}
	assert.Equal(t, "", empty.GetCallerService())
	assert.Equal(t, "", empty.GetCallerNamespace())
}
