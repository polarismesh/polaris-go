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

// Package skill implements Skill Registry query, download, and installation flows.
package skill

import (
	"crypto/sha256"
	"encoding/hex"
	"mime"
	"strings"
	"time"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/skillconnector"
)

const maxDownloadChunkSize = 1024 * 1024

// Options 控制 Skill Flow 的持久化行为。
type Options struct {
	PersistEnable        bool
	PersistDir           string
	PersistMaxWriteRetry int
	PersistMaxReadRetry  int
	PersistRetryInterval time.Duration
	FallbackToLocalCache bool
	LogCtx               *log.ContextLogger
}

// Flow 实现 Skill 查询、下载和安装编排。
type Flow struct {
	connector skillconnector.SkillConnector
	installer *Installer
	persist   *persistentHandler
	fallback  bool
}

// NewFlow 创建 Skill Flow。
func NewFlow(connector skillconnector.SkillConnector) *Flow {
	flow, _ := NewFlowWithOptions(connector, Options{})
	return flow
}

// NewFlowWithOptions 创建带持久化和网络错误回退的 Skill Flow。
func NewFlowWithOptions(connector skillconnector.SkillConnector, options Options) (*Flow, error) {
	if options.PersistRetryInterval <= 0 {
		options.PersistRetryInterval = 500 * time.Millisecond
	}
	handler, err := newPersistentHandler(options)
	if err != nil {
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidConfig, err,
			"fail to initialize Skill persistent handler")
	}
	flow := &Flow{
		connector: connector,
		persist:   handler,
		fallback:  options.FallbackToLocalCache,
	}
	flow.installer = &Installer{flow: flow}
	return flow, nil
}

// GetSkill 获取单个 published Skill。
func (f *Flow) GetSkill(req *model.GetSkillRequest) (*model.SkillResource, error) {
	if f == nil || f.connector == nil {
		return nil, model.NewSDKError(model.ErrCodeSkillNotConfigured, nil,
			"skill connector is not configured")
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	resp, err := f.connector.GetSkill(&skillconnector.GetSkillRequest{
		Namespace: req.Namespace,
		Name:      req.Name,
		Version:   req.Version,
	})
	if err != nil {
		return f.fallbackGet(req, err)
	}
	if resp == nil {
		return nil, model.NewSDKError(model.ErrCodeInvalidResponse, nil,
			"skill connector returned an empty response for %s/%s", req.Namespace, req.Name)
	}
	if resp.Version == nil || resp.Version.Version == "" {
		return nil, model.NewSDKError(model.ErrCodeInvalidResponse, nil,
			"skill connector response for %s/%s has no version", req.Namespace, req.Name)
	}
	if resp.Version.Namespace != req.Namespace || resp.Version.Name != req.Name {
		return nil, model.NewSDKError(model.ErrCodeInvalidResponse, nil,
			"skill connector returned version %s/%s@%s for requested skill %s/%s",
			resp.Version.Namespace, resp.Version.Name, resp.Version.Version,
			req.Namespace, req.Name)
	}
	if req.Version != "" && resp.Version.Version != req.Version {
		return nil, model.NewSDKError(model.ErrCodeInvalidResponse, nil,
			"skill connector returned version %q for requested version %q",
			resp.Version.Version, req.Version)
	}
	if resp.Resource != nil &&
		(resp.Resource.Namespace != req.Namespace || resp.Resource.Name != req.Name) {
		return nil, model.NewSDKError(model.ErrCodeInvalidResponse, nil,
			"skill connector returned resource %s/%s for requested skill %s/%s",
			resp.Resource.Namespace, resp.Resource.Name, req.Namespace, req.Name)
	}
	if !isPublished(resp.Version.Status) {
		return nil, model.NewSDKError(model.ErrCodeSkillNotFound, nil,
			"skill %s/%s version %s is not published", req.Namespace, req.Name, req.Version)
	}
	if int64(len(resp.Content)) > model.MaxSkillFileSize {
		return nil, model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
			"skill content size %d exceeds maximum %d", len(resp.Content), model.MaxSkillFileSize)
	}
	resource := convertResource(resp.Resource)
	if resource == nil {
		resource = &model.SkillResource{Namespace: req.Namespace, Name: req.Name}
	}
	if resource.VersionInfo == nil {
		resource.VersionInfo = &model.SkillVersionInfo{}
	}
	resource.Version = resp.Version.Version
	resource.Content = resp.Content
	resource.VersionDetail = convertResourceVersion(resp.Version)
	resource.FromCache = false
	if resource.VersionInfo.ActiveVersion == "" {
		resource.VersionInfo.ActiveVersion = resp.Version.Version
	}
	f.persistGet(req, resource, resp.Version.Version)
	return resource, nil
}

// GetSkillList 获取 published Skill 列表。
func (f *Flow) GetSkillList(req *model.GetSkillListRequest) ([]*model.SkillResource, error) {
	result, err := f.GetSkillListResult(req)
	if err != nil {
		return nil, err
	}
	return result.Resources, nil
}

// GetSkillListResult 获取包含服务端分页总数的 published Skill 列表。
func (f *Flow) GetSkillListResult(req *model.GetSkillListRequest) (*model.SkillListResult, error) {
	if f == nil || f.connector == nil {
		return nil, model.NewSDKError(model.ErrCodeSkillNotConfigured, nil,
			"skill connector is not configured")
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	resp, err := f.connector.GetSkillList(&skillconnector.ListSkillsRequest{
		Namespace: req.Namespace,
		Name:      req.Name,
		Tag:       req.Tag,
		Owner:     req.Owner,
		Scope:     req.Scope,
		Keyword:   req.Keyword,
		OrderBy:   req.OrderBy,
		OrderType: req.OrderType,
		Offset:    req.Offset,
		Limit:     req.Limit,
	})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, model.NewSDKError(model.ErrCodeInvalidResponse, nil,
			"skill connector returned an empty list response for namespace %s", req.Namespace)
	}
	result := make([]*model.SkillResource, 0, len(resp.Resources))
	for _, resource := range resp.Resources {
		if resource == nil {
			return nil, model.NewSDKError(model.ErrCodeInvalidResponse, nil,
				"skill connector returned an empty skill resource for namespace %s", req.Namespace)
		}
		if resource.Namespace != req.Namespace {
			return nil, model.NewSDKError(model.ErrCodeInvalidResponse, nil,
				"skill connector returned resource %s/%s for requested namespace %s",
				resource.Namespace, resource.Name, req.Namespace)
		}
		if resource.Name == "" || (req.Name != "" && resource.Name != req.Name) {
			return nil, model.NewSDKError(model.ErrCodeInvalidResponse, nil,
				"skill connector returned resource name %q for requested name %q",
				resource.Name, req.Name)
		}
		if resource.VersionInfo == nil || strings.TrimSpace(resource.VersionInfo.ActiveVersion) == "" ||
			(resource.Status != "" && !isPublished(resource.Status)) {
			continue
		}
		converted := convertResource(resource)
		if converted != nil {
			result = append(result, converted)
		}
	}
	return &model.SkillListResult{Total: resp.Total, Resources: result}, nil
}

// DownloadSkill 下载并校验 Skill 包。
func (f *Flow) DownloadSkill(req *model.SkillDownloadRequest) (*model.SkillPackage, error) {
	if f == nil || f.connector == nil {
		return nil, model.NewSDKError(model.ErrCodeSkillNotConfigured, nil,
			"skill connector is not configured")
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	downloadReq := *req
	if downloadReq.Format == "" {
		downloadReq.Format = model.SkillFormatMarkdown
	}
	var (
		first       = true
		frameCount  int
		total       int64
		content     []byte
		version     string
		contentType string
		filename    string
	)
	err := f.connector.DownloadSkill(&skillconnector.DownloadRequest{
		Namespace: req.Namespace,
		Name:      req.Name,
		Version:   req.Version,
		Tag:       req.Tag,
		Format:    downloadReq.Format,
	}, func(chunk *skillconnector.DownloadChunk) error {
		if chunk == nil {
			return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
				"skill download returned an empty frame")
		}
		frameCount++
		if req.Version != "" && chunk.Version != "" && chunk.Version != req.Version {
			return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
				"skill download version %q does not match requested version %q", chunk.Version, req.Version)
		}
		if !first && chunk.Version != "" && chunk.Version != version {
			return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
				"skill download follow-up version %q does not match first frame version %q",
				chunk.Version, version)
		}
		if first {
			first = false
			version = chunk.Version
			contentType = mediaType(chunk.ContentType)
			filename = chunk.Filename
			if version == "" {
				return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
					"skill download first frame has no version")
			}
			switch downloadReq.Format {
			case model.SkillFormatMarkdown:
				if contentType != model.SkillContentTypeMarkdown {
					return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
						"skill markdown content type %q is invalid", chunk.ContentType)
				}
				if chunk.Content == "" {
					return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
						"skill markdown content is empty")
				}
				if len(chunk.ZipChunk) > 0 {
					return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
						"skill markdown response must not contain zip chunks")
				}
				content = []byte(chunk.Content)
				total = int64(len(content))
				if total > model.MaxSkillFileSize {
					return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
						"skill markdown size %d exceeds maximum %d", total, model.MaxSkillFileSize)
				}
			case model.SkillFormatZIP:
				if contentType != model.SkillContentTypeZIP {
					return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
						"skill zip content type %q is invalid", chunk.ContentType)
				}
				if chunk.TotalSize <= 0 || chunk.TotalSize > model.MaxSkillPackageSize {
					return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
						"skill zip total size %d is invalid", chunk.TotalSize)
				}
				if chunk.Content != "" || chunk.Filename == "" {
					return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
						"skill zip first frame must contain filename and must not contain text content")
				}
				total = chunk.TotalSize
				if len(chunk.ZipChunk) == 0 {
					return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
						"skill zip first frame chunk is empty")
				}
				if len(chunk.ZipChunk) > maxDownloadChunkSize {
					return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
						"skill zip chunk size %d exceeds maximum %d", len(chunk.ZipChunk), maxDownloadChunkSize)
				}
				if int64(len(chunk.ZipChunk)) > total {
					return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
						"skill zip first chunk exceeds declared total size %d", total)
				}
				content = make([]byte, 0, int(total))
				content = append(content, chunk.ZipChunk...)
			}
			return nil
		}

		if downloadReq.Format == model.SkillFormatMarkdown {
			return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
				"skill markdown download must contain exactly one frame")
		}
		if chunk.ContentType != "" || chunk.Content != "" ||
			chunk.Filename != "" || chunk.TotalSize != 0 {
			return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
				"skill zip follow-up frames must only contain zip_chunk and version")
		}
		if len(chunk.ZipChunk) == 0 {
			return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
				"skill zip follow-up frame chunk is empty")
		}
		if len(chunk.ZipChunk) > maxDownloadChunkSize {
			return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
				"skill zip chunk size %d exceeds maximum %d", len(chunk.ZipChunk), maxDownloadChunkSize)
		}
		if int64(len(content))+int64(len(chunk.ZipChunk)) > total {
			return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
				"skill zip chunk exceeds declared total size %d", total)
		}
		content = append(content, chunk.ZipChunk...)
		return nil
	})
	if err != nil {
		return f.fallbackDownload(&downloadReq, err)
	}
	if frameCount == 0 {
		return nil, model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
			"skill download returned no frames")
	}
	if downloadReq.Format == model.SkillFormatZIP && int64(len(content)) != total {
		return nil, model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
			"skill zip downloaded %d bytes, expected %d", len(content), total)
	}
	digest := sha256.Sum256(content)
	pkg := &model.SkillPackage{
		Namespace:   req.Namespace,
		Name:        req.Name,
		Version:     version,
		Format:      downloadReq.Format,
		ContentType: contentType,
		Filename:    filename,
		Content:     content,
		Digest:      hex.EncodeToString(digest[:]),
		FromCache:   false,
	}
	f.persistDownload(req, pkg)
	return pkg, nil
}

// Install 下载并安全安装 Skill。
func (f *Flow) Install(req *model.SkillInstallRequest) (*model.InstalledSkill, error) {
	if f == nil || f.installer == nil {
		return nil, model.NewSDKError(model.ErrCodeSkillNotConfigured, nil,
			"skill installer is not configured")
	}
	return f.installer.Install(req)
}

// Destroy 刷盘并停止异步 Skill 持久化。
func (f *Flow) Destroy() {
	if f != nil && f.persist != nil {
		f.persist.destroy()
	}
}

func (f *Flow) fallbackGet(
	req *model.GetSkillRequest, remoteErr error,
) (*model.SkillResource, error) {
	if f == nil || f.persist == nil || !f.fallback || !isSkillNetworkError(remoteErr) {
		return nil, remoteErr
	}
	version := f.persist.resolveVersion(req.Namespace, req.Name, req.Version)
	if version == "" {
		return nil, remoteErr
	}
	resource, err := f.persist.loadGet(req.Namespace, req.Name, version)
	if err != nil {
		return nil, remoteErr
	}
	if resource == nil {
		return nil, remoteErr
	}
	if resource.Status != "" && !isPublished(resource.Status) {
		return nil, remoteErr
	}
	return resource, nil
}

func (f *Flow) fallbackDownload(
	req *model.SkillDownloadRequest, remoteErr error,
) (*model.SkillPackage, error) {
	if f == nil || f.persist == nil || !f.fallback || !isSkillNetworkError(remoteErr) {
		return nil, remoteErr
	}
	version := f.persist.resolveVersion(req.Namespace, req.Name, req.Version)
	if version == "" {
		return nil, remoteErr
	}
	pkg, err := f.persist.loadDownload(req.Namespace, req.Name, version, req.Format, req.Tag)
	if err != nil || pkg == nil {
		return nil, remoteErr
	}
	if req.Format == model.SkillFormatMarkdown &&
		(mediaType(pkg.ContentType) != model.SkillContentTypeMarkdown || len(pkg.Content) == 0) {
		return nil, remoteErr
	}
	if req.Format == model.SkillFormatZIP &&
		(mediaType(pkg.ContentType) != model.SkillContentTypeZIP || len(pkg.Content) == 0) {
		return nil, remoteErr
	}
	return pkg, nil
}

func (f *Flow) persistGet(req *model.GetSkillRequest, resource *model.SkillResource, version string) {
	if f == nil || f.persist == nil || !f.persist.enabled || resource == nil {
		return
	}
	f.persist.saveGet(req.Namespace, req.Name, version, resource, resource.Content, req.Version == "")
}

func (f *Flow) persistDownload(req *model.SkillDownloadRequest, pkg *model.SkillPackage) {
	if f == nil || f.persist == nil || !f.persist.enabled || pkg == nil {
		return
	}
	f.persist.saveDownload(req.Namespace, req.Name, req.Tag, pkg,
		req.Version == "" && strings.TrimSpace(req.Tag) == "")
}

func isSkillNetworkError(err error) bool {
	switch model.GetErrorCodeFromError(err) {
	case model.ErrCodeNetworkError, model.ErrCodeConnectError, model.ErrorCodeRpcError,
		model.ErrorCodeRpcTimeout, model.ErrCodeAPITimeoutError:
		return true
	default:
		return false
	}
}

func mediaType(value string) string {
	if parsed, _, err := mime.ParseMediaType(value); err == nil {
		return strings.ToLower(parsed)
	}
	return strings.ToLower(strings.TrimSpace(value))
}

func isPublished(status string) bool {
	return strings.EqualFold(strings.TrimSpace(status), "published")
}

func hasActiveVersion(versionInfo *model.SkillVersionInfo) bool {
	return versionInfo != nil && strings.TrimSpace(versionInfo.ActiveVersion) != ""
}

func convertResource(resource *skillconnector.Resource) *model.SkillResource {
	if resource == nil {
		return nil
	}
	result := &model.SkillResource{
		ID:            resource.ID,
		Name:          resource.Name,
		Namespace:     resource.Namespace,
		Description:   resource.Description,
		Status:        resource.Status,
		Tags:          append([]string(nil), resource.Tags...),
		Examples:      append([]string(nil), resource.Examples...),
		Ext:           cloneStringMap(resource.Ext),
		Source:        resource.Source,
		MetaVersion:   resource.MetaVersion,
		Scope:         resource.Scope,
		Owner:         resource.Owner,
		DownloadCount: resource.DownloadCount,
		CreateTime:    resource.CreateTime,
		ModifyTime:    resource.ModifyTime,
	}
	if resource.VersionInfo != nil {
		result.VersionInfo = &model.SkillVersionInfo{
			ActiveVersion: resource.VersionInfo.ActiveVersion,
			LatestVersion: resource.VersionInfo.LatestVersion,
			TotalVersions: resource.VersionInfo.TotalVersions,
		}
	}
	return result
}

func convertResourceVersion(version *skillconnector.ResourceVersion) *model.SkillResourceVersion {
	if version == nil {
		return nil
	}
	result := &model.SkillResourceVersion{
		ID:            version.ID,
		Name:          version.Name,
		Namespace:     version.Namespace,
		Version:       version.Version,
		Author:        version.Author,
		Description:   version.Description,
		Status:        version.Status,
		DownloadCount: version.DownloadCount,
		CreateTime:    version.CreateTime,
		ModifyTime:    version.ModifyTime,
	}
	if version.Storage != nil {
		result.Storage = &model.SkillStorageInfo{
			Provider:      version.Storage.Provider,
			Files:         append([]string(nil), version.Storage.Files...),
			Scope:         version.Storage.Scope,
			ContentDigest: version.Storage.ContentDigest,
		}
	}
	return result
}

func cloneStringMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	result := make(map[string]string, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}
