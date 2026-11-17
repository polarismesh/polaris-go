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

package flow

import "github.com/polarismesh/polaris-go/pkg/model"

// SyncGetSkill 获取单个 published Skill。
func (e *Engine) SyncGetSkill(req *model.GetSkillRequest) (*model.SkillResource, error) {
	if e.skillFlow == nil {
		return nil, model.NewSDKError(model.ErrCodeSkillNotConfigured, nil,
			"skill.addresses is not configured or skill.enable is false")
	}
	return e.skillFlow.GetSkill(req)
}

// SyncGetSkillList 获取 published Skill 列表。
func (e *Engine) SyncGetSkillList(req *model.GetSkillListRequest) ([]*model.SkillResource, error) {
	if e.skillFlow == nil {
		return nil, model.NewSDKError(model.ErrCodeSkillNotConfigured, nil,
			"skill.addresses is not configured or skill.enable is false")
	}
	return e.skillFlow.GetSkillList(req)
}

// SyncGetSkillListResult 获取包含服务端分页总数的 published Skill 列表。
func (e *Engine) SyncGetSkillListResult(
	req *model.GetSkillListRequest,
) (*model.SkillListResult, error) {
	if e.skillFlow == nil {
		return nil, model.NewSDKError(model.ErrCodeSkillNotConfigured, nil,
			"skill.addresses is not configured or skill.enable is false")
	}
	return e.skillFlow.GetSkillListResult(req)
}

// SyncDownloadSkill 下载 Skill。
func (e *Engine) SyncDownloadSkill(req *model.SkillDownloadRequest) (*model.SkillPackage, error) {
	if e.skillFlow == nil {
		return nil, model.NewSDKError(model.ErrCodeSkillNotConfigured, nil,
			"skill.addresses is not configured or skill.enable is false")
	}
	return e.skillFlow.DownloadSkill(req)
}

// SyncInstallSkill 安装 Skill。
func (e *Engine) SyncInstallSkill(req *model.SkillInstallRequest) (*model.InstalledSkill, error) {
	if e.skillFlow == nil {
		return nil, model.NewSDKError(model.ErrCodeSkillNotConfigured, nil,
			"skill.addresses is not configured or skill.enable is false")
	}
	return e.skillFlow.Install(req)
}
