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
	"regexp"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"
	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/plugin/servicerouter"
)

// fakeServiceRule 最小 model.ServiceRule 实现，只为承载一个 LaneWrapper 给测试使用
type fakeServiceRule struct {
	value interface{}
}

func (f *fakeServiceRule) GetType() model.EventType      { return model.EventLane }
func (f *fakeServiceRule) IsInitialized() bool           { return true }
func (f *fakeServiceRule) GetRevision() string           { return "" }
func (f *fakeServiceRule) GetHashValue() uint64          { return 0 }
func (f *fakeServiceRule) IsNotExists() bool             { return false }
func (f *fakeServiceRule) GetNamespace() string          { return "" }
func (f *fakeServiceRule) GetService() string            { return "" }
func (f *fakeServiceRule) GetValue() interface{}         { return f.value }
func (f *fakeServiceRule) GetRuleCache() model.RuleCache { return nil }
func (f *fakeServiceRule) GetValidateError() error       { return nil }
func (f *fakeServiceRule) IsCacheLoaded() bool           { return false }
func (f *fakeServiceRule) GetRegexMatcher(string) (*regexp.Regexp, error) {
	return nil, nil
}
func (f *fakeServiceRule) GetMessageCache(proto.Message) interface{}  { return nil }
func (f *fakeServiceRule) SetMessageCache(proto.Message, interface{}) {}

// wrapGroups 构造一个承载指定 LaneGroups 的 ServiceRule
func wrapGroups(groups ...*apitraffic.LaneGroup) model.ServiceRule {
	return &fakeServiceRule{value: &pb.LaneWrapper{LaneGroups: groups}}
}

// makeGroup 便捷构造一个只带 name 的 LaneGroup
func makeGroup(name string) *apitraffic.LaneGroup {
	return &apitraffic.LaneGroup{Name: name}
}

// TestLaneRouter_GetLaneGroups 覆盖 getLaneGroups 4 个合并去重场景
func TestLaneRouter_GetLaneGroups(t *testing.T) {
	groupSrcOnly := makeGroup("src-only")
	groupDstOnly := makeGroup("dst-only")
	groupSharedFromSrc := makeGroup("shared") // caller 侧的 "shared"
	groupSharedFromDst := makeGroup("shared") // callee 侧的 "shared"（同名，应被去重）

	tests := []struct {
		name      string
		srcRule   model.ServiceRule
		dstRule   model.ServiceRule
		wantNames []string
		wantNil   bool
		wantOrder []*apitraffic.LaneGroup // 用指针同一判断去重是否保留了 caller 侧那份
	}{
		{
			// 测试场景：只有 callee 侧有规则，返回 dst 的 groups
			name:      "only_dst_has_rule",
			srcRule:   nil,
			dstRule:   wrapGroups(groupDstOnly),
			wantNames: []string{"dst-only"},
			wantOrder: []*apitraffic.LaneGroup{groupDstOnly},
		},
		{
			// 测试场景：只有 caller 侧有规则，返回 src 的 groups
			name:      "only_src_has_rule",
			srcRule:   wrapGroups(groupSrcOnly),
			dstRule:   nil,
			wantNames: []string{"src-only"},
			wantOrder: []*apitraffic.LaneGroup{groupSrcOnly},
		},
		{
			// 测试场景：两侧都有规则且无重名，返回合并后的完整列表（caller 先）
			name:      "src_and_dst_no_duplicate",
			srcRule:   wrapGroups(groupSrcOnly),
			dstRule:   wrapGroups(groupDstOnly),
			wantNames: []string{"src-only", "dst-only"},
			wantOrder: []*apitraffic.LaneGroup{groupSrcOnly, groupDstOnly},
		},
		{
			// 测试场景：两侧都有同名 group，caller 先到先得，callee 那份被去重丢弃
			// 这是本次改动的核心语义：caller 缓存较新时应覆盖 callee 的过期缓存
			name:      "src_and_dst_duplicate_caller_wins",
			srcRule:   wrapGroups(groupSharedFromSrc, groupSrcOnly),
			dstRule:   wrapGroups(groupSharedFromDst, groupDstOnly),
			wantNames: []string{"shared", "src-only", "dst-only"},
			wantOrder: []*apitraffic.LaneGroup{groupSharedFromSrc, groupSrcOnly, groupDstOnly},
		},
		{
			// 测试场景：两侧都没规则，返回 nil
			name:    "both_nil",
			srcRule: nil,
			dstRule: nil,
			wantNil: true,
		},
		{
			// 测试场景：两侧都是空 wrapper（LaneGroups 为 nil），返回 nil
			name:    "both_empty_wrappers",
			srcRule: wrapGroups(),
			dstRule: wrapGroups(),
			wantNil: true,
		},
	}

	r := &LaneRouter{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			routeInfo := &servicerouter.RouteInfo{
				SourceLaneRule: tt.srcRule,
				DestLaneRule:   tt.dstRule,
			}
			got := r.getLaneGroups(routeInfo)

			if tt.wantNil {
				if got != nil {
					t.Errorf("getLaneGroups() = %v, want nil", got)
				}
				return
			}

			if len(got) != len(tt.wantNames) {
				t.Fatalf("getLaneGroups() returned %d groups, want %d: got=%v", len(got), len(tt.wantNames), groupNames(got))
			}
			for i, g := range got {
				if g.GetName() != tt.wantNames[i] {
					t.Errorf("group[%d] name = %q, want %q", i, g.GetName(), tt.wantNames[i])
				}
				// 指针同一判断：确认去重时保留的是 caller 侧原始对象（而非 callee）
				if tt.wantOrder != nil && g != tt.wantOrder[i] {
					t.Errorf("group[%d] pointer mismatch: expected caller-side instance to win dedup", i)
				}
			}
		})
	}
}

// TestLaneRouter_GetLaneGroups_NilRouteInfo 防御性测试：routeInfo 为 nil 应返回 nil 而非 panic
func TestLaneRouter_GetLaneGroups_NilRouteInfo(t *testing.T) {
	r := &LaneRouter{}
	if got := r.getLaneGroups(nil); got != nil {
		t.Errorf("getLaneGroups(nil) = %v, want nil", got)
	}
}

// TestLaneRouter_GetLaneGroups_NilGroupInList 防御性测试：wrapper.LaneGroups 里含 nil 条目时应跳过
func TestLaneRouter_GetLaneGroups_NilGroupInList(t *testing.T) {
	r := &LaneRouter{}
	routeInfo := &servicerouter.RouteInfo{
		SourceLaneRule: wrapGroups(nil, makeGroup("valid"), nil),
		DestLaneRule:   nil,
	}
	got := r.getLaneGroups(routeInfo)
	if len(got) != 1 || got[0].GetName() != "valid" {
		t.Errorf("getLaneGroups() = %v, want [valid]", groupNames(got))
	}
}

// TestLaneRouter_GetLaneGroups_WrongValueType 防御性测试：ServiceRule.GetValue() 返回非 *LaneWrapper 类型应被忽略
func TestLaneRouter_GetLaneGroups_WrongValueType(t *testing.T) {
	r := &LaneRouter{}
	bogusRule := &fakeServiceRule{value: "not a LaneWrapper"}
	routeInfo := &servicerouter.RouteInfo{
		SourceLaneRule: bogusRule,
		DestLaneRule:   wrapGroups(makeGroup("only-valid")),
	}
	got := r.getLaneGroups(routeInfo)
	if len(got) != 1 || got[0].GetName() != "only-valid" {
		t.Errorf("getLaneGroups() = %v, want [only-valid]", groupNames(got))
	}
}

func groupNames(groups []*apitraffic.LaneGroup) []string {
	names := make([]string, 0, len(groups))
	for _, g := range groups {
		if g == nil {
			names = append(names, "<nil>")
			continue
		}
		names = append(names, g.GetName())
	}
	return names
}

// TestParseWarmupEtime 覆盖 parseWarmupEtime 的三种格式与时区一致性。
// 本函数是 tryStainByWarmup 的核心：etime 解析错了会让 uptime 变成负数，
// warmup 永不染色；或者解析成 UTC 让本地时区差压垮 warmup 窗口。
func TestParseWarmupEtime(t *testing.T) {
	t.Run("rfc3339_with_tz", func(t *testing.T) {
		// 测试场景：RFC3339 带时区的字符串，应保留原时区信息
		got := parseWarmupEtime("2026-04-21T19:14:30+08:00")
		if got.IsZero() {
			t.Fatal("expected non-zero time")
		}
		// 带 +08:00 时区解析出来的时间转为 UTC 应该是 11:14:30
		if u := got.UTC(); u.Hour() != 11 || u.Minute() != 14 || u.Second() != 30 {
			t.Errorf("UTC time = %v, want 11:14:30", u)
		}
	})

	t.Run("local_format_space_separator", func(t *testing.T) {
		// 测试场景：无时区的本地时间字符串 "2026-04-21 19:14:30"
		// 必须按 time.Local 解析，防止被当作 UTC 导致 uptime 负数
		got := parseWarmupEtime("2026-04-21 19:14:30")
		if got.IsZero() {
			t.Fatal("expected non-zero time")
		}
		// 解析结果的 Location 应为 Local（与运行 SDK 的机器时区一致）
		if got.Location() != time.Local {
			t.Errorf("location = %v, want time.Local", got.Location())
		}
		// 核心防回归断言：假设脚本在本地时区刚刚生成 etime 字符串，
		// parseWarmupEtime 再读回来，两者相减的绝对值应该很小（< 1 秒）
		now := time.Now()
		etimeStr := now.Format("2006-01-02 15:04:05")
		parsed := parseWarmupEtime(etimeStr)
		diff := now.Sub(parsed).Seconds()
		if diff < -1 || diff > 1 {
			t.Errorf("round-trip drift = %.3fs, want |drift| < 1s (indicates timezone mismatch)", diff)
		}
	})

	t.Run("local_format_T_separator", func(t *testing.T) {
		// 测试场景：ISO 风格但无时区 "2026-04-21T19:14:30"
		got := parseWarmupEtime("2026-04-21T19:14:30")
		if got.IsZero() {
			t.Error("expected non-zero time")
		}
	})

	t.Run("empty_string", func(t *testing.T) {
		// 测试场景：空串 → 返回零值，调用方回退 time.Now()
		if got := parseWarmupEtime(""); !got.IsZero() {
			t.Errorf("expected zero time for empty input, got %v", got)
		}
	})

	t.Run("invalid_format", func(t *testing.T) {
		// 测试场景：无效格式 → 返回零值
		if got := parseWarmupEtime("not a time"); !got.IsZero() {
			t.Errorf("expected zero time for invalid input, got %v", got)
		}
	})
}

// TestLaneRouter_Enable 覆盖 Enable 的两种状态：有/无规则
func TestLaneRouter_Enable(t *testing.T) {
	r := &LaneRouter{}
	tests := []struct {
		name      string
		routeInfo *servicerouter.RouteInfo
		want      bool
	}{
		{
			// 测试场景：无规则 → Enable 返回 false，路由链不会调用本插件
			name: "no_rule_disabled",
			routeInfo: &servicerouter.RouteInfo{
				SourceLaneRule: nil,
				DestLaneRule:   nil,
			},
			want: false,
		},
		{
			// 测试场景：只有 dst 有规则 → Enable 返回 true
			name: "dst_has_rule_enabled",
			routeInfo: &servicerouter.RouteInfo{
				DestLaneRule: wrapGroups(makeGroup("g1")),
			},
			want: true,
		},
		{
			// 测试场景：只有 src 有规则 → Enable 返回 true
			name: "src_has_rule_enabled",
			routeInfo: &servicerouter.RouteInfo{
				SourceLaneRule: wrapGroups(makeGroup("g1")),
			},
			want: true,
		},
		{
			// 测试场景：routeInfo 为 nil → Enable 返回 false，不 panic
			name:      "nil_route_info",
			routeInfo: nil,
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Enable(tt.routeInfo, nil)
			if got != tt.want {
				t.Errorf("Enable() = %v, want %v", got, tt.want)
			}
		})
	}
}

// BenchmarkLaneRouter_Enable_NoRule 衡量 "启用但无规则" 场景的 Enable 开销。
// 这是本插件作为默认 beforeChain 成员时最常见的热路径：绝大多数服务没有泳道规则，
// Enable 需要每次路由都被调用一次，必须保持极低开销。
func BenchmarkLaneRouter_Enable_NoRule(b *testing.B) {
	r := &LaneRouter{}
	routeInfo := &servicerouter.RouteInfo{} // src/dst 都为 nil
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.Enable(routeInfo, nil)
	}
}

// BenchmarkLaneRouter_Enable_WithRule 衡量 "有规则" 场景的 Enable 开销。
// 用于对比 NoRule 场景，观察合并/去重逻辑引入的额外开销。
func BenchmarkLaneRouter_Enable_WithRule(b *testing.B) {
	r := &LaneRouter{}
	routeInfo := &servicerouter.RouteInfo{
		SourceLaneRule: wrapGroups(makeGroup("g1"), makeGroup("g2")),
		DestLaneRule:   wrapGroups(makeGroup("g2"), makeGroup("g3")),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.Enable(routeInfo, nil)
	}
}

// makeRule 便捷构造带 defaultLabelValue 的 LaneRule
func makeRule(name, labelKey, defaultLabelValue string, enable bool) *apitraffic.LaneRule {
	return &apitraffic.LaneRule{
		Name:              name,
		Enable:            enable,
		LabelKey:          labelKey,
		DefaultLabelValue: defaultLabelValue,
	}
}

// makeGroupWithRules 便捷构造带 rules 的 LaneGroup
func makeGroupWithRules(name string, rules ...*apitraffic.LaneRule) *apitraffic.LaneGroup {
	return &apitraffic.LaneGroup{Name: name, Rules: rules}
}

// TestBuildEnabledLaneValues 覆盖 buildEnabledLaneValues 的 5 个场景。
// 这是 ExcludeEnabledLaneInstance 模式的核心辅助函数:mode=1 下,routeToBaseline
// 需要一个 "已启用泳道值集合",把元数据值命中该集合的实例从基线里排除。
func TestBuildEnabledLaneValues(t *testing.T) {
	tests := []struct {
		name   string
		groups []*apitraffic.LaneGroup
		want   map[string]map[string]struct{}
	}{
		{
			// 单组单规则,labelKey 为空 → 落回默认 instanceLaneKey="lane"
			name: "single_enabled_rule_default_key",
			groups: []*apitraffic.LaneGroup{
				makeGroupWithRules("g1", makeRule("gray", "", "gray", true)),
			},
			want: map[string]map[string]struct{}{
				"lane": {"gray": {}},
			},
		},
		{
			// 规则禁用 → 不计入
			name: "disabled_rule_skipped",
			groups: []*apitraffic.LaneGroup{
				makeGroupWithRules("g1", makeRule("gray", "", "gray", false)),
			},
			want: map[string]map[string]struct{}{},
		},
		{
			// defaultLabelValue 为空 → 跳过
			name: "empty_default_value_skipped",
			groups: []*apitraffic.LaneGroup{
				makeGroupWithRules("g1", makeRule("gray", "", "", true)),
			},
			want: map[string]map[string]struct{}{},
		},
		{
			// 多规则同 laneKey,聚合成 set
			name: "multiple_rules_same_key_aggregated",
			groups: []*apitraffic.LaneGroup{
				makeGroupWithRules("g1",
					makeRule("gray", "", "gray", true),
					makeRule("canary", "", "canary", true),
				),
			},
			want: map[string]map[string]struct{}{
				"lane": {"gray": {}, "canary": {}},
			},
		},
		{
			// 不同 labelKey 各自独立
			name: "custom_label_key_separate_buckets",
			groups: []*apitraffic.LaneGroup{
				makeGroupWithRules("g1",
					makeRule("gray", "custom_lane", "gray", true),
					makeRule("canary", "", "canary", true),
				),
			},
			want: map[string]map[string]struct{}{
				"custom_lane": {"gray": {}},
				"lane":        {"canary": {}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildEnabledLaneValues(tt.groups)
			if len(got) != len(tt.want) {
				t.Fatalf("buildEnabledLaneValues() key count = %d, want %d (got=%v)",
					len(got), len(tt.want), got)
			}
			for k, wantVals := range tt.want {
				gotVals, ok := got[k]
				if !ok {
					t.Errorf("missing key %q in result", k)
					continue
				}
				if len(gotVals) != len(wantVals) {
					t.Errorf("key %q value count = %d, want %d (got=%v)",
						k, len(gotVals), len(wantVals), gotVals)
				}
				for v := range wantVals {
					if _, ok := gotVals[v]; !ok {
						t.Errorf("key %q missing value %q (got=%v)", k, v, gotVals)
					}
				}
			}
		})
	}
}

// TestConfig_SetDefault_BaseLaneMode 验证默认配置下 BaseLaneMode=OnlyUntaggedInstance。
// 这是大部分生产环境的行为基线,用户不显式设置时 lane router 不会走 ExcludeEnabledLaneInstance
// 的特殊分支,保持向后兼容。
func TestConfig_SetDefault_BaseLaneMode(t *testing.T) {
	c := &Config{}
	c.SetDefault()
	if c.BaseLaneMode != OnlyUntaggedInstance {
		t.Errorf("SetDefault().BaseLaneMode = %d, want %d (OnlyUntaggedInstance)",
			c.BaseLaneMode, OnlyUntaggedInstance)
	}
	if err := c.Verify(); err != nil {
		t.Errorf("Verify() = %v, want nil", err)
	}
}

// TestConfig_BaseLaneMode_ExcludeEnabledLaneInstance 验证显式设为模式 1 时配置通过校验。
// 此模式下 routeToBaseline 的 ExcludeEnabledLaneInstance 分支才会生效。
func TestConfig_BaseLaneMode_ExcludeEnabledLaneInstance(t *testing.T) {
	c := &Config{BaseLaneMode: ExcludeEnabledLaneInstance}
	if err := c.Verify(); err != nil {
		t.Errorf("Verify() = %v, want nil", err)
	}
	if c.BaseLaneMode != 1 {
		t.Errorf("BaseLaneMode numeric value = %d, want 1", c.BaseLaneMode)
	}
}

// TestGetLaneKey_FallbackToDefault 确认 labelKey 为空时回落到 instanceLaneKey="lane"。
// routeToBaseline 直接依赖这个函数计算要排除/保留的元数据 key。
func TestGetLaneKey_FallbackToDefault(t *testing.T) {
	tests := []struct {
		name string
		rule *apitraffic.LaneRule
		want string
	}{
		{
			name: "explicit_label_key",
			rule: &apitraffic.LaneRule{LabelKey: "custom"},
			want: "custom",
		},
		{
			name: "empty_label_key_fallback",
			rule: &apitraffic.LaneRule{LabelKey: ""},
			want: instanceLaneKey,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getLaneKey(tt.rule); got != tt.want {
				t.Errorf("getLaneKey() = %q, want %q", got, tt.want)
			}
		})
	}
}
