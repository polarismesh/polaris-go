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
	"github.com/hashicorp/go-multierror"
)

type SubScribeEventType int

const (
	// service实例事件
	EventInstance SubScribeEventType = 1
)

type SubScribeEvent interface {
	// GetSubScribeEventType
	GetSubScribeEventType() SubScribeEventType
}

// 实例事件
type InstanceEvent struct {
	AddEvent    *InstanceAddEvent
	UpdateEvent *InstanceUpdateEvent
	DeleteEvent *InstanceDeleteEvent
}

// GetSubScribeEventType
func (e *InstanceEvent) GetSubScribeEventType() SubScribeEventType {
	return EventInstance
}

// 实例Add事件
type InstanceAddEvent struct {
	Instances []Instance
}

//实例one update struct
type OneInstanceUpdate struct {
	Before Instance
	After  Instance
}

//实例Update事件
type InstanceUpdateEvent struct {
	UpdateList []OneInstanceUpdate
}

//实例Delete事件
type InstanceDeleteEvent struct {
	Instances []Instance
}

// WatchService req
type WatchServiceRequest struct {
	Key ServiceKey
}

// WatchServiceRequest 校验
func (req *WatchServiceRequest) Validate() error {
	if nil == req {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "WatchServiceRequest can not be nil")
	}
	var errs error
	if len(req.Key.Namespace) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("namespace is empty"))
	}
	if len(req.Key.Service) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("service is empty"))
	}
	if errs != nil {
		return NewSDKError(ErrCodeAPIInvalidArgument, errs,
			"fail to validate GetInstancesRequest")
	}
	return nil
}

// WatchServiceResponse
type WatchServiceResponse struct {
	EventChannel        <-chan SubScribeEvent
	GetAllInstancesResp *InstancesResponse
}
