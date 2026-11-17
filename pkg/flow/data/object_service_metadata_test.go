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

package data

import (
	"testing"

	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
)

func TestInstancesResponseKeepsDetachedServiceMetadata(t *testing.T) {
	key := model.ServiceKey{Namespace: "Production", Service: "agent"}
	service := &apiservice.Service{
		Namespace: wrapperspb.String(key.Namespace), Name: wrapperspb.String(key.Service),
		ServiceType: apiservice.ServiceType_SERVICE_TYPE_AI_AGENT,
		Revision:    wrapperspb.String("service-revision"), Metadata: map[string]string{"owner": "team"},
		ExtendedMetadata: []*apiservice.ExtendedMetadata{{
			Type:       apiservice.ExtendedMetadata_EXTENDED_METADATA_SKILL,
			AgentSkill: &apiservice.AgentSkill{Id: "Production:weather@1.0.0", Tags: []string{"original"}},
		}},
	}
	source := pb.NewServiceInstancesInProto(&apiservice.DiscoverResponse{Service: service, AliasFor: service},
		nil, nil, nil, log.GetBaseLogger())
	response := BuildInstancesResponse(key, nil, source)
	require.Equal(t, model.ServiceTypeAIAgent, response.ServiceType)
	require.Equal(t, "service-revision", response.ServiceRevision)
	require.Len(t, response.ExtendedMetadata, 1)
	response.Metadata["owner"] = "caller-mutation"
	response.ExtendedMetadata[0].AgentSkill.Tags[0] = "caller-mutation"
	again := BuildInstancesResponse(key, nil, source)
	require.Equal(t, "team", again.Metadata["owner"])
	require.Equal(t, "original", again.ExtendedMetadata[0].AgentSkill.Tags[0])

	plain := model.NewDefaultServiceInstances(model.ServiceInfo{Namespace: key.Namespace, Service: key.Service}, nil)
	buildInstancesResponse(response, key, nil, nil, 0, plain)
	require.Equal(t, model.ServiceTypeMicroservice, response.ServiceType)
	require.Empty(t, response.ServiceRevision)
	require.Nil(t, response.ExtendedMetadata)
}
