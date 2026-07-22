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

package callauditlog

import (
	"fmt"
	"time"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
)

// init 注册可配置插件,将 Config 与 CallAuditLogReporter 绑定。
func init() {
	plugin.RegisterConfigurablePlugin(&CallAuditLogReporter{}, &Config{})
}

const (
	// auditFormatJSON JSON 格式
	auditFormatJSON = "json"
	// auditFormatKV key=value 格式
	auditFormatKV = "kv"
	// defaultBufferSize 默认异步缓冲 channel 容量
	defaultBufferSize = 4096
	// defaultFlushInterval 默认丢弃告警收敛间隔
	defaultFlushInterval = 5 * time.Second
	// defaultRotationMaxSize 默认单个日志文件最大大小(MB)
	defaultRotationMaxSize = 100
	// defaultRotationMaxAge 默认日志保留天数
	defaultRotationMaxAge = 30
	// defaultRotationBackups 默认最大滚动备份数
	defaultRotationBackups = 10
)

// Config callAuditLog 插件配置。启用与否由 global.statReporter.enable 与 chain 决定,
// 与 prometheus 等统计上报插件一致,不在此处单独开关。
type Config struct {
	// Format 日志格式:json 或 kv,默认 json
	Format string `yaml:"format"`
	// RotateOutputPath 审计日志轮转文件路径,空则使用默认 ./polaris/log/audit/polaris-audit.log
	RotateOutputPath string `yaml:"rotateOutputPath"`
	// RotationMaxSize 单个日志文件最大大小(MB)
	RotationMaxSize int `yaml:"rotationMaxSize"`
	// RotationMaxAge 日志保留天数
	RotationMaxAge int `yaml:"rotationMaxAge"`
	// RotationMaxBackups 最大滚动备份数
	RotationMaxBackups int `yaml:"rotationMaxBackups"`
	// Compress 是否压缩旧日志,未设置时默认 true
	Compress *bool `yaml:"compress"`
	// BufferSize 异步缓冲 channel 容量,默认 4096;满时丢弃并告警
	BufferSize int `yaml:"bufferSize"`
	// FlushInterval 丢弃告警收敛间隔,默认 5s
	FlushInterval time.Duration `yaml:"flushInterval"`
}

// SetDefault 设置默认值。
func (c *Config) SetDefault() {
	if c.Format == "" {
		c.Format = auditFormatJSON
	}
	if c.RotateOutputPath == "" {
		c.RotateOutputPath = model.ReplaceHomeVar(log.DefaultAuditLogRotationFile)
	}
	if c.RotationMaxSize == 0 {
		c.RotationMaxSize = defaultRotationMaxSize
	}
	if c.RotationMaxAge == 0 {
		c.RotationMaxAge = defaultRotationMaxAge
	}
	if c.RotationMaxBackups == 0 {
		c.RotationMaxBackups = defaultRotationBackups
	}
	if c.Compress == nil {
		t := true
		c.Compress = &t
	}
	if c.BufferSize == 0 {
		c.BufferSize = defaultBufferSize
	}
	if c.FlushInterval == 0 {
		c.FlushInterval = defaultFlushInterval
	}
}

// Verify 校验配置合法性。
func (c *Config) Verify() error {
	if c.Format != auditFormatJSON && c.Format != auditFormatKV {
		return fmt.Errorf("callAuditLog: invalid format %q, want json|kv", c.Format)
	}
	if c.BufferSize < 0 {
		return fmt.Errorf("callAuditLog: bufferSize must >= 0")
	}
	if c.FlushInterval < 0 {
		return fmt.Errorf("callAuditLog: flushInterval must >= 0")
	}
	return nil
}
