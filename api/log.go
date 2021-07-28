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

package api

import (
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/hashicorp/go-multierror"
	"path/filepath"

	//加载插件注册函数
	_ "github.com/polarismesh/polaris-go/pkg/plugin/register"
)

//别名类
type Logger log.Logger

//日志级别
const (
	//跟踪级别
	TraceLog = log.TraceLog
	//调试级别
	DebugLog = log.DebugLog
	//一般日志级别
	InfoLog = log.InfoLog
	//警告日志级别
	WarnLog = log.WarnLog
	//错误日志级别
	ErrorLog = log.ErrorLog
	//致命级别
	FatalLog = log.FatalLog
	//当要禁止日志的时候,可以设置此级别
	NoneLog = log.NoneLog
)

const (
	DefaultBaseLogLevel = log.DefaultBaseLogLevel
	//默认统计日志级别
	DefaultStatLogLevel = log.DefaultStatLogLevel
	//默认探测日志级别
	DefaultDetectLogLevel = log.DefaultDetectLogLevel
	//默认统计上报日志级别
	DefaultStatReportLogLevel = log.DefaultStatReportLogLevel
	//默认网络交互日志级别
	DefaultNetworkLogLevel = log.DefaultNetworkLogLevel
)

//设置基础日志对象
func SetBaseLogger(logger Logger) {
	log.SetBaseLogger(logger)
}

//获取基础日志对象
func GetBaseLogger() Logger {
	return log.GetBaseLogger()
}

//设置统计日志对象
func SetStatLogger(logger Logger) {
	log.SetStatLogger(logger)
}

//获取统计日志对象
func GetStatLogger() Logger {
	return log.GetStatLogger()
}

//设置探测日志对象
func SetDetectLogger(logger Logger) {
	log.SetDetectLogger(logger)
}

//获取探测日志对象
func GetDetectLogger() Logger {
	return log.GetDetectLogger()
}

//设置统计上报日志对象
func SetStatReportLogger(logger Logger) {
	log.SetStatReportLogger(logger)
}

//获取统计上报日志对象
func GetStatReportLogger() Logger {
	return log.GetStatReportLogger()
}

//全局配置日志对象
func ConfigLoggers(logDir string, logLevel int) error {
	var err error
	if err = ConfigBaseLogger(logDir, logLevel); nil != err {
		return fmt.Errorf("fail to ConfigBaseLogger: %v", err)
	}
	if err = ConfigStatLogger(logDir, logLevel); nil != err {
		return fmt.Errorf("fail to ConfigStatLogger: %v", err)
	}
	if err = ConfigDetectLogger(logDir, logLevel); nil != err {
		return fmt.Errorf("fail to ConfigDetectLogger: %v", err)
	}
	if err = ConfigStatReportLogger(logDir, logLevel); nil != err {
		return fmt.Errorf("fail to ConfigStatReportLogger: %v", err)
	}
	if err = ConfigNetworkLogger(logDir, logLevel); nil != err {
		return fmt.Errorf("fail to ConfigNetworkLogger: %v", err)
	}
	return nil
}

//配置基础日志对象
func ConfigBaseLogger(logDir string, logLevel int) error {
	option := log.CreateDefaultLoggerOptions(filepath.Join(logDir, log.DefaultBaseLogRotationPath), logLevel)
	return log.ConfigBaseLogger(log.DefaultLogger, option)
}

//配置统计日志对象
func ConfigStatLogger(logDir string, logLevel int) error {
	option := log.CreateDefaultLoggerOptions(filepath.Join(logDir, log.DefaultStatLogRotationPath), logLevel)
	return log.ConfigStatLogger(log.DefaultLogger, option)
}

//配置探测日志对象
func ConfigDetectLogger(logDir string, logLevel int) error {
	option := log.CreateDefaultLoggerOptions(filepath.Join(logDir, log.DefaultDetectLogRotationPath), logLevel)
	return log.ConfigDetectLogger(log.DefaultLogger, option)
}

//配置统计上报日志对象
func ConfigStatReportLogger(logDir string, logLevel int) error {
	option := log.CreateDefaultLoggerOptions(filepath.Join(logDir, log.DefaultStatReportLogRotationPath), logLevel)
	return log.ConfigStatReportLogger(log.DefaultLogger, option)
}

//配置网络交互日志对象
func ConfigNetworkLogger(logDir string, logLevel int) error {
	option := log.CreateDefaultLoggerOptions(filepath.Join(logDir, log.DefaultNetworkLogRotationPath), logLevel)
	return log.ConfigNetworkLogger(log.DefaultLogger, option)
}

//设置所有日志级别
func SetLoggersLevel(loglevel int) error {
	var err error
	logErr := log.GetStatReportLogger().SetLogLevel(loglevel)
	if nil != logErr {
		err = multierror.Append(err, multierror.Prefix(err, fmt.Sprintf("fail to set statReport loglevel")))
	}
	logErr = log.GetBaseLogger().SetLogLevel(loglevel)
	if nil != logErr {
		err = multierror.Append(err, multierror.Prefix(err, fmt.Sprintf("fail to set base loglevel")))
	}
	logErr = log.GetDetectLogger().SetLogLevel(loglevel)
	if nil != logErr {
		err = multierror.Append(err, multierror.Prefix(err, fmt.Sprintf("fail to set detect loglevel")))
	}
	logErr = log.GetStatLogger().SetLogLevel(loglevel)
	if nil != logErr {
		err = multierror.Append(err, multierror.Prefix(err, fmt.Sprintf("fail to set stat loglevel")))
	}
	logErr = log.GetNetworkLogger().SetLogLevel(loglevel)
	if nil != logErr {
		err = multierror.Append(err, multierror.Prefix(err, fmt.Sprintf("fail to set network logLevel")))
	}
	return err
}

//设置日志的目录，会创建新的具有默认打印级别的logger
func SetLoggersDir(logDir string) error {
	//初始化默认基础日志
	var errs error
	var err error
	option := log.CreateDefaultLoggerOptions(filepath.Join(logDir, log.DefaultBaseLogRotationPath), DefaultBaseLogLevel)
	if err = log.ConfigBaseLogger(log.DefaultLogger, option); nil != err {
		errs = multierror.Append(errs, multierror.Prefix(err,
			fmt.Sprintf("fail to create default base logger with logDir: %s", logDir)))
	}
	option = log.CreateDefaultLoggerOptions(filepath.Join(logDir, log.DefaultStatLogRotationPath), DefaultStatLogLevel)
	if err = log.ConfigStatLogger(log.DefaultLogger, option); nil != err {
		errs = multierror.Append(errs, multierror.Prefix(err,
			fmt.Sprintf("fail to create default stat logger with logDir %s", logDir)))
	}
	option = log.CreateDefaultLoggerOptions(filepath.Join(logDir, log.DefaultDetectLogRotationPath),
		DefaultDetectLogLevel)
	if err = log.ConfigDetectLogger(log.DefaultLogger, option); nil != err {
		errs = multierror.Append(errs, multierror.Prefix(err,
			fmt.Sprintf("fail to create default detect logger with logDir %s", logDir)))
	}
	option = log.CreateDefaultLoggerOptions(filepath.Join(logDir, log.DefaultStatReportLogRotationPath),
		DefaultStatReportLogLevel)
	if err = log.ConfigStatReportLogger(log.DefaultLogger, option); nil != err {
		errs = multierror.Append(errs, multierror.Prefix(err,
			fmt.Sprintf("fail to create default statReport logger with logDir %s", logDir)))
	}
	option = log.CreateDefaultLoggerOptions(filepath.Join(logDir, log.DefaultNetworkLogRotationPath),
		DefaultNetworkLogLevel)
	if err = log.ConfigNetworkLogger(log.DefaultLogger, option); nil != err {
		errs = multierror.Append(errs, multierror.Prefix(err,
			fmt.Sprintf("fail to create default network logger with logDir %s", logDir)))
	}
	return errs
}
