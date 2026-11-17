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

package skill

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v2"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
)

const (
	persistDirGet      = "get"
	persistDirDownload = "download"
	persistDirActive   = "active"
	persistExtYAML     = ".yaml"
	persistExtZIP      = ".zip"
	maxPersistTasks    = 1024
	maxPersistBytes    = model.MaxSkillPackageSize
	// YAML metadata and escaping may make the envelope larger than the original
	// Skill content. Bound the envelope at the package limit, then bound the
	// decoded content separately.
	maxPersistEnvelopeBytes = model.MaxSkillPackageSize
)

type persistTask struct {
	name string
	size int64
	run  func() bool
}

type persistentHandler struct {
	enabled       bool
	root          string
	maxWriteRetry int
	maxReadRetry  int
	retryInterval time.Duration
	logCtx        *log.ContextLogger

	mu     sync.Mutex
	cond   *sync.Cond
	tasks  []persistTask
	closed bool
	wg     sync.WaitGroup

	pendingBytes int64
	reservations int
	active       int
}

type persistedStorageInfo struct {
	Provider      string   `yaml:"provider,omitempty" json:"provider,omitempty"`
	Files         []string `yaml:"files,omitempty" json:"files,omitempty"`
	Scope         string   `yaml:"scope,omitempty" json:"scope,omitempty"`
	ContentDigest string   `yaml:"contentDigest,omitempty" json:"contentDigest,omitempty"`
}

type persistedVersionInfo struct {
	ActiveVersion string `yaml:"activeVersion,omitempty" json:"activeVersion,omitempty"`
	LatestVersion string `yaml:"latestVersion,omitempty" json:"latestVersion,omitempty"`
	TotalVersions int32  `yaml:"totalVersions,omitempty" json:"totalVersions,omitempty"`
}

type persistedResource struct {
	ID            int64                 `yaml:"id,omitempty" json:"id,omitempty"`
	Name          string                `yaml:"name,omitempty" json:"name,omitempty"`
	Namespace     string                `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	Description   string                `yaml:"description,omitempty" json:"description,omitempty"`
	Status        string                `yaml:"status,omitempty" json:"status,omitempty"`
	Tags          []string              `yaml:"tags,omitempty" json:"tags,omitempty"`
	Examples      []string              `yaml:"examples,omitempty" json:"examples,omitempty"`
	Ext           map[string]string     `yaml:"ext,omitempty" json:"ext,omitempty"`
	Source        string                `yaml:"source,omitempty" json:"source,omitempty"`
	VersionInfo   *persistedVersionInfo `yaml:"versionInfo,omitempty" json:"versionInfo,omitempty"`
	MetaVersion   int64                 `yaml:"metaVersion,omitempty" json:"metaVersion,omitempty"`
	Scope         string                `yaml:"scope,omitempty" json:"scope,omitempty"`
	Owner         string                `yaml:"owner,omitempty" json:"owner,omitempty"`
	DownloadCount int64                 `yaml:"downloadCount,omitempty" json:"downloadCount,omitempty"`
	CreateTime    string                `yaml:"createTime,omitempty" json:"createTime,omitempty"`
	ModifyTime    string                `yaml:"modifyTime,omitempty" json:"modifyTime,omitempty"`
}

type persistedResourceVersion struct {
	ID            int64                 `yaml:"id,omitempty" json:"id,omitempty"`
	Name          string                `yaml:"name,omitempty" json:"name,omitempty"`
	Namespace     string                `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	Version       string                `yaml:"version,omitempty" json:"version,omitempty"`
	Author        string                `yaml:"author,omitempty" json:"author,omitempty"`
	Description   string                `yaml:"description,omitempty" json:"description,omitempty"`
	Status        string                `yaml:"status,omitempty" json:"status,omitempty"`
	Storage       *persistedStorageInfo `yaml:"storage,omitempty" json:"storage,omitempty"`
	DownloadCount int64                 `yaml:"downloadCount,omitempty" json:"downloadCount,omitempty"`
	CreateTime    string                `yaml:"createTime,omitempty" json:"createTime,omitempty"`
	ModifyTime    string                `yaml:"modifyTime,omitempty" json:"modifyTime,omitempty"`
}

type persistedGetResponse struct {
	Resource        *persistedResource        `yaml:"resource,omitempty" json:"resource,omitempty"`
	ResourceVersion *persistedResourceVersion `yaml:"resourceVersion,omitempty" json:"resourceVersion,omitempty"`
	Content         string                    `yaml:"content,omitempty" json:"content,omitempty"`
}

type persistedDownloadResponse struct {
	Version     string `yaml:"version,omitempty" json:"version,omitempty"`
	Tag         string `yaml:"tag,omitempty" json:"tag,omitempty"`
	ContentType string `yaml:"contentType,omitempty" json:"contentType,omitempty"`
	Content     string `yaml:"content,omitempty" json:"content,omitempty"`
	TotalSize   int64  `yaml:"totalSize,omitempty" json:"totalSize,omitempty"`
	Filename    string `yaml:"filename,omitempty" json:"filename,omitempty"`
	Digest      string `yaml:"digest,omitempty" json:"digest,omitempty"`
}

func newPersistentHandler(options Options) (*persistentHandler, error) {
	handler := &persistentHandler{
		enabled:       options.PersistEnable,
		root:          options.PersistDir,
		maxWriteRetry: options.PersistMaxWriteRetry,
		maxReadRetry:  options.PersistMaxReadRetry,
		retryInterval: options.PersistRetryInterval,
		logCtx:        options.LogCtx,
	}
	if !handler.enabled {
		return handler, nil
	}
	if strings.TrimSpace(handler.root) == "" {
		return nil, fmt.Errorf("skill persist directory must not be empty")
	}
	if handler.maxWriteRetry < 0 || handler.maxReadRetry < 0 {
		return nil, fmt.Errorf("skill persist retry counts must not be negative")
	}
	if handler.retryInterval <= 0 {
		return nil, fmt.Errorf("skill persist retry interval must be positive")
	}
	for _, directory := range []string{persistDirGet, persistDirDownload, persistDirActive} {
		if err := ensurePersistDir(filepath.Join(handler.root, directory)); err != nil {
			return nil, err
		}
	}
	handler.cond = sync.NewCond(&handler.mu)
	handler.wg.Add(1)
	go handler.run()
	return handler, nil
}

func ensurePersistDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("skill persist path %s must be a real directory", path)
	}
	return nil
}

func (h *persistentHandler) run() {
	defer h.wg.Done()
	for {
		h.mu.Lock()
		for len(h.tasks) == 0 {
			if h.closed && h.reservations == 0 {
				h.mu.Unlock()
				return
			}
			h.cond.Wait()
		}
		task := h.tasks[0]
		h.tasks[0] = persistTask{}
		h.tasks = h.tasks[1:]
		h.active++
		h.mu.Unlock()
		ok := task.run()
		h.mu.Lock()
		h.active--
		h.pendingBytes -= task.size
		h.mu.Unlock()
		if !ok {
			h.warnf("fail to persist Skill %s", task.name)
		}
	}
}

func (h *persistentHandler) enqueue(task persistTask) bool {
	if h == nil || !h.enabled {
		return false
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		h.warnf("ignore Skill persistence task %s after close", task.name)
		return false
	}
	if !h.canQueueLocked(task.size) {
		h.mu.Unlock()
		h.warnf("drop Skill persistence task %s: queue capacity exceeded", task.name)
		return false
	}
	h.tasks = append(h.tasks, task)
	h.pendingBytes += task.size
	h.cond.Signal()
	h.mu.Unlock()
	return true
}

func (h *persistentHandler) enqueueFactory(
	name string, size int64, build func() persistTask,
) bool {
	if h == nil || !h.enabled || build == nil {
		return false
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		h.warnf("ignore Skill persistence task %s after close", name)
		return false
	}
	if !h.canQueueLocked(size) {
		h.mu.Unlock()
		h.warnf("drop Skill persistence task %s: queue capacity exceeded", name)
		return false
	}
	h.pendingBytes += size
	h.reservations++
	h.mu.Unlock()

	defer func() {
		if recovered := recover(); recovered != nil {
			h.mu.Lock()
			h.reservations--
			h.pendingBytes -= size
			h.cond.Broadcast()
			h.mu.Unlock()
			panic(recovered)
		}
	}()

	task := build()
	task.name = name
	task.size = size

	h.mu.Lock()
	h.reservations--
	h.tasks = append(h.tasks, task)
	h.cond.Signal()
	h.mu.Unlock()
	return true
}

func (h *persistentHandler) canQueueLocked(size int64) bool {
	if size < 0 || size > maxPersistBytes-h.pendingBytes {
		return false
	}
	return len(h.tasks)+h.reservations+h.active < maxPersistTasks
}

func (h *persistentHandler) destroy() {
	if h == nil || !h.enabled {
		return
	}
	h.mu.Lock()
	if !h.closed {
		h.closed = true
		h.cond.Broadcast()
	}
	h.mu.Unlock()
	h.wg.Wait()
}

func (h *persistentHandler) saveGet(
	namespace, name, version string, resource *model.SkillResource, content string, saveActive bool,
) bool {
	if h == nil || !h.enabled {
		return false
	}
	nameKey := "get " + skillCacheKey(namespace, name, version)
	return h.enqueueFactory(nameKey, int64(len(content)), func() persistTask {
		payload := toPersistedGet(namespace, name, version, resource, content)
		return persistTask{run: func() bool {
			if !h.writeYAML(h.getPath(namespace, name, version), payload) {
				return false
			}
			return !saveActive || h.writeBytes(h.activePath(namespace, name), []byte(version))
		}}
	})
}

func (h *persistentHandler) saveDownload(
	namespace, name, tag string, pkg *model.SkillPackage, saveActive bool,
) bool {
	if h == nil || !h.enabled || pkg == nil {
		return false
	}
	format := pkg.Format
	version := pkg.Version
	return h.enqueueFactory(
		"download "+skillCacheKey(namespace, name, version), int64(len(pkg.Content)), func() persistTask {
			payload := &persistedDownloadResponse{
				Version:     version,
				Tag:         tag,
				ContentType: pkg.ContentType,
				TotalSize:   int64(len(pkg.Content)),
				Filename:    pkg.Filename,
				Digest:      pkg.Digest,
			}
			if format == model.SkillFormatMarkdown {
				payload.Content = string(pkg.Content)
			}
			var content []byte
			if format == model.SkillFormatZIP {
				content = append([]byte(nil), pkg.Content...)
			}
			return persistTask{run: func() bool {
				if format == model.SkillFormatZIP {
					if !h.writeBytes(h.zipPath(namespace, name, version), content) {
						return false
					}
				}
				if !h.writeYAML(h.downloadPath(namespace, name, version, format), payload) {
					return false
				}
				return !saveActive || h.writeBytes(h.activePath(namespace, name), []byte(version))
			}}
		})
}

func (h *persistentHandler) loadGet(
	namespace, name, version string,
) (*model.SkillResource, error) {
	if h == nil || !h.enabled {
		return nil, nil
	}
	data, _, ok := h.readFileWithLegacy(
		h.getPath(namespace, name, version),
		h.legacyGetPath(namespace, name, version),
		maxPersistEnvelopeBytes,
	)
	if !ok {
		return nil, nil
	}
	var payload persistedGetResponse
	if err := yaml.Unmarshal(data, &payload); err != nil {
		h.warnf("ignore corrupt Skill Get cache for %s/%s@%s: %v", namespace, name, version, err)
		return nil, nil
	}
	if payload.ResourceVersion == nil ||
		payload.ResourceVersion.Namespace != namespace ||
		payload.ResourceVersion.Name != name ||
		payload.ResourceVersion.Version != version {
		h.warnf("ignore identity-mismatched Skill Get cache for %s/%s@%s", namespace, name, version)
		return nil, nil
	}
	if int64(len(payload.Content)) > model.MaxSkillFileSize {
		h.warnf("ignore oversized Skill Get cache for %s/%s@%s", namespace, name, version)
		return nil, nil
	}
	resource := fromPersistedResource(payload.Resource)
	if resource == nil {
		resource = &model.SkillResource{Namespace: namespace, Name: name}
	}
	resource.Namespace = namespace
	resource.Name = name
	resource.Version = version
	resource.Content = payload.Content
	resource.VersionDetail = fromPersistedVersion(payload.ResourceVersion)
	resource.FromCache = true
	if resource.VersionInfo == nil {
		resource.VersionInfo = &model.SkillVersionInfo{}
	}
	if resource.VersionInfo.ActiveVersion == "" {
		resource.VersionInfo.ActiveVersion = version
	}
	return resource, nil
}

func (h *persistentHandler) loadDownload(
	namespace, name, version, format, tag string,
) (*model.SkillPackage, error) {
	if h == nil || !h.enabled {
		return nil, nil
	}
	data, usedLegacyPath, ok := h.readFileWithLegacy(
		h.downloadPath(namespace, name, version, format),
		h.legacyDownloadPath(namespace, name, version, format),
		maxPersistEnvelopeBytes,
	)
	if !ok {
		return nil, nil
	}
	var payload persistedDownloadResponse
	if err := yaml.Unmarshal(data, &payload); err != nil {
		h.warnf("ignore corrupt Skill download cache for %s/%s@%s: %v", namespace, name, version, err)
		return nil, nil
	}
	if payload.Version != version {
		h.warnf("ignore identity-mismatched Skill download cache for %s/%s@%s", namespace, name, version)
		return nil, nil
	}
	if int64(len(payload.Content)) > model.MaxSkillFileSize {
		h.warnf("ignore oversized Skill download cache for %s/%s@%s", namespace, name, version)
		return nil, nil
	}
	if tag != "" && payload.Tag != tag {
		h.warnf("ignore Skill download cache for %s/%s@%s: tag %q does not match requested %q",
			namespace, name, version, payload.Tag, tag)
		return nil, nil
	}
	var content []byte
	if format == model.SkillFormatZIP {
		zipPath := h.zipPath(namespace, name, version)
		if usedLegacyPath {
			zipPath = h.legacyZipPath(namespace, name, version)
		}
		content, ok = h.readFile(zipPath, model.MaxSkillPackageSize)
		if !ok {
			return nil, nil
		}
		if payload.TotalSize <= 0 || int64(len(content)) != payload.TotalSize {
			h.warnf("ignore incomplete Skill ZIP cache for %s/%s@%s", namespace, name, version)
			return nil, nil
		}
	} else {
		content = []byte(payload.Content)
	}
	digest := sha256.Sum256(content)
	actualDigest := hex.EncodeToString(digest[:])
	if payload.Digest != "" && payload.Digest != actualDigest {
		h.warnf("ignore digest-mismatched Skill cache for %s/%s@%s", namespace, name, version)
		return nil, nil
	}
	if format == model.SkillFormatMarkdown && payload.TotalSize != 0 &&
		int64(len(content)) != payload.TotalSize {
		h.warnf("ignore length-mismatched Skill Markdown cache for %s/%s@%s", namespace, name, version)
		return nil, nil
	}
	return &model.SkillPackage{
		Namespace:   namespace,
		Name:        name,
		Version:     version,
		Format:      format,
		ContentType: payload.ContentType,
		Filename:    payload.Filename,
		Content:     content,
		Digest:      actualDigest,
		FromCache:   true,
	}, nil
}

func (h *persistentHandler) loadActive(namespace, name string) string {
	if h == nil || !h.enabled {
		return ""
	}
	data, _, ok := h.readFileWithLegacy(
		h.activePath(namespace, name),
		h.legacyActivePath(namespace, name),
		1024,
	)
	if !ok {
		return ""
	}
	version := strings.TrimSpace(string(data))
	if version == "" || model.ValidateSkillRefParts(namespace, name, version) != nil {
		h.warnf("ignore invalid Skill active cache for %s/%s", namespace, name)
		return ""
	}
	return version
}

func (h *persistentHandler) resolveVersion(namespace, name, requested string) string {
	if requested != "" {
		return requested
	}
	return h.loadActive(namespace, name)
}

func (h *persistentHandler) writeYAML(path string, value interface{}) bool {
	data, err := yaml.Marshal(value)
	if err != nil {
		h.warnf("fail to marshal Skill cache %s: %v", path, err)
		return false
	}
	return h.writeBytes(path, data)
}

func (h *persistentHandler) writeBytes(path string, data []byte) bool {
	if int64(len(data)) > maxPersistEnvelopeBytes {
		h.warnf("ignore oversized Skill cache write %s: %d bytes", path, len(data))
		return false
	}
	for attempt := 0; attempt <= h.maxWriteRetry; attempt++ {
		if attempt > 0 {
			time.Sleep(h.retryInterval)
		}
		if err := atomicWriteFile(path, data); err == nil {
			return true
		} else if attempt < h.maxWriteRetry {
			h.debugf("retry Skill cache write %s after error: %v", path, err)
		}
	}
	return false
}

func atomicWriteFile(path string, data []byte) error {
	parent := filepath.Dir(path)
	if err := ensurePersistDir(parent); err != nil {
		return err
	}
	tempPath := filepath.Join(parent, ".tmp-"+uuid.New().String()+filepath.Ext(path))
	file, err := os.OpenFile(tempPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(tempPath)
	}()
	if err = file.Chmod(0600); err == nil {
		if _, err = file.Write(data); err == nil {
			err = file.Sync()
		}
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tempPath, path)
}

func (h *persistentHandler) readFile(path string, maxSize int64) ([]byte, bool) {
	for attempt := 0; attempt <= h.maxReadRetry; attempt++ {
		if attempt > 0 {
			time.Sleep(h.retryInterval)
		}
		data, err := readRegularFile(path, maxSize)
		if err == nil {
			return data, true
		}
		if os.IsNotExist(err) {
			continue
		}
		if attempt < h.maxReadRetry {
			h.debugf("retry Skill cache read %s after error: %v", path, err)
		} else if !os.IsNotExist(err) {
			h.warnf("fail to read Skill cache %s: %v", path, err)
		}
	}
	return nil, false
}

func readRegularFile(path string, maxSize int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("path is not a regular file")
	}
	if info.Size() > maxSize {
		return nil, fmt.Errorf("file size %d exceeds maximum %d", info.Size(), maxSize)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()
	var buffer bytes.Buffer
	_, err = buffer.ReadFrom(io.LimitReader(file, maxSize+1))
	if err != nil {
		return nil, err
	}
	data := buffer.Bytes()
	if int64(len(data)) > maxSize {
		return nil, fmt.Errorf("file exceeds maximum %d", maxSize)
	}
	return data, nil
}

func (h *persistentHandler) getPath(namespace, name, version string) string {
	return filepath.Join(h.root, persistDirGet, skillCacheKey(namespace, name, version)+persistExtYAML)
}

func (h *persistentHandler) downloadPath(namespace, name, version, format string) string {
	return filepath.Join(h.root, persistDirDownload,
		skillCacheKey(namespace, name, version)+"."+format+persistExtYAML)
}

func (h *persistentHandler) zipPath(namespace, name, version string) string {
	return filepath.Join(h.root, persistDirDownload, skillCacheKey(namespace, name, version)+persistExtZIP)
}

func (h *persistentHandler) activePath(namespace, name string) string {
	return filepath.Join(h.root, persistDirActive, skillPairCacheKey(namespace, name))
}

func (h *persistentHandler) legacyGetPath(namespace, name, version string) string {
	return filepath.Join(h.root, persistDirGet, encodeSkillKey(namespace, name, version)+persistExtYAML)
}

func (h *persistentHandler) legacyDownloadPath(namespace, name, version, format string) string {
	return filepath.Join(h.root, persistDirDownload,
		encodeSkillKey(namespace, name, version)+"."+format+persistExtYAML)
}

func (h *persistentHandler) legacyZipPath(namespace, name, version string) string {
	return filepath.Join(h.root, persistDirDownload, encodeSkillKey(namespace, name, version)+persistExtZIP)
}

func (h *persistentHandler) legacyActivePath(namespace, name string) string {
	return filepath.Join(h.root, persistDirActive, encodeSkillPair(namespace, name))
}

func (h *persistentHandler) readFileWithLegacy(
	path, legacyPath string, maxSize int64,
) ([]byte, bool, bool) {
	data, ok := h.readFile(path, maxSize)
	if ok {
		return data, false, true
	}
	if legacyPath == "" || legacyPath == path {
		return nil, false, false
	}
	data, ok = h.readFile(legacyPath, maxSize)
	return data, true, ok
}

func skillCacheKey(namespace, name, version string) string {
	return "skill-" + skillIdentityDigest(namespace, name, version)
}

func skillPairCacheKey(namespace, name string) string {
	return "skill-" + skillIdentityDigest(namespace, name)
}

func skillIdentityDigest(parts ...string) string {
	digest := sha256.New()
	for _, part := range parts {
		_, _ = fmt.Fprintf(digest, "%d:%s\n", len(part), part)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func encodeSkillKey(namespace, name, version string) string {
	return encodeSkillPair(namespace, name) + "#" + url.QueryEscape(version)
}

func encodeSkillPair(namespace, name string) string {
	return url.QueryEscape(namespace) + "#" + url.QueryEscape(name)
}

func toPersistedGet(
	namespace, name, version string, resource *model.SkillResource, content string,
) *persistedGetResponse {
	result := &persistedGetResponse{Content: content}
	if resource == nil {
		return result
	}
	result.Resource = &persistedResource{
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
		result.Resource.VersionInfo = &persistedVersionInfo{
			ActiveVersion: resource.VersionInfo.ActiveVersion,
			LatestVersion: resource.VersionInfo.LatestVersion,
			TotalVersions: resource.VersionInfo.TotalVersions,
		}
	}
	result.ResourceVersion = toPersistedVersion(resource.VersionDetail)
	if result.ResourceVersion == nil {
		result.ResourceVersion = &persistedResourceVersion{}
	}
	result.ResourceVersion.Namespace = namespace
	result.ResourceVersion.Name = name
	result.ResourceVersion.Version = version
	return result
}

func toPersistedVersion(version *model.SkillResourceVersion) *persistedResourceVersion {
	if version == nil {
		return nil
	}
	result := &persistedResourceVersion{
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
		result.Storage = &persistedStorageInfo{
			Provider:      version.Storage.Provider,
			Files:         append([]string(nil), version.Storage.Files...),
			Scope:         version.Storage.Scope,
			ContentDigest: version.Storage.ContentDigest,
		}
	}
	return result
}

func fromPersistedResource(resource *persistedResource) *model.SkillResource {
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

func fromPersistedVersion(version *persistedResourceVersion) *model.SkillResourceVersion {
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

func (h *persistentHandler) debugf(format string, args ...interface{}) {
	if h.logCtx != nil {
		h.logCtx.GetCacheLogger().Debugf(format, args...)
	}
}

func (h *persistentHandler) warnf(format string, args ...interface{}) {
	if h.logCtx != nil {
		h.logCtx.GetCacheLogger().Warnf(format, args...)
	}
}
