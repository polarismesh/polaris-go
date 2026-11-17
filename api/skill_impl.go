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
	"sync/atomic"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

type skillAPI struct {
	context     SDKContext
	ownsContext bool
	closed      uint32
}

type skillListResultEngine interface {
	SyncGetSkillListResult(req *model.GetSkillListRequest) (*model.SkillListResult, error)
}

// GetSkill 获取单个 published Skill。
func (s *skillAPI) GetSkill(req *GetSkillRequest) (*model.SkillResource, error) {
	if err := checkSkillAvailable(s); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"GetSkillRequest can not be nil")
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	return s.context.GetEngine().SyncGetSkill(&req.GetSkillRequest)
}

// GetSkillList 获取 published Skill 列表。
func (s *skillAPI) GetSkillList(req *GetSkillListRequest) ([]*model.SkillResource, error) {
	if err := checkSkillAvailable(s); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"GetSkillListRequest can not be nil")
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	return s.context.GetEngine().SyncGetSkillList(&req.GetSkillListRequest)
}

// GetSkillListResult 获取包含服务端分页总数的 published Skill 列表。
func (s *skillAPI) GetSkillListResult(req *GetSkillListRequest) (*SkillListResult, error) {
	if err := checkSkillAvailable(s); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"GetSkillListRequest can not be nil")
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	engine, ok := s.context.GetEngine().(skillListResultEngine)
	if !ok {
		return nil, model.NewSDKError(model.ErrCodePluginError, nil,
			"skill engine does not support GetSkillListResult")
	}
	result, err := engine.SyncGetSkillListResult(&req.GetSkillListRequest)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, model.NewSDKError(model.ErrCodeInvalidResponse, nil,
			"skill engine returned an empty list result")
	}
	return &SkillListResult{SkillListResult: *result}, nil
}

// DownloadSkill 下载 Skill。
func (s *skillAPI) DownloadSkill(req *SkillDownloadRequest) (*model.SkillPackage, error) {
	if err := checkSkillAvailable(s); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"SkillDownloadRequest can not be nil")
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	downloadReq := req.SkillDownloadRequest
	if downloadReq.Format == "" {
		downloadReq.Format = model.SkillFormatMarkdown
	}
	return s.context.GetEngine().SyncDownloadSkill(&downloadReq)
}

// Install 下载并安装 Skill。
func (s *skillAPI) Install(req *SkillInstallRequest) (*model.InstalledSkill, error) {
	if err := checkSkillAvailable(s); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"SkillInstallRequest can not be nil")
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	return s.context.GetEngine().SyncInstallSkill(&req.SkillInstallRequest)
}

// SDKContext 获取 SDK 上下文。
func (s *skillAPI) SDKContext() SDKContext {
	if s == nil {
		return nil
	}
	return s.context
}

// Close 关闭 API；借用 Context 时不会销毁 Context。
func (s *skillAPI) Close() {
	if s == nil || !atomic.CompareAndSwapUint32(&s.closed, 0, 1) {
		return
	}
	if s.ownsContext && s.context != nil {
		s.context.Destroy()
	}
}

// Destroy 销毁 API。
func (s *skillAPI) Destroy() {
	s.Close()
}

func checkSkillAvailable(s *skillAPI) error {
	if s == nil || s.context == nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil, "SkillAPI can not be nil")
	}
	if atomic.LoadUint32(&s.closed) != 0 {
		return model.NewSDKError(model.ErrCodeInvalidStateError, nil,
			"SkillAPI has been closed")
	}
	return checkAvailable(s)
}

func newSkillAPI() (SkillAPI, error) {
	return NewSkillAPIByConfig(config.NewDefaultConfigurationWithDomain())
}

func newSkillAPIByFile(path string) (SkillAPI, error) {
	context, err := InitContextByFile(path)
	if err != nil {
		return nil, err
	}
	return &skillAPI{context: context, ownsContext: true}, nil
}

func newSkillAPIByConfig(cfg config.Configuration) (SkillAPI, error) {
	context, err := InitContextByConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &skillAPI{context: context, ownsContext: true}, nil
}

func newSkillAPIByContext(context SDKContext) SkillAPI {
	return &skillAPI{context: context, ownsContext: false}
}

func newSkillAPIByDefaultConfigFile() (SkillAPI, error) {
	return NewSkillAPIByFile(config.DefaultConfigFile)
}

func newSkillAPIByAddress(address ...string) (SkillAPI, error) {
	return newSkillAPIByConfig(config.NewDefaultConfiguration(address))
}
