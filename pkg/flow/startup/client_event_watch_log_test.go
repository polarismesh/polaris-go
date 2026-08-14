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

package startup

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/polarismesh/polaris-go/pkg/log"
)

// capturingLogger 记录各级别日志内容，用于断言 logConnectFailure 实际选用的级别。
// 仅实现断言所需的 Warnf/Errorf，其余方法由嵌入的 log.Logger 接口占位（测试不触达）。
type capturingLogger struct {
	log.Logger
	warnMsgs  []string
	errorMsgs []string
}

func (l *capturingLogger) Warnf(format string, args ...interface{}) {
	l.warnMsgs = append(l.warnMsgs, fmt.Sprintf(format, args...))
}

func (l *capturingLogger) Errorf(format string, args ...interface{}) {
	l.errorMsgs = append(l.errorMsgs, fmt.Sprintf(format, args...))
}

// TestLogConnectFailure_LevelByError 走真实的 logConnectFailure 调用链，断言实际落地的日志级别，
// 而非仅验证分级判定函数——确保 warn/error 分支真的被接上。
func TestLogConnectFailure_LevelByError(t *testing.T) {
	notFound := status.Error(codes.NotFound, "client not found in cache")
	unavailable := status.Error(codes.Unavailable, "connection refused")
	deadlineExceeded := status.Error(codes.DeadlineExceeded, "i/o timeout")
	permissionDenied := status.Error(codes.PermissionDenied, "token invalid")

	tests := []struct {
		name      string
		failCount int
		err       error
		wantWarn  int
		wantError int
	}{
		{
			name:      "首次 NotFound 落 warn",
			failCount: 1,
			err:       notFound,
			wantWarn:  1,
			wantError: 0,
		},
		{
			name:      "第 3 次 NotFound 仍落 warn",
			failCount: watchNotFoundWarnCount,
			err:       notFound,
			wantWarn:  1,
			wantError: 0,
		},
		{
			name:      "第 4 次 NotFound 升为 error",
			failCount: watchNotFoundWarnCount + 1,
			err:       notFound,
			wantWarn:  0,
			wantError: 1,
		},
		{
			name:      "瞬时错误 Unavailable 首次记 warn",
			failCount: 1,
			err:       unavailable,
			wantWarn:  1,
			wantError: 0,
		},
		{
			name:      "瞬时错误 Unavailable 高连续失败仍记 warn",
			failCount: watchLogSuppressAfter,
			err:       unavailable,
			wantWarn:  1,
			wantError: 0,
		},
		{
			name:      "瞬时错误 DeadlineExceeded 记 warn",
			failCount: 2,
			err:       deadlineExceeded,
			wantWarn:  1,
			wantError: 0,
		},
		{
			name:      "非瞬时错误 PermissionDenied 首次即 error",
			failCount: 1,
			err:       permissionDenied,
			wantWarn:  0,
			wantError: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 替换全局 baseLogger 以捕获输出，测试结束后恢复，避免影响同包其他测试
			captured := &capturingLogger{}
			origin := log.GetBaseLogger()
			log.SetBaseLogger(captured)
			defer log.SetBaseLogger(origin)

			logCtx := &log.ContextLogger{}
			logCtx.Init()

			w := NewClientEventWatcher(nil, "test-client", nil, logCtx)
			w.failCount = tt.failCount
			w.logConnectFailure(tt.err)

			assert.Len(t, captured.warnMsgs, tt.wantWarn, "warn 条数")
			assert.Len(t, captured.errorMsgs, tt.wantError, "error 条数")
		})
	}
}

// TestHasGRPCCode 覆盖直接错误、SDK 包装后的多层错误链、nil 与非 gRPC 错误。
func TestHasGRPCCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code codes.Code
		want bool
	}{
		{
			name: "nil 错误不匹配任何 code",
			err:  nil,
			code: codes.NotFound,
			want: false,
		},
		{
			name: "直接的 NotFound 匹配",
			err:  status.Error(codes.NotFound, "client not found in cache"),
			code: codes.NotFound,
			want: true,
		},
		{
			name: "包装一层的 NotFound 匹配",
			err:  fmt.Errorf("fail to watch: %w", status.Error(codes.NotFound, "client not found in cache")),
			code: codes.NotFound,
			want: true,
		},
		{
			name: "包装两层的 NotFound 匹配",
			err: fmt.Errorf("outer: %w",
				fmt.Errorf("inner: %w", status.Error(codes.NotFound, "client not found in cache"))),
			code: codes.NotFound,
			want: true,
		},
		{
			name: "Unavailable 不匹配 NotFound",
			err:  status.Error(codes.Unavailable, "connection refused"),
			code: codes.NotFound,
			want: false,
		},
		{
			name: "Unimplemented 不匹配 NotFound",
			err:  status.Error(codes.Unimplemented, "unknown method"),
			code: codes.NotFound,
			want: false,
		},
		{
			name: "直接的 Unimplemented 匹配 Unimplemented",
			err:  status.Error(codes.Unimplemented, "unknown method"),
			code: codes.Unimplemented,
			want: true,
		},
		{
			name: "包装后的 Unimplemented 匹配 Unimplemented",
			err:  fmt.Errorf("wrapped: %w", status.Error(codes.Unimplemented, "unknown method")),
			code: codes.Unimplemented,
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, hasGRPCCode(tt.err, tt.code))
		})
	}
}

// TestIsClientNotFound 校验 NotFound 判定不会与 Unimplemented 互相误判——
// 两者在 runLoop 中的处置完全相反（NotFound 退避重连，Unimplemented 直接退出）。
func TestIsClientNotFound(t *testing.T) {
	tests := []struct {
		name               string
		err                error
		wantClientNotFound bool
		wantUnimplemented  bool
	}{
		{
			name:               "nil",
			err:                nil,
			wantClientNotFound: false,
			wantUnimplemented:  false,
		},
		{
			name:               "服务端 client 缓存未命中",
			err:                status.Error(codes.NotFound, "client not found in cache"),
			wantClientNotFound: true,
			wantUnimplemented:  false,
		},
		{
			name:               "旧版服务端未实现该接口",
			err:                status.Error(codes.Unimplemented, "unknown method WatchClientEvents"),
			wantClientNotFound: false,
			wantUnimplemented:  true,
		},
		{
			name:               "普通非 gRPC 错误",
			err:                errors.New("some plain error"),
			wantClientNotFound: false,
			wantUnimplemented:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantClientNotFound, isClientNotFound(tt.err), "isClientNotFound")
			assert.Equal(t, tt.wantUnimplemented, isUnimplemented(tt.err), "isUnimplemented")
		})
	}
}

// TestShouldLogFailureAsWarn 校验日志分级策略：
// NotFound 的前 watchNotFoundWarnCount 次记 warn（启动期缓存刷新竞态，可自愈），
// 超出次数或其他错误记 error（可能是真实故障）。
func TestShouldLogFailureAsWarn(t *testing.T) {
	notFound := status.Error(codes.NotFound, "client not found in cache")
	unavailable := status.Error(codes.Unavailable, "connection refused")
	deadlineExceeded := status.Error(codes.DeadlineExceeded, "i/o timeout")
	permissionDenied := status.Error(codes.PermissionDenied, "token invalid")

	tests := []struct {
		name      string
		failCount int
		err       error
		want      bool
	}{
		{
			name:      "首次 NotFound 记 warn",
			failCount: 1,
			err:       notFound,
			want:      true,
		},
		{
			name:      "第 3 次 NotFound 仍记 warn",
			failCount: watchNotFoundWarnCount,
			err:       notFound,
			want:      true,
		},
		{
			name:      "第 4 次 NotFound 升为 error",
			failCount: watchNotFoundWarnCount + 1,
			err:       notFound,
			want:      false,
		},
		{
			name:      "瞬时错误 Unavailable 首次记 warn",
			failCount: 1,
			err:       unavailable,
			want:      true,
		},
		{
			name:      "瞬时错误 Unavailable 高连续失败仍记 warn",
			failCount: 100,
			err:       unavailable,
			want:      true,
		},
		{
			name:      "瞬时错误 DeadlineExceeded 记 warn",
			failCount: 1,
			err:       deadlineExceeded,
			want:      true,
		},
		{
			name:      "非瞬时错误 PermissionDenied 记 error",
			failCount: 1,
			err:       permissionDenied,
			want:      false,
		},
		{
			name:      "普通非 gRPC 错误记 error",
			failCount: 1,
			err:       errors.New("some network error"),
			want:      false,
		},
		{
			name:      "nil 错误记 error",
			failCount: 1,
			err:       nil,
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, shouldLogFailureAsWarn(tt.failCount, tt.err))
		})
	}
}
