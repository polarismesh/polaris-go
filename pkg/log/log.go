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
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/hashicorp/go-multierror"
	"github.com/modern-go/reflect2"
	"log"
	"sync/atomic"
)

const (
	//zap实现的logger
	LoggerZap = "zaplog"
)

/**
 * @brief 日志对象，封装了日志打印的接口逻辑
 */
type Logger interface {
	//打印trace级别的日志
	Tracef(format string, args ...interface{})
	//打印debug级别的日志
	Debugf(format string, args ...interface{})
	//打印info级别的日志
	Infof(format string, args ...interface{})
	//打印warn级别的日志
	Warnf(format string, args ...interface{})
	//打印error级别的日志
	Errorf(format string, args ...interface{})
	//打印fatalf级别的日志
	Fatalf(format string, args ...interface{})
	//判断当前级别是否满足日志打印的最低级别
	IsLevelEnabled(l int) bool
	//动态设置日志打印级别
	SetLogLevel(l int) error
}

//可以返回日志目录的日志对象
type DirLogger interface {
	Logger
	GetLogDir() string
}

const (
	//跟踪级别
	TraceLog int = iota
	//调试级别
	DebugLog
	//一般日志级别
	InfoLog
	//警告日志级别
	WarnLog
	//错误日志级别
	ErrorLog
	//致命级别
	FatalLog
	//当要禁止日志的时候,可以设置此级别
	NoneLog

	//最小日志级别
	minLogLevel = TraceLog
	//最大日志级别
	maxLogLevel = NoneLog
)

//全局日志容器对象
var logContainer = newContainer()

// severityName contains the string representation of each severity.
var SeverityName = []string{
	TraceLog: "TRACE",
	DebugLog: "DEBUG",
	InfoLog:  "INFO",
	WarnLog:  "WARNING",
	ErrorLog: "ERROR",
	FatalLog: "FATAL",
}

/**
 * @brief 创建日志容器
 */
func newContainer() *container {
	cont := &container{
		loggers: make([]*atomic.Value, 0, MaxLogger),
	}
	for i := 0; i < MaxLogger; i++ {
		cont.loggers = append(cont.loggers, &atomic.Value{})
	}
	return cont
}

//日志类型
const (
	//基础日志对象
	BaseLogger = iota
	//统计日志对象
	StatLogger
	//统计日志上报对象，记录上报日志的情况
	StatReportLogger
	//探测日志对象
	DetectLogger
	//与系统服务进行网络交互的相关日志
	NetworkLogger
	//日志对象总量
	MaxLogger
)

/**
 * @brief 日志容器接口
 */
type container struct {
	loggers []*atomic.Value
}

//设置基础日志对象1
func (c *container) SetBaseLogger(logger Logger) {
	c.loggers[BaseLogger].Store(&logger)
}

//设置统计日志对象
func (c *container) SetStatLogger(logger Logger) {
	c.loggers[StatLogger].Store(&logger)
}

//设置统计上报日志对象
func (c *container) SetStatReportLogger(logger Logger) {
	c.loggers[StatReportLogger].Store(&logger)
}

//设置探测日志对象
func (c *container) SetDetectLogger(logger Logger) {
	c.loggers[DetectLogger].Store(&logger)
}

//设置网络交互日志对象
func (c *container) SetNetworkLogger(logger Logger) {
	c.loggers[NetworkLogger].Store(&logger)
}

//获取基础日志对象
func (c *container) GetBaseLogger() Logger {
	value := c.loggers[BaseLogger].Load()
	if reflect2.IsNil(value) {
		return nil
	}
	return *(value.(*Logger))
}

//获取统计日志对象
func (c *container) GetStatLogger() Logger {
	value := c.loggers[StatLogger].Load()
	if reflect2.IsNil(value) {
		return nil
	}
	return *(value.(*Logger))
}

//获取统计上报日志对象
func (c *container) GetStatReportLogger() Logger {
	value := c.loggers[StatReportLogger].Load()
	if reflect2.IsNil(value) {
		return nil
	}
	return *(value.(*Logger))
}

//获取探测日志对象
func (c *container) GetDetectLogger() Logger {
	value := c.loggers[DetectLogger].Load()
	if reflect2.IsNil(value) {
		return nil
	}
	return *(value.(*Logger))
}

//获取网络日志对象
func (c *container) GetNetworkLogger() Logger {
	value := c.loggers[NetworkLogger].Load()
	if reflect2.IsNil(value) {
		return nil
	}
	return *(value.(*Logger))
}

//全局设置基础日志对象
func SetBaseLogger(logger Logger) {
	logContainer.SetBaseLogger(logger)
}

//全局设置统计日志对象
func SetStatLogger(logger Logger) {
	logContainer.SetStatLogger(logger)
}

//全局设置统计上报日志对象
func SetStatReportLogger(logger Logger) {
	logContainer.SetStatReportLogger(logger)
}

//全局设置探测日志对象
func SetDetectLogger(logger Logger) {
	logContainer.SetDetectLogger(logger)
}

//全局设置网络交互日志对象
func SetNetworkLogger(logger Logger) {
	logContainer.SetNetworkLogger(logger)
}

//获取全局基础日志对象
func GetBaseLogger() Logger {
	return logContainer.GetBaseLogger()
}

//获取全局统计日志对象
func GetStatLogger() Logger {
	return logContainer.GetStatLogger()
}

//获取统计上报日志对象
func GetStatReportLogger() Logger {
	return logContainer.GetStatReportLogger()
}

//获取全局探测日志对象
func GetDetectLogger() Logger {
	return logContainer.GetDetectLogger()
}

//获取全局网络交互日志对象
func GetNetworkLogger() Logger {
	return logContainer.GetNetworkLogger()
}

// Options defines the set of options for component logging package.
type Options struct {
	// OutputPaths is a list of file system paths to write the log data to.
	// The special values stdout and stderr can be used to output to the
	// standard I/O streams. This defaults to stdout.
	OutputPaths []string

	// ErrorOutputPaths is a list of file system paths to write logger errors to.
	// The special values stdout and stderr can be used to output to the
	// standard I/O streams. This defaults to stderr.
	ErrorOutputPaths []string

	// RotateOutputPath is the path to a rotating log file. This file should
	// be automatically rotated over time, based on the rotation parameters such
	// as RotationMaxSize and RotationMaxAge. The default is to not rotate.
	//
	// This path is used as a foundational path. This is where log output is normally
	// saved. When a rotation needs to take place because the file got too big or too
	// old, then the file is renamed by appending a timestamp to the name. Such renamed
	// files are called backups. Once a backup has been created,
	// output resumes to this path.
	RotateOutputPath string

	// RotationMaxSize is the maximum size in megabytes of a log file before it gets
	// rotated. It defaults to 100 megabytes.
	RotationMaxSize int

	// RotationMaxAge is the maximum number of days to retain old log files based on the
	// timestamp encoded in their filename. Note that a day is defined as 24
	// hours and may not exactly correspond to calendar days due to daylight
	// savings, leap seconds, etc. The default is to remove log files
	// older than 30 days.
	RotationMaxAge int

	// RotationMaxBackups is the maximum number of old log files to retain.  The default
	// is to retain at most 1000 logs.
	RotationMaxBackups int

	//The min log level that actual log output
	LogLevel int
}

//校验日志级别
func VerifyLogLevel(level int) error {
	if level < minLogLevel || level > maxLogLevel {
		return fmt.Errorf("logLevel must be in [%d, %d], now is %d", minLogLevel, maxLogLevel, level)
	}
	return nil
}

//校验日志配置项
func (o Options) Verify() error {
	var errs error
	if len(o.RotateOutputPath) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("RotateOutputPath is required"))
	}
	if o.RotationMaxBackups == 0 {
		errs = multierror.Append(errs, fmt.Errorf("RotationMaxBackups is required"))
	}
	if o.RotationMaxAge == 0 {
		errs = multierror.Append(errs, fmt.Errorf("RotationMaxAge is required"))
	}
	if o.RotationMaxSize == 0 {
		errs = multierror.Append(errs, fmt.Errorf("RotationMaxSize is required"))
	}
	if err := VerifyLogLevel(o.LogLevel); nil != err {
		errs = multierror.Append(errs, err)
	}
	return errs
}

//创建logger的函数
type loggerCreator func(string, *Options, int) (Logger, error)

//logger插件集合
var loggerCreators = make(map[string]loggerCreator, 0)

//注册Logger插件
func RegisterLoggerCreator(name string, creator loggerCreator) {
	loggerCreators[name] = creator
	if name == DefaultLogger {
		//初始化默认基础日志
		var errs error
		var err error
		if err = ConfigDefaultBaseLogger(name); nil != err {
			errs = multierror.Append(errs, multierror.Prefix(err,
				fmt.Sprintf("fail to create default base logger %s", name)))
		}
		if err = ConfigDefaultStatLogger(name); nil != err {
			errs = multierror.Append(errs, multierror.Prefix(err,
				fmt.Sprintf("fail to create default stat logger %s", name)))
		}
		if err = ConfigDefaultDetectLogger(name); nil != err {
			errs = multierror.Append(errs, multierror.Prefix(err,
				fmt.Sprintf("fail to create default detect logger %s", name)))
		}
		if err = ConfigDefaultStatReportLogger(name); nil != err {
			errs = multierror.Append(errs, multierror.Prefix(err,
				fmt.Sprintf("fail to create default statReport logger %s", name)))
		}
		if err = ConfigDefaultNetworkLogger(name); nil != err {
			errs = multierror.Append(errs, multierror.Prefix(err,
				fmt.Sprintf("fail to create default network logger %s", name)))
		}
		if nil != errs {
			log.Fatalf("RegisterLoggerCreator failed, errs is %v", errs)
		}
	}
}

//配置日志插件
func configLogger(pluginName string, loggerName string, options *Options, defaultLevel int) (logger Logger, err error) {
	if err = options.Verify(); nil != err {
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidConfig, err,
			"configLogger: fail to verify options %+v", *options)
	}
	creator, ok := loggerCreators[pluginName]
	if !ok {
		return nil, model.NewSDKError(model.ErrCodePluginError, nil,
			"configLogger: plugin name %s not registered", pluginName)
	}
	if logger, err = creator(loggerName, options, defaultLevel); nil != err {
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidConfig, err,
			"configLogger: fail to create logger for plugin %s, options %+v", pluginName, *options)
	}
	return logger, nil
}

//配置基础日志器
func ConfigBaseLogger(pluginName string, options *Options) error {
	logger, err := configLogger(pluginName, baseLoggerName, options, DefaultBaseLogLevel)
	if nil != err {
		return err
	}
	SetBaseLogger(logger)
	return nil
}

//配置统计日志器
func ConfigStatLogger(pluginName string, options *Options) error {
	logger, err := configLogger(pluginName, statLoggerName, options, DefaultStatLogLevel)
	if nil != err {
		return err
	}
	SetStatLogger(logger)
	return nil
}

//
func ConfigStatReportLogger(pluginName string, options *Options) error {
	logger, err := configLogger(pluginName, statReportLoggerName, options, DefaultStatReportLogLevel)
	if nil != err {
		return err
	}
	SetStatReportLogger(logger)
	return nil
}

//配置探测日志器
func ConfigDetectLogger(pluginName string, options *Options) error {
	logger, err := configLogger(pluginName, detectLoggerName, options, DefaultDetectLogLevel)
	if nil != err {
		return err
	}
	SetDetectLogger(logger)
	return nil
}

//配置网络交互日志器
func ConfigNetworkLogger(pluginName string, options *Options) error {
	logger, err := configLogger(pluginName, networkLoggerName, options, DefaultNetworkLogLevel)
	if nil != err {
		return err
	}
	SetNetworkLogger(logger)
	return nil
}

//配置默认的日志插件
func CreateDefaultLoggerOptions(rotationPath string, logLevel int) *Options {
	return &Options{
		ErrorOutputPaths:   []string{DefaultErrorOutputPath},
		RotateOutputPath:   model.ReplaceHomeVar(rotationPath),
		RotationMaxSize:    DefaultRotationMaxSize,
		RotationMaxAge:     DefaultRotationMaxAge,
		RotationMaxBackups: DefaultRotationMaxBackups,
		LogLevel:           logLevel,
	}
}

//配置默认的基础日志器
func ConfigDefaultBaseLogger(pluginName string) error {
	return ConfigBaseLogger(pluginName, CreateDefaultLoggerOptions(DefaultBaseLogRotationFile, DefaultBaseLogLevel))
}

//配置默认的统计日志器
func ConfigDefaultStatLogger(pluginName string) error {
	return ConfigStatLogger(pluginName, CreateDefaultLoggerOptions(DefaultStatLogRotationFile, DefaultStatLogLevel))
}

//配置默认的统计上报日志器
func ConfigDefaultStatReportLogger(pluginName string) error {
	return ConfigStatReportLogger(pluginName, CreateDefaultLoggerOptions(DefaultStatReportLogRotationFile,
		DefaultStatReportLogLevel))
}

//配置默认的探测日志器
func ConfigDefaultDetectLogger(pluginName string) error {
	return ConfigDetectLogger(pluginName, CreateDefaultLoggerOptions(DefaultDetectLogRotationFile, DefaultDetectLogLevel))
}

//配置默认的网络交互日志器
func ConfigDefaultNetworkLogger(pluginName string) error {
	return ConfigNetworkLogger(pluginName,
		CreateDefaultLoggerOptions(DefaultNetworkLogRotationFile, DefaultNetworkLogLevel))
}
