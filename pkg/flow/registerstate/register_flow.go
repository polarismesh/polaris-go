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

package registerstate

import (
	"fmt"
	"sync"
	"time"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
)

type (
	registerFunc  func(instance *model.InstanceRegisterRequest, header map[string]string) (*model.InstanceRegisterResponse, error)
	heartbeatFunc func(instance *model.InstanceHeartbeatRequest) error
)

const (
	_maxHeartbeatErrorCount = 2
	_headerKeyAsyncRegis    = "async-regis"
	_headerValueAsyncRegis  = "true"
)

func NewRegisterStateManager(minRegisterInterval time.Duration) *RegisterStateManager {
	return &RegisterStateManager{
		minRegisterInterval: minRegisterInterval,
		states:              map[string]*registerState{},
	}
}

type RegisterStateManager struct {
	mu                  sync.RWMutex
	minRegisterInterval time.Duration
	states              map[string]*registerState
}

type registerState struct {
	instance         *model.InstanceRegisterRequest
	lastRegisterTime time.Time
	stoppedchan      chan struct{}
}

func (c *RegisterStateManager) Destroy() {
	c.mu.Lock()
	pre := c.states
	c.states = make(map[string]*registerState)
	c.mu.Unlock()

	for _, state := range pre {
		close(state.stoppedchan)
	}
}

func (c *RegisterStateManager) PutRegister(instance *model.InstanceRegisterRequest, regis registerFunc, beat heartbeatFunc) (*registerState, bool) {
	key := buildRegisterStateKey(instance.Namespace, instance.Service, instance.Host, instance.Port)
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.states[key]
	if ok {
		return nil, false
	}
	state := &registerState{
		instance:         instance,
		lastRegisterTime: time.Now(),
		stoppedchan:      make(chan struct{}),
	}
	c.states[key] = state
	go c.runHeartbeat(state, regis, beat)
	return state, true
}

func (c *RegisterStateManager) RemoveRegister(instance *model.InstanceDeRegisterRequest) {
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

func (c *RegisterStateManager) runHeartbeat(state *registerState, regis registerFunc, beat heartbeatFunc) {
	instance := state.instance
	log.GetBaseLogger().Infof("[HeartBeat] instance heartbeat task started {%s, %s, %s:%d}",
		instance.Namespace, instance.Service, instance.Host, instance.Port)
	ticker := time.NewTicker(time.Duration(*instance.TTL) * time.Second)
	defer ticker.Stop()

	errCnt := 0
	minInterval := c.minRegisterInterval

	for {
		select {
		case <-state.stoppedchan:
			log.GetBaseLogger().Infof("[HeartBeat] instance heartbeat task stopped {%s, %s, %s:%d}",
				instance.Namespace, instance.Service, instance.Host, instance.Port)
			return
		case <-ticker.C:
			hbReq := &model.InstanceHeartbeatRequest{
				Namespace:    instance.Namespace,
				Service:      instance.Service,
				Host:         instance.Host,
				Port:         instance.Port,
				ServiceToken: instance.ServiceToken,
			}
			if err := beat(hbReq); err != nil {
				log.GetBaseLogger().Errorf("heartbeat failed {%s, %s, %s:%d}",
					instance.Namespace, instance.Service, instance.Host, instance.Port, err)
				errCnt++

				needRegis := errCnt > _maxHeartbeatErrorCount && time.Since(state.lastRegisterTime) > minInterval
				if needRegis {
					// 重新记录注册的时间
					state.lastRegisterTime = time.Now()
					_, err = regis(instance, CreateRegisterV2Header())
					if err == nil {
						log.GetBaseLogger().Infof("re-register instatnce success {%s, %s, %s:%d}",
							instance.Namespace, instance.Service, instance.Host, instance.Port)
					} else {
						log.GetBaseLogger().Warnf("re-register instatnce failed {%s, %s, %s:%d}",
							instance.Namespace, instance.Service, instance.Host, instance.Port, err)
					}
				}
				break
			}
			log.GetBaseLogger().Debugf("heartbeat success {%s, %s, %s:%d}",
				instance.Namespace, instance.Service, instance.Host, instance.Port)
			errCnt = 0
			break
		}
	}
}

func CreateRegisterV2Header() map[string]string {
	header := map[string]string{
		_headerKeyAsyncRegis: _headerValueAsyncRegis,
	}
	return header
}
