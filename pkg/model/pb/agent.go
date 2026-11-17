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

package pb

import (
	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// NewAgentServiceFromProto 将 specification Service 转换为 SDK 公开模型。
func NewAgentServiceFromProto(service *apiservice.Service) *model.AgentService {
	if service == nil {
		return nil
	}
	result := &model.AgentService{
		Namespace: service.GetNamespace().GetValue(),
		Name:      service.GetName().GetValue(),
		Type:      model.ServiceType(service.GetServiceType()),
		Metadata:  cloneStringMap(service.GetMetadata()),
		Owners:    service.GetOwners().GetValue(),
		Revision:  service.GetRevision().GetValue(),
	}
	if service.ExtendedMetadata != nil {
		result.ExtendedMetadata = make([]*model.ExtendedMetadata, 0, len(service.ExtendedMetadata))
		for _, item := range service.ExtendedMetadata {
			result.ExtendedMetadata = append(result.ExtendedMetadata, NewExtendedMetadataFromProto(item))
		}
	}
	return result
}

// NewExtendedMetadataFromProto 将 specification 扩展元数据转换为 SDK 模型。
func NewExtendedMetadataFromProto(metadata *apiservice.ExtendedMetadata) *model.ExtendedMetadata {
	if metadata == nil {
		return nil
	}
	return &model.ExtendedMetadata{
		Type:       model.ExtendedMetadataType(metadata.GetType()),
		AgentSkill: NewAgentSkillFromProto(metadata.GetAgentSkill()),
	}
}

// NewAgentSkillFromProto 将 specification AgentSkill 转换为 SDK 模型。
func NewAgentSkillFromProto(skill *apiservice.AgentSkill) *model.AgentSkill {
	if skill == nil {
		return nil
	}
	return &model.AgentSkill{
		ID:          skill.GetId(),
		Name:        skill.GetName(),
		Description: skill.GetDescription(),
		Tags:        append([]string(nil), skill.GetTags()...),
		Examples:    append([]string(nil), skill.GetExamples()...),
		InputModes:  append([]string(nil), skill.GetInputModes()...),
		OutputModes: append([]string(nil), skill.GetOutputModes()...),
		Version:     skill.GetVersion(),
	}
}

// ToExtendedMetadataProto 将 SDK 扩展元数据转换为 specification 类型。
func ToExtendedMetadataProto(metadata []*model.ExtendedMetadata) []*apiservice.ExtendedMetadata {
	if metadata == nil {
		return nil
	}
	result := make([]*apiservice.ExtendedMetadata, 0, len(metadata))
	for _, item := range metadata {
		if item == nil {
			result = append(result, nil)
			continue
		}
		result = append(result, &apiservice.ExtendedMetadata{
			Type:       apiservice.ExtendedMetadata_ExtendedMetadataType(item.Type),
			AgentSkill: ToAgentSkillProto(item.AgentSkill),
		})
	}
	return result
}

// ToAgentSkillProto 将 SDK AgentSkill 转换为 specification 类型。
func ToAgentSkillProto(skill *model.AgentSkill) *apiservice.AgentSkill {
	if skill == nil {
		return nil
	}
	return &apiservice.AgentSkill{
		Id:          skill.ID,
		Name:        skill.Name,
		Description: skill.Description,
		Tags:        append([]string(nil), skill.Tags...),
		Examples:    append([]string(nil), skill.Examples...),
		InputModes:  append([]string(nil), skill.InputModes...),
		OutputModes: append([]string(nil), skill.OutputModes...),
		Version:     skill.Version,
	}
}

// ToAgentServiceProto 将 SDK AgentService 转换为 specification 类型。
func ToAgentServiceProto(service *model.AgentService) *apiservice.Service {
	if service == nil {
		return nil
	}
	return &apiservice.Service{
		Name:             wrapperspb.String(service.Name),
		Namespace:        wrapperspb.String(service.Namespace),
		ServiceType:      apiservice.ServiceType(service.Type),
		Metadata:         cloneStringMap(service.Metadata),
		ExtendedMetadata: ToExtendedMetadataProto(service.ExtendedMetadata),
		Owners:           wrapperspb.String(service.Owners),
		Revision:         wrapperspb.String(service.Revision),
	}
}

func cloneStringMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	result := make(map[string]string, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}
