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

const (
	// DefaultLogger 默认logger
	DefaultLogger   = LoggerZap
	DefaultLogLevel = InfoLog
	// DefaultBaseLogLevel 默认基础日志级别
	DefaultBaseLogLevel = DefaultLogLevel
	// DefaultStatLogLevel 默认统计日志级别
	DefaultStatLogLevel = DefaultLogLevel
	// DefaultDetectLogLevel 默认探测日志级别
	DefaultDetectLogLevel = DefaultLogLevel
	// DefaultStatReportLogLevel 默认统计上报日志级别
	DefaultStatReportLogLevel = DefaultLogLevel
	// DefaultNetworkLogLevel 默认网络交互日志级别
	DefaultNetworkLogLevel = DefaultLogLevel
	// DefaultCacheLogLevel 默认缓存日志级别
	DefaultCacheLogLevel = DefaultLogLevel
	// 默认基础日志名
	baseLoggerName = "base"
	// 默认统计日志名
	statLoggerName = "stat"
	// 默认统计上报日志名
	statReportLoggerName = "statReport"
	// 默认基础日志名
	detectLoggerName = "detect"
	// 默认网络交互日志名
	networkLoggerName = "network"
	// 默认缓存交互日志名
	cacheLoggerName = "cache"
)

const (
	// DefaultErrorOutputPath 默认直接错误输出路径
	DefaultErrorOutputPath = "stderr"
	// DefaultRotationMaxAge 默认滚动日志保留时间
	DefaultRotationMaxAge = 30
	// DefaultRotationMaxSize 默认单个日志最大占用空间
	DefaultRotationMaxSize = 50
	// DefaultRotationMaxBackups 默认最大滚动备份
	DefaultRotationMaxBackups = 5
	// DefaultLogRotationRootDir 默认日志根目录
	DefaultLogRotationRootDir = "./polaris/log"
	// DefaultBaseLogRotationPath 默认基础日志滚动文件
	DefaultBaseLogRotationPath = "/base/polaris.log"
	// DefaultStatLogRotationPath 默认统计日志滚动文件
	DefaultStatLogRotationPath = "/stat/polaris-stat.log"
	// DefaultStatReportLogRotationPath 默认统计上报日志滚动文件
	DefaultStatReportLogRotationPath = "/statReport/polaris-statReport.log"
	// DefaultDetectLogRotationPath 默认探测日志滚动文件
	DefaultDetectLogRotationPath = "/detect/polaris-detect.log"
	// DefaultNetworkLogRotationPath 默认网络交互日志滚动文件
	DefaultNetworkLogRotationPath = "/network/polaris-network.log"
	// DefaultCacheLogRotationPath 默认缓存更新日志滚动文件
	DefaultCacheLogRotationPath = "/cache/polaris-cache.log"
	// DefaultBaseLogRotationFile 默认基础日志滚动文件全路径
	DefaultBaseLogRotationFile = DefaultLogRotationRootDir + DefaultBaseLogRotationPath
	// DefaultStatLogRotationFile 默认统计日志滚动文件全路径
	DefaultStatLogRotationFile = DefaultLogRotationRootDir + DefaultStatLogRotationPath
	// DefaultStatReportLogRotationFile 默认统计上报日志滚动文件全路径
	DefaultStatReportLogRotationFile = DefaultLogRotationRootDir + DefaultStatReportLogRotationPath
	// DefaultDetectLogRotationFile 默认探测日志滚动文件全路径
	DefaultDetectLogRotationFile = DefaultLogRotationRootDir + DefaultDetectLogRotationPath
	// DefaultNetworkLogRotationFile 默认网络交互日志滚动文件全路径
	DefaultNetworkLogRotationFile = DefaultLogRotationRootDir + DefaultNetworkLogRotationPath
	// DefaultCacheLogRotationFile 默认缓存更新日志滚动文件全路径
	DefaultCacheLogRotationFile = DefaultLogRotationRootDir + DefaultCacheLogRotationPath
)
