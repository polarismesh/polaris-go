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

package log

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSetGetAuditLogger 测试场景：SetAuditLogger 注入审计 logger，GetAuditLogger 取回同一实例。
// 前置条件：复用 ratelimit_logger_test.go 中的 noopTestLogger 作为桩 logger。
// 预期结果：GetAuditLogger 返回 SetAuditLogger 设置的实例，接口方法可调用。
func TestSetGetAuditLogger(t *testing.T) {
	logger := &noopTestLogger{level: InfoLog}
	SetAuditLogger(logger)
	got := GetAuditLogger()
	assert.NotNil(t, got)
	assert.Same(t, logger, got)
	assert.True(t, got.IsLevelEnabled(InfoLog))
}

// TestContextLogger_GetAuditLogger 测试场景：ContextLogger.Init 从全局加载 AuditLogger，
// GetAuditLogger 返回该实例；nil ContextLogger 降级返回全局 AuditLogger。
// 前置条件：先 SetAuditLogger 注入实例。
// 预期结果：ContextLogger.GetAuditLogger 与 nil ContextLogger 均返回注入的实例。
func TestContextLogger_GetAuditLogger(t *testing.T) {
	logger := &noopTestLogger{level: InfoLog}
	SetAuditLogger(logger)
	cl := &ContextLogger{}
	cl.Init()
	got := cl.GetAuditLogger()
	assert.NotNil(t, got)
	assert.Same(t, logger, got)

	// nil ContextLogger 降级返回全局 AuditLogger
	var nilCl *ContextLogger
	assert.Same(t, logger, nilCl.GetAuditLogger())
}

// TestAuditLogger_DefaultNil 测试场景：未注入时 GetAuditLogger 返回 nil，不 panic。
// 前置条件：容器对应槽位为空（测试初始状态或被 nil 覆盖）。
// 预期结果：返回 nil。
func TestAuditLogger_DefaultNil(t *testing.T) {
	SetAuditLogger(nil)
	assert.Nil(t, GetAuditLogger())
}
