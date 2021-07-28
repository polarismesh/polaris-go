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

package monitor

import (
	"github.com/polarismesh/polaris-go/pkg/model"
	monitorpb "github.com/polarismesh/polaris-go/plugin/statreporter/pb/v1"
	"strconv"
)

const (
	keySDKSuccess int = iota
	keySDKFail
	numKeySDK
)

const (
	numKeySvc     int = 2
	keySvcSuccess int = 0
	keySvcFail    int = 1
)

const (
	numKeySvcDelay     int = 2
	keySvcSuccessDelay int = 0
	keySvcFailDelay    int = 1
)

//服务统计指标维度
var (
	svcIdx      = []int{keySvcSuccess, keySvcFail, keySvcSuccessDelay, keySvcFailDelay}
	svcDelayIdx = []int{keySvcSuccessDelay, keySvcFailDelay}
	svcResIdx   = []int{keySvcSuccess, keySvcFail}
	svcSuccess  = []bool{true, false}
)

//用于将sdk统计信息放入滑桶中
//SDK存储序列如下：
//|           ------------                   ApiGetOneInstance                                   ------------ |
//|           ------------  ErrCodeSuccess   ------------          | ------------ ErrCodeUnknown ------------ |
//| --<50ms--  | --<100ms-- | --<150ms-- | --<200ms-- |-->=200ms-- |
//| suc | fail | suc | fail | suc | fail | suc | fail | suc | fail |
func addSDKStatToBucket(gauge model.InstanceGauge, dims *dimensionRecord) {
	idx := calcSDKMetricIndex(gauge)
	dims.add32Dimensions(idx, 1)
}

//计算SDK统计下标
func calcSDKMetricIndex(gauge model.InstanceGauge) int {
	errCode := gauge.GetRetCodeValue()
	rangeIndex := int(gauge.GetDelayRange())
	statusIndex := keySDKSuccess
	if gauge.GetRetStatus() == model.RetFail {
		statusIndex = keySDKFail
	}
	return calcSDKMetricIndexByValue(int(gauge.GetAPI()), int(errCode), rangeIndex, statusIndex)
}

const (
	allIndexSize         = int(model.ApiOperationMax) * model.ErrCodeCount * int(model.ApiDelayMax) * numKeySDK
	allIndexApiOperation = model.ErrCodeCount * int(model.ApiDelayMax) * numKeySDK
	allIndexErrCode      = int(model.ApiDelayMax) * numKeySDK
	allIndexDelayRange   = numKeySDK
)

//用于将sdkmetric计算出来的一维idx还原为多维数组的idx
var reveseIdx = make([][]int, allIndexSize, allIndexSize)
var sdkDimensions = make([]int, allIndexSize)

const (
	apiOperationIdx int = iota
	errcodeIdx
	delayIdx
)

//计算SDK统计下标
func calcSDKMetricIndexByValue(apiOperation int, errCode int, rangeIndex int, statusIndex int) int {
	var errCodeIndex int
	if errCode > 0 {
		errCodeIndex = errCode%model.BaseIndexErrCode + 1
	}
	return apiOperation*allIndexApiOperation +
		(errCodeIndex * allIndexErrCode) + (rangeIndex * allIndexDelayRange) + statusIndex
}

//用于将server调用信息放入滑桶中
func addSvcStatToBucket(gauge model.InstanceGauge, dims *dimensionRecord) {
	if gauge.GetRetStatus() == model.RetFail {
		dims.add32Dimensions(keySDKFail, 1)
		dims.add64Dimensions(keySvcFailDelay, int64(*gauge.GetDelay()))
		return
		//bucket.AddMetric(keySvcFailDelay, int64(*gauge.GetDelay()))
		//return bucket.AddMetric(keySvcFail, 1)
	}
	dims.add32Dimensions(keySDKSuccess, 1)
	dims.add64Dimensions(keySvcSuccessDelay, int64(*gauge.GetDelay()))
	//bucket.AddMetric(keySvcSuccessDelay, int64(*gauge.GetDelay()))
	//return bucket.AddMetric(keySvcSuccess, 1)
}

//sdk统计数据转化为日志形式
func sdkStatToString(data *monitorpb.SDKAPIStatistics) string {
	return "uid: " + data.GetKey().GetUid() + " | id: " + data.GetId().GetValue() + " | client_type: " +
		data.GetKey().GetClientType().GetValue() + " | client_version: " + data.GetKey().GetClientVersion().GetValue() +
		" | api: " + data.GetKey().GetSdkApi().GetValue() + " | ret_code: " + data.GetKey().GetResCode().GetValue() +
		" | success: " + strconv.FormatBool(data.GetKey().GetSuccess().GetValue()) + " | result_type: " +
		data.Key.Result.String() + " | delay_range: " + data.GetKey().GetDelayRange().GetValue() +
		" | total_requests_in_minute: " + strconv.Itoa(int(data.GetValue().GetTotalRequestPerMinute().GetValue()))
}

//服务调用统计数据转化为日志格式
func svcStatToString(data *monitorpb.ServiceStatistics) string {
	return "id: " + data.GetId().GetValue() + " | caller_host: " + data.GetKey().GetCallerHost().GetValue() +
		" | namespace: " + data.GetKey().GetNamespace().GetValue() + " | service: " +
		data.GetKey().GetService().GetValue() + " | instance_host: " + data.GetKey().GetInstanceHost().GetValue() +
		" | resCode: " + strconv.FormatInt(int64(data.GetKey().ResCode), 10) + " | success: " +
		strconv.FormatBool(data.GetKey().GetSuccess().GetValue()) + " | total_requests_in_minute: " +
		strconv.Itoa(int(data.GetValue().GetTotalRequestPerMinute().GetValue())) + " | total_delay_in_minute: " +
		strconv.Itoa(int(data.GetValue().GetTotalDelayPerMinute().GetValue())) + "ms"
}

//初始化reverseIdx
func init() {
	idx := 0
	for i := 0; i < allIndexSize; i++ {
		sdkDimensions[i] = i
	}
	for i := 0; i < int(model.ApiOperationMax); i++ {
		for j := 0; j < model.ErrCodeCount; j++ {
			for k := 0; k < int(model.ApiDelayMax); k++ {
				for l := 0; l < numKeySDK; l++ {
					reveseIdx[idx] = []int{i, j, k, l}
					idx++
				}
			}
		}
	}
}
