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

const (
	ConsumerSuitServerPort              = 58000
	ProviderSuitServerPort              = 58001
	LoadBananceSuitServerPort           = 58002
	HealthCheckSuitServerPort           = 58003
	HealthCheckAlwaysSuitServerPort     = 58004
	CircuitBreakSuitServerPort          = 58005
	NearbySuitServerPort                = 58006
	RouterRuleSuitServerPort            = 58007
	DestMetadataSuitServerPort          = 58008
	SetDivisionSuiteServerPort          = 58009
	CanarySuitServerPort                = 58010
	CacheSuitServerPort                 = 58011
	ServiceUpdateSuitServerPort         = 58012
	DefaultServerNormalSuitServerPort   = 58013
	DefaultServerAbNormalSuitServerPort = 58014
	CacheFastUpdateSuitServerPort       = 58015
	CacheFastUpdateFailSuitServerPort   = 58016
)

var (
	ServerSwitchSuitServerPort = []int{50090, 50091, 50092, 50093, 50094}
)
