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
	"github.com/modern-go/reflect2"
)

// ServiceSpecific .
type ServiceSpecific struct {
	Namespace      string                    `yaml:"namespace" json:"namespace"`
	Service        string                    `yaml:"service" json:"service"`
	ServiceRouter  *ServiceRouterConfigImpl  `yaml:"serviceRouter" json:"serviceRouter"`
	CircuitBreaker *CircuitBreakerConfigImpl `yaml:"circuitBreaker" json:"circuitBreaker"`
}

// ServicesSpecificImpl .
type ServicesSpecificImpl struct {
	Services []*ServiceSpecific
}

// Verify .验证
func (s *ServiceSpecific) Verify() error {
	return nil
}

// Init .初始化
func (s *ServiceSpecific) Init() {
	s.ServiceRouter = &ServiceRouterConfigImpl{}
	s.ServiceRouter.Init()
	s.CircuitBreaker = &CircuitBreakerConfigImpl{}
	s.CircuitBreaker.Init()
}

// SetDefault 设置默认
func (s *ServiceSpecific) SetDefault() {
	s.CircuitBreaker.SetDefault()
	s.ServiceRouter.SetDefault()
}

// GetServiceCircuitBreaker 获取熔断器
func (s *ServiceSpecific) GetServiceCircuitBreaker() CircuitBreakerConfig {
	if s == nil || reflect2.IsNil(s) {
		return nil
	}

	return s.CircuitBreaker
}

// GetServiceRouter 获取路由
func (s *ServiceSpecific) GetServiceRouter() ServiceRouterConfig {
	return s.ServiceRouter
}
