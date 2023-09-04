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

package register

import (
	_ "github.com/polarismesh/polaris-go/pkg/plugin/circuitbreaker"
	_ "github.com/polarismesh/polaris-go/pkg/plugin/configconnector"
	_ "github.com/polarismesh/polaris-go/pkg/plugin/configfilter"
	_ "github.com/polarismesh/polaris-go/pkg/plugin/healthcheck"
	_ "github.com/polarismesh/polaris-go/pkg/plugin/loadbalancer"
	_ "github.com/polarismesh/polaris-go/pkg/plugin/localregistry"
	_ "github.com/polarismesh/polaris-go/pkg/plugin/location"
	_ "github.com/polarismesh/polaris-go/pkg/plugin/metrics"
	_ "github.com/polarismesh/polaris-go/pkg/plugin/ratelimiter"
	_ "github.com/polarismesh/polaris-go/pkg/plugin/serverconnector"
	_ "github.com/polarismesh/polaris-go/pkg/plugin/servicerouter"
	_ "github.com/polarismesh/polaris-go/pkg/plugin/weightadjuster"
	_ "github.com/polarismesh/polaris-go/plugin/circuitbreaker/errorcheck"
	_ "github.com/polarismesh/polaris-go/plugin/circuitbreaker/errorcount"
	_ "github.com/polarismesh/polaris-go/plugin/circuitbreaker/errorrate"
	_ "github.com/polarismesh/polaris-go/plugin/configconnector/polaris"
	_ "github.com/polarismesh/polaris-go/plugin/configfilter/crypto"
	_ "github.com/polarismesh/polaris-go/plugin/configfilter/crypto/aes"
	_ "github.com/polarismesh/polaris-go/plugin/healthcheck/http"
	_ "github.com/polarismesh/polaris-go/plugin/healthcheck/tcp"
	_ "github.com/polarismesh/polaris-go/plugin/loadbalancer/hash"
	_ "github.com/polarismesh/polaris-go/plugin/loadbalancer/maglev"
	_ "github.com/polarismesh/polaris-go/plugin/loadbalancer/ringhash"
	_ "github.com/polarismesh/polaris-go/plugin/loadbalancer/weightedrandom"
	_ "github.com/polarismesh/polaris-go/plugin/localregistry/inmemory"
	_ "github.com/polarismesh/polaris-go/plugin/location"
	_ "github.com/polarismesh/polaris-go/plugin/logger/zaplog"
	_ "github.com/polarismesh/polaris-go/plugin/metrics/prometheus"
	_ "github.com/polarismesh/polaris-go/plugin/ratelimiter/bbr"
	_ "github.com/polarismesh/polaris-go/plugin/ratelimiter/reject"
	_ "github.com/polarismesh/polaris-go/plugin/ratelimiter/unirate"
	_ "github.com/polarismesh/polaris-go/plugin/serverconnector/grpc"
	_ "github.com/polarismesh/polaris-go/plugin/servicerouter/canary"
	_ "github.com/polarismesh/polaris-go/plugin/servicerouter/dstmeta"
	_ "github.com/polarismesh/polaris-go/plugin/servicerouter/filteronly"
	_ "github.com/polarismesh/polaris-go/plugin/servicerouter/nearbybase"
	_ "github.com/polarismesh/polaris-go/plugin/servicerouter/rulebase"
	_ "github.com/polarismesh/polaris-go/plugin/servicerouter/setdivision"
	_ "github.com/polarismesh/polaris-go/plugin/servicerouter/zeroprotect"
	_ "github.com/polarismesh/polaris-go/plugin/weightadjuster/ratedelay"
)
