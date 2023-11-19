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

package trigger

import (
	"sync/atomic"
	"time"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/specification/source/go/api/v1/fault_tolerance"
)

// StatusChangeHandler
type StatusChangeHandler interface {
	// CloseToOpen
	CloseToOpen(breaker string)
	// OpenToHalfOpen
	OpenToHalfOpen()
	// HalfOpenToClose
	HalfOpenToClose()
	// HalfOpenToOpen
	HalfOpenToOpen()
}

// Options
type Options struct {
	Resource      model.Resource
	Condition     *fault_tolerance.TriggerCondition
	StatusHandler StatusChangeHandler
	Log           log.Logger
	DelayExecutor func(delay time.Duration, f func())
}

// TriggerCounter .
type TriggerCounter interface {
	// Report .
	Report(success bool)
}

func newBaseCounter(rule string, opt *Options) *baseCounter {
	return &baseCounter{
		ruleName:         rule,
		triggerCondition: opt.Condition,
		res:              opt.Resource,
		handler:          opt.StatusHandler,
		suspended:        0,
		log:              opt.Log,
		delayExecutor:    opt.DelayExecutor,
	}
}

type baseCounter struct {
	ruleName         string
	triggerCondition *fault_tolerance.TriggerCondition
	res              model.Resource
	handler          StatusChangeHandler
	suspended        int32
	log              log.Logger
	delayExecutor    func(delay time.Duration, f func())
}

func (bc *baseCounter) isSuspend() bool {
	return atomic.LoadInt32(&bc.suspended) == 1
}

func (bc *baseCounter) suspend() {
	atomic.StoreInt32(&bc.suspended, 1)
}

func (bc *baseCounter) resume() {
	atomic.StoreInt32(&bc.suspended, 0)
}
