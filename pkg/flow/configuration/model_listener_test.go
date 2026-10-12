// Tencent is pleased to support the open source community by making polaris-go available.
//
// Copyright (C) 2021 Tencent. All rights reserved.
//
// Licensed under the BSD 3-Clause License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// https://opensource.org/licenses/BSD-3-Clause
//
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package configuration

import (
	"errors"
	"strings"
	"testing"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/configconnector"
)

// TestConfigListeners_PanicLogging 验证三种监听入口不泄露 panic 值、异常记录 ERROR，正常回调不误报。
// 使用 GODEBUG=panicnil=0/1 分别运行，验证新旧 panic(nil) 语义。
func TestConfigListeners_PanicLogging(t *testing.T) {
	const secret = "listener-test-secret-must-not-be-logged"
	tests := []struct {
		name        string
		value       interface{}
		shouldPanic bool
	}{
		{name: "normal return"},
		{name: "string", value: secret, shouldPanic: true},
		{name: "error", value: errors.New(secret), shouldPanic: true},
		{name: "struct", value: struct{ Content string }{secret}, shouldPanic: true},
		{name: "nil", shouldPanic: true},
	}
	for _, kind := range []string{"file", "group legacy", "group cancelable"} {
		for _, tt := range tests {
			t.Run(kind+"/"+tt.name, func(t *testing.T) {
				group, logger := newTestConfigGroup(t)
				calls, otherCalls := 0, 0
				callback := func() {
					calls++
					if tt.shouldPanic {
						panic(tt.value)
					}
				}
				if kind == "file" {
					file := newTestConfigFile("old", &configconnector.ConfigFile{
						Namespace: "default", FileGroup: "test-group", FileName: "config.yaml",
					})
					file.AddChangeListener(func(model.ConfigFileChangeEvent) { callback() })
					file.AddChangeListener(func(model.ConfigFileChangeEvent) { otherCalls++ })
					file.fireChangeEvent(model.ConfigFileChangeEvent{
						ConfigFileMetadata: &file.DefaultConfigFileMetadata, NewValue: secret,
					})
				} else {
					listener := func(*model.ConfigGroupChangeEvent) { callback() }
					if kind == "group legacy" {
						group.AddChangeListener(listener)
					} else {
						cancel := group.AddChangeListenerWithCancel(listener)
						defer cancel()
					}
					group.AddChangeListener(func(*model.ConfigGroupChangeEvent) { otherCalls++ })
					group.repoChangeListener(nil, &configconnector.ConfigGroupResponse{Revision: "r1"})
				}
				if calls != 1 || otherCalls != 1 {
					t.Fatalf("callback calls = (%d, %d), want (1, 1)", calls, otherCalls)
				}
				logs := logger.dump()
				if strings.Contains(logs, secret) {
					t.Error("listener log contains plaintext panic payload")
				}
				wantErrors := 0
				if tt.shouldPanic {
					wantErrors = 1
					for _, want := range []string{"panic recovered", "panicType=", "stack: goroutine"} {
						if !strings.Contains(logs, want) {
							t.Errorf("panic log missing %q", want)
						}
					}
				}
				if count := strings.Count(logs, "ERROR "); count != wantErrors {
					t.Errorf("ERROR log count = %d, want %d", count, wantErrors)
				}
			})
		}
	}
}
