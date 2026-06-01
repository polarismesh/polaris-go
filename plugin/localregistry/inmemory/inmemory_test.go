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

package inmemory

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
)

// recordingLogger 实现 log.Logger，把每次调用记录下来供断言。
// 仅在测试中使用：所有 Tracef/Debugf/Infof/Fatalf 都会记录到对应级别下，
// 便于精确判断 logServiceRuleValidationFailure 选择了 WARN 还是 ERROR。
type recordingLogger struct {
	mu       sync.Mutex
	warnMsgs []string
	errMsgs  []string
}

func (l *recordingLogger) Tracef(format string, args ...interface{}) {}
func (l *recordingLogger) Debugf(format string, args ...interface{}) {}
func (l *recordingLogger) Infof(format string, args ...interface{})  {}
func (l *recordingLogger) Warnf(format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warnMsgs = append(l.warnMsgs, fmt.Sprintf(format, args...))
}
func (l *recordingLogger) Errorf(format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errMsgs = append(l.errMsgs, fmt.Sprintf(format, args...))
}
func (l *recordingLogger) Fatalf(format string, args ...interface{}) {}
func (l *recordingLogger) IsLevelEnabled(_ int) bool                 { return true }
func (l *recordingLogger) SetLogLevel(_ int) error                   { return nil }

// 编译期校验 recordingLogger 满足 log.Logger 接口。
var _ log.Logger = (*recordingLogger)(nil)

// TestLogServiceRuleValidationFailure_BehaviorNotRegistered 验证：
// 当校验错误是 *pb.BehaviorNotRegisteredError 时，必须走 Warnf 路径，
// 不能产生任何 Errorf 日志。同时关键字 "references unregistered behavior plugin"
// 必须出现在日志正文中（verify.sh 依赖该关键字做端到端校验）。
func TestLogServiceRuleValidationFailure_BehaviorNotRegistered(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		behavior string
	}{
		{
			name:     "direct typed error",
			err:      &pb.BehaviorNotRegisteredError{Behavior: "tsf"},
			behavior: "tsf",
		},
		{
			name:     "wrapped typed error still triggers warn",
			err:      fmt.Errorf("upper wrap: %w", &pb.BehaviorNotRegisteredError{Behavior: "vendor-x"}),
			behavior: "vendor-x",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := &recordingLogger{}

			logServiceRuleValidationFailure(logger, "svc-a", "ns-a", tt.err)

			assert.Empty(t, logger.errMsgs,
				"BehaviorNotRegisteredError must NOT log at ERROR level")
			assert.Len(t, logger.warnMsgs, 1,
				"BehaviorNotRegisteredError must log exactly one WARN entry")
			msg := logger.warnMsgs[0]
			assert.Contains(t, msg, "references unregistered behavior plugin",
				"WARN message must contain the verify.sh-recognized keyword")
			assert.Contains(t, msg, tt.behavior,
				"WARN message must include the offending behavior name")
			assert.Contains(t, msg, "svc-a",
				"WARN message must include the service name for log triage")
			assert.Contains(t, msg, "ns-a",
				"WARN message must include the namespace for log triage")
		})
	}
}

// TestLogServiceRuleValidationFailure_OtherError 验证：
// 非 *pb.BehaviorNotRegisteredError 的校验错误必须走 Errorf 路径，
// 保留旧版日志文案以便既有监控告警继续命中。
func TestLogServiceRuleValidationFailure_OtherError(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "plain error",
			err:  errors.New("amount illegal"),
		},
		{
			name: "wrapped plain error",
			err:  fmt.Errorf("validate failed: %w", errors.New("validDuration parse error")),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := &recordingLogger{}

			logServiceRuleValidationFailure(logger, "svc-b", "ns-b", tt.err)

			assert.Empty(t, logger.warnMsgs,
				"non-BehaviorNotRegistered errors must NOT downgrade to WARN")
			assert.Len(t, logger.errMsgs, 1,
				"non-BehaviorNotRegistered errors must log exactly one ERROR entry")
			msg := logger.errMsgs[0]
			assert.Contains(t, msg, "fail to validate service rule",
				"ERROR message must keep the legacy phrase for log alert grep compatibility")
			assert.Contains(t, msg, "svc-b")
			assert.Contains(t, msg, "ns-b")
		})
	}
}
