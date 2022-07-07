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

	"github.com/polarismesh/polaris-go/pkg/clock"
	"github.com/polarismesh/polaris-go/pkg/flow/data"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
)

// AsyncGetQuota 同步获取配额信息
func (e *Engine) AsyncGetQuota(request *model.QuotaRequestImpl) (*model.QuotaFutureImpl, error) {
	commonRequest := data.PoolGetCommonRateLimitRequest()
	commonRequest.InitByGetQuotaRequest(request, e.configuration)
	startTime := clock.GetClock().Now()
	future, err := e.flowQuotaAssistant.GetQuota(commonRequest)
	consumeTime := clock.GetClock().Now().Sub(startTime)
	if err != nil {
		(&commonRequest.CallResult).SetFail(model.GetErrorCodeFromError(err), consumeTime)
	} else {
		(&commonRequest.CallResult).SetDelay(consumeTime)
	}
	e.syncRateLimitReportAndFinalize(commonRequest)
	return future, err
}

// AsyncRegister async-regis
func (e *Engine) AsyncRegister(request *model.InstanceRegisterRequest) (*model.InstanceRegisterResponse, error) {
	resp, err := e.SyncRegister(request)
	if err != nil {
		return nil, err
	}

	state, ok := e.registerStates.putRegisterState(request)
	if ok {
		go e.runAsyncRegisterTask(state)
	}
	return resp, nil

}

func (e *Engine) runAsyncRegisterTask(state *registerState) {
	log.GetBaseLogger().Infof("async register task started %s", state.instance.String())
	ticker := time.NewTicker(time.Duration(*state.instance.TTL) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-state.stoppedchan:
			log.GetBaseLogger().Infof("async register task stopped %s", state.instance.String())
			return
		case <-ticker.C:
			hbReq := &model.InstanceHeartbeatRequest{
				Namespace:    state.instance.Namespace,
				Service:      state.instance.Service,
				Host:         state.instance.Host,
				Port:         state.instance.Port,
				ServiceToken: state.instance.ServiceToken,
			}
			err := e.SyncHeartbeat(hbReq)
			if err == nil {
				log.GetBaseLogger().Debugf("heartbeat success %s", state.instance.String())
				break
			}
			sdkErr, ok := err.(model.SDKError)
			if !ok {
				break
			}
			ec := sdkErr.ServerCode()
			// HeartbeatOnDisabledIns
			if ec != 400141 {
				break
			}
			minInterval := e.configuration.GetProvider().GetMinRegisterInterval()
			now := time.Now()
			if now.Before(state.lastRegisterTime.Add(minInterval)) {
				break
			}

			state.lastRegisterTime = now
			_, err = e.SyncRegister(state.instance)
			if err == nil {
				log.GetBaseLogger().Infof("re-register instatnce success %s", state.instance.String())
			} else {
				log.GetBaseLogger().Warnf("re-register instatnce failed %s", state.instance.String(), err)
			}
		}
	}
}
