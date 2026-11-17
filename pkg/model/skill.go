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

package model

import "time"

const (
	// SkillFormatMarkdown 表示 Markdown 单文件下载。
	SkillFormatMarkdown = "markdown"
	// SkillFormatZIP 表示 ZIP 包下载。
	SkillFormatZIP = "zip"
	// SkillContentTypeMarkdown 是 Markdown Skill 的 Content-Type。
	SkillContentTypeMarkdown = "text/markdown"
	// SkillContentTypeZIP 是 ZIP Skill 的 Content-Type。
	SkillContentTypeZIP = "application/zip"
	// SkillFileName 是 Skill 的入口文件名。
	SkillFileName = "SKILL.md"
	// MaxSkillPackageSize 是单个 Skill 包的最大字节数。
	MaxSkillPackageSize int64 = 256 * 1024 * 1024
	// MaxSkillFileSize 是单个 Skill 文件的最大字节数。
	MaxSkillFileSize int64 = 64 * 1024 * 1024
)

// GetSkillRequest 是查询单个 Skill 的请求。
type GetSkillRequest struct {
	Namespace string
	Name      string
	Version   string
}

// Validate 校验查询请求。
func (r *GetSkillRequest) Validate() error {
	if r == nil {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "GetSkillRequest can not be nil")
	}
	if r.Namespace == "" || r.Name == "" {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"GetSkillRequest namespace and name must not be empty")
	}
	if r.Version == "" {
		if err := validateSkillSegment("namespace", r.Namespace); err != nil {
			return err
		}
		if err := validateSkillSegment("name", r.Name); err != nil {
			return err
		}
	} else if err := ValidateSkillRefParts(r.Namespace, r.Name, r.Version); err != nil {
		return err
	}
	return nil
}

// GetSkillListRequest 是查询 Skill 列表的请求。
type GetSkillListRequest struct {
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

// Validate 校验列表请求。
func (r *GetSkillListRequest) Validate() error {
	if r == nil {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "GetSkillListRequest can not be nil")
	}
	if r.Namespace == "" {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"GetSkillListRequest namespace must not be empty")
	}
	if err := validateSkillSegment("namespace", r.Namespace); err != nil {
		return err
	}
	return nil
}

// SkillVersionInfo 是 Skill 当前版本摘要。
type SkillVersionInfo struct {
	ActiveVersion string
	LatestVersion string
	TotalVersions int32
}

// SkillStorageInfo 描述 Skill 版本的存储位置。
type SkillStorageInfo struct {
	Provider      string
	Files         []string
	Scope         string
	ContentDigest string
}

// SkillResourceVersion 是 Skill 指定版本的完整公开信息。
type SkillResourceVersion struct {
	ID            int64
	Name          string
	Namespace     string
	Version       string
	Author        string
	Description   string
	Status        string
	Storage       *SkillStorageInfo
	DownloadCount int64
	CreateTime    string
	ModifyTime    string
}

// SkillResource 是 Skill Registry 中 published Skill 的公开视图。
type SkillResource struct {
	ID            int64
	Name          string
	Namespace     string
	Description   string
	Status        string
	Tags          []string
	Examples      []string
	Ext           map[string]string
	Source        string
	VersionInfo   *SkillVersionInfo
	Version       string
	Content       string
	MetaVersion   int64
	Scope         string
	Owner         string
	DownloadCount int64
	CreateTime    string
	ModifyTime    string
	// VersionDetail 是本次 GetSkill 返回的指定版本信息。
	VersionDetail *SkillResourceVersion
	// FromCache 表示当前响应来自网络错误回退的本地持久化数据。
	FromCache bool
}

// SkillListResult 是 Skill 列表的完整分页结果。
type SkillListResult struct {
	// Total 是服务端匹配过滤条件的总数，而不是当前页数量。
	Total uint32
	// Resources 是经过 published/active 过滤后的当前页资源。
	Resources []*SkillResource
}

// SkillPackage 是下载完成并校验后的 Skill 内容。
type SkillPackage struct {
	Namespace   string
	Name        string
	Version     string
	Format      string
	ContentType string
	Filename    string
	Content     []byte
	Digest      string
	// FromCache 表示当前包来自网络错误回退的本地持久化数据。
	FromCache bool
}

// SkillDownloadRequest 是下载 Skill 的请求。
type SkillDownloadRequest struct {
	Namespace string
	Name      string
	Version   string
	Tag       string
	Format    string
}

// Validate 校验下载请求。
func (r *SkillDownloadRequest) Validate() error {
	if r == nil {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "SkillDownloadRequest can not be nil")
	}
	if r.Namespace == "" || r.Name == "" {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"SkillDownloadRequest namespace and name must not be empty")
	}
	if r.Version == "" {
		if err := validateSkillSegment("namespace", r.Namespace); err != nil {
			return err
		}
		if err := validateSkillSegment("name", r.Name); err != nil {
			return err
		}
	} else if err := ValidateSkillRefParts(r.Namespace, r.Name, r.Version); err != nil {
		return err
	}
	if r.Format != "" && r.Format != SkillFormatMarkdown && r.Format != SkillFormatZIP {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"SkillDownloadRequest format %q must be markdown or zip", r.Format)
	}
	return nil
}

// SkillInstallRequest 是安装 published Skill 的请求。
type SkillInstallRequest struct {
	Namespace string
	Name      string
	Version   string
	CacheDir  string
	Force     bool
}

// Validate 校验安装请求。
func (r *SkillInstallRequest) Validate() error {
	if r == nil {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil, "SkillInstallRequest can not be nil")
	}
	if r.Namespace == "" || r.Name == "" || r.CacheDir == "" {
		return NewSDKError(ErrCodeAPIInvalidArgument, nil,
			"SkillInstallRequest namespace, name and cache dir must not be empty")
	}
	if r.Version == "" {
		if err := validateSkillSegment("namespace", r.Namespace); err != nil {
			return err
		}
		if err := validateSkillSegment("name", r.Name); err != nil {
			return err
		}
	} else if err := ValidateSkillRefParts(r.Namespace, r.Name, r.Version); err != nil {
		return err
	}
	return nil
}

// InstalledSkill 描述已安装 Skill 的缓存状态。
type InstalledSkill struct {
	Namespace  string
	Name       string
	Version    string
	Dir        string
	Digest     string
	TreeDigest string
	Downloaded time.Time
	CacheHit   bool
	// EntryFile is the slash-separated package entry path relative to Dir.
	EntryFile   string
	PackageSize int64
}
