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

import "github.com/polarismesh/polaris-go/pkg/model"

// GetSkillRequest 是查询单个 Skill 的请求。
type GetSkillRequest struct {
	model.GetSkillRequest
}

// GetSkillListRequest 是查询 Skill 列表的请求。
type GetSkillListRequest struct {
	model.GetSkillListRequest
}

// SkillListResult 是包含服务端分页总数的 Skill 列表结果。
type SkillListResult struct {
	model.SkillListResult
}

// SkillDownloadRequest 是下载 Skill 的请求。
type SkillDownloadRequest struct {
	model.SkillDownloadRequest
}

// SkillInstallRequest 是安装 Skill 的请求。
type SkillInstallRequest struct {
	model.SkillInstallRequest
}

// SkillAPI 提供 Skill Registry 查询、下载和安装能力。
type SkillAPI interface {
	SDKOwner
	// GetSkill 获取单个 published Skill。
	GetSkill(req *GetSkillRequest) (*model.SkillResource, error)
	// GetSkillList 获取 published Skill 列表。
	GetSkillList(req *GetSkillListRequest) ([]*model.SkillResource, error)
	// GetSkillListResult 获取包含服务端分页总数的 published Skill 列表。
	GetSkillListResult(req *GetSkillListRequest) (*SkillListResult, error)
	// DownloadSkill 下载 Skill。
	DownloadSkill(req *SkillDownloadRequest) (*model.SkillPackage, error)
	// Install 下载并安装 Skill。
	Install(req *SkillInstallRequest) (*model.InstalledSkill, error)
	// Close 关闭 API；借用 Context 时不会销毁 Context。
	Close()
	// Destroy 销毁 API。
	Destroy()
}

var (
	// NewSkillAPI 通过默认配置创建 SkillAPI。
	NewSkillAPI = newSkillAPI
	// NewSkillAPIByFile 通过配置文件创建 SkillAPI。
	NewSkillAPIByFile = newSkillAPIByFile
	// NewSkillAPIByConfig 通过配置对象创建 SkillAPI。
	NewSkillAPIByConfig = newSkillAPIByConfig
	// NewSkillAPIByContext 通过上下文创建 SkillAPI。
	NewSkillAPIByContext = newSkillAPIByContext
	// NewSkillAPIByDefaultConfigFile 从默认配置文件创建 SkillAPI。
	NewSkillAPIByDefaultConfigFile = newSkillAPIByDefaultConfigFile
	// NewSkillAPIByAddress 通过地址创建 SkillAPI。
	NewSkillAPIByAddress = newSkillAPIByAddress
)
