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
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/sdk"
)

type unsupportedEngine struct {
	sdk.Engine
}

type unsupportedSDKContext struct {
	SDKContext
	engine sdk.Engine
}

func (c *unsupportedSDKContext) Destroy()              {}
func (c *unsupportedSDKContext) IsDestroyed() bool     { return false }
func (c *unsupportedSDKContext) GetEngine() sdk.Engine { return c.engine }

func newOwnershipTestContext(t *testing.T) SDKContext {
	t.Helper()
	cfg := config.NewDefaultConfiguration([]string{"127.0.0.1:1"})
	cfg.GetGlobal().GetAPI().SetBindIP("127.0.0.1")
	context, err := InitContextByConfig(cfg)
	require.NoError(t, err)
	return context
}

func TestBorrowedSkillAPIDoesNotDestroyContext(t *testing.T) {
	context := newOwnershipTestContext(t)
	defer context.Destroy()

	skill := NewSkillAPIByContext(context)
	var waitGroup sync.WaitGroup
	for index := 0; index < 16; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			skill.Close()
		}()
	}
	waitGroup.Wait()
	assert.False(t, context.IsDestroyed())

	_, err := skill.GetSkill(&GetSkillRequest{GetSkillRequest: model.GetSkillRequest{
		Namespace: "Production", Name: "weather",
	}})
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeInvalidStateError, model.GetErrorCodeFromError(err))
}

func TestOwnedSkillAndProviderAPIsDestroyContext(t *testing.T) {
	skill, err := NewSkillAPIByConfig(config.NewDefaultConfiguration([]string{"127.0.0.1:1"}))
	require.NoError(t, err)
	skillContext := skill.SDKContext()
	require.NotNil(t, skillContext)
	skill.Close()
	skill.Close()
	assert.True(t, skillContext.IsDestroyed())

	provider, err := NewProviderAPIByConfig(config.NewDefaultConfiguration([]string{"127.0.0.1:1"}))
	require.NoError(t, err)
	providerContext := provider.SDKContext()
	require.NotNil(t, providerContext)
	provider.Destroy()
	provider.Destroy()
	assert.True(t, providerContext.IsDestroyed())
}

func TestAPIsRejectDestroyedContextAndNilContext(t *testing.T) {
	context := newOwnershipTestContext(t)
	skill := NewSkillAPIByContext(context)
	provider := NewProviderAPIByContext(context)
	consumer := NewConsumerAPIByContext(context)
	context.Destroy()

	_, err := skill.GetSkill(&GetSkillRequest{GetSkillRequest: model.GetSkillRequest{
		Namespace: "Production", Name: "weather",
	}})
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeInvalidStateError, model.GetErrorCodeFromError(err))

	_, err = provider.ReportServiceContract(&ReportServiceContractRequest{
		ServiceContract: &model.ServiceContract{
			GetServiceContractRequest: model.GetServiceContractRequest{
				Namespace: "Production", Service: "search-mcp", Protocol: "http",
			},
		},
	})
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeInvalidStateError, model.GetErrorCodeFromError(err))

	_, err = consumer.GetServiceContract(&GetServiceContractRequest{})
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeInvalidStateError, model.GetErrorCodeFromError(err))

	_, err = NewSkillAPIByContext(nil).GetSkill(&GetSkillRequest{})
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeAPIInvalidArgument, model.GetErrorCodeFromError(err))
}

func TestOptionalEngineCapabilitiesRemainSourceCompatible(t *testing.T) {
	context := &unsupportedSDKContext{engine: &unsupportedEngine{}}
	skill := NewSkillAPIByContext(context)
	_, err := skill.GetSkillListResult(&GetSkillListRequest{GetSkillListRequest: model.GetSkillListRequest{
		Namespace: "Production",
	}})
	require.Error(t, err)
	assert.Equal(t, model.ErrCodePluginError, model.GetErrorCodeFromError(err))
}
