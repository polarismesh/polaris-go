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
	"time"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
)

const (
	ErrorCode_HeartbeartOnDisable = 400141
)

// SyncRegisterV2 async-regis
func (e *Engine) SyncRegisterV2(request *model.InstanceRegisterRequest) (*model.InstanceRegisterResponse, error) {
	request.SetDefaultTTL()
	request.SetRegisterVersion(model.AsyncRegisterVersion)

	resp, err := e.SyncRegister(request)
	if err != nil {
		return nil, err
	}

	state, ok := e.registerStates.putRegisterState(request)
	if ok {
		go e.runHeartbeat(state)
	}
	return resp, nil

}

func (e *Engine) runHeartbeat(state *registerState) {
	instance := state.instance
	log.GetBaseLogger().Infof("async register task started {%s, %s, %s:%d}",
		instance.Namespace, instance.Service, instance.Host, instance.Port)
	ticker := time.NewTicker(time.Duration(*instance.TTL) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-state.stoppedchan:
			log.GetBaseLogger().Infof("async register task stopped {%s, %s, %s:%d}",
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
			err := e.SyncHeartbeat(hbReq)
			if err == nil {
				log.GetBaseLogger().Debugf("heartbeat success {%s, %s, %s:%d}",
					instance.Namespace, instance.Service, instance.Host, instance.Port)
				break
			}
			log.GetBaseLogger().Errorf("heartbeat failed {%s, %s, %s:%d}",
				instance.Namespace, instance.Service, instance.Host, instance.Port, err)
			sdkErr, ok := err.(model.SDKError)
			if !ok {
				break
			}
			ec := sdkErr.ServerCode()
			if ec != ErrorCode_HeartbeartOnDisable {
				break
			}
			minInterval := e.configuration.GetProvider().GetMinRegisterInterval()
			if time.Since(state.lastRegisterTime) < minInterval {
				break
			}

			state.lastRegisterTime = time.Now()
			_, err = e.SyncRegister(instance)
			if err == nil {
				log.GetBaseLogger().Infof("re-register instatnce success {%s, %s, %s:%d}",
					instance.Namespace, instance.Service, instance.Host, instance.Port)
			} else {
				log.GetBaseLogger().Warnf("re-register instatnce failed {%s, %s, %s:%d}",
					instance.Namespace, instance.Service, instance.Host, instance.Port, err)
			}
		}
	}
}
