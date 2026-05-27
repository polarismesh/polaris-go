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

package flow

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// TestFormatArgumentsToLabels_Empty 验证空输入返回空串，避免 Prometheus label 异常值。
func TestFormatArgumentsToLabels_Empty(t *testing.T) {
	assert.Equal(t, "", formatArgumentsToLabels(nil))
	assert.Equal(t, "", formatArgumentsToLabels([]model.Argument{}))
}

// TestFormatArgumentsToLabels_SingleArgument 验证单参数格式与 Argument.String() 一致。
func TestFormatArgumentsToLabels_SingleArgument(t *testing.T) {
	args := []model.Argument{model.BuildHeaderArgument("X-Token", "abc")}
	got := formatArgumentsToLabels(args)
	// HEADER:X-Token:abc
	assert.Equal(t, "HEADER:X-Token:abc", got)
}

// TestFormatArgumentsToLabels_MultipleSorted 验证多参数会按字典序排序拼接，
// 保证同一组 Arguments 上报到 Prometheus 时产生稳定的 label 字符串。
func TestFormatArgumentsToLabels_MultipleSorted(t *testing.T) {
	args := []model.Argument{
		model.BuildHeaderArgument("zzz", "v1"),
		model.BuildHeaderArgument("aaa", "v2"),
		model.BuildCustomArgument("bbb", "v3"),
	}
	got := formatArgumentsToLabels(args)
	// 期望按字典序排序后再用 | 拼接
	expected := "CUSTOM:bbb:v3|HEADER:aaa:v2|HEADER:zzz:v1"
	assert.Equal(t, expected, got)
}
