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

package ringhash

import (
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/algorithm/hash"
	"github.com/hashicorp/go-multierror"
)

const (
	//默认虚拟节点数
	DefaultVnodeCount = 10
)

//一致性hash配置对象
type Config struct {
	HashFunction string `yaml:"hashFunction" json:"hashFunction"`
	VnodeCount   int    `yaml:"vnodeCount" json:"vnodeCount"`
}

//检验一致性hash配置
func (c *Config) Verify() error {
	var errs error
	if c.VnodeCount <= 0 {
		errs = multierror.Append(errs, fmt.Errorf("ringhash.vnodeCount must be greater than 0"))
	}
	return errs
}

//设置一致性hash默认值
func (c *Config) SetDefault() {
	if c.VnodeCount == 0 {
		c.VnodeCount = DefaultVnodeCount
	}
	if len(c.HashFunction) == 0 {
		c.HashFunction = hash.DefaultHashFuncName
	}
}
