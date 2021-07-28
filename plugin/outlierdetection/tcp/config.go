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

package tcp

import (
	"fmt"
	"strconv"
	"time"

	"github.com/hashicorp/go-multierror"
)

const (
	// DefaultTimeOut 默认超时时间
	DefaultTimeOut = 100 * time.Millisecond
	// minTimeout 探活最小的Timeout阈值
	minTimeout = 10 * time.Millisecond
	// maxTimeout 探活最大的Timeout阈值
	maxTimeout = 100 * time.Millisecond
)

// Config 健康探测的配置
type Config struct {
	// Timeout 探测超时时间
	Timeout time.Duration `yaml:"timeout" json:"timeout"`
	// RetryTimes 重试次数
	RetryTimes int `yaml:"retry" json:"retry"`
	// SendPackge 探测请求包 4字节 如 0x0000abcd
	SendPackgeConf string `yaml:"send" json:"send"`
	// CheckPackage 期待应答包 4字节 如 0x0000dcba
	CheckPackageConf string `yaml:"receive" json:"receive"`
}

// verify 检验健康探测配置
func (r *Config) Verify() error {
	var errs error
	if r.Timeout < minTimeout {
		errs = multierror.Append(errs, fmt.Errorf("TCP Timeout Must Be Greater Than 0ms"))
	}

	if r.Timeout > maxTimeout {
		errs = multierror.Append(errs, fmt.Errorf("TCP Timeout Must Be Less Than 100ms"))
	}
	if r.RetryTimes < 0 {
		errs = multierror.Append(errs, fmt.Errorf("TCP RetryTimes Must Be Greater Than Or Equal 0"))
	}
	//  如果配置了收发报，校验一下收发包的格式
	if r.SendPackgeConf != "" {
		if _, err := strconv.ParseUint(r.SendPackgeConf, 0, 32); err != nil {
			errs = multierror.Append(errs, fmt.Errorf("TCP SendPackage Should Be 4 Bytes, like 0x12345678"))
		}
	}
	if r.CheckPackageConf != "" {
		if _, err := strconv.ParseUint(r.CheckPackageConf, 0, 32); err != nil {
			errs = multierror.Append(errs, fmt.Errorf("TCP ExceptPackage Should Be 4 Bytes, like 0x12345678"))
		}
	}
	return errs
}

// SetDefault 设置默认值
func (r *Config) SetDefault() {
	if 0 == r.Timeout {
		r.Timeout = DefaultTimeOut
	}
}
