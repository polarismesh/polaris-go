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

package startup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	configflow "github.com/polarismesh/polaris-go/pkg/flow/configuration"
)

// mockConfigFlow 实现 watchedConfigFileProvider，返回预设的监听文件元数据与内容。
type mockConfigFlow struct {
	items        []configflow.ConfigFileMetadataItem
	contentItems map[string]configflow.ConfigFileContentItem
}

func (m *mockConfigFlow) GetWatchedConfigFileMetadata() []configflow.ConfigFileMetadataItem {
	return m.items
}

// GetWatchedConfigFileContent 按 cacheKey 命中返回预设的配置文件内容项。
func (m *mockConfigFlow) GetWatchedConfigFileContent(namespace, fileGroup, fileName string) (configflow.ConfigFileContentItem, bool) {
	if m.contentItems == nil {
		return configflow.ConfigFileContentItem{}, false
	}
	key := namespace + "+" + fileGroup + "+" + fileName
	item, ok := m.contentItems[key]
	return item, ok
}

// mockClientEventStream 记录 Send 的 ACK 事件，用于断言。
type mockClientEventStream struct {
	sent []*apiservice.ClientEvent
}

func (m *mockClientEventStream) Send(e *apiservice.ClientEvent) error {
	m.sent = append(m.sent, e)
	return nil
}

func (m *mockClientEventStream) Recv() (*apiservice.ClientEvent, error) {
	return nil, io.EOF
}

func (m *mockClientEventStream) Close() error { return nil }

// TestBuildAckContent_ConfigHit 命中监听文件时 ACK 含 version/md5/content/applied=true 并回显查询目标。
func TestBuildAckContent_ConfigHit(t *testing.T) {
	w := &ClientEventWatcher{
		clientID: "c1",
		configFlow: &mockConfigFlow{
			contentItems: map[string]configflow.ConfigFileContentItem{
				"default+g1+f1": {
					Namespace: "default", Group: "g1", FileName: "f1",
					Version: 3, Md5: "md5_1", Content: "config-body",
					EffectiveTime: 1723458600123, Pulled: true,
				},
			},
		},
	}
	push := `{"kind":"config","config":{"namespace":"default","group":"g1","file_name":"f1"}}`
	raw := w.buildAckContent(push)
	assert.Contains(t, raw, `"effective_time":1723458600123`, "命中时 ACK 应回带 effective_time")
	var ack clientEventAck
	assert.NoError(t, json.Unmarshal([]byte(raw), &ack))
	assert.Equal(t, "config", ack.Kind)
	assert.Equal(t, "default", ack.Config.Namespace)
	assert.Equal(t, "g1", ack.Config.Group)
	assert.Equal(t, "f1", ack.Config.FileName)
	assert.Equal(t, uint64(3), ack.Version)
	assert.Equal(t, "md5_1", ack.Md5)
	assert.Equal(t, "config-body", ack.Content, "ACK 应含配置文件内容")
	assert.Equal(t, int64(1723458600123), ack.EffectiveTime, "ACK 应回带配置生效时间")
	assert.True(t, ack.Applied)
}

// TestBuildAckContent_ConfigEmptyContent 命中监听文件但内容为空串时，
// ACK 的 content 字段仍显式输出空串（不因 omitempty 丢失），供服务端区分"空内容"与"未返回"。
func TestBuildAckContent_ConfigEmptyContent(t *testing.T) {
	w := &ClientEventWatcher{
		clientID: "c1",
		configFlow: &mockConfigFlow{
			contentItems: map[string]configflow.ConfigFileContentItem{
				"default+g1+f1": {Namespace: "default", Group: "g1", FileName: "f1", Content: "", Pulled: true},
			},
		},
	}
	push := `{"kind":"config","config":{"namespace":"default","group":"g1","file_name":"f1"}}`
	raw := w.buildAckContent(push)
	assert.Contains(t, raw, `"content":""`, "空内容应显式输出 content 字段，不可被 omitempty 丢弃")
	var ack clientEventAck
	assert.NoError(t, json.Unmarshal([]byte(raw), &ack))
	assert.True(t, ack.Applied)
	assert.Equal(t, "", ack.Content)
}

// TestBuildAckContent_ConfigMiss 未命中监听文件时 applied=false。
func TestBuildAckContent_ConfigMiss(t *testing.T) {
	w := &ClientEventWatcher{
		clientID:   "c1",
		configFlow: &mockConfigFlow{items: []configflow.ConfigFileMetadataItem{}},
	}
	push := `{"kind":"config","config":{"namespace":"default","group":"g1","file_name":"f1"}}`
	raw := w.buildAckContent(push)
	assert.NotContains(t, raw, "effective_time", "未命中时 effective_time 应 omitempty 省略")
	var ack clientEventAck
	assert.NoError(t, json.Unmarshal([]byte(raw), &ack))
	assert.False(t, ack.Applied)
	assert.Equal(t, "config", ack.Kind)
	assert.Equal(t, "default", ack.Config.Namespace)
	assert.Equal(t, reasonNotWatched, ack.Reason, "未监听应回 not_watched 便于运维区分")
	assert.Equal(t, int64(0), ack.EffectiveTime, "未命中时生效时间应为零值")
}

// TestBuildAckContent_ConfigPending 已监听但尚未拉取生效（Pulled=false）时 applied=false 且 reason=pending，
// 不回带 content/md5/effective_time，供服务端区分"未生效"与"已生效"。
func TestBuildAckContent_ConfigPending(t *testing.T) {
	w := &ClientEventWatcher{
		clientID: "c1",
		configFlow: &mockConfigFlow{
			contentItems: map[string]configflow.ConfigFileContentItem{
				// 仅回退到 notifiedVersion，无 content/md5/effectiveTime，Pulled=false
				"default+g1+f1": {Namespace: "default", Group: "g1", FileName: "f1", Version: 2, Pulled: false},
			},
		},
	}
	push := `{"kind":"config","config":{"namespace":"default","group":"g1","file_name":"f1"}}`
	raw := w.buildAckContent(push)
	assert.NotContains(t, raw, "effective_time", "未生效时 effective_time 应 omitempty 省略")
	var ack clientEventAck
	assert.NoError(t, json.Unmarshal([]byte(raw), &ack))
	assert.False(t, ack.Applied, "未拉取生效不应置 applied=true")
	assert.Equal(t, reasonPending, ack.Reason, "已监听未生效应回 pending 便于与 not_watched 区分")
	assert.Equal(t, uint64(2), ack.Version, "pending 时仍回带已知版本供服务端参考")
	assert.Empty(t, ack.Content, "未生效时不回带内容")
}

// TestBuildAckContent_NilConfigFlow 配置中心未启用时 applied=false 且 reason=config_disabled。
func TestBuildAckContent_NilConfigFlow(t *testing.T) {
	w := &ClientEventWatcher{clientID: "c1", configFlow: nil}
	push := `{"kind":"config","config":{"namespace":"default","group":"g1","file_name":"f1"}}`
	var ack clientEventAck
	assert.NoError(t, json.Unmarshal([]byte(w.buildAckContent(push)), &ack))
	assert.False(t, ack.Applied)
	assert.Equal(t, reasonConfigDisabled, ack.Reason)
}

// TestBuildAckContent_UnknownKind 未知 kind 回 applied=false。
func TestBuildAckContent_UnknownKind(t *testing.T) {
	w := &ClientEventWatcher{clientID: "c1", configFlow: &mockConfigFlow{}}
	push := `{"kind":"unknown","config":{"namespace":"default","group":"g1","file_name":"f1"}}`
	var ack clientEventAck
	assert.NoError(t, json.Unmarshal([]byte(w.buildAckContent(push)), &ack))
	assert.False(t, ack.Applied)
	assert.Equal(t, "unknown", ack.Kind)
	assert.Equal(t, reasonUnknownKind, ack.Reason)
}

// TestBuildAckContent_BadContent content 解析失败回最小 ACK。
func TestBuildAckContent_BadContent(t *testing.T) {
	w := &ClientEventWatcher{clientID: "c1", configFlow: &mockConfigFlow{}}
	var ack clientEventAck
	assert.NoError(t, json.Unmarshal([]byte(w.buildAckContent("not json")), &ack))
	assert.False(t, ack.Applied)
	assert.Equal(t, reasonBadContent, ack.Reason)
}

// TestHandlePush_AckIndexAndType 验证 ACK 回带 PUSH 的 index、类型为 ACK、client_id 一致。
func TestHandlePush_AckIndexAndType(t *testing.T) {
	w := &ClientEventWatcher{
		clientID:   "c1",
		configFlow: &mockConfigFlow{items: []configflow.ConfigFileMetadataItem{}},
	}
	stream := &mockClientEventStream{}
	push := &apiservice.ClientEvent{
		Type:    apiservice.ClientEvent_PUSH,
		Index:   42,
		Content: `{"kind":"config","config":{"namespace":"default","group":"g1","file_name":"f1"}}`,
	}
	assert.NoError(t, w.handlePush(stream, push))
	assert.Len(t, stream.sent, 1)
	ack := stream.sent[0]
	assert.Equal(t, apiservice.ClientEvent_ACK, ack.GetType())
	assert.Equal(t, uint64(42), ack.GetIndex())
	assert.Equal(t, "c1", ack.GetClientId())
}

// ============ 以下为 code review 修复项的回归测试 ============

// TestBuildAckContent_TypedNilConfigFlowNoPanic 回归 P0-1：
// 配置中心未启用时 Engine 会得到 (*ConfigFileFlow)(nil)，若被直接装入接口则
// `configFlow != nil` 判断失效并解引用 nil receiver。此处直接构造 typed-nil 验证不再 panic。
func TestBuildAckContent_TypedNilConfigFlowNoPanic(t *testing.T) {
	var typedNil *configflow.ConfigFileFlow
	w := &ClientEventWatcher{clientID: "c1", configFlow: typedNil}
	push := `{"kind":"config","config":{"namespace":"default","group":"g1","file_name":"f1"}}`
	// 不 panic 即通过
	var ack clientEventAck
	assert.NoError(t, json.Unmarshal([]byte(w.buildAckContent(push)), &ack))
	assert.False(t, ack.Applied)
	assert.Equal(t, reasonNotWatched, ack.Reason, "typed-nil 走 nil guard 后视为未监听")
}

// TestGetWatchedConfigFileMetadata_TypedNilNoPanic 回归 P0-1 的 ReportClient 路径：
// nil receiver 调用元数据方法应返回空切片而非 panic。
func TestGetWatchedConfigFileMetadata_TypedNilNoPanic(t *testing.T) {
	var typedNil *configflow.ConfigFileFlow
	items := typedNil.GetWatchedConfigFileMetadata()
	assert.NotNil(t, items)
	assert.Empty(t, items)
}

// TestBuildAckContent_ContentTruncated 回归 P1-5：
// 超大配置内容应被截断并标记，避免超出 gRPC 消息体上限导致 ACK 发不出。
func TestBuildAckContent_ContentTruncated(t *testing.T) {
	big := strings.Repeat("x", watchMaxAckContentBytes+1024)
	w := &ClientEventWatcher{
		clientID: "c1",
		configFlow: &mockConfigFlow{
			contentItems: map[string]configflow.ConfigFileContentItem{
				"default+g1+f1": {Namespace: "default", Group: "g1", FileName: "f1",
					Version: 7, Md5: "md5_big", Content: big, Pulled: true},
			},
		},
	}
	push := `{"kind":"config","config":{"namespace":"default","group":"g1","file_name":"f1"}}`
	var ack clientEventAck
	assert.NoError(t, json.Unmarshal([]byte(w.buildAckContent(push)), &ack))
	assert.True(t, ack.Applied)
	assert.True(t, ack.ContentTruncated, "超限应标记截断")
	assert.Equal(t, len(big), ack.ContentLength, "应回原始长度")
	assert.Len(t, ack.Content, watchMaxAckContentBytes, "内容应被截到上限")
	assert.Equal(t, "md5_big", ack.Md5, "md5 仍为完整内容摘要，供服务端校验")
}

// TestBuildAckContent_ContentNotTruncatedAtLimit 边界：正好等于上限时不截断。
func TestBuildAckContent_ContentNotTruncatedAtLimit(t *testing.T) {
	exact := strings.Repeat("y", watchMaxAckContentBytes)
	w := &ClientEventWatcher{
		clientID: "c1",
		configFlow: &mockConfigFlow{
			contentItems: map[string]configflow.ConfigFileContentItem{
				"default+g1+f1": {Namespace: "default", Group: "g1", FileName: "f1", Content: exact, Pulled: true},
			},
		},
	}
	push := `{"kind":"config","config":{"namespace":"default","group":"g1","file_name":"f1"}}`
	var ack clientEventAck
	assert.NoError(t, json.Unmarshal([]byte(w.buildAckContent(push)), &ack))
	assert.False(t, ack.ContentTruncated, "正好等于上限不应截断")
	assert.Len(t, ack.Content, watchMaxAckContentBytes)
}

// panicConfigFlow 在查询时 panic，用于验证 handlePush 的 recover。
type panicConfigFlow struct{}

func (p *panicConfigFlow) GetWatchedConfigFileMetadata() []configflow.ConfigFileMetadataItem {
	return nil
}

func (p *panicConfigFlow) GetWatchedConfigFileContent(_, _, _ string) (configflow.ConfigFileContentItem, bool) {
	panic("injected panic for test")
}

// TestHandlePush_RecoversPanic 回归 P0-2：
// 单条事件处理过程中的 panic 必须被 recover，返回错误而非终止宿主进程。
func TestHandlePush_RecoversPanic(t *testing.T) {
	w := &ClientEventWatcher{clientID: "c1", configFlow: &panicConfigFlow{}}
	stream := &mockClientEventStream{}
	push := &apiservice.ClientEvent{
		Type:    apiservice.ClientEvent_PUSH,
		Index:   9,
		Content: `{"kind":"config","config":{"namespace":"default","group":"g1","file_name":"f1"}}`,
	}
	// 不崩进程，且返回 panic 哨兵错误
	err := w.handlePush(stream, push)
	assert.ErrorIs(t, err, errHandlePushPanic)
}

// TestClose_Idempotent 验证 Close 可重复调用不 panic、不阻塞。
func TestClose_Idempotent(t *testing.T) {
	w := NewClientEventWatcher(nil, "c1", nil, nil)
	close(w.done) // 模拟 runLoop 已退出，避免 Close 阻塞等待
	w.Close()
	w.Close() // 第二次应直接返回
	assert.True(t, w.isClosed())
}

// TestIsUnimplemented 回归 P2-10：识别服务端未实现该接口的错误以停止无意义重连。
func TestIsUnimplemented(t *testing.T) {
	assert.False(t, isUnimplemented(nil))
	assert.False(t, isUnimplemented(errors.New("some network error")))
	assert.True(t, isUnimplemented(status.Error(codes.Unimplemented, "not implemented")))
	// 被包装一层后仍应识别
	wrapped := fmt.Errorf("connect failed: %w", status.Error(codes.Unimplemented, "not implemented"))
	assert.True(t, isUnimplemented(wrapped))
	assert.False(t, isUnimplemented(status.Error(codes.Unavailable, "unavailable")))
}
