/**
 * Tencent is pleased to support the open source community by making CL5 available.
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

package plugin

import (
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"sync"
	"time"
)

type PluginAPI int

const (
	MethodReportAlarm PluginAPI = iota
	MethodStat
	MethodCircuitBreak
	MethodChooseInstance
	MethodGetServices
	MethodGetInstances
	MethodLoadInstances
	MethodUpdateInstances
	MethodPersistMessage
	MethodLoadPersistedMessage
	MethodGetServiceRouteRule
	MethodLoadServiceRouteRule
	MethodLoadMeshConfig
	MethodGetServiceRateLimitRule
	MethodLoadServiceRateLimitRule
	MethodDetectInstance
	MethodInitQuota
	MethodRegisterServiceHandler
	MethodDeRegisterServiceHandler
	MethodRegisterInstance
	MethodDeregisterInstance
	MethodHeartbeat
	MethodReportClient
	MethodUpdateServers
	MethodGetRateLimitConnector
	MethodEnable
	MethodGetFilteredInstances
	MethodReportStat
	MethodRealTimeAdjustDynamicWeight
	MethodTimingAdjustDynamicWeight
	MethodWatchService
)

//将plugin的api编号转化为名字
var methodMap = map[PluginAPI]string{
	MethodReportAlarm:                 "ReportAlarm",
	MethodStat:                        "Stat",
	MethodCircuitBreak:                "CircuitBreak",
	MethodChooseInstance:              "ChooseInstance",
	MethodGetServices:                 "GetServices",
	MethodGetInstances:                "GetInstances",
	MethodLoadInstances:               "LoadInstances",
	MethodUpdateInstances:             "UpdateInstances",
	MethodPersistMessage:              "PersistMessage",
	MethodLoadPersistedMessage:        "LoadPersistedMessage",
	MethodGetServiceRouteRule:         "GetServiceRouteRule",
	MethodLoadServiceRouteRule:        "LoadServiceRouteRule",
	MethodLoadMeshConfig: 			   "LoadMeshConfig",
	MethodGetServiceRateLimitRule:     "GetServiceRateLimitRule",
	MethodLoadServiceRateLimitRule:    "LoadServiceRateLimitRule",
	MethodDetectInstance:              "DetectInstance",
	MethodInitQuota:                   "InitQuota",
	MethodRegisterServiceHandler:      "RegisterServiceHandler",
	MethodDeRegisterServiceHandler:    "DeRegisterServiceHandler",
	MethodRegisterInstance:            "RegisterInstance",
	MethodDeregisterInstance:          "DeregisterInstance",
	MethodHeartbeat:                   "Heartbeat",
	MethodReportClient:                "ReportClient",
	MethodUpdateServers:               "UpdateServers",
	MethodGetRateLimitConnector:       "GetRateLimitConnector",
	MethodEnable:                      "Enable",
	MethodGetFilteredInstances:        "GetFilteredInstances",
	MethodReportStat:                  "ReportStat",
	MethodRealTimeAdjustDynamicWeight: "RealTimeAdjustDynamicWeight",
	MethodTimingAdjustDynamicWeight:   "TimingAdjustDynamicWeight",
	MethodWatchService:                "WatchService",
}

//获取插件api的名字
func GetPluginAPIName(p PluginAPI) string {
	return methodMap[p]
}

type PluginAPIDelayRange int

//API延时范围常量
const (
	PluginApiDelayBelow10 PluginAPIDelayRange = iota
	PluginApiDelayBelow20
	PluginApiDelayBelow30
	PluginApiDelayBelow40
	PluginApiDelayBelow50
	PluginApiDelayOver50
	PluginApiDelayMax
)

//将api的延时范围转化为string
func (d PluginAPIDelayRange) String() string {
	return delayRangeMap[d]
}

const (
	maxDelayRange = 50 * time.Millisecond
	timeSetp      = 10 * time.Millisecond
)

//将api的延时范围转化为string
var delayRangeMap = map[PluginAPIDelayRange]string{
	PluginApiDelayBelow10: "[0ms,10ms)",
	PluginApiDelayBelow20: "[10ms,20ms)",
	PluginApiDelayBelow30: "[20ms,30ms)",
	PluginApiDelayBelow40: "[30ms,40ms)",
	PluginApiDelayBelow50: "[40ms,50ms)",
	PluginApiDelayOver50:  "[50ms,)",
}

//将插件接口调用延时转化为PluginAPIDelayRange
func GetPluginAPIDelayRange(delay time.Duration) PluginAPIDelayRange {
	if delay > maxDelayRange {
		delay = maxDelayRange
	}
	diff := delay.Nanoseconds() / timeSetp.Nanoseconds()
	return PluginAPIDelayRange(diff)
}

//插件接口统计数据
type PluginMethodGauge struct {
	model.EmptyInstanceGauge
	PluginType   common.Type
	PluginId     int32
	PluginMethod PluginAPI
	RetCode      model.ErrCode
	Success      bool
	DelayRange   PluginAPIDelayRange
}

//获取PluginMethodGauge的pool
var pluginStatPool = &sync.Pool{}

//从pluginStatPool中获取PluginMethodGauge
func getPluginStatFromPool() *PluginMethodGauge {
	value := pluginStatPool.Get()
	if nil == value {
		return &PluginMethodGauge{}
	}
	return value.(*PluginMethodGauge)
}

//将PluginMethodGauge放回pluginStatPool中
func PoolPutPluginMethodGauge(g *PluginMethodGauge) {
	pluginStatPool.Put(g)
}

////上报一次插件接口调用结果
//func ReportPluginStat(p plugin.Plugin, engine model.Engine, api PluginAPI, err error, delay time.Duration) {
//	errCode := model.ErrCodeSuccess
//	success := true
//	if err != nil {
//		errCode = err.(model.SDKError).ErrorCode()
//		success = false
//	}
//	statGauge := getPluginStatFromPool()
//	statGauge.PluginType = p.Type()
//	statGauge.PluginId = p.ID()
//	statGauge.PluginMethod = api
//	statGauge.RetCode = errCode
//	statGauge.Success = success
//	statGauge.DelayRange = GetPluginAPIDelayRange(delay)
//	engine.SyncReportStat(model.PluginAPIStat, statGauge)
//	PoolPutPluginMethodGauge(statGauge)
//}
