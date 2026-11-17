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

package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// Console is used only for isolated fixture setup, not as a Core SDK service API.
func (s *suite) prepareAgentFixture(ctx context.Context) error {
	name := s.serviceName("agent")
	if err := s.recordService(name); err != nil {
		return err
	}
	skills := []model.AgentSkill{
		skillValues(s.opts.namespace, currencySkill, "1.0.0", "currency"),
		skillValues(s.opts.namespace, weatherSkill, "1.0.0", "weather"),
	}
	extended := make([]map[string]interface{}, 0, len(skills))
	for _, skill := range skills {
		extended = append(extended, map[string]interface{}{
			"type": "EXTENDED_METADATA_SKILL",
			"agent_skill": map[string]interface{}{
				"id": skill.ID, "name": skill.Name, "version": skill.Version,
				"description": skill.Description, "tags": skill.Tags, "examples": skill.Examples,
				"input_modes": skill.InputModes, "output_modes": skill.OutputModes,
			},
		})
	}
	_, err := s.console.request(ctx, http.MethodPost, "/naming/v1/services", []map[string]interface{}{{
		"namespace": s.opts.namespace, "name": name, "service_type": "SERVICE_TYPE_AI_AGENT",
		"metadata": s.serviceMetadata(), "extended_metadata": extended,
	}})
	return err
}

func (s *suite) agentFixture(ctx context.Context) error {
	return eventually(ctx, 20*time.Second, func() error {
		service, err := s.getAgent(s.serviceName("agent"))
		if err != nil {
			return err
		}
		if service.Type != model.ServiceTypeAIAgent || service.Metadata[runLabel] != s.opts.runID {
			return fmt.Errorf("Consumer did not return the actual AI_AGENT fixture metadata")
		}
		return requireIDs(service, s.opts.namespace+":"+currencySkill+"@1.0.0", s.opts.namespace+":"+weatherSkill+"@1.0.0")
	})
}

func (s *suite) ordinaryService(ctx context.Context) error {
	name := s.serviceName("ordinary")
	if err := s.recordService(name); err != nil {
		return err
	}
	if _, err := s.console.request(ctx, http.MethodPost, "/naming/v1/services", []map[string]interface{}{{
		"namespace": s.opts.namespace, "name": name, "service_type": "SERVICE_TYPE_MICROSERVICE",
		"metadata": s.serviceMetadata(),
	}}); err != nil {
		return err
	}
	return eventually(ctx, 15*time.Second, func() error {
		service, err := s.getAgent(name)
		if err != nil {
			return err
		}
		if service.Type != model.ServiceTypeMicroservice {
			return fmt.Errorf("Consumer changed the ordinary service type")
		}
		return nil
	})
}
