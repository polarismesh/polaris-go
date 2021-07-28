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
	//默认logger
	DefaultLogger = LoggerZap
	//默认基础日志级别
	DefaultBaseLogLevel = InfoLog
	//默认统计日志级别
	DefaultStatLogLevel = InfoLog
	//默认探测日志级别
	DefaultDetectLogLevel = InfoLog
	//默认统计上报日志级别
	DefaultStatReportLogLevel = InfoLog
	//默认网络交互日志级别
	DefaultNetworkLogLevel = InfoLog
	//默认基础日志名
	baseLoggerName = "base"
	//默认统计日志名
	statLoggerName = "stat"
	//默认统计上报日志名
	statReportLoggerName = "statReport"
	//默认基础日志名
	detectLoggerName = "detect"
	//默认网络交互日志名
	networkLoggerName = "network"
)

const (
	//默认直接错误输出路径
	DefaultErrorOutputPath = "stderr"
	//默认滚动日志保留时间
	DefaultRotationMaxAge = 30
	//默认单个日志最大占用空间
	DefaultRotationMaxSize = 50
	//默认最大滚动备份
	DefaultRotationMaxBackups = 5
	//默认日志根目录
	DefaultLogRotationRootDir = "./polaris/log"
	//默认基础日志滚动文件
	DefaultBaseLogRotationPath = "/base/polaris.log"
	//默认统计日志滚动文件
	DefaultStatLogRotationPath = "/stat/polaris-stat.log"
	//默认统计上报日志滚动文件
	DefaultStatReportLogRotationPath = "/statReport/polaris-statReport.log"
	//默认探测日志滚动文件
	DefaultDetectLogRotationPath = "/detect/polaris-detect.log"
	//默认网络交互日志滚动文件
	DefaultNetworkLogRotationPath = "/network/polaris-network.log"
	//默认基础日志滚动文件全路径
	DefaultBaseLogRotationFile = DefaultLogRotationRootDir + DefaultBaseLogRotationPath
	//默认统计日志滚动文件全路径
	DefaultStatLogRotationFile = DefaultLogRotationRootDir + DefaultStatLogRotationPath
	//默认统计上报日志滚动文件全路径
	DefaultStatReportLogRotationFile = DefaultLogRotationRootDir + DefaultStatReportLogRotationPath
	//默认探测日志滚动文件全路径
	DefaultDetectLogRotationFile = DefaultLogRotationRootDir + DefaultDetectLogRotationPath
	//默认网络交互日志滚动文件全路径
	DefaultNetworkLogRotationFile = DefaultLogRotationRootDir + DefaultNetworkLogRotationPath
)
