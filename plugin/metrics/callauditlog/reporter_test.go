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
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/natefinch/lumberjack"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/sdk"
)

// stubValueContext 嵌入 sdk.ValueContext 接口,仅覆盖 Now();其余方法测试不调用。
type stubValueContext struct {
	sdk.ValueContext
	nowVal time.Time
}

// Now 返回预设时间。
func (s *stubValueContext) Now() time.Time { return s.nowVal }

// spyLogger 实现 log.Logger,记录 Warnf/Errorf 调用次数,用于验证收敛告警。
type spyLogger struct {
	warnCount int32
	errCount  int32
}

// Tracef 实现 log.Logger。
func (s *spyLogger) Tracef(string, ...interface{}) {}

// Debugf 实现 log.Logger。
func (s *spyLogger) Debugf(string, ...interface{}) {}

// Infof 实现 log.Logger。
func (s *spyLogger) Infof(string, ...interface{}) {}

// Warnf 实现 log.Logger,累加告警计数。
func (s *spyLogger) Warnf(string, ...interface{}) { atomic.AddInt32(&s.warnCount, 1) }

// Errorf 实现 log.Logger,累加错误计数。
func (s *spyLogger) Errorf(string, ...interface{}) { atomic.AddInt32(&s.errCount, 1) }

// Fatalf 实现 log.Logger。
func (s *spyLogger) Fatalf(string, ...interface{}) {}

// IsLevelEnabled 实现 log.Logger。
func (s *spyLogger) IsLevelEnabled(int) bool { return true }

// SetLogLevel 实现 log.Logger。
func (s *spyLogger) SetLogLevel(int) error { return nil }

// buildReporter 构造测试用 reporter,手动填充字段并跳过 initAuditLogger。
// startFlush 为 true 时启动后台 flushLoop;sink 写入临时目录文件以便断言。
func buildReporter(t *testing.T, cfg *Config, spy *spyLogger, startFlush bool) *CallAuditLogReporter {
	t.Helper()
	cfg.SetDefault()
	// 将全局 logger 设为 spy,使 ContextLogger.GetStatReportLogger() 返回 spy
	log.SetStatReportLogger(spy)
	log.SetBaseLogger(spy)
	cl := &log.ContextLogger{}
	cl.Init()
	r := &CallAuditLogReporter{
		cfg:       cfg,
		globalCtx: &stubValueContext{nowVal: time.Now()},
		logCtx:    cl,
		clientIP:  "127.0.0.1",
		clientID:  "test-client-id",
		formatFn:  formatJSON,
		queue:     make(chan *auditEntry, cfg.BufferSize),
	}
	r.RunContext = common.NewRunContext()
	r.sink = &lumberjack.Logger{Filename: filepath.Join(t.TempDir(), "audit.log"), MaxSize: 100, LocalTime: true}
	if startFlush {
		r.wg.Add(1)
		go r.flushLoop()
	}
	return r
}

// makeServiceCallResult 构造最小合法的 ServiceCallResult。
func makeServiceCallResult() *model.ServiceCallResult {
	inst := &stubInstance{namespace: "Prod", service: "svc", host: "10.0.0.1", port: 8080, id: "i1"}
	r := &model.ServiceCallResult{}
	r.SetCalledInstance(inst)
	r.SetRetStatus(model.RetSuccess)
	r.SetRetCode(200)
	r.SetDelay(5 * time.Millisecond)
	return r
}

// TestReportStat_NonServiceStat 测试场景:非 ServiceStat 类型直接返回 nil 且不入队。
func TestReportStat_NonServiceStat(t *testing.T) {
	r := buildReporter(t, &Config{BufferSize: 10}, &spyLogger{}, false)
	defer r.Destroy()
	if err := r.ReportStat(model.SDKAPIStat, makeServiceCallResult()); err != nil {
		t.Errorf("NonServiceStat should return nil, got %v", err)
	}
	if atomic.LoadUint64(&r.droppedCount) != 0 || len(r.queue) != 0 {
		t.Errorf("NonServiceStat should not enqueue")
	}
}

// TestReportStat_NilOrWrongType 测试场景:nil 或类型断言失败返回 nil。
func TestReportStat_NilOrWrongType(t *testing.T) {
	r := buildReporter(t, &Config{BufferSize: 10}, &spyLogger{}, false)
	defer r.Destroy()
	if err := r.ReportStat(model.ServiceStat, nil); err != nil {
		t.Errorf("nil gauge should return nil, got %v", err)
	}
	// 传入非 ServiceCallResult 类型,类型断言失败
	if err := r.ReportStat(model.ServiceStat, &model.RateLimitGauge{}); err != nil {
		t.Errorf("wrong type should return nil, got %v", err)
	}
}

// TestReportStat_EnqueueAndDrain 测试场景:ServiceStat 入队,Destroy 排空后审计文件有内容。
func TestReportStat_EnqueueAndDrain(t *testing.T) {
	r := buildReporter(t, &Config{BufferSize: 10}, &spyLogger{}, true)
	if err := r.ReportStat(model.ServiceStat, makeServiceCallResult()); err != nil {
		t.Fatalf("ReportStat failed: %v", err)
	}
	r.Destroy()
	data, err := os.ReadFile(r.sink.Filename)
	if err != nil {
		t.Fatalf("read audit file fail: %v", err)
	}
	if len(data) == 0 {
		t.Errorf("audit file should have content after drain")
	}
}

// TestReportStat_DropWhenFull 测试场景:queue 满时丢弃并计数,且始终返回 nil。
func TestReportStat_DropWhenFull(t *testing.T) {
	// 不启动 flushLoop,queue 不被消费,保证第 2 次入队时 queue 已满
	r := buildReporter(t, &Config{BufferSize: 1}, &spyLogger{}, false)
	defer r.Destroy()
	if err := r.ReportStat(model.ServiceStat, makeServiceCallResult()); err != nil {
		t.Fatalf("first ReportStat failed: %v", err)
	}
	if err := r.ReportStat(model.ServiceStat, makeServiceCallResult()); err != nil {
		t.Errorf("full queue ReportStat should return nil, got %v", err)
	}
	if atomic.LoadUint64(&r.droppedCount) != 1 {
		t.Errorf("droppedCount should be 1, got %d", atomic.LoadUint64(&r.droppedCount))
	}
}

// panicInstance 覆盖 GetHost 使其 panic,用于验证 ReportStat 的 recover。
type panicInstance struct {
	stubInstance
}

// GetHost 触发 panic。
func (p *panicInstance) GetHost() string { panic("boom") }

// TestReportStat_RecoverPanic 测试场景:构造过程 panic 时 ReportStat recover 仍返回 nil。
func TestReportStat_RecoverPanic(t *testing.T) {
	spy := &spyLogger{}
	r := buildReporter(t, &Config{BufferSize: 10}, spy, false)
	defer r.Destroy()
	inst := &panicInstance{stubInstance: stubInstance{namespace: "Prod", service: "svc", host: "h", port: 80, id: "i"}}
	scr := &model.ServiceCallResult{}
	scr.SetCalledInstance(inst)
	scr.SetRetStatus(model.RetSuccess)
	scr.SetRetCode(200)
	scr.SetDelay(time.Millisecond)
	if err := r.ReportStat(model.ServiceStat, scr); err != nil {
		t.Errorf("panic should be recovered and return nil, got %v", err)
	}
	if atomic.LoadInt32(&spy.errCount) == 0 {
		t.Errorf("panic should be logged via Errorf")
	}
}

// TestIsEnable 测试场景:仅当 statReporter 启用且 chain 显式包含 callAuditLog 时才启用插件,
// 保证未启用审计的用户不会被 Init(不创建后台 goroutine / 不注册全局审计 logger)。
func TestIsEnable(t *testing.T) {
	tests := []struct {
		name   string
		enable bool
		chain  []string
		want   bool
	}{
		{name: "not_in_chain", enable: true, chain: []string{"prometheus"}, want: false},
		{name: "in_chain", enable: true, chain: []string{PluginName}, want: true},
		{name: "in_chain_with_others", enable: true, chain: []string{"prometheus", PluginName}, want: true},
		{name: "disabled_but_in_chain", enable: false, chain: []string{PluginName}, want: false},
		{name: "empty_chain", enable: true, chain: []string{}, want: false},
	}
	r := &CallAuditLogReporter{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.NewDefaultConfiguration([]string{"127.0.0.1:8091"})
			cfg.GetGlobal().GetStatReporter().SetEnable(tt.enable)
			cfg.GetGlobal().GetStatReporter().SetChain(tt.chain)
			if got := r.IsEnable(cfg); got != tt.want {
				t.Errorf("IsEnable(enable=%v, chain=%v) = %v, want %v", tt.enable, tt.chain, got, tt.want)
			}
		})
	}
}
func TestMaybeLogDrop_Converged(t *testing.T) {
	spy := &spyLogger{}
	r := buildReporter(t, &Config{BufferSize: 1}, spy, false)
	defer r.Destroy()
	// 首次:新增 5,告警一次
	atomic.AddUint64(&r.droppedCount, 5)
	r.maybeLogDrop()
	if atomic.LoadInt32(&spy.warnCount) != 1 {
		t.Errorf("warnCount should be 1, got %d", atomic.LoadInt32(&spy.warnCount))
	}
	// 无新增,不再告警
	r.maybeLogDrop()
	if atomic.LoadInt32(&spy.warnCount) != 1 {
		t.Errorf("warnCount should remain 1, got %d", atomic.LoadInt32(&spy.warnCount))
	}
	// 新增 3,再告警一次
	atomic.AddUint64(&r.droppedCount, 3)
	r.maybeLogDrop()
	if atomic.LoadInt32(&spy.warnCount) != 2 {
		t.Errorf("warnCount should be 2, got %d", atomic.LoadInt32(&spy.warnCount))
	}
}
