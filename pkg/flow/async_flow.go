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
	"github.com/polarismesh/polaris-go/pkg/clock"
	"github.com/polarismesh/polaris-go/pkg/flow/data"
	"github.com/polarismesh/polaris-go/pkg/model"
)

// 同步获取配额信息
func (e *Engine) AsyncGetQuota(request *model.QuotaRequestImpl) (*model.QuotaFutureImpl, error) {
	commonRequest := data.PoolGetCommonRateLimitRequest()
	commonRequest.InitByGetQuotaRequest(request, e.configuration)
	startTime := clock.GetClock().Now()
	future, err := e.flowQuotaAssistant.GetQuota(commonRequest)
	consumeTime := clock.GetClock().Now().Sub(startTime)
	if nil != err {
		(&commonRequest.CallResult).SetFail(model.GetErrorCodeFromError(err), consumeTime)
	} else {
		(&commonRequest.CallResult).SetDelay(consumeTime)
	}
	e.syncRateLimitReportAndFinalize(commonRequest)
	return future, err
}
