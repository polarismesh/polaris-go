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

package http

import (
	"fmt"
	"github.com/hashicorp/go-multierror"
	"strings"
)

// RequestHeader request header key and value
type RequestHeader struct {
	Key   string `yaml:"key" json:"key"`
	Value string `yaml:"value" json:"value"`
}

// ExpectedStatus status verifier to decide healthy
type ExpectedStatus struct {
	Start int `yaml:"start" json:"start"`
	End   int `yaml:"end" json:"end"`
}

const (
	defaultExpectStatusStart = 200
	defaultExpectStatusEnd   = 400
)

// Config 健康探测的配置
type Config struct {
	// Path HTTP请求的pattern 如/health
	Path string `yaml:"path" json:"path"`
	// Host host to add into the health check request header
	Host string `yaml:"host" json:"host"`
	// RequestHeadersToAdd headers to add into the health check request header
	RequestHeadersToAdd []*RequestHeader `yaml:"requestHeadersToAdd" json:"requestHeadersToAdd"`
	// ExpectedStatuses expected status define the status range to verify http codes
	ExpectedStatuses []*ExpectedStatus `yaml:"expectedStatuses" json:"expectedStatuses"`
}

// SetDefault 设置默认值
func (r *Config) SetDefault() {
	if len(r.ExpectedStatuses) == 0 {
		r.ExpectedStatuses = []*ExpectedStatus{
			{Start: defaultExpectStatusStart, End: defaultExpectStatusEnd},
		}
	}
}

// verify 检验健康探测配置
func (r *Config) Verify() error {
	var errs error
	// http 如果配置了pattern，校验一下
	if r.Path != "" && !strings.HasPrefix(r.Path, "/") {
		errs = multierror.Append(errs, fmt.Errorf("HTTP path Must Start With '/'"))
	}
	if len(r.ExpectedStatuses) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("expectStatuses can not be empty"))
	}
	return errs
}
