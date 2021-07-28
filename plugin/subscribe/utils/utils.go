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
 * specific language governing permissionsr and limitations under the License.
 */

package utils

import (
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

func CheckAddInstances(event *common.ServiceEventObject) *model.InstanceAddEvent {
	if event.NewValue == nil {
		return nil
	}
	addEvent := &model.InstanceAddEvent{}
	newList := event.NewValue.(*pb.ServiceInstancesInProto).GetInstances()
	if event.OldValue == nil {
		for _, v := range newList {
			addEvent.Instances = append(addEvent.Instances, v)
		}
		return addEvent
	} else {
		oldList := event.OldValue.(*pb.ServiceInstancesInProto).GetInstances()
		isAdd := false
		for _, v := range newList {
			isAdd = true
			for _, v1 := range oldList {
				if v.GetId() == v1.GetId() {
					isAdd = false
					break
				}
			}
			if isAdd {
				addEvent.Instances = append(addEvent.Instances, v)
			}
		}
		if len(addEvent.Instances) != 0 {
			return addEvent
		} else {
			return nil
		}
	}
}

func CheckUpdateInstances(event *common.ServiceEventObject) *model.InstanceUpdateEvent {
	if event.OldValue != nil && event.NewValue != nil {
		upEvent := &model.InstanceUpdateEvent{}
		newList := event.NewValue.(*pb.ServiceInstancesInProto).GetInstances()
		oldList := event.OldValue.(*pb.ServiceInstancesInProto).GetInstances()
		for _, v := range newList {
			for _, v1 := range oldList {
				if v.GetId() == v1.GetId() {
					if v.GetRevision() != v1.GetRevision() {
						oneUp := model.OneInstanceUpdate{
							Before: v1,
							After:  v,
						}
						upEvent.UpdateList = append(upEvent.UpdateList, oneUp)
					}
					break
				}
			}
		}
		if len(upEvent.UpdateList) != 0 {
			return upEvent
		} else {
			return nil
		}
	} else {
		return nil
	}
}

func CheckDeleteInstances(event *common.ServiceEventObject) *model.InstanceDeleteEvent {
	if event.OldValue == nil {
		return nil
	}
	delEvent := &model.InstanceDeleteEvent{}
	oldList := event.OldValue.(*pb.ServiceInstancesInProto).GetInstances()
	if event.NewValue == nil {
		for _, v := range oldList {
			delEvent.Instances = append(delEvent.Instances, v)
		}
		return delEvent
	} else {
		newList := event.NewValue.(*pb.ServiceInstancesInProto).GetInstances()
		isDel := false
		for _, v := range oldList {
			isDel = true
			for _, v1 := range newList {
				if v.GetId() == v1.GetId() {
					isDel = false
					break
				}
			}
			if isDel {
				delEvent.Instances = append(delEvent.Instances, v)
			}
		}
		if len(delEvent.Instances) != 0 {
			return delEvent
		} else {
			return nil
		}
	}
}
