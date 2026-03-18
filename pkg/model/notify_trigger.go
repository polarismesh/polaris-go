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

// NotifyTrigger 通知开关，标识本次需要获取哪些资源
type NotifyTrigger struct {
	EnableDstInstances bool
	EnableDstRoute     bool
	EnableNearbyRoute  bool
	EnableSrcRoute     bool
	EnableDstRateLimit bool
	EnableServices     bool
}

// Clear 清理缓存信息
func (n *NotifyTrigger) Clear() {
	n.EnableDstInstances = false
	n.EnableDstRoute = false
	n.EnableNearbyRoute = false
	n.EnableSrcRoute = false
	n.EnableDstRateLimit = false
	n.EnableServices = false
}
