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

package schedule

import (
	"github.com/polarismesh/polaris-go/pkg/flow/data"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

//服务上下线监听回调
type ServiceEventHandler struct {
	TaskValues model.TaskValues
}

//解析服务主键
func (s *ServiceEventHandler) parseSvcKey(eventObject interface{}) *model.ServiceKey {
	c := eventObject.(model.RegistryValue)
	if !c.IsInitialized() {
		return nil
	}
	if c.GetType() != model.EventInstances {
		return nil
	}
	svcObj := c.(model.ServiceInstances)
	return &model.ServiceKey{
		Namespace: svcObj.GetNamespace(),
		Service:   svcObj.GetService(),
	}
}

//服务上线回调
func (s *ServiceEventHandler) OnServiceAdded(event *common.PluginEvent) error {
	svcKey := s.parseSvcKey(event.EventObject.(*common.ServiceEventObject).NewValue)
	if nil == svcKey {
		return nil
	}
	s.TaskValues.AddValue(*svcKey, &data.AllEqualsComparable{})
	return nil
}

//服务下线回调
func (s *ServiceEventHandler) OnServiceDeleted(event *common.PluginEvent) error {
	svcObject := event.EventObject.(*common.ServiceEventObject).OldValue
	svcKey := s.parseSvcKey(svcObject)
	if nil == svcKey {
		return nil
	}
	s.TaskValues.DeleteValue(*svcKey, &data.AllEqualsComparable{})
	return nil
}
