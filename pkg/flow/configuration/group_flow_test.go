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
	"reflect"
	"testing"

	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/configconnector"
	"github.com/polarismesh/polaris-go/pkg/sdk"
)

// configGroupTestConnector 复用现有连接器替身，返回可控的配置组响应并记录拉取次数。
type configGroupTestConnector struct {
	MockConnector
	response *configconnector.ConfigGroupResponse
	requests []*configconnector.ConfigGroup
}

// GetConfigGroup 记录 req 的副本并返回预设响应；测试中仅串行调用，不访问远端服务。
func (c *configGroupTestConnector) GetConfigGroup(req *configconnector.ConfigGroup) (
	*configconnector.ConfigGroupResponse, error) {
	request := *req
	c.requests = append(c.requests, &request)
	return c.response, nil
}

// TestConfigGroupFlow_CancelableListeners 验证两个 API 入口对应的 flow 在首次拉取和缓存命中时均返回可取消对象。
func TestConfigGroupFlow_CancelableListeners(t *testing.T) {
	tests := []struct {
		name string
		mode model.GetConfigFileRequestMode
		get  func(*ConfigGroupFlow) (model.ConfigFileGroup, error)
	}{
		{
			name: "GetConfigGroup",
			mode: model.SDKMode,
			get: func(flow *ConfigGroupFlow) (model.ConfigFileGroup, error) {
				return flow.GetConfigGroup("default", "test-group")
			},
		},
		{
			name: "GetConfigGroupWithReq",
			mode: model.AgentMode,
			get: func(flow *ConfigGroupFlow) (model.ConfigFileGroup, error) {
				return flow.GetConfigGroupWithReq(&model.GetConfigGroupRequest{
					Namespace: "default", FileGroup: "test-group", Mode: model.AgentMode, Subscribe: true,
				})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 不启动定时同步，通过真实仓库的 pull 精确控制每一轮事件。
			globalCtx := sdk.NewValueContext()
			connector := &configGroupTestConnector{
				response: &configconnector.ConfigGroupResponse{
					Code:         uint32(apimodel.Code_ExecuteSuccess),
					Revision:     "r1",
					ReleaseFiles: []*model.SimpleConfigFile{{FileName: "config.yaml", Version: 1}},
				},
			}
			flow := &ConfigGroupFlow{
				connector:  connector,
				globalCtx:  globalCtx,
				logCtx:     globalCtx.GetContextLogger(),
				groupCache: make(map[string]model.ConfigFileGroup),
				repos:      make(map[string]*ConfigGroupRepo),
				inFlight:   make(map[string]*inFlightEntry),
			}
			group, err := tt.get(flow)
			if err != nil {
				t.Fatalf("get config group: %v", err)
			}
			listener, ok := group.(model.CancelableConfigGroupListener)
			if !ok {
				t.Fatalf("returned group %T does not support cancelable listeners", group)
			}
			files, revision, exists := group.GetFiles()
			if !exists || revision != "r1" || !reflect.DeepEqual(files, connector.response.ReleaseFiles) {
				t.Fatalf("GetFiles() = (%v, %q, %v), want initial files at r1", files, revision, exists)
			}
			calls := 0
			cancel := listener.AddChangeListenerWithCancel(func(*model.ConfigGroupChangeEvent) { calls++ })
			cached, err := tt.get(flow)
			if err != nil {
				t.Fatalf("get cached config group: %v", err)
			}
			if cached != group {
				t.Fatal("cache hit returned a different config group")
			}
			if _, ok := cached.(model.CancelableConfigGroupListener); !ok {
				t.Fatal("cached group lost cancelable listener capability")
			}
			wantReq := &configconnector.ConfigGroup{Namespace: "default", Group: "test-group", Mode: tt.mode}
			if !reflect.DeepEqual(connector.requests, []*configconnector.ConfigGroup{wantReq}) {
				t.Fatalf("initial requests = %+v, want one request %+v", connector.requests, wantReq)
			}
			repo := flow.repos["default@test-group"]
			connector.response = &configconnector.ConfigGroupResponse{
				Code: uint32(apimodel.Code_ExecuteSuccess), Revision: "r2",
				ReleaseFiles: []*model.SimpleConfigFile{{FileName: "config.yaml", Version: 2}},
			}
			if err := repo.pull(); err != nil {
				t.Fatalf("pull changed config group: %v", err)
			}
			if calls != 1 {
				t.Fatalf("callback calls = %d, want 1", calls)
			}
			cancel()
			connector.response = &configconnector.ConfigGroupResponse{
				Code: uint32(apimodel.Code_ExecuteSuccess), Revision: "r3",
				ReleaseFiles: []*model.SimpleConfigFile{{FileName: "config.yaml", Version: 3}},
			}
			if err := repo.pull(); err != nil {
				t.Fatalf("pull config group after cancel: %v", err)
			}
			if calls != 1 {
				t.Errorf("callback received an event after cancel: calls = %d, want 1", calls)
			}
			_, revision, exists = group.GetFiles()
			if !exists || revision != "r3" {
				t.Errorf("cancel interrupted group synchronization: revision = %q, exists = %v", revision, exists)
			}
		})
	}
}
