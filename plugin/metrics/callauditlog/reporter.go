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

// Package callauditlog 服务调用审计日志插件,通过 StatReporter 链逐条记录服务调用结果到独立审计文件。
// ReportStat 非阻塞入队、后台异步刷盘,channel 满时丢弃并告警,绝不阻塞业务调用或中断 reporter 链。
package callauditlog

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/natefinch/lumberjack"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	statreporter "github.com/polarismesh/polaris-go/pkg/plugin/metrics"
	"github.com/polarismesh/polaris-go/pkg/sdk"
)

// PluginName 插件名,对应 polaris.yaml 中 statReporter.chain 的配置项。
const PluginName = "callAuditLog"

var _ statreporter.StatReporter = (*CallAuditLogReporter)(nil)

// init 注册插件实现。
func init() {
	plugin.RegisterPlugin(&CallAuditLogReporter{})
}

// CallAuditLogReporter 服务调用审计日志插件,逐条记录服务调用结果到独立审计文件。
// 通过 statReporter.chain 启用;ReportStat 仅做构造与非阻塞入队,后台 goroutine 异步写盘,
// channel 满时丢弃并计数告警,绝不阻塞业务调用或中断 reporter 链。
type CallAuditLogReporter struct {
	*plugin.PluginBase
	*common.RunContext
	// cfg 插件配置
	cfg *Config
	// globalCtx 全局上下文,用于获取当前时间
	globalCtx sdk.ValueContext
	// logCtx 上下文日志,用于打印插件运行日志(丢弃告警等),不写审计文件
	logCtx *log.ContextLogger
	// clientIP 主调方 IP 兜底值(GetBindIP)
	clientIP string
	// clientID 主调方 SDK 客户端唯一标识(GetClientId),写入审计条目 caller_id
	clientID string

	// sink 审计日志轮转 sink
	sink *lumberjack.Logger
	// formatFn 审计行格式化函数(formatJSON 或 formatKV)
	formatFn func(*auditEntry) []byte

	// queue 异步缓冲 channel,ReportStat 非阻塞入队
	queue chan *auditEntry
	// wg 等待后台刷盘 goroutine 退出
	wg sync.WaitGroup

	// droppedCount 累计丢弃条目数(atomic 写,后台 goroutine 读)
	droppedCount uint64
	// lastDropLog 上次告警时的累计丢弃数,仅后台 goroutine 读写
	lastDropLog uint64
}

// Type 返回插件类型。
func (r *CallAuditLogReporter) Type() common.Type {
	return common.TypeStatReporter
}

// Name 返回插件名。
func (r *CallAuditLogReporter) Name() string {
	return PluginName
}

// IsEnable 判断插件是否启用。
// 仅当 global.statReporter 启用且其 chain 中显式包含本插件时返回 true;
// 否则返回 false,使插件不被 Init——从而不创建后台刷盘 goroutine、不注册全局审计 logger,
// 避免对未启用审计的用户产生任何副作用(区别于 PluginBase 默认恒 true 的行为)。
// cfg 为 SDK 全局配置,由插件管理器在 Init 前传入,不允许为 nil。
func (r *CallAuditLogReporter) IsEnable(cfg config.Configuration) bool {
	statReporter := cfg.GetGlobal().GetStatReporter()
	if !statReporter.IsEnable() {
		return false
	}
	for _, name := range statReporter.GetChain() {
		if name == PluginName {
			return true
		}
	}
	return false
}

// Init 初始化插件:读取配置、创建审计 logger、启动后台刷盘 goroutine。
func (r *CallAuditLogReporter) Init(ctx *plugin.InitContext) error {
	r.RunContext = common.NewRunContext()
	r.globalCtx = ctx.ValueCtx
	r.PluginBase = plugin.NewPluginBase(ctx)
	r.logCtx = ctx.ValueCtx.GetContextLogger()
	r.clientIP = ctx.Config.GetGlobal().GetAPI().GetBindIP()
	r.clientID = ctx.ValueCtx.GetClientId()
	cfgValue := ctx.Config.GetGlobal().GetStatReporter().GetPluginConfig(PluginName)
	if cfg, ok := cfgValue.(*Config); ok {
		r.cfg = cfg
	} else {
		r.cfg = &Config{}
	}
	r.cfg.SetDefault()
	if err := r.cfg.Verify(); err != nil {
		return err
	}
	if r.cfg.Format == auditFormatKV {
		r.formatFn = formatKV
	} else {
		r.formatFn = formatJSON
	}
	r.queue = make(chan *auditEntry, r.cfg.BufferSize)
	if err := r.initAuditLogger(); err != nil {
		return err
	}
	r.wg.Add(1)
	go r.flushLoop()
	log.GetBaseLogger().Infof("[callAuditLog] init, path=%s format=%s bufferSize=%d flushInterval=%v",
		r.cfg.RotateOutputPath, r.cfg.Format, r.cfg.BufferSize, r.cfg.FlushInterval)
	return nil
}

// ReportStat 上报统计信息。审计插件仅处理 ServiceStat(外部业务调用),
// 逐条构造审计条目后非阻塞入队。永不返回 error(内部 recover),
// 绝不中断 reporter 链或阻塞业务调用;channel 满时丢弃并计数。
func (r *CallAuditLogReporter) ReportStat(metricsType model.MetricType, metricsVal model.InstanceGauge) error {
	defer func() {
		if err := recover(); err != nil {
			r.logCtx.GetStatReportLogger().Errorf("[callAuditLog] ReportStat panic: %v", err)
		}
	}()
	if metricsType != model.ServiceStat {
		return nil
	}
	val, ok := metricsVal.(*model.ServiceCallResult)
	if !ok || val == nil {
		return nil
	}
	entry := buildAuditEntry(val, r.clientIP, r.clientID, r.globalCtx.Now())
	select {
	case r.queue <- entry:
	default:
		atomic.AddUint64(&r.droppedCount, 1)
	}
	return nil
}

// Info 返回插件元信息,审计插件无监听端口。
func (r *CallAuditLogReporter) Info() model.StatInfo {
	return model.StatInfo{Target: PluginName}
}

// flushLoop 后台消费 queue 写审计日志,并周期性收敛丢弃告警。
// 退出条件:queue 被排空后 channel 不可读,或 RunContext 触发 Done 进入 drain。
func (r *CallAuditLogReporter) flushLoop() {
	defer r.wg.Done()
	ticker := time.NewTicker(r.cfg.FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case entry, ok := <-r.queue:
			if !ok {
				return
			}
			r.writeOne(entry)
		case <-ticker.C:
			r.maybeLogDrop()
		case <-r.Done():
			r.drain()
			return
		}
	}
}

// drain 排空 queue 中剩余条目,用于优雅退出。queue 不主动 close,避免迟到的入队触发 panic。
// 退出前收敛一次丢弃告警,确保最后一个 tick 之后新增的丢弃计数不会被漏记。
func (r *CallAuditLogReporter) drain() {
	for {
		select {
		case entry, ok := <-r.queue:
			if !ok {
				r.maybeLogDrop()
				return
			}
			r.writeOne(entry)
		default:
			r.maybeLogDrop()
			return
		}
	}
}

// writeOne 格式化并写入一条审计日志,写盘错误收敛告警不中断消费。
func (r *CallAuditLogReporter) writeOne(entry *auditEntry) {
	defer func() {
		if err := recover(); err != nil {
			r.logCtx.GetStatReportLogger().Errorf("[callAuditLog] write audit log panic: %v", err)
		}
	}()
	line := r.formatFn(entry)
	if len(line) == 0 {
		return
	}
	if _, err := r.sink.Write(line); err != nil {
		r.logCtx.GetStatReportLogger().Warnf("[callAuditLog] write audit log fail: %v", err)
	}
}

// maybeLogDrop 收敛丢弃告警:累计丢弃数较上次有新增时打印一次,每个 FlushInterval 至多一次,避免日志风暴。
func (r *CallAuditLogReporter) maybeLogDrop() {
	total := atomic.LoadUint64(&r.droppedCount)
	if total <= r.lastDropLog {
		return
	}
	delta := total - r.lastDropLog
	r.lastDropLog = total
	r.logCtx.GetStatReportLogger().Warnf("[callAuditLog] dropped %d audit entries (total dropped: %d)", delta, total)
}

// Destroy 销毁插件:触发后台 goroutine 排空退出,关闭审计日志文件。
// 不 close queue,Destroy 期间迟到的入队会被 GC 回收,避免 send-on-closed-channel panic。
func (r *CallAuditLogReporter) Destroy() error {
	if r.RunContext != nil {
		_ = r.RunContext.Destroy()
	}
	r.wg.Wait()
	if r.sink != nil {
		_ = r.sink.Close()
	}
	if r.PluginBase != nil {
		_ = r.PluginBase.Destroy()
	}
	return nil
}

// initAuditLogger 创建 lumberjack 轮转 sink,并将 rawAuditLogger 注册到 pkg/log 的 AuditLogger 槽位,
// 纳入统一 logger 管理。rawAuditLogger 直接写 sink,输出纯审计行(无 zap 前缀/嵌套)。
func (r *CallAuditLogReporter) initAuditLogger() error {
	compress := false
	if r.cfg.Compress != nil {
		compress = *r.cfg.Compress
	}
	r.sink = &lumberjack.Logger{
		Filename:   r.cfg.RotateOutputPath,
		MaxSize:    r.cfg.RotationMaxSize,
		MaxBackups: r.cfg.RotationMaxBackups,
		MaxAge:     r.cfg.RotationMaxAge,
		Compress:   compress,
		LocalTime:  true,
	}
	log.SetAuditLogger(&rawAuditLogger{sink: r.sink})
	return nil
}

// rawAuditLogger 实现 log.Logger 接口,Infof 直接写 lumberjack,输出纯审计行。
// 注册到 pkg/log 的 AuditLogger 槽位供 GetAuditLogger/ContextLogger.GetAuditLogger 使用;
// 审计插件自身的写盘路径(writeOne)直接操作 sink,此实现满足接口契约即可。
type rawAuditLogger struct {
	sink *lumberjack.Logger
}

// write 将一行字节写入 sink,写盘错误静默忽略(审计行不可恢复,避免影响调用方)。
func (l *rawAuditLogger) write(line []byte) {
	if len(line) > 0 {
		_, _ = l.sink.Write(line)
	}
}

// Tracef 实现 log.Logger。
func (l *rawAuditLogger) Tracef(format string, args ...interface{}) {
	l.write([]byte(fmt.Sprintf(format, args...)))
}

// Debugf 实现 log.Logger。
func (l *rawAuditLogger) Debugf(format string, args ...interface{}) {
	l.write([]byte(fmt.Sprintf(format, args...)))
}

// Infof 实现 log.Logger。
func (l *rawAuditLogger) Infof(format string, args ...interface{}) {
	l.write([]byte(fmt.Sprintf(format, args...)))
}

// Warnf 实现 log.Logger。
func (l *rawAuditLogger) Warnf(format string, args ...interface{}) {
	l.write([]byte(fmt.Sprintf(format, args...)))
}

// Errorf 实现 log.Logger。
func (l *rawAuditLogger) Errorf(format string, args ...interface{}) {
	l.write([]byte(fmt.Sprintf(format, args...)))
}

// Fatalf 实现 log.Logger。
func (l *rawAuditLogger) Fatalf(format string, args ...interface{}) {
	l.write([]byte(fmt.Sprintf(format, args...)))
}

// IsLevelEnabled 始终返回 true,审计行全量输出。
func (l *rawAuditLogger) IsLevelEnabled(int) bool { return true }

// SetLogLevel 空操作,审计日志不分级。
func (l *rawAuditLogger) SetLogLevel(int) error { return nil }
