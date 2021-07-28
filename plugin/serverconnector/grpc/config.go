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

package grpc

import (
	"fmt"
	"github.com/hashicorp/go-multierror"
)

const (
	//默认GRPC链路包接收大小
	DefaultMaxCallRecvMsgSize = 50 * 1024 * 1024
	//GRPC链路包接收大小的设置上限
	MaxMaxCallRecvMsgSize = 500 * 1024 * 1024
)

//GRPC插件级别配置
type networkConfig struct {
	MaxCallRecvMsgSize int `yaml:"maxCallRecvMsgSize"`
}

//校验GRPC配置值
func (r *networkConfig) Verify() error {
	var errs error
	if r.MaxCallRecvMsgSize <= 0 || r.MaxCallRecvMsgSize > MaxMaxCallRecvMsgSize {
		errs = multierror.Append(errs, fmt.Errorf("grpc.maxCallRecvMsgSize must be int (0, 524288000]"))
	}
	return errs
}

//设置GRPC配置默认值
func (r *networkConfig) SetDefault() {
	if r.MaxCallRecvMsgSize <= 0 {
		r.MaxCallRecvMsgSize = DefaultMaxCallRecvMsgSize
	}
}
