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
	"github.com/hashicorp/go-multierror"
)

var (
	//默认打开限流能力
	DefaultRateLimitEnable = true
)

//服务提供者配置
type ProviderConfigImpl struct {
	//限流配置
	RateLimit *RateLimitConfigImpl `yaml:"rateLimit" json:"rateLimit"`
}

//是否启用限流能力
func (p *ProviderConfigImpl) GetRateLimit() RateLimitConfig {
	return p.RateLimit
}

//校验配置参数
func (p *ProviderConfigImpl) Verify() error {
	if nil == p {
		return errors.New("ProviderConfig is nil")
	}
	var errs error
	var err error
	if err = p.RateLimit.Verify(); nil != err {
		errs = multierror.Append(errs, err)
	}
	return errs
}

//设置默认参数
func (p *ProviderConfigImpl) SetDefault() {
	if nil == p.RateLimit {
		p.RateLimit = &RateLimitConfigImpl{}
	}
	p.RateLimit.SetDefault()
}

//配置初始化
func (p *ProviderConfigImpl) Init() {
	p.RateLimit = &RateLimitConfigImpl{}
	p.RateLimit.Init()
}
