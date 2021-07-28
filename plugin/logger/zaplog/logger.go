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

package zaplog

import (
	"fmt"
	plog "github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/natefinch/lumberjack"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"path/filepath"
	"sync/atomic"
	"time"
)

//使用zap框架的log实现
type zapLogger struct {
	outputLevel int32
	logger      *zap.Logger
	logDir      string
}

//归一化日志级别
func getOutputLevel(level int, defaultLevel int) int {
	if level >= plog.NoneLog {
		return plog.NoneLog
	}
	if level < 0 {
		return defaultLevel
	}
	return level
}

//配置zapCore日志机制
func prepareZap(name string, options *plog.Options, defaultLevel int) (plog.Logger, error) {
	encCfg := zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "scope",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stack",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeTime:     formatDate,
	}
	var enc = zapcore.NewConsoleEncoder(encCfg)
	var rotaterSink zapcore.WriteSyncer
	if len(options.RotateOutputPath) > 0 {
		rotaterSink = zapcore.AddSync(&lumberjack.Logger{
			Filename:   options.RotateOutputPath,
			MaxSize:    options.RotationMaxSize,
			MaxBackups: options.RotationMaxBackups,
			MaxAge:     options.RotationMaxAge,
			LocalTime:  true,
		})
	}
	var errSink zapcore.WriteSyncer
	var closeErrorSink func()
	var err error
	if len(options.ErrorOutputPaths) > 0 {
		errSink, closeErrorSink, err = zap.Open(options.ErrorOutputPaths...)
		if err != nil {
			return nil, err
		}
	}

	var outputSink zapcore.WriteSyncer
	if len(options.OutputPaths) > 0 {
		outputSink, _, err = zap.Open(options.OutputPaths...)
		if err != nil {
			closeErrorSink()
			return nil, err
		}
	}

	var sink zapcore.WriteSyncer
	if rotaterSink != nil && outputSink != nil {
		sink = zapcore.NewMultiWriteSyncer(outputSink, rotaterSink)
	} else if rotaterSink != nil {
		sink = rotaterSink
	} else {
		sink = outputSink
	}
	outputLevel := getOutputLevel(options.LogLevel, defaultLevel)
	core := zapcore.NewCore(enc, sink, zap.NewAtomicLevelAt(zapcore.DebugLevel))
	logger := zap.New(core, zap.ErrorOutput(errSink), zap.AddCaller(), zap.AddCallerSkip(2)).Named(name)
	return &zapLogger{
		outputLevel: int32(outputLevel),
		logger:      logger,
		logDir:      filepath.Dir(options.RotateOutputPath),
	}, nil
}

//时间格式化函数
func formatDate(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
	t = t.Local()
	year, month, day := t.Date()
	hour, minute, second := t.Clock()
	micros := t.Nanosecond() / 1000

	buf := make([]byte, 27)

	buf[0] = byte((year/1000)%10) + '0'
	buf[1] = byte((year/100)%10) + '0'
	buf[2] = byte((year/10)%10) + '0'
	buf[3] = byte(year%10) + '0'
	buf[4] = '-'
	buf[5] = byte((month)/10) + '0'
	buf[6] = byte((month)%10) + '0'
	buf[7] = '-'
	buf[8] = byte((day)/10) + '0'
	buf[9] = byte((day)%10) + '0'
	buf[10] = ' '
	buf[11] = byte((hour)/10) + '0'
	buf[12] = byte((hour)%10) + '0'
	buf[13] = ':'
	buf[14] = byte((minute)/10) + '0'
	buf[15] = byte((minute)%10) + '0'
	buf[16] = ':'
	buf[17] = byte((second)/10) + '0'
	buf[18] = byte((second)%10) + '0'
	buf[19] = '.'
	buf[20] = byte((micros/100000)%10) + '0'
	buf[21] = byte((micros/10000)%10) + '0'
	buf[22] = byte((micros/1000)%10) + '0'
	buf[23] = byte((micros/100)%10) + '0'
	buf[24] = byte((micros/10)%10) + '0'
	buf[25] = byte((micros)%10) + '0'
	buf[26] = 'Z'

	enc.AppendString(string(buf))
}

//打印trace级别的日志
func (z *zapLogger) Tracef(format string, args ...interface{}) {
	z.printf(z.logger.Debug, plog.TraceLog, format, args...)
}

//打印debug级别的日志
func (z *zapLogger) Debugf(format string, args ...interface{}) {
	z.printf(z.logger.Debug, plog.DebugLog, format, args...)
}

//打印info级别的日志
func (z *zapLogger) Infof(format string, args ...interface{}) {
	z.printf(z.logger.Info, plog.InfoLog, format, args...)
}

//打印warn级别的日志
func (z *zapLogger) Warnf(format string, args ...interface{}) {
	z.printf(z.logger.Warn, plog.WarnLog, format, args...)
}

//打印error级别的日志
func (z *zapLogger) Errorf(format string, args ...interface{}) {
	z.printf(z.logger.Error, plog.ErrorLog, format, args...)
}

//打印fatalf级别的日志
func (z *zapLogger) Fatalf(format string, args ...interface{}) {
	z.printf(z.logger.Fatal, plog.FatalLog, format, args...)
}

//判断当前级别是否满足日志打印的最低级别
func (z *zapLogger) IsLevelEnabled(l int) bool {
	outputLevel := atomic.LoadInt32(&z.outputLevel)
	return int32(l) >= outputLevel
}

//动态设置日志级别
func (z *zapLogger) SetLogLevel(l int) error {
	if err := plog.VerifyLogLevel(l); nil != err {
		return model.NewSDKError(model.ErrCodeAPIInvalidConfig, err, "fail to verify log level")
	}
	atomic.StoreInt32(&z.outputLevel, int32(l))
	return nil
}

//返回日志的目录
func (z *zapLogger) GetLogDir() string {
	return z.logDir
}

//通用打印函数
func (z *zapLogger) printf(
	logFun func(msg string, fields ...zap.Field), level int, format string, args ...interface{}) {
	if !z.IsLevelEnabled(level) {
		return
	}
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	logFun(msg)
}

//初始化
func init() {
	plog.RegisterLoggerCreator(plog.LoggerZap, prepareZap)
}
