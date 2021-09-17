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

package local

import (
	"github.com/polarismesh/polaris-go/pkg/metric"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"sync"
	"sync/atomic"
)

//本地实例数据，包括熔断，动态权重等信息
type InstanceLocalValue interface {
	//获取统计滑窗
	GetSliceWindows(int32) []*metric.SliceWindow
	//实例的熔断状态
	GetCircuitBreakerStatus() model.CircuitBreakerStatus
	//实例的健康检查状态
	GetActiveDetectStatus() model.ActiveDetectStatus
	GetExtendedData(pluginIndex int32) interface{}
	SetExtendedData(pluginIndex int32, data interface{})
}

//创建默认的实例本地信息
func NewInstanceLocalValue() InstanceLocalValue {
	return &DefaultInstanceLocalValue{
		sliceWindows: make(map[int32][]*metric.SliceWindow, 0),
		extendedData: &sync.Map{},
	}
}

//用于存储实例的本地信息
type DefaultInstanceLocalValue struct {
	//各插件统计窗口, 每个插件给于通过插件名进行唯一标识
	sliceWindows map[int32][]*metric.SliceWindow
	//各个插件的特定数据
	extendedData *sync.Map
	cbStatus     atomic.Value
	odStatus     atomic.Value
}

//获取滑窗
func (lv *DefaultInstanceLocalValue) GetSliceWindows(pluginIndex int32) []*metric.SliceWindow {
	return lv.sliceWindows[pluginIndex]
}

//获取插件数据
func (lv *DefaultInstanceLocalValue) GetExtendedData(pluginIndex int32) interface{} {
	res, load := lv.extendedData.Load(pluginIndex)
	if load {
		return res
	}
	return nil
}

//设置插件数据
func (lv *DefaultInstanceLocalValue) SetExtendedData(pluginIndex int32, data interface{}) {
	lv.extendedData.Store(pluginIndex, data)
}

//获取滑窗
func (lv *DefaultInstanceLocalValue) SetSliceWindows(pluginIndex int32, windows []*metric.SliceWindow) {
	lv.sliceWindows[pluginIndex] = windows
}

//设置熔断信息
func (lv *DefaultInstanceLocalValue) SetCircuitBreakerStatus(st model.CircuitBreakerStatus) {
	lv.cbStatus.Store(st)
}

//设置健康检测信息
func (lv *DefaultInstanceLocalValue) SetActiveDetectStatus(st model.ActiveDetectStatus) {
	lv.odStatus.Store(st)
}

//返回熔断信息
func (lv *DefaultInstanceLocalValue) GetCircuitBreakerStatus() model.CircuitBreakerStatus {
	res := lv.cbStatus.Load()
	if nil == res {
		return nil
	}
	return res.(model.CircuitBreakerStatus)
}

//返回健康检测信息
func (lv *DefaultInstanceLocalValue) GetActiveDetectStatus() model.ActiveDetectStatus {
	res := lv.odStatus.Load()
	if nil == res {
		return nil
	}
	return res.(model.ActiveDetectStatus)
}

//服务localvalue接口
type ServiceLocalValue interface {
	//通过插件ID获取服务级缓存数据
	GetServiceDataByPluginId(pluginIdx int32) interface{}
	//设置通过插件ID索引的服务级缓存数据
	SetServiceDataByPluginId(pluginIdx int32, data interface{})
	//通过插件类型来获取服务级缓存数据
	GetServiceDataByPluginType(pluginType common.Type) interface{}
	//设置通过插件类型来索引的服务级缓存数据
	SetServiceDataByPluginType(pluginType common.Type, data interface{})
}

//服务localvalue实现
type DefaultServiceLocalValue struct {
	svcDataByPluginId   *sync.Map
	svcDataByPluginType *sync.Map
}

//创建服务localvalue
func NewServiceLocalValue() *DefaultServiceLocalValue {
	return &DefaultServiceLocalValue{
		svcDataByPluginId:   &sync.Map{},
		svcDataByPluginType: &sync.Map{},
	}
}

//获取服务localvalue数据
func (sv *DefaultServiceLocalValue) GetServiceDataByPluginId(pluginIdx int32) interface{} {
	res, ok := sv.svcDataByPluginId.Load(pluginIdx)
	if ok {
		return res
	}
	return nil
}

//设置服务localvalue数据
func (sv *DefaultServiceLocalValue) SetServiceDataByPluginId(pluginIdx int32, data interface{}) {
	sv.svcDataByPluginId.Store(pluginIdx, data)
}

//获取服务localvalue数据
func (sv *DefaultServiceLocalValue) GetServiceDataByPluginType(pluginType common.Type) interface{} {
	res, ok := sv.svcDataByPluginType.Load(pluginType)
	if ok {
		return res
	}
	return nil
}

//设置服务localvalue数据
func (sv *DefaultServiceLocalValue) SetServiceDataByPluginType(pluginType common.Type, data interface{}) {
	sv.svcDataByPluginType.Store(pluginType, data)
}
