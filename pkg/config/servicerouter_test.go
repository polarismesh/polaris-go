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

package config

import (
	"reflect"
	"testing"
)

// TestServiceRouterConfigImpl_DeduplicateChains 覆盖配置去重语义：
// 同一个路由插件不能同时出现在多条链中，beforeChain > chain > afterChain 优先级。
func TestServiceRouterConfigImpl_DeduplicateChains(t *testing.T) {
	tests := []struct {
		name       string
		before     []string
		chain      []string
		after      []string
		wantBefore []string
		wantChain  []string
		wantAfter  []string
		wantWarn   int
	}{
		{
			// 测试场景：三条链都没重复 → 保持原样，无告警
			name:       "no_duplicate",
			before:     []string{"laneRouter"},
			chain:      []string{"ruleBasedRouter", "nearbyBasedRouter"},
			after:      []string{"filterOnly"},
			wantBefore: []string{"laneRouter"},
			wantChain:  []string{"ruleBasedRouter", "nearbyBasedRouter"},
			wantAfter:  []string{"filterOnly"},
			wantWarn:   0,
		},
		{
			// 测试场景：laneRouter 同时出现在 before 和 chain → chain 中的被移除
			name:       "duplicate_removed_from_chain",
			before:     []string{"laneRouter"},
			chain:      []string{"laneRouter", "ruleBasedRouter"},
			after:      []string{"filterOnly"},
			wantBefore: []string{"laneRouter"},
			wantChain:  []string{"ruleBasedRouter"},
			wantAfter:  []string{"filterOnly"},
			wantWarn:   1,
		},
		{
			// 测试场景：filterOnly 同时出现在 chain 和 after → after 中的被移除
			name:       "duplicate_removed_from_after",
			before:     []string{"laneRouter"},
			chain:      []string{"ruleBasedRouter", "filterOnly"},
			after:      []string{"filterOnly"},
			wantBefore: []string{"laneRouter"},
			wantChain:  []string{"ruleBasedRouter", "filterOnly"},
			wantAfter:  []string{},
			wantWarn:   1,
		},
		{
			// 测试场景：同一个插件在 before 和 after 都出现 → after 中的被移除（before 优先）
			name:       "duplicate_before_wins_over_after",
			before:     []string{"laneRouter"},
			chain:      []string{"ruleBasedRouter"},
			after:      []string{"laneRouter", "filterOnly"},
			wantBefore: []string{"laneRouter"},
			wantChain:  []string{"ruleBasedRouter"},
			wantAfter:  []string{"filterOnly"},
			wantWarn:   1,
		},
		{
			// 测试场景：chain 内部重复 → 后出现的被移除
			name:       "duplicate_within_chain",
			before:     []string{},
			chain:      []string{"ruleBasedRouter", "ruleBasedRouter", "nearbyBasedRouter"},
			after:      []string{"filterOnly"},
			wantBefore: []string{},
			wantChain:  []string{"ruleBasedRouter", "nearbyBasedRouter"},
			wantAfter:  []string{"filterOnly"},
			wantWarn:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &ServiceRouterConfigImpl{
				BeforeChain: append([]string{}, tt.before...),
				Chain:       append([]string{}, tt.chain...),
				AfterChain:  append([]string{}, tt.after...),
			}
			s.deduplicateChains()

			if !reflect.DeepEqual(s.BeforeChain, tt.wantBefore) {
				t.Errorf("BeforeChain = %v, want %v", s.BeforeChain, tt.wantBefore)
			}
			if !reflect.DeepEqual(s.Chain, tt.wantChain) {
				t.Errorf("Chain = %v, want %v", s.Chain, tt.wantChain)
			}
			if !reflect.DeepEqual(s.AfterChain, tt.wantAfter) {
				t.Errorf("AfterChain = %v, want %v", s.AfterChain, tt.wantAfter)
			}
			if len(s.chainWarnings) != tt.wantWarn {
				t.Errorf("chainWarnings count = %d, want %d (msgs=%v)", len(s.chainWarnings), tt.wantWarn, s.chainWarnings)
			}
		})
	}
}

// fakeWarnLogger 收集 Warnf 调用，供 FlushChainWarnings 验证
type fakeWarnLogger struct {
	messages []string
}

func (f *fakeWarnLogger) Warnf(format string, args ...interface{}) {
	// 为了断言简单，只记录 format 字符串（实测不需要验证参数内容）
	_ = args
	f.messages = append(f.messages, format)
}

// TestServiceRouterConfigImpl_FlushChainWarnings 覆盖：
//  1. 有告警时通过 logger 输出并清空缓冲
//  2. logger 为 nil 时仍然清空缓冲（不 panic）
func TestServiceRouterConfigImpl_FlushChainWarnings(t *testing.T) {
	t.Run("flush_to_logger", func(t *testing.T) {
		s := &ServiceRouterConfigImpl{
			BeforeChain: []string{"laneRouter"},
			Chain:       []string{"laneRouter", "ruleBasedRouter"},
		}
		s.deduplicateChains()
		if len(s.chainWarnings) != 1 {
			t.Fatalf("precondition: expected 1 warning after dedup, got %d", len(s.chainWarnings))
		}

		logger := &fakeWarnLogger{}
		s.FlushChainWarnings(logger)

		if len(logger.messages) != 1 {
			t.Errorf("logger received %d messages, want 1", len(logger.messages))
		}
		if len(s.chainWarnings) != 0 {
			t.Errorf("chainWarnings not cleared after flush: %v", s.chainWarnings)
		}
	})

	t.Run("flush_nil_logger_still_clears", func(t *testing.T) {
		s := &ServiceRouterConfigImpl{
			BeforeChain: []string{"laneRouter"},
			Chain:       []string{"laneRouter"},
		}
		s.deduplicateChains()
		if len(s.chainWarnings) != 1 {
			t.Fatalf("precondition: expected 1 warning, got %d", len(s.chainWarnings))
		}
		s.FlushChainWarnings(nil)
		if len(s.chainWarnings) != 0 {
			t.Errorf("chainWarnings not cleared after nil-logger flush: %v", s.chainWarnings)
		}
	})
}
