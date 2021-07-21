/**
 * Tencent is pleased to support the open source community by making CL5 available.
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
	"strings"
	"time"

	"github.com/hashicorp/go-multierror"
)

const (
	// DefaultTimeOut 默认超时时间
	DefaultTimeOut = 100 * time.Millisecond
	// DefaultHTTPPattern 默认HTTP请求路径
	DefaultHTTPPattern = ""
	// minTimeout 探活最小的Timeout阈值
	minTimeout = 10 * time.Millisecond
	// maxTimeout 探活最大的Timeout阈值
	maxTimeout = 100 * time.Millisecond
)

// Config 健康探测的配置
type Config struct {
	// Timeout 探测超时时间
	Timeout time.Duration `yaml:"timeout" json:"timeout"`
	// HTTPPattern HTTP请求的pattern 如/health
	HTTPPattern string `yaml:"path" json:"path"`
}

// SetDefault 设置默认值
func (r *Config) SetDefault() {
	if 0 == r.Timeout {
		r.Timeout = DefaultTimeOut
	}
	if "" == r.HTTPPattern {
		r.HTTPPattern = DefaultHTTPPattern
	}
}

// verify 检验健康探测配置
func (r *Config) Verify() error {
	var errs error
	if r.Timeout < minTimeout {
		errs = multierror.Append(errs, fmt.Errorf("HTTP Timeout Must Be Greater Than 0ms"))
	}

	if r.Timeout > maxTimeout {
		errs = multierror.Append(errs, fmt.Errorf("HTTP Timeout Must Be Less Than 100ms"))
	}
	// http 如果配置了pattern，校验一下
	if r.HTTPPattern != "" && !strings.HasPrefix(r.HTTPPattern, "/") {
		errs = multierror.Append(errs, fmt.Errorf("HTTP HTTPPattern Must Start With '/'"))
	}
	return errs
}
