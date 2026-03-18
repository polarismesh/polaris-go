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

package event

import (
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
)

type LosslessEvent interface {
	InstanceEvent
}

type LosslessEventImpl struct {
	InstanceEventImpl
	LosslessInfo model.LosslessInfo `json:"lossless_info,omitempty"`
}

func GetLosslessEvent(eventName EventName, losslessInfo model.LosslessInfo) BaseEventImpl {
	return BaseEventImpl{
		BaseType:  LosslessEventType,
		EventType: LosslessEventType.EventTypeString(),
		EventName: eventName,
		EventTime: time.Now().Format("2006-01-02 15:04:05"),
		LosslessEvent: &LosslessEventImpl{
			InstanceEventImpl: InstanceEventImpl{
				Namespace: losslessInfo.Instance.Namespace,
				Service:   losslessInfo.Instance.Service,
				Host:      losslessInfo.Instance.Host,
				Port:      losslessInfo.Instance.Port,
			},
			LosslessInfo: losslessInfo,
		},
	}
}
