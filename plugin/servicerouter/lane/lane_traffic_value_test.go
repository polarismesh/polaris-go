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
// 用于模拟 api.go/api.consumer.go convert() 把 Arguments 展开到 SourceService.Metadata
// 之后, lane router 的 fallback 路径仍能取到值。
type stubSourceService struct {
	ns       string
	svc      string
	metadata map[string]string
}

func (s *stubSourceService) GetNamespace() string          { return s.ns }
func (s *stubSourceService) GetService() string            { return s.svc }
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
// HEADER / QUERY / COOKIE / METHOD / CALLER_IP / PATH 的 envVars 命中路径。
// 每个 case 只写一个维度的 key, 断言只有对应类型能读出值,
// 借此确认不同维度之间不会互相读串。
func TestFindTrafficValue_SixDimensions(t *testing.T) {
	type caseDef struct {
		name     string
		envVars  map[string]string
		arg      *apitraffic.SourceMatch
		expect   string
	}
	cases := []caseDef{
		{
			name:    "HEADER hits $header.user",
			envVars: map[string]string{"$header.user": "gray"},
			arg:     buildSourceMatch(apitraffic.SourceMatch_HEADER, "user", "gray"),
			expect:  "gray",
		},
		{
			name:    "QUERY hits $query.scene",
			envVars: map[string]string{"$query.scene": "canary"},
			arg:     buildSourceMatch(apitraffic.SourceMatch_QUERY, "scene", "canary"),
			expect:  "canary",
		},
		{
			name:    "COOKIE hits $cookie.sid",
			envVars: map[string]string{"$cookie.sid": "abc"},
			arg:     buildSourceMatch(apitraffic.SourceMatch_COOKIE, "sid", "abc"),
			expect:  "abc",
		},
		{
			name:    "METHOD hits $method",
			envVars: map[string]string{"$method": "POST"},
			arg:     buildSourceMatch(apitraffic.SourceMatch_METHOD, "", "POST"),
			expect:  "POST",
		},
		{
			name:    "CALLER_IP hits $caller_ip",
			envVars: map[string]string{"$caller_ip": "10.0.0.1"},
			arg:     buildSourceMatch(apitraffic.SourceMatch_CALLER_IP, "", "10.0.0.1"),
			expect:  "10.0.0.1",
		},
		{
			name:    "PATH hits $Path",
			envVars: map[string]string{"$Path": "/api/v1/echo"},
			arg:     buildSourceMatch(apitraffic.SourceMatch_PATH, "", "/api/v1/echo"),
			expect:  "/api/v1/echo",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := findTrafficValue(c.arg, c.envVars, nil)
			if got != c.expect {
				t.Fatalf("findTrafficValue got %q, want %q", got, c.expect)
			}
		})
	}
}

// TestFindTrafficValue_DimensionIsolation 锁死一个回归契约:
// envVars 里只有 $header.user, 用 QUERY 查 user 必须返回 "" (而不是读到 header 的值)。
// 这是修复前 bug 的核心反向保护——旧实现 envVars[arg.GetKey()] 会把 header 和 query
// 的同名短 key 读串, 现在按前缀命名空间后不可能再发生。
func TestFindTrafficValue_DimensionIsolation(t *testing.T) {
	envVars := map[string]string{
		"$header.user": "gray",
	}
	queryArg := buildSourceMatch(apitraffic.SourceMatch_QUERY, "user", "gray")
	if got := findTrafficValue(queryArg, envVars, nil); got != "" {
		t.Fatalf("expected QUERY.user to miss (dimension isolation), got %q", got)
	}
	cookieArg := buildSourceMatch(apitraffic.SourceMatch_COOKIE, "user", "gray")
	if got := findTrafficValue(cookieArg, envVars, nil); got != "" {
		t.Fatalf("expected COOKIE.user to miss (dimension isolation), got %q", got)
	}
}

// TestFindTrafficValue_FallbackToSourceMetadata 验证:
// 当 envVars 里没有对应前缀 key, 但 SourceService.Metadata 里有时, 应 fallback 读出。
// 这一路径对齐 api.go / api/consumer.go convert() 把 Arguments 写回 SourceService.Metadata
// 之后的 ProcessRouters / GetOneInstance 链路。
func TestFindTrafficValue_FallbackToSourceMetadata(t *testing.T) {
	src := &stubSourceService{
		ns:  "default",
		svc: "LaneEchoClient",
		metadata: map[string]string{
			"$header.user": "gray",
			"$method":      "POST",
			"$caller_ip":   "10.0.0.1",
		},
	}
	headerArg := buildSourceMatch(apitraffic.SourceMatch_HEADER, "user", "gray")
	if got := findTrafficValue(headerArg, nil, src); got != "gray" {
		t.Errorf("HEADER fallback got %q, want gray", got)
	}
	methodArg := buildSourceMatch(apitraffic.SourceMatch_METHOD, "", "POST")
	if got := findTrafficValue(methodArg, nil, src); got != "POST" {
		t.Errorf("METHOD fallback got %q, want POST", got)
	}
	ipArg := buildSourceMatch(apitraffic.SourceMatch_CALLER_IP, "", "10.0.0.1")
	if got := findTrafficValue(ipArg, nil, src); got != "10.0.0.1" {
		t.Errorf("CALLER_IP fallback got %q, want 10.0.0.1", got)
	}
}

// TestFindTrafficValue_EnvVarsWinsOverSourceMetadata:
// envVars 和 sourceService 同时存在时, envVars 优先 (与 Java LaneUtils 的
// "trafficLabels 优先于 MessageMetadataContainer" 语义对齐)。
func TestFindTrafficValue_EnvVarsWinsOverSourceMetadata(t *testing.T) {
	envVars := map[string]string{"$header.user": "fromEnv"}
	src := &stubSourceService{
		metadata: map[string]string{"$header.user": "fromSrc"},
	}
	arg := buildSourceMatch(apitraffic.SourceMatch_HEADER, "user", "fromEnv")
	if got := findTrafficValue(arg, envVars, src); got != "fromEnv" {
		t.Fatalf("envVars should win over sourceService metadata, got %q", got)
	}
}

// TestFindTrafficValue_Custom 验证 CUSTOM 维度按原 key 查 (不套前缀),
// 与 Argument.ToLabels 的 Custom 分支保持一致。
func TestFindTrafficValue_Custom(t *testing.T) {
	envVars := map[string]string{"uid": "42"}
	arg := buildSourceMatch(apitraffic.SourceMatch_CUSTOM, "uid", "42")
	if got := findTrafficValue(arg, envVars, nil); got != "42" {
		t.Fatalf("CUSTOM got %q, want 42", got)
	}
}

// TestFindTrafficValue_CallerMetadata 验证 CALLER_METADATA 不走 envVars,
// 直接读 SourceService.Metadata 的原始 key (不套前缀)。
func TestFindTrafficValue_CallerMetadata(t *testing.T) {
	src := &stubSourceService{metadata: map[string]string{"env": "prod"}}
	arg := buildSourceMatch(apitraffic.SourceMatch_CALLER_METADATA, "env", "prod")
	if got := findTrafficValue(arg, nil, src); got != "prod" {
		t.Fatalf("CALLER_METADATA got %q, want prod", got)
	}
	// 无 sourceService 时返回 ""
	if got := findTrafficValue(arg, nil, nil); got != "" {
		t.Fatalf("CALLER_METADATA with nil source should return empty, got %q", got)
	}
}

// TestMatchTrafficRule_CrossDimensionAND 用一条 AND 规则同时要求
// HEADER user=gray 与 METHOD=POST, 验证 matchTrafficRule 能端到端地
// 正确贯通 findTrafficValue 的 6 维命名空间。
// 这是 pkg/flow/data/object.go 的 ToLabels 合并 + lane router findTrafficValue
// 前缀查询的组合验证。
func TestMatchTrafficRule_CrossDimensionAND(t *testing.T) {
	rule := &apitraffic.TrafficMatchRule{
		MatchMode: apitraffic.TrafficMatchRule_AND,
		Arguments: []*apitraffic.SourceMatch{
			buildSourceMatch(apitraffic.SourceMatch_HEADER, "user", "gray"),
			buildSourceMatch(apitraffic.SourceMatch_METHOD, "", "POST"),
		},
	}
	envVarsOK := map[string]string{
		"$header.user": "gray",
		"$method":      "POST",
	}
	if !matchTrafficRule(rule, envVarsOK, nil) {
		t.Fatalf("AND rule with both dimensions satisfied should match")
	}
	envVarsMissMethod := map[string]string{
		"$header.user": "gray",
		"$method":      "GET",
	}
	if matchTrafficRule(rule, envVarsMissMethod, nil) {
		t.Fatalf("AND rule must fail when method doesn't match")
	}
}
