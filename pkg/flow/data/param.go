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

package data

import (
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/modern-go/reflect2"
	"time"
)

//控制参数提供者
type ControlParamProvider interface {
	//获取超时值指针
	GetTimeoutPtr() *time.Duration
	//设置超时时间间隔
	SetTimeout(duration time.Duration)
	//获取重试次数指针
	GetRetryCountPtr() *int
	//设置重试次数
	SetRetryCount(int)
}

//为服务注册的请求设置默认值
func BuildControlParam(
	provider ControlParamProvider, cfg config.Configuration, param *model.ControlParam) {
	if reflect2.IsNil(provider) || nil == provider.GetTimeoutPtr() {
		param.Timeout = cfg.GetGlobal().GetAPI().GetTimeout()
	} else {
		param.Timeout = *provider.GetTimeoutPtr()
	}
	if reflect2.IsNil(provider) || nil == provider.GetRetryCountPtr() {
		param.MaxRetry = cfg.GetGlobal().GetAPI().GetMaxRetryTimes()
	} else {
		param.MaxRetry = *provider.GetRetryCountPtr()
	}
	param.RetryInterval = cfg.GetGlobal().GetAPI().GetRetryInterval()
	if !reflect2.IsNil(provider) {
		provider.SetTimeout(param.Timeout)
		provider.SetRetryCount(param.MaxRetry)
	}

}
