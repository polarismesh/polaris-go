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

package servicerouter

import (
	"errors"
	"testing"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/sdk"
)

// noopLogger 最小实现，避免 ContextLogger.GetBaseLogger() 返回 nil 导致测试 panic。
// 这里不需要真正输出日志，保留默认所有方法为 no-op 即可。
type noopLogger struct{}

func (noopLogger) Tracef(format string, args ...interface{}) {}
func (noopLogger) Debugf(format string, args ...interface{}) {}
func (noopLogger) Infof(format string, args ...interface{})  {}
func (noopLogger) Warnf(format string, args ...interface{})  {}
func (noopLogger) Errorf(format string, args ...interface{}) {}
func (noopLogger) Fatalf(format string, args ...interface{}) {}
func (noopLogger) IsLevelEnabled(l int) bool                 { return false }
func (noopLogger) SetLogLevel(l int) error                   { return nil }

// ensureLoggersInitialized 保证全局 log.*Logger() 返回非 nil，测试才不会在
// processServiceRouters 里的 logger.IsLevelEnabled 处 panic。
// 所有 logger 都用同一个 no-op 实现即可，不会影响断言逻辑。
func ensureLoggersInitialized() {
	var lg log.Logger = noopLogger{}
	log.SetBaseLogger(lg)
	log.SetStatLogger(lg)
	log.SetStatReportLogger(lg)
	log.SetDetectLogger(lg)
	log.SetNetworkLogger(lg)
	log.SetCacheLogger(lg)
	log.SetEventLogger(lg)
	log.SetLosslessLogger(lg)
	log.SetRouteLogger(lg)
}

// stubServiceRouter 伪 ServiceRouter，用于单元测试 processServiceRouters 的控制流。
// 所有与实际路由算法无关的行为都被短路，便于集中断言 RouteInfo / Cluster 的状态迁移。
// 注意: 这里**不**嵌入 *plugin.PluginBase——直接实现 plugin.Plugin 接口, 避免因
// 零值的 PluginBase 指针为 nil, 导致 processServiceRouters 调 router.ID() 时 panic。
type stubServiceRouter struct {
	id     int32
	name   string
	enable bool
	// onFilter 被 GetFilteredInstances 调用时触发，方便测试用例记录副作用。
	onFilter func(routeInfo *RouteInfo)
}

func (s *stubServiceRouter) Type() common.Type                                 { return common.TypeServiceRouter }
func (s *stubServiceRouter) ID() int32                                         { return s.id }
func (s *stubServiceRouter) GetSDKContextID() string                           { return "" }
func (s *stubServiceRouter) Name() string                                      { return s.name }
func (s *stubServiceRouter) Init(ctx *plugin.InitContext) error                { return nil }
func (s *stubServiceRouter) Start() error                                      { return nil }
func (s *stubServiceRouter) Destroy() error                                    { return nil }
func (s *stubServiceRouter) IsEnable(cfg config.Configuration) bool            { return true }
func (s *stubServiceRouter) Enable(r *RouteInfo, c model.ServiceClusters) bool { return s.enable }
func (s *stubServiceRouter) GetFilteredInstances(
	routeInfo *RouteInfo, svcClusters model.ServiceClusters, cluster *model.Cluster,
) (*RouteResult, error) {
	if s.onFilter != nil {
		s.onFilter(routeInfo)
	}
	result := &RouteResult{}
	result.OutputCluster = cluster
	result.Status = Normal
	return result, nil
}

// trackingFilterOnly 伪 FilterOnlyRouter，被调用即置 called=true 并 mutate
// ignoreFilterOnlyOnEndChain，用于判定"filter-only 兜底是否被触发"。
// 同 stubServiceRouter, 不嵌 *plugin.PluginBase, 直接自实现 plugin.Plugin 全部方法。
type trackingFilterOnly struct {
	id     int32
	called bool
}

func (s *trackingFilterOnly) Type() common.Type                                 { return common.TypeServiceRouter }
func (s *trackingFilterOnly) ID() int32                                         { return s.id }
func (s *trackingFilterOnly) GetSDKContextID() string                           { return "" }
func (s *trackingFilterOnly) Name() string                                      { return "trackingFilterOnly" }
func (s *trackingFilterOnly) Init(ctx *plugin.InitContext) error                { return nil }
func (s *trackingFilterOnly) Start() error                                      { return nil }
func (s *trackingFilterOnly) Destroy() error                                    { return nil }
func (s *trackingFilterOnly) IsEnable(cfg config.Configuration) bool            { return true }
func (s *trackingFilterOnly) Enable(r *RouteInfo, c model.ServiceClusters) bool { return true }
func (s *trackingFilterOnly) GetFilteredInstances(
	routeInfo *RouteInfo, svcClusters model.ServiceClusters, cluster *model.Cluster,
) (*RouteResult, error) {
	s.called = true
	// 对齐真实 filteronly 行为：被调即置位 ignoreFilterOnlyOnEndChain。
	routeInfo.SetIgnoreFilterOnlyOnEndChain(true)
	return &RouteResult{OutputCluster: cluster, Status: Normal}, nil
}

// stubSupplier 是 plugin.Supplier 的最小实现，processServiceRouters 只通过它拿事件订阅者。
// 这里返回空列表即可满足调用路径，不需要真的注册插件。
type stubSupplier struct{}

func (s *stubSupplier) GetPlugin(typ common.Type, name string) (plugin.Plugin, error) {
	return nil, errors.New("stubSupplier: GetPlugin not implemented")
}
func (s *stubSupplier) GetPlugins(typ common.Type) ([]plugin.Plugin, error) {
	return nil, nil
}
func (s *stubSupplier) GetPluginById(id int32) (plugin.Plugin, error) {
	return nil, errors.New("stubSupplier: GetPluginById not implemented")
}
func (s *stubSupplier) GetPluginsByType(typ common.Type) []string {
	return nil
}
func (s *stubSupplier) GetEventSubscribers(event common.PluginEventType) []common.PluginEventHandler {
	return nil
}
func (s *stubSupplier) RegisterEventSubscriber(
	event common.PluginEventType, handler common.PluginEventHandler) {
}

// buildTestCtx 构造带 stubSupplier 的 ValueContext，满足 GetFilterCluster* 的依赖。
func buildTestCtx() sdk.ValueContext {
	ensureLoggersInitialized()
	ctx := sdk.NewValueContext()
	// 重新 Init 一次 ContextLogger，让它捕获 ensureLoggersInitialized 设置的全局 logger。
	*ctx.GetContextLogger() = log.ContextLogger{}
	ctx.GetContextLogger().Init()
	ctx.SetValue(sdk.ContextKeyPlugins, &stubSupplier{})
	return ctx
}

// buildTestClusters 构造一个带一个实例的 ServiceClusters，让 Validate/cluster 操作能通过。
func buildTestClusters() model.ServiceClusters {
	svcInfo := model.ServiceInfo{Namespace: "default", Service: "test"}
	return model.NewDefaultServiceInstances(svcInfo, nil).GetServiceClusters()
}

// newTestRouteInfo 返回一份已注入 FilterOnlyRouter 的最小可用 RouteInfo，
// 避免 Validate 报 FilterOnlyRouter is required。
func newTestRouteInfo(fo *trackingFilterOnly) *RouteInfo {
	return &RouteInfo{
		DestService:      &model.ServiceInfo{Namespace: "default", Service: "test"},
		FilterOnlyRouter: fo,
	}
}

// TestGetFilterClusterBefore_NoFilterOnlyFallback 验证核心修复语义：
// 前置链（beforeChain）即使所有 router 都 Enable() = false / 被跳过，也绝不能
// 触发 FilterOnlyRouter 的兜底——否则 FilterOnly 会调用
// SetIgnoreFilterOnlyOnEndChain(true)，让上层 getServiceRoutedInstances 把主链
// （ruleBasedRouter / nearbyBasedRouter / dstMetaRouter 等）整条跳过。
func TestGetFilterClusterBefore_NoFilterOnlyFallback(t *testing.T) {
	ctx := buildTestCtx()
	clusters := buildTestClusters()
	fo := &trackingFilterOnly{}
	routeInfo := newTestRouteInfo(fo)

	// 前置链只有一个 Enable() = false 的 router：对应 laneRouter 在"没有泳道规则"时
	// 的真实行为。
	disabledRouter := &stubServiceRouter{name: "laneRouter", enable: false}

	result, err := GetFilterClusterBefore(ctx, []ServiceRouter{disabledRouter}, routeInfo, clusters)
	if err != nil {
		t.Fatalf("GetFilterClusterBefore failed: %v", err)
	}
	if result == nil {
		t.Fatal("GetFilterClusterBefore returned nil result")
	}
	if fo.called {
		t.Errorf("FilterOnlyRouter 被意外触发，GetFilterClusterBefore 应当在链尾跳过全死全活兜底")
	}
	if routeInfo.IsIgnoreFilterOnlyOnEndChain() {
		t.Errorf("ignoreFilterOnlyOnEndChain 被意外置位；将导致主链被跳过")
	}
}

// TestGetFilterCluster_FallsBackToFilterOnly 对照测试：主链入口 GetFilterCluster
// 必须保持历史行为——在链尾追加一次 FilterOnlyRouter 以保证"全死全活"语义。
// 否则回归到没有全活兜底的场景会让 ruleBased 过滤不到实例时直接返回空。
func TestGetFilterCluster_FallsBackToFilterOnly(t *testing.T) {
	ctx := buildTestCtx()
	clusters := buildTestClusters()
	fo := &trackingFilterOnly{}
	routeInfo := newTestRouteInfo(fo)

	disabledRouter := &stubServiceRouter{name: "ruleBasedRouter", enable: false}

	_, err := GetFilterCluster(ctx, []ServiceRouter{disabledRouter}, routeInfo, clusters)
	if err != nil {
		t.Fatalf("GetFilterCluster failed: %v", err)
	}
	if !fo.called {
		t.Errorf("GetFilterCluster 在链尾未触发 FilterOnlyRouter 兜底；主链必须保留全死全活语义")
	}
}
