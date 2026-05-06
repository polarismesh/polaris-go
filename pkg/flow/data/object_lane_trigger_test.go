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

// TestInitByGetOneRequest_EnableSrcLane 在 SourceService 有效时, EnableSrcLane 应被触发,
// 与 caller + callee 两侧拉取 lane rule 的能力对齐。
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
