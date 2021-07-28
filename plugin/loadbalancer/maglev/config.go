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

package maglev

import (
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/algorithm/hash"
	"github.com/hashicorp/go-multierror"
	"math"
)

const (
	//默认初始化表向量区间
	DefaultTableSize = 65537
)

//一致性hash配置对象
type Config struct {
	HashFunction string `yaml:"hashFunction" json:"hashFunction"`
	TableSize    int    `yaml:"tableSize" json:"tableSize"`
}

//检验一致性hash配置
func (c *Config) Verify() error {
	var errs error
	if !isPrime(c.TableSize) {
		errs = multierror.Append(errs, fmt.Errorf("maglev.tableSize must be prime"))
	}
	return errs
}

//判断是否质数
func isPrime(n int) bool {
	if n <= 3 {
		return n > 1
	}
	sqrt := int(math.Sqrt(float64(n)))
	for i := 2; i <= sqrt; i++ {
		if n%i == 0 {
			return false
		}
	}
	return true
}

//设置一致性hash默认值
func (c *Config) SetDefault() {
	if c.TableSize == 0 {
		c.TableSize = DefaultTableSize
	}
	if len(c.HashFunction) == 0 {
		c.HashFunction = hash.DefaultHashFuncName
	}
}
