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
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSkillRef(t *testing.T) {
	namespace, name, version, err := ParseSkillRef("Production:demo-skill@1.2.3")
	require.NoError(t, err)
	assert.Equal(t, "Production", namespace)
	assert.Equal(t, "demo-skill", name)
	assert.Equal(t, "1.2.3", version)

	ref, err := BuildSkillRef(namespace, name, version)
	require.NoError(t, err)
	assert.Equal(t, "Production:demo-skill@1.2.3", ref)
}

func TestParseSkillRefRejectsUnsafeValues(t *testing.T) {
	for _, value := range []string{
		"Production:demo-skill",
		":demo-skill@1.0.0",
		"Production:@1.0.0",
		"Production:demo-skill@",
		"Production:demo@skill@1.0.0",
		"Production:demo:skill@1.0.0",
		" Production:demo-skill@1.0.0",
		"Production:../demo@1.0.0",
	} {
		t.Run("invalid_"+strings.ReplaceAll(value, ":", "_"), func(t *testing.T) {
			_, _, _, err := ParseSkillRef(value)
			require.Error(t, err)
			assert.Equal(t, ErrCodeAPIInvalidArgument, GetErrorCodeFromError(err))
		})
	}
}

func TestNormalizeAgentSkills(t *testing.T) {
	skills := []*AgentSkill{
		{ID: "Production:z@1.0.0", Name: "z", Version: "1.0.0"},
		{ID: "Production:a@1.0.0"},
	}
	normalized, err := NormalizeAgentSkills(skills)
	require.NoError(t, err)
	assert.Equal(t, "a", normalized[1].Name)
	assert.Equal(t, "1.0.0", normalized[1].Version)

	_, err = NormalizeAgentSkills([]*AgentSkill{
		{ID: "Production:a@1.0.0"},
		{ID: "Production:a@1.0.0"},
	})
	require.Error(t, err)
	assert.Equal(t, ErrCodeAPIInvalidArgument, GetErrorCodeFromError(err))
}

func TestValidateInstanceMetadata(t *testing.T) {
	metadata := map[string]string{
		MetadataKeyAIAgentName: "customer-agent",
		MetadataKeyAIAgentCard: `{"name":"customer-agent"}`,
		MetadataKeyA2AVersion:  "1.0",
	}
	require.NoError(t, ValidateInstanceMetadata(metadata))

	tooMany := make(map[string]string, MaxInstanceMetadataEntries+1)
	for i := 0; i <= MaxInstanceMetadataEntries; i++ {
		tooMany[fmt.Sprintf("key-%02d", i)] = "v"
	}
	err := ValidateInstanceMetadata(tooMany)
	require.Error(t, err)
	assert.Equal(t, ErrCodeAPIInvalidArgument, GetErrorCodeFromError(err))

	invalid := map[string]string{"api-token": "secret"}
	err = ValidateInstanceMetadata(invalid)
	require.Error(t, err)
	assert.Equal(t, ErrCodeAPIInvalidArgument, GetErrorCodeFromError(err))
}

func TestAgentMetadataKeyConstants(t *testing.T) {
	assert.Equal(t, "ai-agent-name", MetadataKeyAIAgentName)
	assert.Equal(t, "ai-agent-card", MetadataKeyAIAgentCard)
	assert.Equal(t, "ai-a2a-version", MetadataKeyA2AVersion)
	assert.Equal(t, MetadataKeyAIAgentCard, MetadataKeyA2AAgentCard)
	assert.Equal(t, "a2a.transport", MetadataKeyA2ATransport)
	assert.Equal(t, "a2a.path", MetadataKeyA2APath)
}
