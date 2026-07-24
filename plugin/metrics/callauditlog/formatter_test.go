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
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// stubInstance 实现 model.Instance 接口,仅覆盖审计需要的 getter;
// 嵌入 model.Instance(nil)使其余方法满足接口契约,测试不调用它们。
type stubInstance struct {
	model.Instance
	namespace string
	service   string
	host      string
	port      uint32
	id        string
}

// GetNamespace 返回命名空间。
func (s *stubInstance) GetNamespace() string { return s.namespace }

// GetService 返回服务名。
func (s *stubInstance) GetService() string { return s.service }

// GetHost 返回主机地址。
func (s *stubInstance) GetHost() string { return s.host }

// GetPort 返回端口。
func (s *stubInstance) GetPort() uint32 { return s.port }

// GetId 返回实例 ID。
func (s *stubInstance) GetId() string { return s.id }

// TestBuildAuditEntry_FullFields 测试场景:全字段填充时,审计条目正确映射各字段。
func TestBuildAuditEntry_FullFields(t *testing.T) {
	now := time.Date(2026, 7, 17, 15, 30, 0, 0, time.UTC)
	inst := &stubInstance{namespace: "Prod", service: "user-svc", host: "10.0.0.1", port: 8080, id: "inst-1"}
	r := &model.ServiceCallResult{}
	r.SetCalledInstance(inst)
	r.SetRetStatus(model.RetSuccess)
	r.SetRetCode(200)
	r.SetDelay(50 * time.Millisecond)
	r.SetMethod("/api/get")
	r.SetCallerService(&model.ServiceInfo{Namespace: "Prod", Service: "order-svc"})
	r.SetCalledIP("10.0.1.5")
	r.SetTimestamp(now)
	r.RuleName = "rule-1"

	e := buildAuditEntry(r, "127.0.0.1", "client-uid-1", now.Add(time.Second))
	if e.CallerService != "order-svc" || e.CallerNamespace != "Prod" {
		t.Errorf("caller mismatch: service=%s namespace=%s", e.CallerService, e.CallerNamespace)
	}
	if e.CallerIP != "10.0.1.5" {
		t.Errorf("callerIP should use CalledIP, got %s", e.CallerIP)
	}
	if e.CallerID != "client-uid-1" {
		t.Errorf("callerID should use clientID, got %s", e.CallerID)
	}
	if e.CalleeService != "user-svc" || e.CalleeNamespace != "Prod" {
		t.Errorf("callee mismatch: service=%s namespace=%s", e.CalleeService, e.CalleeNamespace)
	}
	if e.CalleeHost != "10.0.0.1:8080" {
		t.Errorf("calleeHost mismatch: %s", e.CalleeHost)
	}
	if e.CalleeID != "inst-1" {
		t.Errorf("calleeID mismatch: %s", e.CalleeID)
	}
	if e.Method != "/api/get" {
		t.Errorf("method mismatch: %s", e.Method)
	}
	if e.DelayMs != 50 {
		t.Errorf("delayMs mismatch: %d", e.DelayMs)
	}
	if e.RetCode != 200 {
		t.Errorf("retCode mismatch: %d", e.RetCode)
	}
	if e.RetStatus != "success" {
		t.Errorf("retStatus mismatch: %s", e.RetStatus)
	}
	if e.RuleName != "rule-1" {
		t.Errorf("ruleName mismatch: %s", e.RuleName)
	}
	if e.Timestamp != now.Format(time.RFC3339Nano) {
		t.Errorf("timestamp mismatch: %s", e.Timestamp)
	}
}

// TestBuildAuditEntry_Fallbacks 测试场景:CalledIP 与 Timestamp 未设置时,回退到 clientIP 与 now。
func TestBuildAuditEntry_Fallbacks(t *testing.T) {
	now := time.Date(2026, 7, 17, 15, 30, 0, 0, time.UTC)
	inst := &stubInstance{namespace: "Prod", service: "user-svc", host: "10.0.0.1", port: 8080}
	r := &model.ServiceCallResult{}
	r.SetCalledInstance(inst)
	r.SetRetStatus(model.RetFail)
	r.SetRetCode(500)
	r.SetDelay(10 * time.Millisecond)

	e := buildAuditEntry(r, "192.168.1.1", "client-uid-2", now)
	if e.CallerIP != "192.168.1.1" {
		t.Errorf("callerIP should fallback to clientIP, got %s", e.CallerIP)
	}
	if e.CallerID != "client-uid-2" {
		t.Errorf("callerID should use clientID, got %s", e.CallerID)
	}
	if e.Timestamp != now.Format(time.RFC3339Nano) {
		t.Errorf("timestamp should fallback to now, got %s", e.Timestamp)
	}
	if e.RetStatus != "fail" {
		t.Errorf("retStatus mismatch: %s", e.RetStatus)
	}
}

// TestBuildAuditEntry_NilPointers 测试场景:CalledInstance/Delay/RetCode 为 nil 时不 panic。
func TestBuildAuditEntry_NilPointers(t *testing.T) {
	now := time.Now()
	r := &model.ServiceCallResult{}
	e := buildAuditEntry(r, "127.0.0.1", "client-uid-3", now)
	if e.CalleeHost != "" {
		t.Errorf("calleeHost should be empty when CalledInstance nil, got %s", e.CalleeHost)
	}
	if e.DelayMs != 0 {
		t.Errorf("delayMs should be 0 when Delay nil, got %d", e.DelayMs)
	}
	if e.RetCode != 0 {
		t.Errorf("retCode should be 0 when RetCode nil, got %d", e.RetCode)
	}
}

// TestFormatJSON_Valid 测试场景:JSON 输出合法且以换行结尾。
func TestFormatJSON_Valid(t *testing.T) {
	e := &auditEntry{Timestamp: "ts", CallerService: "svc", CallerID: "uid-1", RetStatus: "success"}
	b := formatJSON(e)
	if len(b) == 0 || b[len(b)-1] != '\n' {
		t.Fatalf("formatJSON should end with newline: %v", b)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b[:len(b)-1], &m); err != nil {
		t.Fatalf("formatJSON output not valid JSON: %v", err)
	}
	if m["caller_service"] != "svc" {
		t.Errorf("caller_service mismatch: %v", m["caller_service"])
	}
	if m["caller_id"] != "uid-1" {
		t.Errorf("caller_id mismatch: %v", m["caller_id"])
	}
}

// TestFormatKV_Contains 测试场景:KV 输出含各字段键值对(字符串值带引号)且以换行结尾。
func TestFormatKV_Contains(t *testing.T) {
	e := &auditEntry{Timestamp: "ts", CallerService: "svc", CallerID: "uid-1", DelayMs: 50, RuleName: "r1"}
	b := formatKV(e)
	s := string(b)
	if !strings.HasSuffix(s, "\n") {
		t.Fatalf("formatKV should end with newline")
	}
	if !strings.Contains(s, `caller_service="svc"`) {
		t.Errorf("formatKV missing caller_service: %s", s)
	}
	if !strings.Contains(s, `caller_id="uid-1"`) {
		t.Errorf("formatKV missing caller_id: %s", s)
	}
	if !strings.Contains(s, "delay_ms=50") {
		t.Errorf("formatKV missing delay_ms: %s", s)
	}
	if !strings.Contains(s, `rule_name="r1"`) {
		t.Errorf("formatKV missing rule_name: %s", s)
	}
}

// TestFormatKV_EscapesSpaceAndNewline 测试场景:字段含空格/换行/引号时,%q 转义确保仍为单行且可被反向解析。
func TestFormatKV_EscapesSpaceAndNewline(t *testing.T) {
	e := &auditEntry{
		Timestamp: "ts",
		Method:    "GET /a b\nc",
		RuleName:  `rule "x"`,
	}
	b := formatKV(e)
	s := string(b)
	// 整条审计必须仍是单行:除结尾换行外不得含裸换行,否则破坏按行解析
	if strings.Count(s, "\n") != 1 {
		t.Fatalf("formatKV output must be single line, got %d newlines: %q", strings.Count(s, "\n"), s)
	}
	// method 值中的空格/换行被转义为字面量,可用 strconv.Unquote 还原
	if !strings.Contains(s, `method="GET /a b\nc"`) {
		t.Errorf("method not properly escaped: %s", s)
	}
	// rule_name 中的引号被转义
	if !strings.Contains(s, `rule_name="rule \"x\""`) {
		t.Errorf("rule_name not properly escaped: %s", s)
	}
}
