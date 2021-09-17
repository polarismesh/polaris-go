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

package healthcheck

import (
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
)

//HealthChecker 【扩展点接口】主动健康探测策略
type HealthChecker interface {
	plugin.Plugin
	// 对单个实例进行探测，返回探测结果
	// DetectInstance 每个探测方法自己去判断当前周期是否需要探测，如果无需探测，则返回nil
	DetectInstance(model.Instance) (DetectResult, error)
}

// DetectResult 健康探测结果
type DetectResult interface {
	// IsSuccess 是否探测成功
	IsSuccess() bool
	// GetDetectTime 本次探测时间
	GetDetectTime() time.Time
	// GetDetectInstance 探测是实例
	GetDetectInstance() model.Instance
}

// DetectResultImp 探活返回的结果，plugin.DetectResult的实现
type DetectResultImp struct {
	Success        bool
	DetectTime     time.Time      // 探测时间
	DetectInstance model.Instance // 探测的实例
}

// GetDetectType 探测类型，与探测插件名相同
func (r *DetectResultImp) IsSuccess() bool {
	return r.Success
}

// GetDetectTime 探测时间
func (r *DetectResultImp) GetDetectTime() time.Time {
	return r.DetectTime
}

// GetDetectInstance 获取探活的实例
func (r *DetectResultImp) GetDetectInstance() model.Instance {
	return r.DetectInstance
}

//初始化
func init() {
	plugin.RegisterPluginInterface(common.TypeHealthCheck, new(HealthChecker))
}
