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

package composite

import (
	"sync"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/specification/source/go/api/v1/fault_tolerance"
)

type CompositeCircuitBreaker struct {
	// countersCache
	countersCache map[fault_tolerance.Level]map[model.Resource]*ResourceCounters
	// healthCheckCache map[model.Resource]*ResourceHealthChecker
	healthCheckCache *sync.Map
	// serviceHealthCheckCache map[model.ServiceKey]map[model.Resource]*ResourceHealthChecker
	serviceHealthCheckCache *sync.Map
	//
	engineFlow model.Engine
}
