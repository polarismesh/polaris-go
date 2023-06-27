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
	ConsumerSuitServerPort              = 18000
	ProviderSuitServerPort              = 18001
	LoadBananceSuitServerPort           = 18002
	HealthCheckSuitServerPort           = 18003
	HealthCheckAlwaysSuitServerPort     = 18004
	CircuitBreakSuitServerPort          = 18005
	NearbySuitServerPort                = 18006
	RouterRuleSuitServerPort            = 18007
	DestMetadataSuitServerPort          = 18008
	SetDivisionSuiteServerPort          = 18009
	CanarySuitServerPort                = 18010
	CacheSuitServerPort                 = 18011
	ServiceUpdateSuitServerPort         = 18012
	DefaultServerNormalSuitServerPort   = 18013
	DefaultServerAbNormalSuitServerPort = 18014
	CacheFastUpdateSuitServerPort       = 18015
	CacheFastUpdateFailSuitServerPort   = 18016
)

var (
	ServerSwitchSuitServerPort = []int{10090, 10091, 10092, 10093, 10094}
)
