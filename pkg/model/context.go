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

package model

import (
	"context"
	"github.com/polarismesh/polaris-go/pkg/clock"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// 主流程引擎的上下文key
	// SDK初始化后，会将主流程引擎对象放入上下文中，供插件按需使用
	ContextKeyEngine = "engine"
	//SDK的唯一标识id
	ContextKeyToken = "SDKToken"
	//sdkcontext上面的pluginManager
	ContextKeyPlugins = "plugins"
	//sdkContext创建开始时间
	ContextKeyTakeEffectTime = "SDKTakeEffectTime"
	//sdkContext创建结束时间
	ContextKeyFinishInitTime = "SDKFinishInitTime"
)

//sdkContext的唯一标识
type SDKToken struct {
	IP       string
	PID      int32
	UID      string
	Client   string
	Version  string
	PodName  string
	HostName string
}

//地域信息
type LocationInfo interface {
	//获取地域明细
	GetLocation() *Location
	//在地域信息获取过程中的错误信息
	GetLastError() SDKError
	//获取地域信息状态
	GetStatus() uint32
	//查看地域信息是否已初始化状态
	IsLocationInitialized() bool
	//查看地域信息是否ready状态
	IsLocationReady() bool
}

//用于主流程传递kv数据的上下文对象，线程安全
type ValueContext interface {
	//设置kv值
	SetValue(key string, value interface{})
	//获取kv值
	GetValue(key string) (interface{}, bool)
	//获取当前节点地域信息
	GetCurrentLocation() LocationInfo
	//获取客户端ID
	GetClientId() string
	//获取引擎接口
	GetEngine() Engine
	//等待location是否达到locationStatus
	WaitLocationInfo(ctx context.Context, locationStatus uint32) bool
	//设置当前节点地域信息
	//返回是否由非ready转换为ready
	SetCurrentLocation(*Location, SDKError) bool
	//获取当前时间戳
	Now() time.Time
	//计算时间间隔
	Since(time.Time) time.Duration
}

//创建kv上下文对象
func NewValueContext() ValueContext {
	ctx := &valueContext{
		coreMap: &sync.Map{},
	}
	ctx.clock = clock.GetClock()
	ctx.currentLocation.Store(&locationInfo{
		locationStatus: LocationInit,
	})
	ctx.locationInitializedNotify.Context, ctx.locationInitializedNotify.cancel = context.WithCancel(context.Background())
	ctx.locationReadyNotify.Context, ctx.locationReadyNotify.cancel = context.WithCancel(context.Background())
	return ctx
}

const (
	//地域信息初始化，未获取到地域信息
	LocationInit uint32 = iota
	//地域信息获取失败，出现异常
	LocationError
	//地域信息获取成功
	LocationReady
	//地域信息获取成功，但是是空的，即没有在cmdb上面发现地域信息
	LocationEmpty
)

//地域信息包装类型，含控制及状态信息
type locationInfo struct {
	//地域详情
	location *Location
	//上一次获取失败
	lastErr SDKError
	//地域信息状态
	locationStatus uint32
}

//获取地域明细
func (l *locationInfo) GetLocation() *Location {
	return l.location
}

//在地域信息获取过程中的错误信息
func (l *locationInfo) GetLastError() SDKError {
	return l.lastErr
}

//获取地域信息状态
func (l *locationInfo) GetStatus() uint32 {
	return l.locationStatus
}

//查看地域信息是否已经初始化过
func (l *locationInfo) IsLocationInitialized() bool {
	return l.GetStatus() > LocationInit
}

//查看地域信息是否ready状态
func (l *locationInfo) IsLocationReady() bool {
	return l.GetStatus() == LocationReady
}

//通过context.Done通知一个事件的发生
type contextNotifier struct {
	context.Context
	cancel     context.CancelFunc
	onceNotify sync.Once
}

//通知事件发生
func (c *contextNotifier) Notify() {
	c.onceNotify.Do(func() {
		c.cancel()
	})
}

//ValueContext的实现类
type valueContext struct {
	//当前地域信息
	currentLocation atomic.Value
	//用于查看location是否initialized
	locationInitializedNotify contextNotifier
	//用于查看location是否ready
	locationReadyNotify contextNotifier
	//当前时间戳，存放类型为*time.Time
	currentTimestamp atomic.Value
	//时钟，用于获取当前时间戳
	clock clock.Clock
	//使用线程安全的map进行值的存储
	coreMap *sync.Map
}

//等待地域信息状态
func (v *valueContext) WaitLocationInfo(ctx context.Context, locationStatus uint32) bool {
	switch locationStatus {
	case LocationInit:
		<-v.locationInitializedNotify.Done()
		return true
	case LocationError:
		<-v.locationInitializedNotify.Done()
		return v.GetCurrentLocation().GetStatus() == LocationError
	case LocationReady:
		for {
			select {
			case <-ctx.Done():
				return false
			case <-v.locationReadyNotify.Done():
				return true
			}
		}
	}
	return false
}

//设置kv值
func (v *valueContext) SetValue(key string, value interface{}) {
	v.coreMap.Store(key, value)
}

//获取kv值
func (v *valueContext) GetValue(key string) (interface{}, bool) {
	return v.coreMap.Load(key)
}

//获取当前节点地域信息
func (v *valueContext) GetCurrentLocation() LocationInfo {
	value := v.currentLocation.Load()
	return value.(LocationInfo)
}

//设置当前节点地域信息
func (v *valueContext) SetCurrentLocation(location *Location, lastErr SDKError) bool {
	locInfo := &locationInfo{
		location: location,
		lastErr:  lastErr,
	}
	if nil != lastErr {
		locInfo.locationStatus = LocationError
	} else if location != nil && !location.IsEmpty() {
		locInfo.locationStatus = LocationReady
	} else {
		locInfo.locationStatus = LocationEmpty
	}
	lastLocationStatus := v.currentLocation.Load().(LocationInfo).GetStatus()
	var becomeReady bool
	switch lastLocationStatus {
	case LocationReady:
		//LocationReady状态，只能接受ready的更新
		if locInfo.locationStatus == LocationReady {
			v.currentLocation.Store(locInfo)
		}
		becomeReady = false
	default:
		//其他2个状态可接受任意更新
		v.currentLocation.Store(locInfo)
		becomeReady = locInfo.locationStatus == LocationReady
	}
	v.locationInitializedNotify.Notify()
	if becomeReady {
		v.locationReadyNotify.Notify()
	}
	return becomeReady
}

//获取当前时间戳
func (v *valueContext) Now() time.Time {
	return v.clock.Now()
}

//计算时间间隔
func (v *valueContext) Since(startTime time.Time) time.Duration {
	return v.Now().Sub(startTime)
}

//查看地域信息是否ready状态
func (v *valueContext) IsLocationReady() bool {
	return v.GetCurrentLocation().IsLocationReady()
}

//获取客户端ID
func (v *valueContext) GetClientId() string {
	tokenValue, ok := v.GetValue(ContextKeyToken)
	if !ok {
		return ""
	}
	sdkToken := tokenValue.(SDKToken)
	return sdkToken.UID
}

//获取客户端ID
func (v *valueContext) GetEngine() Engine {
	value, ok := v.GetValue(ContextKeyEngine)
	if !ok {
		return nil
	}
	return value.(Engine)
}