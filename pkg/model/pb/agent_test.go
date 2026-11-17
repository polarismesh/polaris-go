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
	"testing"

	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/local"
)

func TestAgentServiceProtoRoundTrip(t *testing.T) {
	original := &model.AgentService{
		Namespace: "Production",
		Name:      "customer-agent",
		Type:      model.ServiceTypeAIAgent,
		Metadata:  map[string]string{"owner": "agent-team"},
		ExtendedMetadata: []*model.ExtendedMetadata{
			{
				Type: model.ExtendedMetadataSkill,
				AgentSkill: &model.AgentSkill{
					ID:      "Production:demo-skill@1.2.3",
					Name:    "demo-skill",
					Version: "1.2.3",
					Tags:    []string{"support"},
				},
			},
			{
				Type: model.ExtendedMetadataType(999),
			},
		},
		Owners:   "agent-team",
		Revision: "rev-1",
	}
	converted := ToAgentServiceProto(original)
	actual := NewAgentServiceFromProto(converted)
	assert.Equal(t, original.Namespace, actual.Namespace)
	assert.Equal(t, original.Name, actual.Name)
	assert.Equal(t, original.Type, actual.Type)
	assert.Equal(t, original.Metadata, actual.Metadata)
	assert.Equal(t, original.Owners, actual.Owners)
	assert.Equal(t, original.Revision, actual.Revision)
	require.Len(t, actual.ExtendedMetadata, 2)
	require.NotNil(t, actual.ExtendedMetadata[0].AgentSkill)
	assert.Equal(t, "1.2.3", actual.ExtendedMetadata[0].AgentSkill.Version)
	assert.Equal(t, model.ExtendedMetadataType(999), actual.ExtendedMetadata[1].Type)
}

func TestServiceInstancesProtoKeepsServiceMetadata(t *testing.T) {
	response := &apiservice.DiscoverResponse{
		AliasFor: &apiservice.Service{
			Name: wrapperspb.String("customer-agent"), Namespace: wrapperspb.String("Production"),
			Revision: wrapperspb.String("rev-1"),
		},
		Service: &apiservice.Service{
			Name:        wrapperspb.String("customer-agent"),
			Namespace:   wrapperspb.String("Production"),
			ServiceType: apiservice.ServiceType_SERVICE_TYPE_AI_AGENT,
			Metadata:    map[string]string{"owner": "agent-team"},
			Owners:      wrapperspb.String("agent-team"),
			Revision:    wrapperspb.String("rev-1"),
			ExtendedMetadata: []*apiservice.ExtendedMetadata{{
				Type: apiservice.ExtendedMetadata_EXTENDED_METADATA_SKILL,
				AgentSkill: &apiservice.AgentSkill{
					Id:      "Production:demo-skill@1.2.3",
					Name:    "demo-skill",
					Version: "1.2.3",
					Tags:    []string{"support"},
				},
			}},
		},
	}
	instances := NewServiceInstancesInProto(response, nil, nil, nil, log.GetBaseLogger())
	assert.Equal(t, model.ServiceTypeAIAgent, instances.GetServiceType())
	assert.Equal(t, "agent-team", instances.GetMetadata()["owner"])
	assert.Equal(t, "rev-1", instances.GetServiceRevision())
	extended := instances.GetExtendedMetadata()
	require.Len(t, extended, 1)
	assert.Equal(t, "1.2.3", extended[0].AgentSkill.Version)
	extended[0].AgentSkill.Tags[0] = "changed"
	assert.Equal(t, "support", instances.GetExtendedMetadata()[0].AgentSkill.Tags[0])
}

func TestServiceInstancesProtoDoesNotProjectMissingAgentService(t *testing.T) {
	response := &apiservice.DiscoverResponse{
		Code: wrapperspb.UInt32(uint32(apimodel.Code_NotFoundResource)),
		Service: &apiservice.Service{
			Name:      wrapperspb.String("customer-agent"),
			Namespace: wrapperspb.String("Production"),
		},
	}
	instances := NewServiceInstancesInProto(response, nil, nil, nil, log.GetBaseLogger())
	assert.Nil(t, instances.GetExtendedMetadata())
}

func TestServiceInstancesProtoDoesNotInferTypeFromJavaInstanceMetadata(t *testing.T) {
	response := &apiservice.DiscoverResponse{
		Service: &apiservice.Service{
			Name:      wrapperspb.String("java-agent"),
			Namespace: wrapperspb.String("Production"),
			Revision:  wrapperspb.String("rev-java"),
		},
		Instances: []*apiservice.Instance{{
			Id: wrapperspb.String("instance-1"),
			Metadata: map[string]string{
				model.MetadataKeyAIAgentName: "java-agent",
			},
		}},
	}
	instances := NewServiceInstancesInProto(response, func(string) local.InstanceLocalValue {
		return local.NewInstanceLocalValue()
	}, nil, nil, log.GetBaseLogger())
	assert.Equal(t, model.ServiceTypeMicroservice, instances.GetServiceType())
	assert.Empty(t, instances.GetServiceRevision())
}

func TestServiceInstancesProtoDoesNotInferAgentFromEmptyMetadata(t *testing.T) {
	tests := []struct {
		name      string
		service   *apiservice.Service
		instances []*apiservice.Instance
	}{
		{
			name: "empty_instance_agent_name",
			service: &apiservice.Service{
				Name:      wrapperspb.String("plain-service"),
				Namespace: wrapperspb.String("Production"),
				Revision:  wrapperspb.String("rev-plain"),
			},
			instances: []*apiservice.Instance{{
				Id: wrapperspb.String("instance-1"),
				Metadata: map[string]string{
					model.MetadataKeyAIAgentName: "",
				},
			}},
		},
		{
			name: "empty_skill_reference",
			service: &apiservice.Service{
				Name:      wrapperspb.String("plain-service"),
				Namespace: wrapperspb.String("Production"),
				Revision:  wrapperspb.String("rev-plain"),
				ExtendedMetadata: []*apiservice.ExtendedMetadata{{
					Type:       apiservice.ExtendedMetadata_EXTENDED_METADATA_SKILL,
					AgentSkill: &apiservice.AgentSkill{},
				}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := &apiservice.DiscoverResponse{
				Service:   tt.service,
				Instances: tt.instances,
			}
			instances := NewServiceInstancesInProto(response, func(string) local.InstanceLocalValue {
				return local.NewInstanceLocalValue()
			}, nil, nil, log.GetBaseLogger())
			assert.Equal(t, model.ServiceTypeMicroservice, instances.GetServiceType())
		})
	}
}

func TestServiceInstancesProtoDoesNotInferTypeFromMountedSkill(t *testing.T) {
	response := &apiservice.DiscoverResponse{
		Service: &apiservice.Service{
			Name:      wrapperspb.String("skill-agent"),
			Namespace: wrapperspb.String("Production"),
			Revision:  wrapperspb.String("rev-skill"),
			ExtendedMetadata: []*apiservice.ExtendedMetadata{{
				Type:       apiservice.ExtendedMetadata_EXTENDED_METADATA_SKILL,
				AgentSkill: &apiservice.AgentSkill{Id: "Production:demo@1.0.0"},
			}},
		},
	}
	instances := NewServiceInstancesInProto(response, nil, nil, nil, log.GetBaseLogger())
	assert.Equal(t, model.ServiceTypeMicroservice, instances.GetServiceType())
	require.Len(t, instances.GetExtendedMetadata(), 1)
}

func TestServiceRevisionDoesNotUseInstanceAggregateRevision(t *testing.T) {
	instances := NewServiceInstancesInProto(&apiservice.DiscoverResponse{
		Service: &apiservice.Service{
			Name: wrapperspb.String("customer-agent"), Namespace: wrapperspb.String("Production"),
			Revision: wrapperspb.String("instance-aggregate-revision"),
		},
	}, nil, nil, nil, log.GetBaseLogger())
	assert.Equal(t, "instance-aggregate-revision", instances.GetRevision())
	assert.Empty(t, instances.GetServiceRevision())
}

func TestServiceRevisionRequiresMatchingResourceProjection(t *testing.T) {
	for _, test := range []struct {
		name      string
		namespace string
		service   string
		expected  string
	}{
		{name: "same_resource", namespace: "Production", service: "customer-agent", expected: "resource-revision"},
		{name: "different_service", namespace: "Production", service: "another-agent"},
		{name: "different_namespace", namespace: "Staging", service: "customer-agent"},
	} {
		t.Run(test.name, func(t *testing.T) {
			instances := NewServiceInstancesInProto(&apiservice.DiscoverResponse{
				Service: &apiservice.Service{
					Name: wrapperspb.String("customer-agent"), Namespace: wrapperspb.String("Production"),
					Revision: wrapperspb.String("instance-aggregate-revision"),
				},
				AliasFor: &apiservice.Service{
					Name: wrapperspb.String(test.service), Namespace: wrapperspb.String(test.namespace),
					Revision: wrapperspb.String("resource-revision"),
				},
			}, nil, nil, nil, log.GetBaseLogger())
			assert.Equal(t, "instance-aggregate-revision", instances.GetRevision())
			assert.Equal(t, test.expected, instances.GetServiceRevision())
		})
	}
}
