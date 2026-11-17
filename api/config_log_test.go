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

package api

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/polarismesh/polaris-go/pkg/config"
)

func TestMarshalConfigForLogRedactsTokens(t *testing.T) {
	cfg := config.NewDefaultConfiguration([]string{"127.0.0.1:8091"})
	cfg.GetGlobal().GetServerConnector().SetToken("server-secret")
	cfg.GetConfigFile().GetConfigConnectorConfig().SetToken("config-secret")
	cfg.GetSkill().SetToken("skill-secret")

	text, err := marshalConfigForLog(cfg)
	require.NoError(t, err)
	logged := string(text)
	for _, secret := range []string{"server-secret", "config-secret", "skill-secret"} {
		assert.NotContains(t, logged, secret)
	}
	assert.Equal(t, 3, strings.Count(logged, "token: '***'"))
	assert.Contains(t, logged, "127.0.0.1:8091")
}
