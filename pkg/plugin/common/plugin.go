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

package common

import (
	"context"
	"github.com/modern-go/reflect2"
	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"sync/atomic"
)

//Type 插件类型，每个扩展点有自己独立的插件类型
type Type uint32

const (
	// TypePluginBase
	TypePluginBase Type = 0x1000
	//TypeServerConnector 注册中心连接器扩展点
	TypeServerConnector Type = 0x1001
	//TypeLocalRegistry 本地缓存扩展点
	TypeLocalRegistry Type = 0x1002
	//TypeServiceRouter 服务路由扩展点
	TypeServiceRouter Type = 0x1003
	//TypeLoadBalancer 负载均衡扩展点
	TypeLoadBalancer Type = 0x1004
	//TypeHealthCheck 健康探测扩展点
	TypeHealthCheck Type = 0x1005
	//TypeCircuitBreaker 节点熔断扩展点
	TypeCircuitBreaker Type = 0x1006
	//TypeWeightAdjuster 动态权重调整扩展点
	TypeWeightAdjuster Type = 0x1007
	//TypeStatReporter 统计上报扩展点
	TypeStatReporter Type = 0x1008
	//TypeAlarmReporter 告警扩展点
	TypeAlarmReporter Type = 0x1009
	//TypeRateLimiter 限流扩展点
	TypeRateLimiter Type = 0x1010

	TypeSubScribe Type = 0x1011
)

var typeToPresent = map[Type]string{
	TypePluginBase:      "TypePluginBase",
	TypeServerConnector: "serverConnector",
	TypeLocalRegistry:   "localRegistry",
	TypeServiceRouter:   "serviceRouter",
	TypeLoadBalancer:    "loadBalancer",
	TypeHealthCheck:     "healthChecker",
	TypeCircuitBreaker:  "circuitBreaker",
	TypeWeightAdjuster:  "weightAdjuster",
	TypeStatReporter:    "statReporter",
	TypeAlarmReporter:   "alarmReporter",
	TypeRateLimiter:     "rateLimiter",
	TypeSubScribe:       "subScribe",
}

//ToString方法
func (t Type) String() string {
	return typeToPresent[t]
}

type PluginEventType int

const (
	//本地缓存实例创建后触发的时机
	OnInstanceLocalValueCreated PluginEventType = 0x8001
	//在所有插件创建完毕后触发的事件
	OnContextStarted PluginEventType = 0x8002
	//sdk内存中添加了一个服务（实例或路由）触发的事件
	OnServiceAdded PluginEventType = 0x8003
	//sdk内存中更新了一个服务（实例或路由）触发的事件
	OnServiceUpdated PluginEventType = 0x8004
	//sdk内存中删除了一个服务（实例或路由）触发的事件
	OnServiceDeleted PluginEventType = 0x8005
	//一个经过路由的cluster返回给用户
	OnRoutedClusterReturned PluginEventType = 0x8006
	//一个服务的localvalue创建触发的事件
	OnServiceLocalValueCreated PluginEventType = 0x8007
	//一个限流规则的限流窗口创建时触发的事件
	OnRateLimitWindowCreated PluginEventType = 0x8008
	//一个限流规则的限流窗口被删除时触发的事件
	OnRateLimitWindowDeleted PluginEventType = 0x8009
)

//插件事件
type PluginEvent struct {
	//事件类型
	EventType PluginEventType
	//事件对象
	EventObject interface{}
}

//服务变更对象，对于OnServiceAdded，OnServiceUpdated，OnServiceDeleted的事件，会传递该对象
type ServiceEventObject struct {
	// 事件对象信息
	SvcEventKey model.ServiceEventKey
	// 缓存中已有的对象，如果是新增，则为nil
	OldValue interface{}
	// 新加入缓存的对象，如果是删除，则为nil
	NewValue interface{}
	// 新旧缓存信息的区别
	DiffInfo interface{}
}

// 版本号变化
type RevisionChange struct {
	OldRevision string
	NewRevision string
}

// 限流规则的变化信息
type RateLimitDiffInfo struct {
	//哪些规则的版本变化了，key为ruleID，value为RevisionChange
	UpdatedRules map[string]*RevisionChange
	//哪些规则被删除了，key为ruleID，value为revision
	DeletedRules map[string]string
}

// 网格规则的变化信息
type MeshResourceDiffInfo struct {
	// 网格ID
	MeshID string
	// 网格规则类型
	ResourceType *namingpb.MeshResource
	//哪些规则的版本变化了，key为规则的名字，value为RevisionChange
	UpdatedResources map[string]*RevisionChange
	//哪些规则被删除了，key为规则名字，value为revision
	DeletedResources map[string]string
}

//触发插件事件的回调
type PluginEventHandler struct {
	Callback func(event *PluginEvent) error
}

//控制插件启动销毁的运行上下文
type RunContext struct {
	ctx    context.Context
	cancel context.CancelFunc
}

//创建插件运行上下文
func NewRunContext() *RunContext {
	ctx := &RunContext{}
	ctx.ctx, ctx.cancel = context.WithCancel(context.Background())
	return ctx
}

//销毁运行上下文
func (c *RunContext) Destroy() error {
	c.cancel()
	return nil
}

//判断是否已经销毁
func (c *RunContext) IsDestroyed() bool {
	select {
	case <-c.ctx.Done():
		return true
	default:
		return false
	}
}

//获取控制channel
func (c *RunContext) Done() <-chan struct{} {
	return c.ctx.Done()
}

//Notifier 通知回调器的函数
type Notifier struct {
	sdkError atomic.Value
	ctx      context.Context
	cancel   context.CancelFunc
}

//创建通知器
func NewNotifier() *Notifier {
	notifier := &Notifier{}
	notifier.ctx, notifier.cancel = context.WithCancel(context.Background())
	return notifier
}

//获取回调错误
func (n *Notifier) GetError() model.SDKError {
	sdkErrValue := n.sdkError.Load()
	if reflect2.IsNil(sdkErrValue) {
		return nil
	}
	return sdkErrValue.(model.SDKError)
}

//获取回调上下文
func (n *Notifier) GetContext() context.Context {
	return n.ctx
}

//执行回调通知
func (n *Notifier) Notify(sdkErr model.SDKError) {
	if nil != sdkErr {
		n.sdkError.Store(sdkErr)
	}
	n.cancel()
}

//要加载的插件类型
var LoadedPluginTypes = []Type{
	TypeServerConnector,
	TypeServiceRouter,
	TypeLoadBalancer,
	TypeHealthCheck,
	TypeCircuitBreaker,
	TypeWeightAdjuster,
	TypeStatReporter,
	TypeAlarmReporter,
	TypeLocalRegistry,
	TypeRateLimiter,
	TypeSubScribe,
}
