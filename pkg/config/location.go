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

// LocationConfigImpl 地理位置配置.
type LocationConfigImpl struct {
	Providers []*LocationProviderConfigImpl `yaml:"providers" json:"providers"`
}

// GetProviders 获取所有的provider
func (a *LocationConfigImpl) GetProviders() []*LocationProviderConfigImpl {
	return a.Providers
}

// GetProvider 根据类型获取对应的provider
func (a *LocationConfigImpl) GetProvider(providerType string) *LocationProviderConfigImpl {
	for _, provider := range a.Providers {
		if provider.Type == providerType {
			return provider
		}
	}
	return nil
}

// Init 初始化
func (a *LocationConfigImpl) Init() {
}

// Verify 检验LocalCacheConfig配置.
func (a *LocationConfigImpl) Verify() error {
	for _, provider := range a.Providers {
		if err := provider.Verify(); err != nil {
			return err
		}
	}
	return nil
}

// SetDefault 设置LocalCacheConfig配置的默认值.
func (a *LocationConfigImpl) SetDefault() {

}
