/**
 * Tencent is pleased to support the open source community by making polaris-go available.
 *
 * Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
 *
 * Licensed under the BSD 3-Clause License (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * https://opensource.org/licenses/BSD-3-Clause
 *
 * Unless required by applicable law or agreed to in writing, software distributed
 * under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
 * CONDITIONS OF ANY KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations under the License.
 */

package data

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

// TestInitByGetOneRequest_ArgumentsToEnvironmentVariables 验证:
// GetOneInstanceRequest 的 Arguments (Header / Query / Cookie / Method / Caller-IP / Path /
// Custom) 会被合并到 RouteInfo.EnvironmentVariables,使用带前缀的 label key
// ($header./$query./$cookie./$method/$caller_ip/$Path, Custom 按原 key),
// 与 lane router findTrafficValue 以及 polaris-java LaneUtils.findTrafficValue 的约定对齐。
func TestInitByGetOneRequest_ArgumentsToEnvironmentVariables(t *testing.T) {
	cfg := config.NewDefaultConfiguration(nil)

	tests := []struct {
		name     string
		args     []model.Argument
		expected map[string]string
	}{
		{
			name: "header and query arguments",
			args: []model.Argument{
				model.BuildHeaderArgument("user", "gray"),
				model.BuildQueryArgument("scene", "canary"),
			},
			expected: map[string]string{
				"$header.user":  "gray",
				"$query.scene":  "canary",
			},
		},
		{
			name:     "no arguments",
			args:     nil,
			expected: nil, // EnvironmentVariables 保持 nil,不隐式创建
		},
		{
			name: "custom argument",
			args: []model.Argument{
				model.BuildCustomArgument("uid", "42"),
			},
			expected: map[string]string{"uid": "42"},
		},
		{
			name: "method / caller_ip / path arguments",
			args: []model.Argument{
				model.BuildMethodArgument("GET"),
				model.BuildCallerIPArgument("10.0.0.1"),
				model.BuildPathArgument("/api/v1/echo"),
			},
			expected: map[string]string{
				"$method":     "GET",
				"$caller_ip":  "10.0.0.1",
				"$Path":       "/api/v1/echo",
			},
		},
		{
			name: "header and query with same short key do not collide",
			args: []model.Argument{
				model.BuildHeaderArgument("user", "gray"),
				model.BuildQueryArgument("user", "qval"),
				model.BuildCookieArgument("user", "cval"),
			},
			expected: map[string]string{
				"$header.user": "gray",
				"$query.user":  "qval",
				"$cookie.user": "cval",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &model.GetOneInstanceRequest{}
			req.Namespace = "default"
			req.Service = "LaneEchoClient"
			req.SourceService = &model.ServiceInfo{
				Namespace: "default",
				Service:   "LaneRouterGateway",
			}
			req.Arguments = tt.args

			common := &CommonInstancesRequest{}
			common.InitByGetOneRequest(req, cfg)

			if tt.expected == nil {
				assert.Nil(t, common.RouteInfo.EnvironmentVariables)
				return
			}
			assert.NotNil(t, common.RouteInfo.EnvironmentVariables)
			for k, v := range tt.expected {
				assert.Equal(t, v, common.RouteInfo.EnvironmentVariables[k], "key=%s", k)
			}
		})
	}
}

// TestInitByGetMultiRequest_ArgumentsToEnvironmentVariables: 与 GetOne 对称验证。
func TestInitByGetMultiRequest_ArgumentsToEnvironmentVariables(t *testing.T) {
	cfg := config.NewDefaultConfiguration(nil)

	req := &model.GetInstancesRequest{}
	req.Namespace = "default"
	req.Service = "LaneEchoClient"
	req.SourceService = &model.ServiceInfo{
		Namespace: "default",
		Service:   "LaneRouterGateway",
	}
	req.Arguments = []model.Argument{
		model.BuildHeaderArgument("user", "gray"),
	}

	common := &CommonInstancesRequest{}
	common.InitByGetMultiRequest(req, cfg)

	assert.NotNil(t, common.RouteInfo.EnvironmentVariables)
	assert.Equal(t, "gray", common.RouteInfo.EnvironmentVariables["$header.user"])
}

// TestInitByGetOneRequest_EnableSrcLane 在 SourceService 有效时,EnableSrcLane 应被触发。
// 这与 caller+callee 两侧拉取 lane rule 的能力对齐。
func TestInitByGetOneRequest_EnableSrcLane(t *testing.T) {
	cfg := config.NewDefaultConfiguration(nil)

	req := &model.GetOneInstanceRequest{}
	req.Namespace = "default"
	req.Service = "LaneEchoClient"
	req.SourceService = &model.ServiceInfo{
		Namespace: "default",
		Service:   "LaneRouterGateway",
	}

	common := &CommonInstancesRequest{}
	common.InitByGetOneRequest(req, cfg)

	assert.True(t, common.Trigger.EnableSrcRoute)
	assert.True(t, common.Trigger.EnableSrcLane)
	assert.True(t, common.Trigger.EnableDstLane)
}

// TestInitByGetOneRequest_NoSourceService_NoSrcLaneTrigger:
// 没有 SourceService 时 EnableSrcLane 不应被触发。
func TestInitByGetOneRequest_NoSourceService_NoSrcLaneTrigger(t *testing.T) {
	cfg := config.NewDefaultConfiguration(nil)

	req := &model.GetOneInstanceRequest{}
	req.Namespace = "default"
	req.Service = "LaneEchoClient"
	// no SourceService

	common := &CommonInstancesRequest{}
	common.InitByGetOneRequest(req, cfg)

	assert.False(t, common.Trigger.EnableSrcRoute)
	assert.False(t, common.Trigger.EnableSrcLane)
	assert.True(t, common.Trigger.EnableDstLane)
}
