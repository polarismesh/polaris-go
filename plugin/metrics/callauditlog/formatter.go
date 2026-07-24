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
	"fmt"
	"strings"
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// auditEntry 单条服务调用审计记录,对应审计日志的一行。
type auditEntry struct {
	// Timestamp 调用发生时间戳(RFC3339Nano)
	Timestamp string `json:"timestamp"`
	// CallerService 主调服务名
	CallerService string `json:"caller_service"`
	// CallerNamespace 主调服务命名空间
	CallerNamespace string `json:"caller_namespace"`
	// CallerIP 主调方 IP
	CallerIP string `json:"caller_ip"`
	// CallerID 主调方 SDK 客户端唯一标识(GetClientId,标识发起调用的 SDK 客户端)
	CallerID string `json:"caller_id"`
	// CalleeNamespace 被调服务命名空间
	CalleeNamespace string `json:"callee_namespace"`
	// CalleeService 被调服务名
	CalleeService string `json:"callee_service"`
	// CalleeHost 被调实例地址(host:port)
	CalleeHost string `json:"callee_host"`
	// CalleeID 被调实例 ID
	CalleeID string `json:"callee_id"`
	// Method 调用接口方法
	Method string `json:"method"`
	// DelayMs 调用耗时(毫秒)
	DelayMs int64 `json:"delay_ms"`
	// RetCode 业务返回码
	RetCode int32 `json:"ret_code"`
	// RetStatus 调用状态(success/fail/timeout 等)
	RetStatus string `json:"ret_status"`
	// RuleName 生效规则名,可选
	RuleName string `json:"rule_name,omitempty"`
}

// buildAuditEntry 从服务调用结果构造审计条目。
// val      服务调用结果,字段缺失时按降级策略填充。
// clientIP 主调方 IP 兜底值(GetBindIP),当 val.CalledIP 为空时使用。
// clientID 主调方 SDK 客户端唯一标识(GetClientId),写入审计条目 caller_id。
// now      上报时刻,当 val.Timestamp 未设置时作为调用时间兜底。
// 返回填充完成的审计条目,不会因 nil 指针 panic。
func buildAuditEntry(val *model.ServiceCallResult, clientIP, clientID string, now time.Time) *auditEntry {
	entry := &auditEntry{
		CallerService:   val.GetCallerService(),
		CallerNamespace: val.GetCallerNamespace(),
		CallerID:        clientID,
		Method:          val.GetMethod(),
		RetCode:         val.GetRetCodeValue(),
		RetStatus:       string(val.GetRetStatus()),
		RuleName:        val.RuleName,
	}
	// 主调 IP:用户显式 CalledIP 优先,否则使用兜底 clientIP
	if val.CalledIP != "" {
		entry.CallerIP = val.CalledIP
	} else {
		entry.CallerIP = clientIP
	}
	// 调用时间戳:用户显式 Timestamp 优先,否则使用上报时刻
	ts := val.GetTimestamp()
	if ts.IsZero() {
		ts = now
	}
	entry.Timestamp = ts.Format(time.RFC3339Nano)
	// 被调实例信息
	if inst := val.GetCalledInstance(); inst != nil {
		entry.CalleeNamespace = inst.GetNamespace()
		entry.CalleeService = inst.GetService()
		entry.CalleeHost = fmt.Sprintf("%s:%d", inst.GetHost(), inst.GetPort())
		entry.CalleeID = inst.GetId()
	}
	// 调用耗时(ms)
	if d := val.GetDelay(); d != nil {
		entry.DelayMs = d.Milliseconds()
	}
	return entry
}

// formatJSON 将审计条目序列化为 JSON 行(以换行结尾)。
func formatJSON(e *auditEntry) []byte {
	b, err := json.Marshal(e)
	if err != nil {
		return nil
	}
	return append(b, '\n')
}

// formatKV 将审计条目序列化为 key=value 空格分隔的行(以换行结尾)。
// 字符串值统一用 %q 加引号并转义(含空格、换行、引号等),
// 避免 method / service / rule_name 等字段含分隔符或换行时破坏「一行一条」的审计前提与下游按行/按空格解析。
func formatKV(e *auditEntry) []byte {
	var buf strings.Builder
	fmt.Fprintf(&buf, "timestamp=%q", e.Timestamp)
	fmt.Fprintf(&buf, " caller_namespace=%q", e.CallerNamespace)
	fmt.Fprintf(&buf, " caller_service=%q", e.CallerService)
	fmt.Fprintf(&buf, " caller_ip=%q", e.CallerIP)
	fmt.Fprintf(&buf, " caller_id=%q", e.CallerID)
	fmt.Fprintf(&buf, " callee_namespace=%q", e.CalleeNamespace)
	fmt.Fprintf(&buf, " callee_service=%q", e.CalleeService)
	fmt.Fprintf(&buf, " callee_host=%q", e.CalleeHost)
	fmt.Fprintf(&buf, " callee_id=%q", e.CalleeID)
	fmt.Fprintf(&buf, " method=%q", e.Method)
	fmt.Fprintf(&buf, " delay_ms=%d", e.DelayMs)
	fmt.Fprintf(&buf, " ret_code=%d", e.RetCode)
	fmt.Fprintf(&buf, " ret_status=%q", e.RetStatus)
	if e.RuleName != "" {
		fmt.Fprintf(&buf, " rule_name=%q", e.RuleName)
	}
	buf.WriteByte('\n')
	return []byte(buf.String())
}
