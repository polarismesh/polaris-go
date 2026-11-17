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

// Package skillconnector defines the Skill Registry connector extension point.
package skillconnector

import (
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

// GetSkillRequest 是内部 Skill 查询请求。
type GetSkillRequest struct {
	Namespace string
	Name      string
	Version   string
}

// ListSkillsRequest 是内部 Skill 列表请求。
type ListSkillsRequest struct {
	Namespace string
	Name      string
	Tag       string
	Owner     string
	Scope     string
	Keyword   string
	OrderBy   string
	OrderType string
	Offset    uint32
	Limit     uint32
}

// VersionInfo 是 Skill 版本摘要。
type VersionInfo struct {
	ActiveVersion string
	LatestVersion string
	TotalVersions int32
}

// Resource 是 Skill Registry 资源。
type Resource struct {
	ID            int64
	Name          string
	Namespace     string
	Description   string
	Status        string
	Tags          []string
	Examples      []string
	Ext           map[string]string
	Source        string
	VersionInfo   *VersionInfo
	MetaVersion   int64
	Scope         string
	Owner         string
	DownloadCount int64
	CreateTime    string
	ModifyTime    string
}

// ResourceVersion 是 Skill 指定版本信息。
type ResourceVersion struct {
	ID            int64
	Name          string
	Namespace     string
	Version       string
	Author        string
	Description   string
	Status        string
	Storage       *StorageInfo
	DownloadCount int64
	CreateTime    string
	ModifyTime    string
}

// StorageInfo 是 Skill 版本的存储引用。
type StorageInfo struct {
	Provider      string
	Files         []string
	Scope         string
	ContentDigest string
}

// GetSkillResponse 包含 Skill 资源、版本和文本内容。
type GetSkillResponse struct {
	Resource *Resource
	Version  *ResourceVersion
	Content  string
}

// ListSkillsResponse 是 Skill 列表响应。
type ListSkillsResponse struct {
	Total     uint32
	Resources []*Resource
}

// DownloadRequest 是内部 Skill 下载请求。
type DownloadRequest struct {
	Namespace string
	Name      string
	Version   string
	Tag       string
	Format    string
}

// DownloadChunk 是 Skill 下载流的一帧。
type DownloadChunk struct {
	Version     string
	ContentType string
	Content     string
	ZipChunk    []byte
	TotalSize   int64
	Filename    string
}

// DownloadHandler 消费 Skill 下载流。
type DownloadHandler func(chunk *DownloadChunk) error

// SkillConnector 是 Skill Registry 连接器插件接口。
type SkillConnector interface {
	plugin.Plugin
	GetSkill(req *GetSkillRequest) (*GetSkillResponse, error)
	GetSkillList(req *ListSkillsRequest) (*ListSkillsResponse, error)
	DownloadSkill(req *DownloadRequest, handler DownloadHandler) error
}

func init() {
	plugin.RegisterPluginInterface(common.TypeSkillConnector, new(SkillConnector))
}
