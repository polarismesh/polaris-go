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

package log

// ContextLogger 上下文日志记录器，用于在多 SDK 实例场景下携带客户端标签信息，
// 使得日志输出能够区分不同的 SDK 实例。不可序列化（yaml/json 标签为 "-"）。
// 所有 logger 字段均为私有，仅通过 getter 方法暴露，以保证 label 注入的一致性。
type ContextLogger struct {
	baseLogger       Logger
	networkLogger    Logger
	cacheLogger      Logger
	statLogger       Logger
	statReportLogger Logger
	detectLogger     Logger
	eventLogger      Logger
	losslessLogger   Logger
}

// Init 获取基础日志记录器.
func (c *ContextLogger) Init() {
	c.baseLogger = GetBaseLogger()
	c.networkLogger = GetNetworkLogger()
	c.cacheLogger = GetCacheLogger()
	c.statLogger = GetStatLogger()
	c.statReportLogger = GetStatReportLogger()
	c.detectLogger = GetDetectLogger()
	c.eventLogger = GetEventLogger()
	c.losslessLogger = GetLosslessLogger()
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
	c.baseLogger = LoggerWithFields(c.baseLogger, kvs...)
	c.networkLogger = LoggerWithFields(c.networkLogger, kvs...)
	c.cacheLogger = LoggerWithFields(c.cacheLogger, kvs...)
	c.statLogger = LoggerWithFields(c.statLogger, kvs...)
	c.statReportLogger = LoggerWithFields(c.statReportLogger, kvs...)
	c.detectLogger = LoggerWithFields(c.detectLogger, kvs...)
	c.eventLogger = LoggerWithFields(c.eventLogger, kvs...)
	c.losslessLogger = LoggerWithFields(c.losslessLogger, kvs...)
}

// GetBaseLogger 获取基础日志记录器.
func (c *ContextLogger) GetBaseLogger() Logger {
	if c == nil {
		return GetBaseLogger()
	}
	return c.baseLogger
}

// GetNetworkLogger 获取网络日志记录器.
func (c *ContextLogger) GetNetworkLogger() Logger {
	if c == nil {
		return GetNetworkLogger()
	}
	return c.networkLogger
}

// GetCacheLogger 获取缓存日志记录器.
func (c *ContextLogger) GetCacheLogger() Logger {
	if c == nil {
		return GetCacheLogger()
	}
	return c.cacheLogger
}

// GetStatLogger 获取统计日志记录器.
func (c *ContextLogger) GetStatLogger() Logger {
	if c == nil {
		return GetStatLogger()
	}
	return c.statLogger
}

// GetStatReportLogger 获取统计上报日志记录器.
func (c *ContextLogger) GetStatReportLogger() Logger {
	if c == nil {
		return GetStatReportLogger()
	}
	return c.statReportLogger
}

// GetDetectLogger 获取探测日志记录器.
func (c *ContextLogger) GetDetectLogger() Logger {
	if c == nil {
		return GetDetectLogger()
	}
	return c.detectLogger
}

// GetEventLogger 获取事件日志记录器.
func (c *ContextLogger) GetEventLogger() Logger {
	if c == nil {
		return GetEventLogger()
	}
	return c.eventLogger
}

// GetLosslessLogger 获取无损日志记录器.
func (c *ContextLogger) GetLosslessLogger() Logger {
	if c == nil {
		return GetLosslessLogger()
	}
	return c.losslessLogger
}
