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

package mock

import (
	"context"
	"sync"

	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	skillmanage "github.com/polarismesh/specification/source/go/api/v1/skill_manage"
)

const mockSkillChunkSize = 1024 * 1024

// SkillServer 是 PolarisSkill gRPC 的内存测试服务。
type SkillServer struct {
	mu        sync.RWMutex
	resources map[string]*skillmanage.SkillResource
	versions  map[string]*skillmanage.SkillResourceVersion
	contents  map[string][]byte
}

// NewSkillServer 创建内存 Skill 服务。
func NewSkillServer() *SkillServer {
	return &SkillServer{
		resources: make(map[string]*skillmanage.SkillResource),
		versions:  make(map[string]*skillmanage.SkillResourceVersion),
		contents:  make(map[string][]byte),
	}
}

// AddSkill 添加 published Skill。
func (s *SkillServer) AddSkill(resource *skillmanage.SkillResource, version *skillmanage.SkillResourceVersion,
	content []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resources[skillKey(resource.GetNamespace(), resource.GetName(), version.GetVersion())] = resource
	s.resources[skillKey(resource.GetNamespace(), resource.GetName(), "")] = resource
	s.versions[skillKey(version.GetNamespace(), version.GetName(), version.GetVersion())] = version
	s.contents[skillKey(version.GetNamespace(), version.GetName(), version.GetVersion())] = append([]byte(nil), content...)
}

// GetSkill 获取 published Skill。
func (s *SkillServer) GetSkill(_ context.Context,
	req *skillmanage.GetSkillRequest) (*skillmanage.GetSkillResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	version := req.GetVersion()
	if version == "" {
		if resource := s.resources[skillKey(req.GetNamespace(), req.GetName(), "")]; resource != nil {
			version = resource.GetVersionInfo().GetActiveVersion()
		}
	}
	resource := s.resources[skillKey(req.GetNamespace(), req.GetName(), version)]
	versionInfo := s.versions[skillKey(req.GetNamespace(), req.GetName(), version)]
	if resource == nil || versionInfo == nil {
		return &skillmanage.GetSkillResponse{
			Code: uint32(apimodel.Code_NotFoundResource),
			Info: "skill not found",
		}, nil
	}
	return &skillmanage.GetSkillResponse{
		Code:     uint32(apimodel.Code_ExecuteSuccess),
		Info:     "success",
		Resource: resource,
		Version:  versionInfo,
		Content:  string(s.contents[skillKey(req.GetNamespace(), req.GetName(), version)]),
	}, nil
}

// GetSkillList 获取 published Skill 列表。
func (s *SkillServer) GetSkillList(
	_ context.Context, req *skillmanage.ListSkillsRequest,
) (*skillmanage.ListSkillsResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := &skillmanage.ListSkillsResponse{
		Code: uint32(apimodel.Code_ExecuteSuccess),
		Info: "success",
	}
	seen := make(map[string]struct{})
	for _, resource := range s.resources {
		if resource.GetNamespace() != req.GetNamespace() {
			continue
		}
		if _, ok := seen[resource.GetNamespace()+"/"+resource.GetName()]; ok {
			continue
		}
		seen[resource.GetNamespace()+"/"+resource.GetName()] = struct{}{}
		result.Resources = append(result.Resources, resource)
	}
	result.Total = uint32(len(result.Resources))
	return result, nil
}

// DownloadSkill 下载 Skill。ZIP 按 1 MiB 分片。
func (s *SkillServer) DownloadSkill(
	req *skillmanage.DownloadSkillRequest, stream skillmanage.PolarisSkill_DownloadSkillServer,
) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	version := req.GetVersion()
	if version == "" {
		if resource := s.resources[skillKey(req.GetNamespace(), req.GetName(), "")]; resource != nil {
			version = resource.GetVersionInfo().GetActiveVersion()
		}
	}
	versionInfo := s.versions[skillKey(req.GetNamespace(), req.GetName(), version)]
	content := s.contents[skillKey(req.GetNamespace(), req.GetName(), version)]
	if versionInfo == nil {
		return stream.Send(&skillmanage.DownloadSkillResponse{
			Code: uint32(apimodel.Code_NotFoundResource),
			Info: "skill not found",
		})
	}
	if req.GetFormat() == "markdown" {
		return stream.Send(&skillmanage.DownloadSkillResponse{
			Code:        uint32(apimodel.Code_ExecuteSuccess),
			Info:        "success",
			Version:     version,
			ContentType: "text/markdown",
			Content:     string(content),
		})
	}
	firstEnd := len(content)
	if firstEnd > mockSkillChunkSize {
		firstEnd = mockSkillChunkSize
	}
	if err := stream.Send(&skillmanage.DownloadSkillResponse{
		Code:        uint32(apimodel.Code_ExecuteSuccess),
		Info:        "success",
		Version:     version,
		ContentType: "application/zip",
		TotalSize:   int64(len(content)),
		Filename:    req.GetName() + ".zip",
		ZipChunk:    append([]byte(nil), content[:firstEnd]...),
	}); err != nil {
		return err
	}
	for offset := firstEnd; offset < len(content); {
		end := offset + mockSkillChunkSize
		if end > len(content) {
			end = len(content)
		}
		if err := stream.Send(&skillmanage.DownloadSkillResponse{
			Code:     uint32(apimodel.Code_ExecuteSuccess),
			Info:     "success",
			Version:  version,
			ZipChunk: append([]byte(nil), content[offset:end]...),
		}); err != nil {
			return err
		}
		offset = end
	}
	return nil
}

func skillKey(namespace, name, version string) string {
	return namespace + "\x00" + name + "\x00" + version
}
