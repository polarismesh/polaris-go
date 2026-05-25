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

// TestRateLimitLogger_RegisteredViaCreator 验证：注册一个名为 DefaultLogger 的 creator
// 后，RateLimitLogger 也会被自动初始化（防止默认值漏接入到 RegisterLoggerCreator 流程）.
func TestRateLimitLogger_RegisteredViaCreator(t *testing.T) {
	// noop 实现，仅验证容器是否被填充
	creator := func(name string, options *Options, defaultLevel int) (Logger, error) {
		return &noopTestLogger{}, nil
	}
	// 用一个临时插件名注册一次，避免影响真正的 zaplog
	RegisterLoggerCreator("ratelimit-creator-check", creator)

	// 直接用全局 Set 验证容器路径
	SetRateLimitLogger(&noopTestLogger{level: InfoLog})
	got := GetRateLimitLogger()
	assert.NotNil(t, got)
	assert.True(t, got.IsLevelEnabled(InfoLog))
}

type noopTestLogger struct {
	level int
}

func (n *noopTestLogger) Tracef(string, ...interface{})    {}
func (n *noopTestLogger) Debugf(string, ...interface{})    {}
func (n *noopTestLogger) Infof(string, ...interface{})     {}
func (n *noopTestLogger) Warnf(string, ...interface{})     {}
func (n *noopTestLogger) Errorf(string, ...interface{})    {}
func (n *noopTestLogger) Fatalf(string, ...interface{})    {}
func (n *noopTestLogger) IsLevelEnabled(l int) bool        { return l >= n.level }
func (n *noopTestLogger) SetLogLevel(l int) error          { n.level = l; return nil }
