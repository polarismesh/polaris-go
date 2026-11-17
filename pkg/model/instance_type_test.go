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

package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeInstanceType(t *testing.T) {
	tests := []struct {
		input    InstanceType
		expected InstanceType
	}{
		{"", InstanceTypeMicroservice},
		{InstanceTypeMicroservice, InstanceTypeMicroservice},
		{InstanceTypeMCP, InstanceTypeMCP},
		{" MCP ", InstanceTypeMCP},
		{InstanceTypeA2A, InstanceTypeA2A},
		{" A2A ", InstanceTypeA2A},
		{"unbounded-value", InstanceTypeMicroservice},
	}
	for _, test := range tests {
		assert.Equal(t, test.expected, NormalizeInstanceType(test.input), "input=%q", test.input)
	}
}

func TestServiceCallResultInstanceType(t *testing.T) {
	result := &ServiceCallResult{}
	assert.Equal(t, InstanceTypeMicroservice, result.GetInstanceType())
	result.SetInstanceType(InstanceTypeMCP)
	assert.Equal(t, InstanceTypeMCP, result.GetInstanceType())
	result.InstanceType = "caller-controlled"
	assert.Equal(t, InstanceTypeMicroservice, result.GetInstanceType())
}
