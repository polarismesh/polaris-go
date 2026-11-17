// Tencent is pleased to support the open source community by making polaris-go available.
//
// Copyright (C) 2021 Tencent. All rights reserved.
//
// Licensed under the BSD 3-Clause License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// https://opensource.org/licenses/BSD-3-Clause
//
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package polaris implements the Polaris Skill Registry gRPC connector.
package polaris

import (
	"fmt"

	"github.com/hashicorp/go-multierror"
)

const (
	defaultMaxCallRecvMsgSize = 128 * 1024 * 1024
	maxCallRecvMsgSizeLimit   = 500 * 1024 * 1024
)

type networkConfig struct {
	MaxCallRecvMsgSize int `yaml:"maxCallRecvMsgSize"`
}

// Verify 校验插件配置。
func (r *networkConfig) Verify() error {
	if r.MaxCallRecvMsgSize <= 0 || r.MaxCallRecvMsgSize > maxCallRecvMsgSizeLimit {
		return multierror.Append(nil, fmt.Errorf(
			"grpc.maxCallRecvMsgSize must be in (0, %d]", maxCallRecvMsgSizeLimit))
	}
	return nil
}

// SetDefault 设置插件默认配置。
func (r *networkConfig) SetDefault() {
	if r.MaxCallRecvMsgSize <= 0 {
		r.MaxCallRecvMsgSize = defaultMaxCallRecvMsgSize
	}
}
