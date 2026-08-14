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
	"strings"
	"sync"
	"testing"

	"github.com/golang/protobuf/proto"
	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/config"
	configflow "github.com/polarismesh/polaris-go/pkg/flow/configuration"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/localregistry"
	"github.com/polarismesh/polaris-go/pkg/sdk"
)

// TestConfigMetadataPayload_Marshal 验证 ReportClient 上报 config_metadata 的 JSON 结构，
// 确认顶层 kind/config_watch 与数组元素的 snake_case 字段名（与服务端配置中心三级树命名一致）。
func TestConfigMetadataPayload_Marshal(t *testing.T) {
	payload := configMetadataPayload{
		Kind: "config",
		ConfigWatch: []configflow.ConfigFileMetadataItem{
			{Namespace: "default", Group: "g1", FileName: "f1", Version: 3, Md5: "md5_1"},
			{Namespace: "default", Group: "g2", FileName: "f2", Version: 5, Md5: ""},
		},
	}
	data, err := json.Marshal(payload)
	assert.NoError(t, err)
	s := string(data)
	assert.Contains(t, s, `"kind":"config"`)
	assert.Contains(t, s, `"config_watch":[`)
	// snake_case 字段
	assert.Contains(t, s, `"file_name":"f1"`)
	assert.Contains(t, s, `"namespace":"default"`)
	assert.Contains(t, s, `"group":"g1"`)
	assert.Contains(t, s, `"version":3`)
	assert.Contains(t, s, `"md5":"md5_1"`)
}

// TestConfigMetadataPayload_EmptyWatch 验证无监听文件时 JSON 仍含 kind 与空 config_watch 数组。
func TestConfigMetadataPayload_EmptyWatch(t *testing.T) {
	payload := configMetadataPayload{Kind: "config", ConfigWatch: []configflow.ConfigFileMetadataItem{}}
	data, err := json.Marshal(payload)
	assert.NoError(t, err)
	assert.Equal(t, `{"kind":"config","config_watch":[]}`, string(data))
}

// TestClientInfoFileName_UniquePerClientID 验证持久化文件名按 clientID 隔离：
// 同进程多 context（clientID 不同）得到不同文件名，避免 client_info.json 互相覆盖。
func TestClientInfoFileName_UniquePerClientID(t *testing.T) {
	f1 := clientInfoFileName("host-1234-0")
	f2 := clientInfoFileName("host-1234-1")
	f3 := clientInfoFileName("pod-xyz-0")
	assert.Equal(t, "client_info_host-1234-0.json", f1)
	assert.Equal(t, "client_info_host-1234-1.json", f2)
	assert.Equal(t, "client_info_pod-xyz-0.json", f3)
	assert.NotEqual(t, f1, f2, "同进程多 context 文件名应不同")
	assert.NotEqual(t, f1, f3, "不同 clientID 文件名应不同")
}

// TestClientInfoFileName_Format 验证文件名格式与扩展名。
func TestClientInfoFileName_Format(t *testing.T) {
	name := clientInfoFileName("c1")
	assert.Equal(t, "client_info_c1.json", name)
	assert.True(t, strings.HasSuffix(name, ".json"), "应以 .json 结尾")
	assert.True(t, strings.HasPrefix(name, "client_info_"), "应以 client_info_ 开头")
}

// TestClientInfoLoadCandidates_Order 验证回退读取顺序：
// 首选按 clientID 隔离的文件，次选固定名共享文件。
// 顺序错误会导致重启换 PID 时读不到可复用的地域缓存。
func TestClientInfoLoadCandidates_Order(t *testing.T) {
	candidates := clientInfoLoadCandidates("host-1234-0")
	assert.Len(t, candidates, 2)
	assert.Equal(t, "client_info_host-1234-0.json", candidates[0], "首选按 clientID 隔离的文件")
	assert.Equal(t, clientInfoSharedFile, candidates[1], "次选固定名共享文件")
}

// TestClientInfoSharedFileConstant 验证固定名共享文件常量稳定。
func TestClientInfoSharedFileConstant(t *testing.T) {
	assert.Equal(t, "client_info.json", clientInfoSharedFile)
}

// TestClientInfoNeedsPersist 验证持久化写入判断逻辑：
// location 或 configMetadata 任一变化（或首次写入）即需写入；两者均不变时跳过。
// 重点覆盖 configMetadata 单独变化（订阅列表变化、地域不变）也触发写入的场景，
// 这是修复 client_info.json 中 config_watch 停留过期快照的关键路径。
func TestClientInfoNeedsPersist(t *testing.T) {
	locA := &model.Location{Region: "ap", Zone: "z1", Campus: "c1"}
	locB := &model.Location{Region: "ap", Zone: "z2", Campus: "c1"}
	const emptyWatch = `{"kind":"config","config_watch":[]}`
	const oneWatch = `{"kind":"config","config_watch":[{"namespace":"default","group":"g1","file_name":"f1"}]}`
	// 同一组监听文件、仅数组顺序不同的两份快照，用于验证顺序免疫
	const twoWatchAB = `{"kind":"config","config_watch":[` +
		`{"namespace":"default","group":"g1","file_name":"f1","version":3,"md5":"m1"},` +
		`{"namespace":"default","group":"g2","file_name":"f2","version":5,"md5":"m2"}]}`
	const twoWatchBA = `{"kind":"config","config_watch":[` +
		`{"namespace":"default","group":"g2","file_name":"f2","version":5,"md5":"m2"},` +
		`{"namespace":"default","group":"g1","file_name":"f1","version":3,"md5":"m1"}]}`
	// 与 twoWatchAB 同集合但 f1 的 version 不同，用于验证 version 变化会触发写入
	const twoWatchABVersionBump = `{"kind":"config","config_watch":[` +
		`{"namespace":"default","group":"g1","file_name":"f1","version":4,"md5":"m1"},` +
		`{"namespace":"default","group":"g2","file_name":"f2","version":5,"md5":"m2"}]}`
	// 与 twoWatchAB 同集合但 f2 的 md5 不同，用于验证 md5 变化会触发写入
	const twoWatchABMd5Change = `{"kind":"config","config_watch":[` +
		`{"namespace":"default","group":"g1","file_name":"f1","version":3,"md5":"m1"},` +
		`{"namespace":"default","group":"g2","file_name":"f2","version":5,"md5":"m2x"}]}`

	tests := []struct {
		name               string
		lastLocation       *model.Location
		newLocation        *model.Location
		lastConfigMetadata string
		newConfigMetadata  string
		want               bool
	}{
		{
			name:               "首次写入_lastLocation为nil_必写",
			lastLocation:       nil,
			newLocation:        locA,
			lastConfigMetadata: "",
			newConfigMetadata:  "",
			want:               true,
		},
		{
			name:               "地域与configMetadata均未变化_跳过",
			lastLocation:       locA,
			newLocation:        locA,
			lastConfigMetadata: emptyWatch,
			newConfigMetadata:  emptyWatch,
			want:               false,
		},
		{
			name:               "仅地域变化_写入",
			lastLocation:       locA,
			newLocation:        locB,
			lastConfigMetadata: emptyWatch,
			newConfigMetadata:  emptyWatch,
			want:               true,
		},
		{
			name:               "仅configMetadata变化_新增订阅_写入",
			lastLocation:       locA,
			newLocation:        locA,
			lastConfigMetadata: emptyWatch,
			newConfigMetadata:  oneWatch,
			want:               true,
		},
		{
			name:               "仅configMetadata变化_取消所有订阅_写入",
			lastLocation:       locA,
			newLocation:        locA,
			lastConfigMetadata: oneWatch,
			newConfigMetadata:  emptyWatch,
			want:               true,
		},
		{
			name:               "configMetadata从空到非空_订阅前空模板到有内容_写入",
			lastLocation:       locA,
			newLocation:        locA,
			lastConfigMetadata: "",
			newConfigMetadata:  oneWatch,
			want:               true,
		},
		{
			name:               "地域与configMetadata均变化_写入",
			lastLocation:       locA,
			newLocation:        locB,
			lastConfigMetadata: emptyWatch,
			newConfigMetadata:  oneWatch,
			want:               true,
		},
		{
			name:               "顺序免疫_同集合不同顺序_跳过",
			lastLocation:       locA,
			newLocation:        locA,
			lastConfigMetadata: twoWatchAB,
			newConfigMetadata:  twoWatchBA,
			want:               false,
		},
		{
			name:               "顺序免疫_自比较_跳过",
			lastLocation:       locA,
			newLocation:        locA,
			lastConfigMetadata: twoWatchAB,
			newConfigMetadata:  twoWatchAB,
			want:               false,
		},
		{
			name:               "仅version变化_写入",
			lastLocation:       locA,
			newLocation:        locA,
			lastConfigMetadata: twoWatchAB,
			newConfigMetadata:  twoWatchABVersionBump,
			want:               true,
		},
		{
			name:               "仅md5变化_写入",
			lastLocation:       locA,
			newLocation:        locA,
			lastConfigMetadata: twoWatchAB,
			newConfigMetadata:  twoWatchABMd5Change,
			want:               true,
		},
		{
			name:               "无法解析时回退字符串比较_内容不同_写入",
			lastLocation:       locA,
			newLocation:        locA,
			lastConfigMetadata: `not-json`,
			newConfigMetadata:  `still-not-json-but-different`,
			want:               true,
		},
		{
			name:               "无法解析时回退字符串比较_内容相同_跳过",
			lastLocation:       locA,
			newLocation:        locA,
			lastConfigMetadata: `not-json`,
			newConfigMetadata:  `not-json`,
			want:               false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clientInfoNeedsPersist(tt.lastLocation, tt.newLocation,
				tt.lastConfigMetadata, tt.newConfigMetadata)
			assert.Equal(t, tt.want, got)
		})
	}
}

// nopLogger 覆盖 persistHandler/updateLocation 触达的 Infof/Warnf/Errorf，嵌入 log.Logger 占位其余方法。
type nopLogger struct {
	log.Logger
}

func (nopLogger) Infof(string, ...interface{})  {}
func (nopLogger) Warnf(string, ...interface{})  {}
func (nopLogger) Errorf(string, ...interface{}) {}

// fakePersistRegistry 嵌入 InstancesRegistry 接口，仅覆盖本测试用到的 PersistMessage。
type fakePersistRegistry struct {
	localregistry.InstancesRegistry
}

func (f *fakePersistRegistry) PersistMessage(string, proto.Message) error { return nil }

// fakePersistValueContext 嵌入 ValueContext 接口，仅覆盖本测试用到的方法。
type fakePersistValueContext struct {
	sdk.ValueContext
}

func (f *fakePersistValueContext) GetClientId() string { return "race-client" }

func (f *fakePersistValueContext) SetCurrentLocation(*model.Location, model.SDKError) bool {
	return false
}

// fakePersistConfiguration 及其内部链嵌入配置接口，令 updateLocation 读到空 location providers 以进入读分支。
type fakePersistConfiguration struct{ config.Configuration }

func (fakePersistConfiguration) GetGlobal() config.GlobalConfig { return fakePersistGlobalConfig{} }

type fakePersistGlobalConfig struct{ config.GlobalConfig }

func (fakePersistGlobalConfig) GetLocation() config.LocationConfig {
	return fakePersistLocationConfig{}
}

type fakePersistLocationConfig struct{ config.LocationConfig }

func (fakePersistLocationConfig) GetProviders() []*config.LocationProviderConfigImpl { return nil }

// TestReportClientCallBack_PersistLocationConcurrent 回归 P1：
// persistHandlerWithLocationCheck（写 lastLocation/lastConfigMetadata）与 updateLocation（读 lastLocation）
// 可能被定时任务 Process 与 TriggerNow 的 doReportNow 并发触发。此测试在多协程下高频交错两条路径，
// 配合 go test -race 验证 persistMu 对这两个字段的保护（去掉锁即报 race）。
func TestReportClientCallBack_PersistLocationConcurrent(t *testing.T) {
	origin := log.GetBaseLogger()
	log.SetBaseLogger(nopLogger{})
	defer log.SetBaseLogger(origin)
	logCtx := &log.ContextLogger{}
	logCtx.Init()

	cb := &ReportClientCallBack{
		registry:      &fakePersistRegistry{},
		configuration: fakePersistConfiguration{},
		globalCtx:     &fakePersistValueContext{},
		logCtx:        logCtx,
	}
	buildResp := func(region, metadata string) *apiservice.Response {
		return &apiservice.Response{
			Client: &apiservice.Client{
				Location: &apimodel.Location{
					Region: &wrapperspb.StringValue{Value: region},
					Zone:   &wrapperspb.StringValue{Value: "z1"},
					Campus: &wrapperspb.StringValue{Value: "c1"},
				},
				ConfigMetadata: &wrapperspb.StringValue{Value: metadata},
			},
		}
	}

	const workers = 8
	const iters = 200
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				if id%2 == 0 {
					// 交替地域与订阅元数据，强制走入持久化写入分支
					region, metadata := "ap-guangzhou", `{"kind":"config","config_watch":[]}`
					if i%2 == 0 {
						region, metadata = "ap-shenzhen", `{"kind":"config","config_watch":[{"namespace":"default"}]}`
					}
					_ = cb.persistHandlerWithLocationCheck(buildResp(region, metadata))
				} else {
					cb.updateLocation(&model.Location{Region: "ap-guangzhou", Zone: "z1", Campus: "c1"}, nil)
				}
			}
		}(w)
	}
	wg.Wait()
}
