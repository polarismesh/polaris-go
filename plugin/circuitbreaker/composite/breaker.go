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
	"time"

	regexp "github.com/dlclark/regexp2"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/healthcheck"
	"github.com/polarismesh/specification/source/go/api/v1/fault_tolerance"
)

type CompositeCircuitBreaker struct {
	// countersCache
	countersCache map[fault_tolerance.Level]map[model.Resource]*ResourceCounters
	// healthCheckers .
	healthCheckers map[string]healthcheck.HealthChecker
	// healthCheckCache map[model.Resource]*ResourceHealthChecker
	healthCheckCache *sync.Map
	// serviceHealthCheckCache map[model.ServiceKey]map[model.Resource]*ResourceHealthChecker
	serviceHealthCheckCache *sync.Map
	// engineFlow
	engineFlow model.Engine
	// regexpCache regexp -> *regexp.Regexp
	rlock       sync.RWMutex
	regexpCache map[string]*regexp.Regexp
	//
	checkPeriod time.Duration
}

// CheckResource get the resource circuitbreaker status
func (c *CompositeCircuitBreaker) CheckResource(model.Resource) model.SpecCircuitBreakerStatus {
	return nil
}

// Report report resource invoke result stat
func (c *CompositeCircuitBreaker) Report(*model.ResourceStat) error {
	return nil
}

func (c *CompositeCircuitBreaker) loadOrStoreCompiledRegex(s string) *regexp.Regexp {
	c.rlock.Lock()
	defer c.rlock.Unlock()

	if val, ok := c.regexpCache[s]; ok {
		return val
	}

	val := regexp.MustCompile(s, regexp.RE2)
	c.regexpCache[s] = val
	return val
}
