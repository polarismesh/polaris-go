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

package common

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/polarismesh/polaris-go/pkg/model"
)

type metricInstance struct {
	model.Instance
}

func (m *metricInstance) GetNamespace() string { return "Production" }
func (m *metricInstance) GetService() string   { return "search-mcp" }
func (m *metricInstance) GetHost() string      { return "127.0.0.1" }
func (m *metricInstance) GetPort() uint32      { return 8080 }
func (m *metricInstance) GetLogicSet() string  { return "" }

func TestConvertInsGaugeToLabelsInstanceType(t *testing.T) {
	for _, test := range []struct {
		input    model.InstanceType
		expected string
	}{
		{"", string(model.InstanceTypeMicroservice)},
		{model.InstanceTypeMCP, string(model.InstanceTypeMCP)},
		{model.InstanceTypeA2A, string(model.InstanceTypeA2A)},
		{"caller-controlled", string(model.InstanceTypeMicroservice)},
	} {
		gauge := &model.ServiceCallResult{CalledInstance: &metricInstance{}, InstanceType: test.input}
		labels := ConvertInsGaugeToLabels(gauge, "127.0.0.1")
		assert.Equal(t, test.expected, labels[CalleeInstanceType])
	}
	assert.Contains(t, ServiceCallLabelOrder, CalleeInstanceType)
}
