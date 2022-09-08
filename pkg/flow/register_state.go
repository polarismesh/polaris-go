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

package flow

import (
	"fmt"
	"sync"
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
)

type registerStates struct {
	mu     sync.RWMutex
	states map[string]*registerState
}

type registerState struct {
	instance         *model.InstanceRegisterRequest
	lastRegisterTime time.Time
	stoppedchan      chan struct{}
}

func (c *registerStates) destroy() {
	c.mu.Lock()
	pre := c.states
	c.states = make(map[string]*registerState)
	c.mu.Unlock()

	for _, state := range pre {
		close(state.stoppedchan)
	}
}

func (c *registerStates) putRegisterState(instance *model.InstanceRegisterRequest) (*registerState, bool) {
	key := buildRegisterStateKey(instance.Namespace, instance.Service, instance.Host, instance.Port)
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.states[key]
	if !ok {
		state := &registerState{
			instance:         instance,
			lastRegisterTime: time.Now(),
			stoppedchan:      make(chan struct{}),
		}
		c.states[key] = state
		return state, true
	}
	return nil, false
}

func (c *registerStates) removeRegisterState(instance *model.InstanceDeRegisterRequest) {
	key := buildRegisterStateKey(instance.Namespace, instance.Service, instance.Host, instance.Port)
	c.mu.Lock()
	defer c.mu.Unlock()
	state, ok := c.states[key]
	if ok {
		close(state.stoppedchan)
		delete(c.states, key)
	}
}

func buildRegisterStateKey(namespace string, service string, host string, port int) string {
	return fmt.Sprintf("%s##%s##%s##%d", namespace, service, host, port)
}
