/*
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
 *  under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
 * CONDITIONS OF ANY KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations under the License.
 *
 */

package ctx

import (
	"github.com/polarismesh/polaris-go/pkg/log"
)

// ContextLogger 上下文日志记录器，用于在多 SDK 实例场景下携带客户端标签信息，
// 使得日志输出能够区分不同的 SDK 实例。不可序列化（yaml/json 标签为 "-"）。
type ContextLogger struct {
	BaseLogger       log.Logger `yaml:"-" json:"-"`
	NetworkLogger    log.Logger `yaml:"-" json:"-"`
	CacheLogger      log.Logger `yaml:"-" json:"-"`
	StatLogger       log.Logger `yaml:"-" json:"-"`
	StatReportLogger log.Logger `yaml:"-" json:"-"`
	DetectLogger     log.Logger `yaml:"-" json:"-"`
}

// Init 获取基础日志记录器.
func (c *ContextLogger) Init() {
	c.BaseLogger = log.GetBaseLogger()
	c.NetworkLogger = log.GetNetworkLogger()
	c.CacheLogger = log.GetCacheLogger()
	c.StatLogger = log.GetStatLogger()
	c.StatReportLogger = log.GetStatReportLogger()
	c.DetectLogger = log.GetDetectLogger()
}

// AddFields 为所有日志记录器添加固定字段（如客户端标签），返回新的 ContextLogger 实例。
// 如果 labels 为空则不做任何操作。使用 log.LoggerWithFields 进行类型安全的降级处理，
// 即使 Logger 实现未实现 FieldLogger 接口也不会 panic。
func (c *ContextLogger) AddFields(labels map[string]string) {
	if len(labels) == 0 {
		return
	}
	kvs := make([]string, 0, len(labels)*2)
	for k, v := range labels {
		kvs = append(kvs, k, v)
	}
	c.BaseLogger = log.LoggerWithFields(c.BaseLogger, kvs...)
	c.NetworkLogger = log.LoggerWithFields(c.NetworkLogger, kvs...)
	c.CacheLogger = log.LoggerWithFields(c.CacheLogger, kvs...)
	c.StatLogger = log.LoggerWithFields(c.StatLogger, kvs...)
	c.StatReportLogger = log.LoggerWithFields(c.StatReportLogger, kvs...)
	c.DetectLogger = log.LoggerWithFields(c.DetectLogger, kvs...)
}

// GetBaseLogger 获取基础日志记录器.
func (c *ContextLogger) GetBaseLogger() log.Logger {
	if c == nil {
		return log.GetBaseLogger()
	}
	return c.BaseLogger
}

// GetNetworkLogger 获取网络日志记录器.
func (c *ContextLogger) GetNetworkLogger() log.Logger {
	if c == nil {
		return log.GetNetworkLogger()
	}
	return c.NetworkLogger
}

// GetCacheLogger 获取缓存日志记录器.
func (c *ContextLogger) GetCacheLogger() log.Logger {
	if c == nil {
		return log.GetCacheLogger()
	}
	return c.CacheLogger
}

// GetStatLogger 获取统计日志记录器.
func (c *ContextLogger) GetStatLogger() log.Logger {
	if c == nil {
		return log.GetStatLogger()
	}
	return c.StatLogger
}

// GetStatReportLogger 获取统计上报日志记录器.
func (c *ContextLogger) GetStatReportLogger() log.Logger {
	if c == nil {
		return log.GetStatReportLogger()
	}
	return c.StatReportLogger
}

// GetDetectLogger 获取探测日志记录器.
func (c *ContextLogger) GetDetectLogger() log.Logger {
	if c == nil {
		return log.GetDetectLogger()
	}
	return c.DetectLogger
}
