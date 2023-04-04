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

package model

import (
	"fmt"
	"time"

	"github.com/hashicorp/go-multierror"
)

type WatchAllInstancesRequest struct {
	ServiceKey
	// WatchModel model to wait responses
	WatchMode WatchMode
	// WaitIndex is used to enable a blocking query. Waits
	// until the timeout or the next index is reached
	WaitIndex uint64
	// WaitTime is used to bound the duration of a wait.
	// Defaults to that of the Config, but can be overridden.
	WaitTime time.Duration
	// InstancesListener listener for service listeners
	InstancesListener InstancesListener
}

func (req *WatchAllInstancesRequest) Validate() error {
	if nil == req {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "WatchServiceRequest can not be nil")
	}
	var errs error
	if len(req.Namespace) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("namespace is empty"))
	}
	if len(req.Service) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("service is empty"))
	}
	if req.WatchMode == WatchModeLongPull && req.WaitTime == 0 {
		errs = multierror.Append(errs, fmt.Errorf("wait time must not be 0 when specific notify mode"))
	}
	if req.WatchMode == WatchModeNotify && req.InstancesListener == nil {
		errs = multierror.Append(errs, fmt.Errorf("listeners is empty when specific notify mode"))
	}
	return errs
}

type WatchAllInstancesResponse struct {
	watchId           uint64
	instancesResponse *InstancesResponse
	cancelWatch       func(uint64)
}

func NewWatchAllInstancesResponse(
	watchId uint64, response *InstancesResponse, cancelWatch func(uint642 uint64)) *WatchAllInstancesResponse {
	return &WatchAllInstancesResponse{
		watchId:           watchId,
		instancesResponse: response,
		cancelWatch:       cancelWatch,
	}
}

func (w *WatchAllInstancesResponse) InstancesResponse() *InstancesResponse {
	return w.instancesResponse
}

func (w *WatchAllInstancesResponse) WatchId() uint64 {
	return w.watchId
}

func (w *WatchAllInstancesResponse) CancelWatch() {
	if w.cancelWatch != nil {
		w.cancelWatch(w.watchId)
	}
}

type WatchRequest struct {
	ServiceEventKey

	// WatchModel model to wait responses
	WatchMode WatchMode

	// WaitIndex is used to enable a blocking query. Waits
	// until the timeout or the next index is reached
	WaitIndex uint64

	// WaitTime is used to bound the duration of a wait.
	// Defaults to that of the Config, but can be overridden.
	WaitTime time.Duration

	// InstancesListener listener for service listeners
	InstancesListener []InstancesListener

	// ServiceRuleListener listener for service rule listener
	ServiceRuleListener []ServiceRuleListener
}

func (req *WatchRequest) Validate() error {
	if nil == req {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "WatchServiceRequest can not be nil")
	}
	var errs error
	if len(req.Namespace) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("namespace is empty"))
	}
	if len(req.Service) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("service is empty"))
	}
	if req.Type == EventUnknown {
		errs = multierror.Append(errs, fmt.Errorf("event type is unknown"))
	}
	if req.WatchMode == WatchModeNotify && len(req.InstancesListener) == 0 && len(req.ServiceRuleListener) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("listeners is empty when specific notify mode"))
	}
	return errs
}

type WatchResponse struct {
	// InstancesResponse instances to this service
	InstancesResponse *InstancesResponse
	// ServiceRuleResponse service rule to this service
	ServiceRuleResponse *ServiceRuleResponse
}

type WatchMode int

const (
	// WatchModeLongPull watch model by long pulling, the invocation would be hang on until revision updated or timeout
	WatchModeLongPull WatchMode = iota
	// WatchModeNotify watch model by notify to listener
	WatchModeNotify
)

type InstancesListener interface {
	// OnInstancesUpdate notify when service instances changed
	OnInstancesUpdate(*InstancesResponse)
}

type ServiceRuleListener interface {
	// OnServiceRuleUpdate notify when service rule changed
	OnServiceRuleUpdate(*ServiceRuleResponse)
}
