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

package lane

import (
	"testing"

	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
)

// noopLogger 最小 log.Logger, 避开 matchTrafficRule 里 log.GetRouteLogger() 返回 nil
// 导致的 IsLevelEnabled panic。测试里不需要真的打日志。
type noopLogger struct{}

func (noopLogger) Tracef(format string, args ...interface{}) {}
func (noopLogger) Debugf(format string, args ...interface{}) {}
func (noopLogger) Infof(format string, args ...interface{})  {}
func (noopLogger) Warnf(format string, args ...interface{})  {}
func (noopLogger) Errorf(format string, args ...interface{}) {}
func (noopLogger) Fatalf(format string, args ...interface{}) {}
func (noopLogger) IsLevelEnabled(l int) bool                 { return false }
func (noopLogger) SetLogLevel(l int) error                   { return nil }

func init() {
	var lg log.Logger = noopLogger{}
	log.SetBaseLogger(lg)
	log.SetRouteLogger(lg)
}

// stubSourceService 是 model.ServiceMetadata 的最小实现, 仅返回构造时传入的 metadata.
// 模拟 api.go / api/consumer.go 的 convert() 已经把 Arguments 摊平到
// SourceService.Metadata 的带前缀 label key 之后, lane router 从 Metadata 读取的行为。
type stubSourceService struct {
	ns       string
	svc      string
	metadata map[string]string
}

func (s *stubSourceService) GetNamespace() string           { return s.ns }
func (s *stubSourceService) GetService() string             { return s.svc }
func (s *stubSourceService) GetMetadata() map[string]string { return s.metadata }

// buildSourceMatch 构造一个按 EXACT 模式匹配的 SourceMatch。
// wrapperspb.String 的 nil 处理由 matchStringValue 自己兜住, 这里直接用非 nil value。
func buildSourceMatch(typ apitraffic.SourceMatch_Type, key, expect string) *apitraffic.SourceMatch {
	return &apitraffic.SourceMatch{
		Type: typ,
		Key:  key,
		Value: &apimodel.MatchString{
			Type:  apimodel.MatchString_EXACT,
			Value: wrapperspb.String(expect),
		},
	}
}

// TestFindTrafficValue_SixDimensions 6 类匹配维度全覆盖:
// HEADER / QUERY / COOKIE / METHOD / CALLER_IP / PATH 通过 SourceService.Metadata
// 的带前缀 label key 命中。
// 每个 case 只在 Metadata 里放一个维度的值, 断言只有对应类型能读出值,
// 借此确认不同维度之间不会互相读串 (HEADER.user 不会读到 QUERY.user)。
func TestFindTrafficValue_SixDimensions(t *testing.T) {
	type caseDef struct {
		name     string
		metadata map[string]string
		arg      *apitraffic.SourceMatch
		expect   string
	}
	cases := []caseDef{
		{
			name:     "HEADER hits $header.user",
			metadata: map[string]string{model.LabelKeyHeader + "user": "gray"},
			arg:      buildSourceMatch(apitraffic.SourceMatch_HEADER, "user", "gray"),
			expect:   "gray",
		},
		{
			name:     "QUERY hits $query.scene",
			metadata: map[string]string{model.LabelKeyQuery + "scene": "canary"},
			arg:      buildSourceMatch(apitraffic.SourceMatch_QUERY, "scene", "canary"),
			expect:   "canary",
		},
		{
			name:     "COOKIE hits $cookie.sid",
			metadata: map[string]string{model.LabelKeyCookie + "sid": "abc"},
			arg:      buildSourceMatch(apitraffic.SourceMatch_COOKIE, "sid", "abc"),
			expect:   "abc",
		},
		{
			name:     "METHOD hits $method",
			metadata: map[string]string{model.LabelKeyMethod: "POST"},
			arg:      buildSourceMatch(apitraffic.SourceMatch_METHOD, "", "POST"),
			expect:   "POST",
		},
		{
			name:     "CALLER_IP hits $caller_ip",
			metadata: map[string]string{model.LabelKeyCallerIP: "10.0.0.1"},
			arg:      buildSourceMatch(apitraffic.SourceMatch_CALLER_IP, "", "10.0.0.1"),
			expect:   "10.0.0.1",
		},
		{
			name:     "PATH hits $Path",
			metadata: map[string]string{model.LabelKeyPath: "/api/v1/echo"},
			arg:      buildSourceMatch(apitraffic.SourceMatch_PATH, "", "/api/v1/echo"),
			expect:   "/api/v1/echo",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := &stubSourceService{metadata: c.metadata}
			got := findTrafficValue(c.arg, src)
			if got != c.expect {
				t.Fatalf("findTrafficValue got %q, want %q", got, c.expect)
			}
		})
	}
}

// TestFindTrafficValue_DimensionIsolation 锁死一个回归契约:
// SourceService.Metadata 里只有 "$header.user"=gray, 用 QUERY 查 user 必须返回 "" (
// 不会读到 header 的值)。这是修复前 bug 的核心反向保护——带前缀的 label key 让 6
// 维天然隔离, 跨维度读串不可能再发生。
func TestFindTrafficValue_DimensionIsolation(t *testing.T) {
	src := &stubSourceService{
		metadata: map[string]string{model.LabelKeyHeader + "user": "gray"},
	}
	queryArg := buildSourceMatch(apitraffic.SourceMatch_QUERY, "user", "gray")
	if got := findTrafficValue(queryArg, src); got != "" {
		t.Fatalf("expected QUERY.user to miss (dimension isolation), got %q", got)
	}
	cookieArg := buildSourceMatch(apitraffic.SourceMatch_COOKIE, "user", "gray")
	if got := findTrafficValue(cookieArg, src); got != "" {
		t.Fatalf("expected COOKIE.user to miss (dimension isolation), got %q", got)
	}
}

// TestFindTrafficValue_Custom 验证 CUSTOM 维度按原始 key 直接在 SourceService.Metadata
// 中查找, 与 Argument.ToLabels 的 Custom 分支 (不套前缀) 保持一致。
func TestFindTrafficValue_Custom(t *testing.T) {
	src := &stubSourceService{metadata: map[string]string{"uid": "42"}}
	arg := buildSourceMatch(apitraffic.SourceMatch_CUSTOM, "uid", "42")
	if got := findTrafficValue(arg, src); got != "42" {
		t.Fatalf("CUSTOM got %q, want 42", got)
	}
}

// TestFindTrafficValue_CallerMetadata 验证 CALLER_METADATA 按原始 key 读
// SourceService.Metadata (与 Custom 一样不套前缀, 代表调用方的业务元数据)。
func TestFindTrafficValue_CallerMetadata(t *testing.T) {
	src := &stubSourceService{metadata: map[string]string{"env": "prod"}}
	arg := buildSourceMatch(apitraffic.SourceMatch_CALLER_METADATA, "env", "prod")
	if got := findTrafficValue(arg, src); got != "prod" {
		t.Fatalf("CALLER_METADATA got %q, want prod", got)
	}
	// 无 sourceService 时返回 ""
	if got := findTrafficValue(arg, nil); got != "" {
		t.Fatalf("CALLER_METADATA with nil source should return empty, got %q", got)
	}
}

// TestFindTrafficValue_NilSource 验证 SourceService 为 nil 时所有维度都返回 "",
// 不会 panic, 行为和 rulebase 的 getRuleMetaValueStr 一致。
func TestFindTrafficValue_NilSource(t *testing.T) {
	headerArg := buildSourceMatch(apitraffic.SourceMatch_HEADER, "user", "gray")
	if got := findTrafficValue(headerArg, nil); got != "" {
		t.Fatalf("HEADER with nil source should return empty, got %q", got)
	}
	methodArg := buildSourceMatch(apitraffic.SourceMatch_METHOD, "", "POST")
	if got := findTrafficValue(methodArg, nil); got != "" {
		t.Fatalf("METHOD with nil source should return empty, got %q", got)
	}
}

// TestMatchTrafficRule_CrossDimensionAND 用一条 AND 规则同时要求
// HEADER user=gray 与 METHOD=POST, 验证 matchTrafficRule 能端到端地
// 正确贯通 findTrafficValue 的 6 维命名空间 (带前缀的 label key)。
func TestMatchTrafficRule_CrossDimensionAND(t *testing.T) {
	rule := &apitraffic.TrafficMatchRule{
		MatchMode: apitraffic.TrafficMatchRule_AND,
		Arguments: []*apitraffic.SourceMatch{
			buildSourceMatch(apitraffic.SourceMatch_HEADER, "user", "gray"),
			buildSourceMatch(apitraffic.SourceMatch_METHOD, "", "POST"),
		},
	}
	okSrc := &stubSourceService{
		metadata: map[string]string{
			model.LabelKeyHeader + "user": "gray",
			model.LabelKeyMethod:          "POST",
		},
	}
	if !matchTrafficRule(rule, okSrc) {
		t.Fatalf("AND rule with both dimensions satisfied should match")
	}
	missSrc := &stubSourceService{
		metadata: map[string]string{
			model.LabelKeyHeader + "user": "gray",
			model.LabelKeyMethod:          "GET",
		},
	}
	if matchTrafficRule(rule, missSrc) {
		t.Fatalf("AND rule must fail when method doesn't match")
	}
}
