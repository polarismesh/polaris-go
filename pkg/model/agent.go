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
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	// MetadataKeyAIAgentName 是实例 metadata 中标识 AI Agent 逻辑名的 key。
	MetadataKeyAIAgentName = "ai-agent-name"
	// MetadataKeyAIAgentCard 是实例 metadata 中存放 AgentCard JSON 的标准 key。
	MetadataKeyAIAgentCard = "ai-agent-card"
	// MetadataKeyA2AVersion 是实例 metadata 中存放 AgentCard schema 版本的标准 key。
	MetadataKeyA2AVersion = "ai-a2a-version"
	// MetadataKeyA2AAgentCard 是 MetadataKeyAIAgentCard 的兼容别名。
	//
	// Deprecated: use MetadataKeyAIAgentCard.
	MetadataKeyA2AAgentCard = MetadataKeyAIAgentCard
	// MetadataKeyA2ATransport 是旧版 transport metadata key。
	//
	// Deprecated: AI Agent instances use ai-agent-name, ai-agent-card and ai-a2a-version.
	MetadataKeyA2ATransport = "a2a.transport"
	// MetadataKeyA2APath 是旧版 endpoint path metadata key。
	//
	// Deprecated: AI Agent instances use ai-agent-name, ai-agent-card and ai-a2a-version.
	MetadataKeyA2APath = "a2a.path"
)

const (
	// MaxInstanceMetadataEntries 是单个实例允许的 metadata 项数上限。
	MaxInstanceMetadataEntries = 64
	// MaxMetadataKeyRunes 是单个 metadata key 的 Unicode 字符上限。
	MaxMetadataKeyRunes = 128
	// MaxMetadataValueRunes 是单个 metadata value 的 Unicode 字符上限。
	MaxMetadataValueRunes = 65535
)

// ServiceType 是协议无关的服务类型。
type ServiceType int32

const (
	// ServiceTypeMicroservice 表示普通微服务。
	ServiceTypeMicroservice ServiceType = 0
	// ServiceTypeMCPServer 表示 MCP Server。
	ServiceTypeMCPServer ServiceType = 1
	// ServiceTypeAIAgent 表示 AI Agent。
	ServiceTypeAIAgent ServiceType = 2
)

// String 返回服务类型的稳定字符串表示。
func (s ServiceType) String() string {
	switch s {
	case ServiceTypeMicroservice:
		return "SERVICE_TYPE_MICROSERVICE"
	case ServiceTypeMCPServer:
		return "SERVICE_TYPE_MCP_SERVER"
	case ServiceTypeAIAgent:
		return "SERVICE_TYPE_AI_AGENT"
	default:
		return fmt.Sprintf("SERVICE_TYPE_UNKNOWN(%d)", int32(s))
	}
}

// ExtendedMetadataType 是服务扩展元数据类型。
type ExtendedMetadataType int32

const (
	// ExtendedMetadataUnknown 表示当前 SDK 不理解的扩展类型。
	ExtendedMetadataUnknown ExtendedMetadataType = 0
	// ExtendedMetadataSkill 表示 Agent Skill 引用。
	ExtendedMetadataSkill ExtendedMetadataType = 1
)

// AgentSkill 是 Agent Skill 的结构化描述。
type AgentSkill struct {
	ID          string
	Name        string
	Description string
	Tags        []string
	Examples    []string
	InputModes  []string
	OutputModes []string
	Version     string
	// Security 保存 A2A 0.3 skill 的原始 security 声明。
	Security json.RawMessage
	// SecurityRequirements 保存 A2A 1.0 skill 的原始 securityRequirements 声明。
	SecurityRequirements json.RawMessage
}

// ExtendedMetadata 是服务级扩展元数据。
type ExtendedMetadata struct {
	Type       ExtendedMetadataType
	AgentSkill *AgentSkill
}

// AgentService 是服务发现返回的完整 AI Agent 服务快照。
type AgentService struct {
	Namespace        string
	Name             string
	Type             ServiceType
	Metadata         map[string]string
	ExtendedMetadata []*ExtendedMetadata
	Owners           string
	Revision         string
}

// GetService 返回服务名。
func (a *AgentService) GetService() string {
	if a == nil {
		return ""
	}
	return a.Name
}

// GetNamespace 返回命名空间。
func (a *AgentService) GetNamespace() string {
	if a == nil {
		return ""
	}
	return a.Namespace
}

// GetMetadata 返回服务 metadata。
func (a *AgentService) GetMetadata() map[string]string {
	if a == nil {
		return nil
	}
	return a.Metadata
}

// BuildSkillRef 构建 namespace:name@version 形式的 Skill 引用。
func BuildSkillRef(namespace, name, version string) (string, error) {
	if err := ValidateSkillRefParts(namespace, name, version); err != nil {
		return "", err
	}
	return namespace + ":" + name + "@" + version, nil
}

// ValidateSkillRefParts 校验 namespace、name 和 version 是安全的引用片段。
func ValidateSkillRefParts(namespace, name, version string) error {
	if err := validateSkillSegment("namespace", namespace); err != nil {
		return err
	}
	if err := validateSkillSegment("name", name); err != nil {
		return err
	}
	return validateSkillSegment("version", version)
}

// ParseSkillRef 解析 namespace:name@version 形式的 Skill 引用。
func ParseSkillRef(value string) (namespace, name, version string, err error) {
	if strings.TrimSpace(value) != value || value == "" {
		return "", "", "", NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"skill ref must be non-empty and must not contain surrounding whitespace")
	}
	if strings.Count(value, ":") != 1 || strings.Count(value, "@") != 1 {
		return "", "", "", NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"skill ref %q must use namespace:name@version", value)
	}
	parts := strings.SplitN(value, ":", 2)
	nameAndVersion := strings.SplitN(parts[1], "@", 2)
	namespace, name, version = parts[0], nameAndVersion[0], nameAndVersion[1]
	if err = ValidateSkillRefParts(namespace, name, version); err != nil {
		return "", "", "", err
	}
	return namespace, name, version, nil
}

// NormalizeAgentSkills 校验 Skill 引用、补齐 name/version 并按 ID 去重。
func NormalizeAgentSkills(skills []*AgentSkill) ([]*AgentSkill, error) {
	result := make([]*AgentSkill, 0, len(skills))
	seen := make(map[string]struct{}, len(skills))
	for _, skill := range skills {
		if skill == nil {
			return nil, NewSDKError(ErrCodeAPIInvalidArgument, nil, "agent skill must not be nil")
		}
		_, parsedName, parsedVersion, err := ParseSkillRef(skill.ID)
		if err != nil {
			return nil, NewSDKError(ErrCodeAPIInvalidArgument, err, "invalid agent skill id %q", skill.ID)
		}
		if skill.Name != "" && skill.Name != parsedName {
			return nil, NewSDKError(ErrCodeAPIInvalidArgument, nil,
				"agent skill %q name %q does not match id", skill.ID, skill.Name)
		}
		if skill.Version != "" && skill.Version != parsedVersion {
			return nil, NewSDKError(ErrCodeAPIInvalidArgument, nil,
				"agent skill %q version %q does not match id", skill.ID, skill.Version)
		}
		if _, ok := seen[skill.ID]; ok {
			return nil, NewSDKError(ErrCodeAPIInvalidArgument, nil, "duplicate agent skill %q", skill.ID)
		}
		seen[skill.ID] = struct{}{}
		copySkill := *skill
		copySkill.Name = parsedName
		copySkill.Version = parsedVersion
		copySkill.Tags = append([]string(nil), skill.Tags...)
		copySkill.Examples = append([]string(nil), skill.Examples...)
		copySkill.InputModes = append([]string(nil), skill.InputModes...)
		copySkill.OutputModes = append([]string(nil), skill.OutputModes...)
		copySkill.Security = append(json.RawMessage(nil), skill.Security...)
		copySkill.SecurityRequirements = append(json.RawMessage(nil), skill.SecurityRequirements...)
		result = append(result, &copySkill)
	}
	return result, nil
}

// NormalizeAgentSkillValues 校验值类型 Skill 列表并按 ID 去重。
func NormalizeAgentSkillValues(skills []AgentSkill) ([]*AgentSkill, error) {
	pointers := make([]*AgentSkill, 0, len(skills))
	for index := range skills {
		skill := skills[index]
		pointers = append(pointers, &skill)
	}
	return NormalizeAgentSkills(pointers)
}

// ValidateInstanceMetadata 校验实例 metadata 的通用大小和敏感 key 限制。
func ValidateInstanceMetadata(metadata map[string]string) error {
	if len(metadata) > MaxInstanceMetadataEntries {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"instance metadata has %d entries, maximum is %d", len(metadata), MaxInstanceMetadataEntries)
	}
	for key, value := range metadata {
		if key == "" {
			return NewSDKError(ErrCodeAPIInvalidArgument, nil, "instance metadata key must not be empty")
		}
		keyRunes := utf8.RuneCountInString(key)
		if keyRunes > MaxMetadataKeyRunes {
			return NewSDKError(ErrCodeAPIInvalidArgument, nil,
				"instance metadata key %q has %d characters, maximum is %d",
				key, keyRunes, MaxMetadataKeyRunes)
		}
		valueRunes := utf8.RuneCountInString(value)
		if valueRunes > MaxMetadataValueRunes {
			return NewSDKError(ErrCodeAPIInvalidArgument, nil,
				"instance metadata value for %q has %d characters, maximum is %d",
				key, valueRunes, MaxMetadataValueRunes)
		}
		lowerKey := strings.ToLower(key)
		for _, sensitive := range []string{"token", "secret", "password", "authorization"} {
			if strings.Contains(lowerKey, sensitive) {
				return NewSDKError(ErrCodeAPIInvalidArgument, nil,
					"instance metadata key %q looks sensitive", key)
			}
		}
	}

	return nil
}

func validateSkillSegment(field, value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"skill %s must be non-empty and must not contain surrounding whitespace", field)
	}
	if value == "." || value == ".." || strings.ContainsAny(value, "/\\:@") {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"skill %s %q is not a safe path segment and must not contain ':' or '@'", field, value)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return NewSDKError(ErrCodeAPIInvalidArgument, nil,
				"skill %s contains control characters", field)
		}
	}
	return nil
}
