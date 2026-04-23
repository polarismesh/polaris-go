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

package api

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// TestGetOneInstanceRequest_Convert_WithSourceServiceNilMetadata 验证:
// 当用户显式传入 SourceService 但未初始化 Metadata 时，convert() 不应 panic,
// 而应自动补上 Metadata 并把 Arguments 写入其中。
// 这是泳道路由 (GetOneInstance + AddArguments) 场景下的典型调用路径。
func TestGetOneInstanceRequest_Convert_WithSourceServiceNilMetadata(t *testing.T) {
	req := &GetOneInstanceRequest{}
	req.Namespace = "default"
	req.Service = "LaneEchoClient"
	// 用户常见写法: 只给出 SourceService 的 Namespace/Service，不预初始化 Metadata
	req.SourceService = &model.ServiceInfo{
		Namespace: "default",
		Service:   "LaneRouterGateway",
	}
	req.AddArguments(
		model.BuildHeaderArgument("user", "gray"),
		model.BuildQueryArgument("scene", "canary"),
	)

	assert.NotPanics(t, func() { req.convert() })

	assert.NotNil(t, req.SourceService.Metadata)
	assert.Equal(t, "gray", req.SourceService.Metadata["$header.user"])
	assert.Equal(t, "canary", req.SourceService.Metadata["$query.scene"])
}

// TestGetOneInstanceRequest_Convert_WithNilSourceService 保持原有行为:
// 完全不传 SourceService 时 convert() 应自动创建。
func TestGetOneInstanceRequest_Convert_WithNilSourceService(t *testing.T) {
	req := &GetOneInstanceRequest{}
	req.Namespace = "default"
	req.Service = "LaneEchoClient"
	req.AddArguments(model.BuildHeaderArgument("user", "gray"))

	assert.NotPanics(t, func() { req.convert() })
	assert.NotNil(t, req.SourceService)
	assert.Equal(t, "gray", req.SourceService.Metadata["$header.user"])
}

// TestGetOneInstanceRequest_Convert_NoArguments_NoMetadataInit:
// 没有 Arguments 时不应触及 SourceService.Metadata,保持用户原始结构不动。
func TestGetOneInstanceRequest_Convert_NoArguments_NoMetadataInit(t *testing.T) {
	req := &GetOneInstanceRequest{}
	req.Namespace = "default"
	req.Service = "LaneEchoClient"
	req.SourceService = &model.ServiceInfo{
		Namespace: "default",
		Service:   "LaneRouterGateway",
	}

	assert.NotPanics(t, func() { req.convert() })
	// 无 Arguments -> 保持 Metadata 为 nil,不隐式创建。
	assert.Nil(t, req.SourceService.Metadata)
}

// TestGetInstancesRequest_Convert_WithSourceServiceNilMetadata: 与 GetOneInstance 对称验证。
func TestGetInstancesRequest_Convert_WithSourceServiceNilMetadata(t *testing.T) {
	req := &GetInstancesRequest{}
	req.Namespace = "default"
	req.Service = "LaneEchoClient"
	req.SourceService = &model.ServiceInfo{
		Namespace: "default",
		Service:   "LaneRouterGateway",
	}
	req.AddArguments(model.BuildHeaderArgument("user", "gray"))

	assert.NotPanics(t, func() { req.convert() })
	assert.NotNil(t, req.SourceService.Metadata)
	assert.Equal(t, "gray", req.SourceService.Metadata["$header.user"])
}
