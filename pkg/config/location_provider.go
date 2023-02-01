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

package config

import (
	"errors"
)

type LocationProviderConfigImpl struct {
	Type    string                 `yaml:"type" json:"type"`
	Options map[string]interface{} `yaml:"options" json:"options"`
}

func (l LocationProviderConfigImpl) GetType() string {
	return l.Type
}

func (l LocationProviderConfigImpl) GetOptions() map[string]interface{} {
	return l.Options
}

func (l LocationProviderConfigImpl) Verify() error {
	if l.Type == "" {
		return errors.New("type is empty")
	}
	return nil
}

func (l LocationProviderConfigImpl) SetDefault() {
}
