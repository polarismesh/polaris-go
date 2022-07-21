/**
 * Tencent is pleased to support the open source community by making Polaris available.
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

package util

import (
	"reflect"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
)

// RemoveFromChangeListenerChans remove chan from chan slice
func RemoveFromChangeListenerChans(changeListenerChans []chan model.ConfigFileChangeEvent, changeChan chan model.ConfigFileChangeEvent) (result []chan model.ConfigFileChangeEvent) {
	index := -1

	for k, v := range changeListenerChans {
		if reflect.ValueOf(v).Pointer() == reflect.ValueOf(changeChan).Pointer() {
			index = k
		}
	}
	if index == -1 {
		log.GetBaseLogger().Debugf("no value match")
		return changeListenerChans
	}
	return append(changeListenerChans[:index], changeListenerChans[index+1:]...)
}

// RemoveFromChangeListeners remove function from function slice
func RemoveFromChangeListeners(changeListeners []func(event model.ConfigFileChangeEvent), changeEvent model.OnConfigFileChange) (result []func(event model.ConfigFileChangeEvent)) {
	index := -1

	for k, v := range changeListeners {
		if reflect.ValueOf(v).Pointer() == reflect.ValueOf(changeEvent).Pointer() {
			index = k
		}
	}
	if index == -1 {
		log.GetBaseLogger().Debugf("no value match")
		return changeListeners
	}
	return append(changeListeners[:index], changeListeners[index+1:]...)
}
