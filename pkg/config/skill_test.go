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

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSkillDefaults(t *testing.T) {
	cfg := NewDefaultConfiguration([]string{"127.0.0.1:8091"})
	assert.False(t, cfg.GetSkill().IsEnable())
	assert.Empty(t, cfg.GetSkill().GetAddresses())
	assert.Equal(t, DefaultSkillConnectTimeout, cfg.GetSkill().GetConnectTimeout())
	assert.Equal(t, DefaultSkillMessageTimeout, cfg.GetSkill().GetMessageTimeout())
	assert.Equal(t, DefaultSkillDownloadTimeout, cfg.GetSkill().GetDownloadTimeout())
	assert.Equal(t, DefaultSkillConnectorType, cfg.GetSkill().GetConnectorType())
	assert.True(t, cfg.GetSkill().IsPersistEnable())
	assert.Equal(t, DefaultSkillPersistDir, cfg.GetSkill().GetPersistDir())
	assert.Equal(t, DefaultSkillPersistMaxWriteRetry, cfg.GetSkill().GetPersistMaxWriteRetry())
	assert.Equal(t, DefaultSkillPersistMaxReadRetry, cfg.GetSkill().GetPersistMaxReadRetry())
	assert.Equal(t, DefaultSkillPersistRetryInterval, cfg.GetSkill().GetPersistRetryInterval())
	assert.True(t, cfg.GetSkill().IsFallbackToLocalCache())
	require.NoError(t, cfg.GetSkill().Verify())
}

func TestSkillEnabledRequiresAddresses(t *testing.T) {
	cfg := NewDefaultConfiguration([]string{"127.0.0.1:8091"})
	cfg.GetSkill().(*SkillConfigImpl).SetEnable(true)
	require.Error(t, cfg.GetSkill().Verify())

	cfg.GetSkill().(*SkillConfigImpl).SetAddresses([]string{"127.0.0.1:8094"})
	require.NoError(t, cfg.GetSkill().Verify())
}

func TestSkillDownloadTimeoutMustBePositive(t *testing.T) {
	cfg := NewDefaultConfiguration([]string{"127.0.0.1:8091"})
	cfg.GetSkill().SetDownloadTimeout(0)
	require.Error(t, cfg.GetSkill().Verify())
}

func TestSkillPersistenceConfigValidation(t *testing.T) {
	cfg := NewDefaultConfiguration([]string{"127.0.0.1:8091"})
	skill := cfg.GetSkill()
	skill.SetConnectorType("")
	require.Error(t, skill.Verify())
	skill.SetConnectorType(DefaultSkillConnectorType)
	skill.SetPersistDir("")
	require.Error(t, skill.Verify())
	skill.SetPersistDir(t.TempDir())
	skill.SetPersistMaxWriteRetry(-1)
	require.Error(t, skill.Verify())
	skill.SetPersistMaxWriteRetry(0)
	skill.SetPersistMaxReadRetry(-1)
	require.Error(t, skill.Verify())
	skill.SetPersistMaxReadRetry(0)
	skill.SetPersistRetryInterval(0)
	require.Error(t, skill.Verify())
	skill.SetPersistRetryInterval(DefaultSkillPersistRetryInterval)
	skill.SetFallbackToLocalCache(false)
	require.False(t, skill.IsFallbackToLocalCache())
	require.NoError(t, skill.Verify())
}

func TestOptionalSkillConfigSectionsUseDefaults(t *testing.T) {
	const bindAddress = "global:\n  api:\n    bindIP: 127.0.0.1\n  serverConnector:\n    addresses:\n      - 127.0.0.1:8091\n"
	tests := map[string]string{
		"skill_null":        bindAddress + "skill: null\n",
		"skill_empty":       bindAddress + "skill:\n",
		"skill_empty_map":   bindAddress + "skill: {}\n",
		"skill_plugin_null": bindAddress + "skill:\n  plugin: null\n",
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			var (
				cfg *ConfigurationImpl
				err error
			)
			require.NotPanics(t, func() {
				cfg, err = LoadConfiguration([]byte(input))
			})
			require.NoError(t, err)
			require.NotNil(t, cfg.GetSkill())
			assert.False(t, cfg.GetSkill().IsEnable())
			assert.Equal(t, DefaultSkillConnectTimeout, cfg.GetSkill().GetConnectTimeout())
			assert.Equal(t, DefaultSkillMessageTimeout, cfg.GetSkill().GetMessageTimeout())
			assert.Equal(t, DefaultSkillDownloadTimeout, cfg.GetSkill().GetDownloadTimeout())
			assert.Equal(t, DefaultSkillConnectorType, cfg.GetSkill().GetConnectorType())
			assert.True(t, cfg.GetSkill().IsPersistEnable())
			require.NoError(t, cfg.GetSkill().Verify())
		})
	}
}
