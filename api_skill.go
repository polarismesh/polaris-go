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

package polaris

import (
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

// GetSkillRequest 是查询单个 Skill 的请求。
type GetSkillRequest api.GetSkillRequest

// GetSkillListRequest 是查询 Skill 列表的请求。
type GetSkillListRequest api.GetSkillListRequest

// SkillDownloadRequest 是下载 Skill 的请求。
type SkillDownloadRequest api.SkillDownloadRequest

// SkillInstallRequest 是安装 Skill 的请求。
type SkillInstallRequest api.SkillInstallRequest

// SkillAPI 提供 Skill Registry 查询、下载和安装能力。
type SkillAPI interface {
	api.SDKOwner
	GetSkill(req *GetSkillRequest) (*model.SkillResource, error)
	GetSkillList(req *GetSkillListRequest) ([]*model.SkillResource, error)
	DownloadSkill(req *SkillDownloadRequest) (*model.SkillPackage, error)
	Install(req *SkillInstallRequest) (*model.InstalledSkill, error)
	Destroy()
}

type skillAPI struct {
	rawAPI api.SkillAPI
}

func (s *skillAPI) SDKContext() api.SDKContext {
	return s.rawAPI.SDKContext()
}

func (s *skillAPI) GetSkill(req *GetSkillRequest) (*model.SkillResource, error) {
	return s.rawAPI.GetSkill((*api.GetSkillRequest)(req))
}

func (s *skillAPI) GetSkillList(req *GetSkillListRequest) ([]*model.SkillResource, error) {
	return s.rawAPI.GetSkillList((*api.GetSkillListRequest)(req))
}

func (s *skillAPI) DownloadSkill(req *SkillDownloadRequest) (*model.SkillPackage, error) {
	return s.rawAPI.DownloadSkill((*api.SkillDownloadRequest)(req))
}

func (s *skillAPI) Install(req *SkillInstallRequest) (*model.InstalledSkill, error) {
	return s.rawAPI.Install((*api.SkillInstallRequest)(req))
}

func (s *skillAPI) Destroy() {
	s.rawAPI.Destroy()
}

// NewSkillAPI 通过默认配置创建 SkillAPI。
func NewSkillAPI() (SkillAPI, error) {
	rawAPI, err := api.NewSkillAPI()
	if err != nil {
		return nil, err
	}
	return &skillAPI{rawAPI: rawAPI}, nil
}

// NewSkillAPIByFile 通过配置文件创建 SkillAPI。
func NewSkillAPIByFile(path string) (SkillAPI, error) {
	rawAPI, err := api.NewSkillAPIByFile(path)
	if err != nil {
		return nil, err
	}
	return &skillAPI{rawAPI: rawAPI}, nil
}

// NewSkillAPIByConfig 通过配置对象创建 SkillAPI。
func NewSkillAPIByConfig(cfg config.Configuration) (SkillAPI, error) {
	rawAPI, err := api.NewSkillAPIByConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &skillAPI{rawAPI: rawAPI}, nil
}

// NewSkillAPIByContext 通过上下文创建 SkillAPI。
func NewSkillAPIByContext(context api.SDKContext) SkillAPI {
	return &skillAPI{rawAPI: api.NewSkillAPIByContext(context)}
}

// NewSkillAPIByDefaultConfigFile 从默认配置文件创建 SkillAPI。
func NewSkillAPIByDefaultConfigFile() (SkillAPI, error) {
	return NewSkillAPIByFile(config.DefaultConfigFile)
}

// NewSkillAPIByAddress 通过地址创建 SkillAPI。
func NewSkillAPIByAddress(address ...string) (SkillAPI, error) {
	rawAPI, err := api.NewSkillAPIByAddress(address...)
	if err != nil {
		return nil, err
	}
	return &skillAPI{rawAPI: rawAPI}, nil
}
