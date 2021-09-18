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

package detect

import (
	"github.com/polarismesh/polaris-go/pkg/model"
	"time"
)

// healthCheckingStatus 实现接口HealthCheckingStatus
type healthCheckingStatus struct {
	// status 探测结果状态
	status model.HealthCheckStatus
	// startTime 开始时间
	startTime time.Time
}

// GetStatus 获取健康探测的状态结果
func (od *healthCheckingStatus) GetStatus() model.HealthCheckStatus {
	return od.status
}

// GetStartTime 获取健康探测的时间
func (od *healthCheckingStatus) GetStartTime() time.Time {
	return od.startTime
}
