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
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/skillconnector"
)

type fakeConnector struct {
	mu             sync.Mutex
	chunks         []*skillconnector.DownloadChunk
	activeVersion  string
	downloadCount  int
	downloadErr    error
	getSkillFunc   func(*skillconnector.GetSkillRequest) (*skillconnector.GetSkillResponse, error)
	listSkillsFunc func(*skillconnector.ListSkillsRequest) (*skillconnector.ListSkillsResponse, error)
}

func (f *fakeConnector) Type() common.Type                  { return common.TypeSkillConnector }
func (f *fakeConnector) ID() int32                          { return 1 }
func (f *fakeConnector) Name() string                       { return "fake" }
func (f *fakeConnector) GetSDKContextID() string            { return "fake-sdk" }
func (f *fakeConnector) Init(*plugin.InitContext) error     { return nil }
func (f *fakeConnector) Start() error                       { return nil }
func (f *fakeConnector) Destroy() error                     { return nil }
func (f *fakeConnector) IsEnable(config.Configuration) bool { return true }

func (f *fakeConnector) GetSkill(req *skillconnector.GetSkillRequest) (*skillconnector.GetSkillResponse, error) {
	if f.getSkillFunc != nil {
		return f.getSkillFunc(req)
	}
	version := req.Version
	if version == "" {
		version = f.activeVersion
	}
	if version == "" {
		version = "1.0.0"
	}
	return &skillconnector.GetSkillResponse{
		Resource: &skillconnector.Resource{
			Name:      req.Name,
			Namespace: req.Namespace,
			Status:    "published",
			VersionInfo: &skillconnector.VersionInfo{
				ActiveVersion: version,
			},
		},
		Version: &skillconnector.ResourceVersion{
			Name:      req.Name,
			Namespace: req.Namespace,
			Version:   version,
			Status:    "published",
		},
		Content: "# Skill",
	}, nil
}

func (f *fakeConnector) GetSkillList(req *skillconnector.ListSkillsRequest) (*skillconnector.ListSkillsResponse, error) {
	if f.listSkillsFunc != nil {
		return f.listSkillsFunc(req)
	}
	return &skillconnector.ListSkillsResponse{
		Total: 2,
		Resources: []*skillconnector.Resource{
			{Name: "published", Namespace: req.Namespace, Status: "published", VersionInfo: &skillconnector.VersionInfo{
				ActiveVersion: "1.0.0",
			}},
			{Name: "draft", Namespace: req.Namespace, Status: "draft"},
		},
	}, nil
}

func (f *fakeConnector) DownloadSkill(
	req *skillconnector.DownloadRequest, handler skillconnector.DownloadHandler,
) error {
	f.mu.Lock()
	f.downloadCount++
	f.mu.Unlock()
	if f.downloadErr != nil {
		return f.downloadErr
	}
	for _, chunk := range f.chunks {
		if err := handler(chunk); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeConnector) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.downloadCount
}

func TestDownloadSkillMarkdown(t *testing.T) {
	connector := &fakeConnector{chunks: []*skillconnector.DownloadChunk{
		{
			Version:     "1.0.0",
			ContentType: "text/markdown; charset=utf-8",
			Content:     "# Demo Skill",
		},
	}}
	flow := NewFlow(connector)
	pkg, err := flow.DownloadSkill(&model.SkillDownloadRequest{
		Namespace: "Production",
		Name:      "demo",
		Format:    model.SkillFormatMarkdown,
	})
	require.NoError(t, err)
	assert.Equal(t, "# Demo Skill", string(pkg.Content))
	assert.Equal(t, "1.0.0", pkg.Version)
	assert.NotEmpty(t, pkg.Digest)
}

func TestDownloadSkillDoesNotMutateSharedRequest(t *testing.T) {
	connector := &fakeConnector{chunks: []*skillconnector.DownloadChunk{{
		Version:     "1.0.0",
		ContentType: "text/markdown; charset=utf-8",
		Content:     "# Demo Skill",
	}}}
	flow := NewFlow(connector)
	request := &model.SkillDownloadRequest{
		Namespace: "Production",
		Name:      "demo",
		Version:   "1.0.0",
	}

	const callers = 32
	var waitGroup sync.WaitGroup
	errors := make(chan error, callers)
	for index := 0; index < callers; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			_, err := flow.DownloadSkill(request)
			errors <- err
		}()
	}
	waitGroup.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	assert.Empty(t, request.Format)
}

func TestGetSkillReturnsPublishedContent(t *testing.T) {
	flow := NewFlow(&fakeConnector{})
	resource, err := flow.GetSkill(&model.GetSkillRequest{
		Namespace: "Production",
		Name:      "demo",
	})
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", resource.Version)
	assert.Equal(t, "# Skill", resource.Content)
	assert.Equal(t, "1.0.0", resource.VersionInfo.ActiveVersion)
}

func TestGetSkillListUsesActiveVersion(t *testing.T) {
	flow := NewFlow(&fakeConnector{})
	resources, err := flow.GetSkillList(&model.GetSkillListRequest{Namespace: "Production"})
	require.NoError(t, err)
	require.Len(t, resources, 1)
	assert.Equal(t, "published", resources[0].Name)
	assert.Equal(t, "1.0.0", resources[0].VersionInfo.ActiveVersion)
}

func TestGetSkillRejectsInvalidConnectorResponses(t *testing.T) {
	tests := []struct {
		name     string
		response *skillconnector.GetSkillResponse
		wantErr  string
	}{
		{
			name:    "nil_response",
			wantErr: "empty response",
		},
		{
			name: "missing_version",
			response: &skillconnector.GetSkillResponse{
				Resource: &skillconnector.Resource{
					Namespace: "Production",
					Name:      "demo",
				},
			},
			wantErr: "no version",
		},
		{
			name: "version_mismatch",
			response: &skillconnector.GetSkillResponse{
				Resource: &skillconnector.Resource{
					Namespace: "Production",
					Name:      "demo",
				},
				Version: &skillconnector.ResourceVersion{
					Namespace: "Production",
					Name:      "demo",
					Version:   "2.0.0",
					Status:    "published",
				},
				Content: "# Wrong version",
			},
			wantErr: "requested version",
		},
		{
			name: "service_identity_mismatch",
			response: &skillconnector.GetSkillResponse{
				Resource: &skillconnector.Resource{
					Namespace: "Other",
					Name:      "demo",
				},
				Version: &skillconnector.ResourceVersion{
					Namespace: "Production",
					Name:      "demo",
					Version:   "1.0.0",
					Status:    "published",
				},
				Content: "# Wrong namespace",
			},
			wantErr: "requested skill",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flow := NewFlow(&fakeConnector{
				getSkillFunc: func(*skillconnector.GetSkillRequest) (*skillconnector.GetSkillResponse, error) {
					return tt.response, nil
				},
			})
			_, err := flow.GetSkill(&model.GetSkillRequest{
				Namespace: "Production",
				Name:      "demo",
				Version:   "1.0.0",
			})
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
			require.Equal(t, model.ErrCodeInvalidResponse, model.GetErrorCodeFromError(err))
		})
	}
}

func TestGetSkillListRejectsInvalidConnectorResponses(t *testing.T) {
	tests := []struct {
		name     string
		response *skillconnector.ListSkillsResponse
		wantErr  string
	}{
		{name: "nil_response", wantErr: "empty list response"},
		{
			name: "nil_resource",
			response: &skillconnector.ListSkillsResponse{
				Resources: []*skillconnector.Resource{nil},
			},
			wantErr: "empty skill resource",
		},
		{
			name: "namespace_mismatch",
			response: &skillconnector.ListSkillsResponse{
				Resources: []*skillconnector.Resource{{
					Namespace: "Other",
					Name:      "demo",
					VersionInfo: &skillconnector.VersionInfo{
						ActiveVersion: "1.0.0",
					},
				}},
			},
			wantErr: "requested namespace",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flow := NewFlow(&fakeConnector{
				listSkillsFunc: func(*skillconnector.ListSkillsRequest) (*skillconnector.ListSkillsResponse, error) {
					return tt.response, nil
				},
			})
			_, err := flow.GetSkillList(&model.GetSkillListRequest{Namespace: "Production"})
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
			require.Equal(t, model.ErrCodeInvalidResponse, model.GetErrorCodeFromError(err))
		})
	}
}

func TestGetSkillListFiltersNonPublishedActiveResource(t *testing.T) {
	flow := NewFlow(&fakeConnector{
		listSkillsFunc: func(*skillconnector.ListSkillsRequest) (*skillconnector.ListSkillsResponse, error) {
			return &skillconnector.ListSkillsResponse{
				Resources: []*skillconnector.Resource{{
					Namespace: "Production",
					Name:      "draft",
					Status:    "draft",
					VersionInfo: &skillconnector.VersionInfo{
						ActiveVersion: "1.0.0",
					},
				}},
			}, nil
		},
	})
	resources, err := flow.GetSkillList(&model.GetSkillListRequest{Namespace: "Production"})
	require.NoError(t, err)
	require.Empty(t, resources)
}

func TestDownloadSkillZIPChunks(t *testing.T) {
	content := bytes.Repeat([]byte{0x5a}, maxDownloadChunkSize+17)
	connector := &fakeConnector{chunks: []*skillconnector.DownloadChunk{
		{
			Version:     "1.0.0",
			ContentType: "application/zip",
			TotalSize:   int64(len(content)),
			Filename:    "demo.zip",
			ZipChunk:    append([]byte(nil), content[:maxDownloadChunkSize]...),
		},
		{ZipChunk: append([]byte(nil), content[maxDownloadChunkSize:]...)},
	}}
	flow := NewFlow(connector)
	pkg, err := flow.DownloadSkill(&model.SkillDownloadRequest{
		Namespace: "Production",
		Name:      "demo",
		Format:    model.SkillFormatZIP,
	})
	require.NoError(t, err)
	assert.Equal(t, content, pkg.Content)
	assert.Equal(t, "demo.zip", pkg.Filename)
}

func TestDownloadSkillRejectsInvalidStreams(t *testing.T) {
	tests := []struct {
		name    string
		chunks  []*skillconnector.DownloadChunk
		format  string
		wantErr string
	}{
		{
			name: "total_mismatch",
			chunks: []*skillconnector.DownloadChunk{{
				Version: "1.0.0", ContentType: "application/zip", TotalSize: 10,
				Filename: "demo.zip", ZipChunk: []byte("short"),
			}},
			format:  model.SkillFormatZIP,
			wantErr: "expected 10",
		},
		{
			name: "empty_chunk",
			chunks: []*skillconnector.DownloadChunk{{
				Version: "1.0.0", ContentType: "application/zip", TotalSize: 10, Filename: "demo.zip",
			}},
			format:  model.SkillFormatZIP,
			wantErr: "first frame chunk is empty",
		},
		{
			name: "missing_filename",
			chunks: []*skillconnector.DownloadChunk{{
				Version: "1.0.0", ContentType: "application/zip", TotalSize: 1, ZipChunk: []byte("a"),
			}},
			format:  model.SkillFormatZIP,
			wantErr: "must contain filename",
		},
		{
			name: "package_too_large",
			chunks: []*skillconnector.DownloadChunk{{
				Version: "1.0.0", ContentType: "application/zip",
				TotalSize: model.MaxSkillPackageSize + 1, ZipChunk: []byte("a"),
			}},
			format:  model.SkillFormatZIP,
			wantErr: "total size",
		},
		{
			name: "follow_up_version_changed",
			chunks: []*skillconnector.DownloadChunk{
				{Version: "1.0.0", ContentType: "application/zip", TotalSize: 2,
					Filename: "demo.zip", ZipChunk: []byte("a")},
				{Version: "1.0.1", ZipChunk: []byte("b")},
			},
			format:  model.SkillFormatZIP,
			wantErr: "does not match requested version",
		},
		{
			name: "follow_up_header",
			chunks: []*skillconnector.DownloadChunk{
				{Version: "1.0.0", ContentType: "application/zip", TotalSize: 2,
					Filename: "demo.zip", ZipChunk: []byte("a")},
				{Version: "1.0.0", ContentType: "application/zip", ZipChunk: []byte("b")},
			},
			format:  model.SkillFormatZIP,
			wantErr: "only contain zip_chunk",
		},
		{
			name: "content_type_mismatch",
			chunks: []*skillconnector.DownloadChunk{{
				Version: "1.0.0", ContentType: "text/plain", Content: "# Skill",
			}},
			format:  model.SkillFormatMarkdown,
			wantErr: "content type",
		},
		{
			name: "markdown_multiple_frames",
			chunks: []*skillconnector.DownloadChunk{
				{Version: "1.0.0", ContentType: "text/markdown", Content: "# Skill"},
				{Version: "1.0.0", ContentType: "text/markdown", Content: "more"},
			},
			format:  model.SkillFormatMarkdown,
			wantErr: "exactly one frame",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flow := NewFlow(&fakeConnector{chunks: tt.chunks})
			_, err := flow.DownloadSkill(&model.SkillDownloadRequest{
				Namespace: "Production",
				Name:      "demo",
				Version:   "1.0.0",
				Format:    tt.format,
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Equal(t, model.ErrCodeSkillPackageInvalid, model.GetErrorCodeFromError(err))
		})
	}
}

func TestInstallSkillAndCacheHit(t *testing.T) {
	content, err := buildZip(t, map[string]string{"SKILL.md": "# Demo Skill\n"})
	require.NoError(t, err)
	connector := &fakeConnector{chunks: zipChunks(content, "1.0.0", "demo.zip")}
	flow := NewFlow(connector)
	cacheDir := t.TempDir()
	req := &model.SkillInstallRequest{
		Namespace: "Production",
		Name:      "demo",
		Version:   "1.0.0",
		CacheDir:  cacheDir,
	}
	first, err := flow.Install(req)
	require.NoError(t, err)
	assert.False(t, first.CacheHit)
	assert.Equal(t, 1, connector.count())
	_, statErr := os.Stat(filepath.Join(first.Dir, "SKILL.md"))
	require.NoError(t, statErr)
	_, manifestErr := os.Stat(filepath.Join(first.Dir, skillManifestName))
	require.NoError(t, manifestErr)

	second, err := flow.Install(req)
	require.NoError(t, err)
	assert.True(t, second.CacheHit)
	assert.Equal(t, first.Digest, second.Digest)
	assert.Equal(t, 1, connector.count())
}

func TestForceInstallKeepsPublishedGenerationReadable(t *testing.T) {
	content, err := buildZip(t, map[string]string{
		"SKILL.md": "# Registry Skill\n",
		"tool.txt": "tool",
	})
	require.NoError(t, err)
	flow := NewFlow(&fakeConnector{chunks: zipChunks(content, "1.0.0", "demo.zip")})
	req := &model.SkillInstallRequest{
		Namespace: "Production",
		Name:      "demo",
		Version:   "1.0.0",
		CacheDir:  t.TempDir(),
	}
	installed, err := flow.Install(req)
	require.NoError(t, err)
	entry := filepath.Join(installed.Dir, filepath.FromSlash(installed.EntryFile))
	expected, err := ioutil.ReadFile(entry)
	require.NoError(t, err)

	done := make(chan struct{})
	finished := make(chan struct{})
	readFailure := make(chan error, 1)
	go func() {
		defer close(finished)
		for {
			select {
			case <-done:
				return
			default:
			}
			data, readErr := ioutil.ReadFile(entry)
			if readErr != nil {
				readFailure <- fmt.Errorf("published entry became unreadable: %w", readErr)
				return
			}
			if !bytes.Equal(data, expected) {
				readFailure <- fmt.Errorf("published entry changed during Force install")
				return
			}
		}
	}()

	req.Force = true
	for index := 0; index < 400; index++ {
		_, err = flow.Install(req)
		require.NoError(t, err)
	}
	close(done)
	<-finished
	select {
	case readErr := <-readFailure:
		require.NoError(t, readErr)
	default:
	}
}

func TestForceInstallPublishesNewGenerationAndKeepsOldContent(t *testing.T) {
	oldContent, err := buildZip(t, map[string]string{"SKILL.md": "# Old Skill\n"})
	require.NoError(t, err)
	newContent, err := buildZip(t, map[string]string{"SKILL.md": "# New Skill\n"})
	require.NoError(t, err)
	cacheDir := t.TempDir()
	req := &model.SkillInstallRequest{
		Namespace: "Production",
		Name:      "demo",
		Version:   "1.0.0",
		CacheDir:  cacheDir,
	}
	oldFlow := NewFlow(&fakeConnector{chunks: zipChunks(oldContent, "1.0.0", "demo.zip")})
	oldInstalled, err := oldFlow.Install(req)
	require.NoError(t, err)

	time.Sleep(time.Millisecond)
	req.Force = true
	newFlow := NewFlow(&fakeConnector{chunks: zipChunks(newContent, "1.0.0", "demo.zip")})
	newInstalled, err := newFlow.Install(req)
	require.NoError(t, err)
	assert.NotEqual(t, oldInstalled.Dir, newInstalled.Dir)

	oldEntry, err := ioutil.ReadFile(filepath.Join(oldInstalled.Dir, filepath.FromSlash(oldInstalled.EntryFile)))
	require.NoError(t, err)
	assert.Equal(t, "# Old Skill\n", string(oldEntry))
	newEntry, err := ioutil.ReadFile(filepath.Join(newInstalled.Dir, filepath.FromSlash(newInstalled.EntryFile)))
	require.NoError(t, err)
	assert.Equal(t, "# New Skill\n", string(newEntry))

	req.Force = false
	cached, err := newFlow.Install(req)
	require.NoError(t, err)
	assert.True(t, cached.CacheHit)
	assert.Equal(t, newInstalled.Dir, cached.Dir)
}

func TestForceInstallReusingGenerationKeepsCurrentSelection(t *testing.T) {
	contentA, err := buildZip(t, map[string]string{"SKILL.md": "# Skill A\n"})
	require.NoError(t, err)
	contentB, err := buildZip(t, map[string]string{"SKILL.md": "# Skill B\n"})
	require.NoError(t, err)
	connector := &fakeConnector{chunks: zipChunks(contentA, "1.0.0", "demo.zip")}
	flow := NewFlow(connector)
	req := &model.SkillInstallRequest{
		Namespace: "Production", Name: "demo", Version: "1.0.0", CacheDir: t.TempDir(),
	}
	first, err := flow.Install(req)
	require.NoError(t, err)
	time.Sleep(time.Millisecond)
	req.Force = true
	connector.chunks = zipChunks(contentB, "1.0.0", "demo.zip")
	second, err := flow.Install(req)
	require.NoError(t, err)
	require.NotEqual(t, first.Dir, second.Dir)
	time.Sleep(time.Millisecond)
	connector.chunks = zipChunks(contentA, "1.0.0", "demo.zip")
	forced, err := flow.Install(req)
	require.NoError(t, err)
	require.Equal(t, first.Digest, forced.Digest)
	require.Equal(t, 3, connector.count())

	req.Force = false
	cached, err := NewFlow(connector).Install(req)
	require.NoError(t, err)
	require.Equal(t, 3, connector.count(), "an unchanged successful Force result should be a cache hit")
	require.Equal(t, forced.Digest, cached.Digest,
		"ordinary install reverted the latest successful Force selection")
	require.Equal(t, forced.Dir, cached.Dir)
}

func TestCorruptCurrentGenerationTriggersRepair(t *testing.T) {
	contentA, err := buildZip(t, map[string]string{"SKILL.md": "# Skill A\n"})
	require.NoError(t, err)
	contentB, err := buildZip(t, map[string]string{"SKILL.md": "# Skill B\n"})
	require.NoError(t, err)
	connector := &fakeConnector{chunks: zipChunks(contentA, "1.0.0", "demo.zip")}
	flow := NewFlow(connector)
	req := &model.SkillInstallRequest{
		Namespace: "Production", Name: "demo", Version: "1.0.0", CacheDir: t.TempDir(),
	}
	first, err := flow.Install(req)
	require.NoError(t, err)
	time.Sleep(time.Millisecond)
	req.Force = true
	connector.chunks = zipChunks(contentB, "1.0.0", "demo.zip")
	latest, err := flow.Install(req)
	require.NoError(t, err)
	require.NotEqual(t, first.Dir, latest.Dir)
	require.NoError(t, writeFile(
		filepath.Join(latest.Dir, filepath.FromSlash(latest.EntryFile)), []byte("broken"), 0644))

	req.Force = false
	connector.downloadErr = errors.New("offline")
	_, err = flow.Install(req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "offline")
	require.Equal(t, 3, connector.count())

	connector.downloadErr = nil
	repaired, err := NewFlow(connector).Install(req)
	require.NoError(t, err)
	entry, err := ioutil.ReadFile(filepath.Join(repaired.Dir, filepath.FromSlash(repaired.EntryFile)))
	require.NoError(t, err)
	require.Equal(t, 4, connector.count(),
		"corruption must repair the selected generation instead of using an older package")
	require.Equal(t, latest.Digest, repaired.Digest)
	require.Equal(t, "# Skill B\n", string(entry))
	require.NotEqual(t, latest.Dir, repaired.Dir)
}

func TestCacheWithoutSelectionDoesNotFallBackFromCorruptGeneration(t *testing.T) {
	contentA, err := buildZip(t, map[string]string{"SKILL.md": "# Skill A\n"})
	require.NoError(t, err)
	contentB, err := buildZip(t, map[string]string{"SKILL.md": "# Skill B\n"})
	require.NoError(t, err)
	connector := &fakeConnector{chunks: zipChunks(contentA, "1.0.0", "demo.zip")}
	flow := NewFlow(connector)
	req := &model.SkillInstallRequest{
		Namespace: "Production", Name: "demo", Version: "1.0.0", CacheDir: t.TempDir(),
	}
	first, err := flow.Install(req)
	require.NoError(t, err)
	time.Sleep(time.Millisecond)
	req.Force = true
	connector.chunks = zipChunks(contentB, "1.0.0", "demo.zip")
	latest, err := flow.Install(req)
	require.NoError(t, err)
	require.NotEqual(t, first.Dir, latest.Dir)
	require.NoError(t, os.Remove(selectionFilePath(filepath.Dir(latest.Dir))))
	require.NoError(t, writeFile(
		filepath.Join(latest.Dir, filepath.FromSlash(latest.EntryFile)), []byte("broken"), 0644))

	req.Force = false
	repaired, err := flow.Install(req)
	require.NoError(t, err)
	require.Equal(t, 3, connector.count(),
		"a markerless cache must repair a damaged generation instead of using older content")
	require.Equal(t, latest.Digest, repaired.Digest)
	entry, err := ioutil.ReadFile(filepath.Join(repaired.Dir, filepath.FromSlash(repaired.EntryFile)))
	require.NoError(t, err)
	require.Equal(t, "# Skill B\n", string(entry))
}

func TestCorruptCurrentSelectionReturnsDiskError(t *testing.T) {
	content, err := buildZip(t, map[string]string{"SKILL.md": "# Skill A\n"})
	require.NoError(t, err)
	connector := &fakeConnector{chunks: zipChunks(content, "1.0.0", "demo.zip")}
	flow := NewFlow(connector)
	req := &model.SkillInstallRequest{
		Namespace: "Production", Name: "demo", Version: "1.0.0", CacheDir: t.TempDir(),
	}
	installed, err := flow.Install(req)
	require.NoError(t, err)
	require.NoError(t, writeFile(selectionFilePath(filepath.Dir(installed.Dir)), []byte("{"), 0644))

	_, err = flow.Install(req)
	require.Error(t, err)
	require.EqualValues(t, model.ErrCodeDiskError, model.GetErrorCodeFromError(err))
	require.Equal(t, 1, connector.count())
}

func TestFailedForceInstallKeepsCurrentSelection(t *testing.T) {
	contentA, err := buildZip(t, map[string]string{"SKILL.md": "# Skill A\n"})
	require.NoError(t, err)
	contentB, err := buildZip(t, map[string]string{"SKILL.md": "# Skill B\n"})
	require.NoError(t, err)
	connector := &fakeConnector{chunks: zipChunks(contentA, "1.0.0", "demo.zip")}
	flow := NewFlow(connector)
	req := &model.SkillInstallRequest{
		Namespace: "Production", Name: "demo", Version: "1.0.0", CacheDir: t.TempDir(),
	}
	selected, err := flow.Install(req)
	require.NoError(t, err)

	req.Force = true
	connector.chunks = zipChunks(contentB, "1.0.0", "demo.zip")
	connector.downloadErr = errors.New("download failed")
	_, err = flow.Install(req)
	require.Error(t, err)
	require.Equal(t, 2, connector.count())

	req.Force = false
	connector.downloadErr = nil
	cached, err := flow.Install(req)
	require.NoError(t, err)
	require.True(t, cached.CacheHit)
	require.Equal(t, selected.Dir, cached.Dir)
	require.Equal(t, 2, connector.count())
	entry, err := ioutil.ReadFile(filepath.Join(cached.Dir, filepath.FromSlash(cached.EntryFile)))
	require.NoError(t, err)
	require.Equal(t, "# Skill A\n", string(entry))
}

func TestInstallSkillsConcurrentlyCreateSharedNamespace(t *testing.T) {
	content, err := buildZip(t, map[string]string{"SKILL.md": "# Demo Skill\n"})
	require.NoError(t, err)
	flow := NewFlow(&fakeConnector{chunks: zipChunks(content, "1.0.0", "demo.zip")})
	cacheDir := t.TempDir()
	const installCount = 32

	start := make(chan struct{})
	results := make(chan error, installCount)
	var waitGroup sync.WaitGroup
	for index := 0; index < installCount; index++ {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			<-start
			_, installErr := flow.Install(&model.SkillInstallRequest{
				Namespace: "Production",
				Name:      fmt.Sprintf("skill-%02d", index),
				Version:   "1.0.0",
				CacheDir:  cacheDir,
			})
			results <- installErr
		}(index)
	}
	close(start)
	waitGroup.Wait()
	close(results)
	for installErr := range results {
		require.NoError(t, installErr)
	}
}

func TestInstallSkillConcurrentlyFromIndependentFlows(t *testing.T) {
	content, err := buildZip(t, map[string]string{"SKILL.md": "# Demo Skill\n"})
	require.NoError(t, err)
	cacheDir := t.TempDir()
	const installCount = 16

	start := make(chan struct{})
	results := make(chan error, installCount)
	dirs := make(chan string, installCount)
	var waitGroup sync.WaitGroup
	for index := 0; index < installCount; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			installed, installErr := NewFlow(&fakeConnector{
				chunks: zipChunks(content, "1.0.0", "demo.zip"),
			}).Install(&model.SkillInstallRequest{
				Namespace: "Production",
				Name:      "demo",
				Version:   "1.0.0",
				CacheDir:  cacheDir,
			})
			if installErr == nil {
				dirs <- installed.Dir
			}
			results <- installErr
		}()
	}
	close(start)
	waitGroup.Wait()
	close(results)
	close(dirs)
	for installErr := range results {
		require.NoError(t, installErr)
	}
	var expectedDir string
	for dir := range dirs {
		if expectedDir == "" {
			expectedDir = dir
			continue
		}
		require.Equal(t, expectedDir, dir)
	}
	require.NotEmpty(t, expectedDir)
}

func TestTreeDigestRejectsCrossFileBoundaryCollision(t *testing.T) {
	content, err := buildZip(t, map[string]string{"SKILL.md": "# Skill", "z": "payload"})
	require.NoError(t, err)
	connector := &fakeConnector{chunks: zipChunks(content, "1.0.0", "demo.zip")}
	flow := NewFlow(connector)
	req := &model.SkillInstallRequest{
		Namespace: "Production",
		Name:      "demo",
		Version:   "1.0.0",
		CacheDir:  t.TempDir(),
	}
	first, err := flow.Install(req)
	require.NoError(t, err)

	require.NoError(t, os.Remove(filepath.Join(first.Dir, "z")))
	require.NoError(t, writeFile(filepath.Join(first.Dir, model.SkillFileName),
		[]byte("# Skill\x00z\x00payload"), 0644))

	second, err := flow.Install(req)
	require.NoError(t, err)
	assert.False(t, second.CacheHit)
	assert.Equal(t, 2, connector.count())
}

func TestTreeDigestRejectsOldManifestVersion(t *testing.T) {
	content, err := buildZip(t, map[string]string{"SKILL.md": "# Demo Skill\n"})
	require.NoError(t, err)
	connector := &fakeConnector{chunks: zipChunks(content, "1.0.0", "demo.zip")}
	flow := NewFlow(connector)
	req := &model.SkillInstallRequest{
		Namespace: "Production",
		Name:      "demo",
		Version:   "1.0.0",
		CacheDir:  t.TempDir(),
	}
	first, err := flow.Install(req)
	require.NoError(t, err)

	manifestPath := filepath.Join(first.Dir, skillManifestName)
	manifestData, err := readFile(manifestPath)
	require.NoError(t, err)
	var manifest installManifest
	require.NoError(t, json.Unmarshal(manifestData, &manifest))
	manifest.TreeDigestVersion = treeDigestVersion - 1
	manifestData, err = json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, writeFile(manifestPath, manifestData, 0644))

	second, err := flow.Install(req)
	require.NoError(t, err)
	assert.False(t, second.CacheHit)
	assert.Equal(t, 2, connector.count())
}

func TestTreeDigestIncludesNestedManifestName(t *testing.T) {
	content, err := buildZip(t, map[string]string{
		"SKILL.md":                 "# Demo Skill\n",
		"data/.polaris-skill.json": `{"nested":true}`,
	})
	require.NoError(t, err)
	connector := &fakeConnector{chunks: zipChunks(content, "1.0.0", "demo.zip")}
	flow := NewFlow(connector)
	req := &model.SkillInstallRequest{
		Namespace: "Production",
		Name:      "demo",
		Version:   "1.0.0",
		CacheDir:  t.TempDir(),
	}
	first, err := flow.Install(req)
	require.NoError(t, err)
	require.NoError(t, writeFile(
		filepath.Join(first.Dir, "data", skillManifestName), []byte(`{"nested":false}`), 0644))

	second, err := flow.Install(req)
	require.NoError(t, err)
	assert.False(t, second.CacheHit)
	assert.Equal(t, 2, connector.count())
}

func TestTreeDigestRejectsNonRegularCacheFile(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "data"), 0755))
	outside := filepath.Join(root, "outside")
	require.NoError(t, writeFile(outside, []byte("outside"), 0644))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "data", skillManifestName)))

	_, err := digestTree(root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a regular file")
}

func TestInstallSkillPreservesCaseDistinctIdentities(t *testing.T) {
	content, err := buildZip(t, map[string]string{"SKILL.md": "# Demo Skill\n"})
	require.NoError(t, err)
	connector := &fakeConnector{chunks: zipChunks(content, "1.0.0", "demo.zip")}
	flow := NewFlow(connector)
	cacheDir := t.TempDir()

	first, err := flow.Install(&model.SkillInstallRequest{
		Namespace: "Production",
		Name:      "CaseSkill",
		Version:   "1.0.0",
		CacheDir:  cacheDir,
	})
	require.NoError(t, err)
	second, err := flow.Install(&model.SkillInstallRequest{
		Namespace: "Production",
		Name:      "caseskill",
		Version:   "1.0.0",
		CacheDir:  cacheDir,
	})
	require.NoError(t, err)

	assert.NotEqual(t, first.Dir, second.Dir)
	assertManifestIdentity(t, first.Dir, "CaseSkill")
	assertManifestIdentity(t, second.Dir, "caseskill")
}

func assertManifestIdentity(t *testing.T, dir, name string) {
	t.Helper()
	data, err := readFile(filepath.Join(dir, skillManifestName))
	require.NoError(t, err)
	var manifest installManifest
	require.NoError(t, json.Unmarshal(data, &manifest))
	assert.Equal(t, name, manifest.Name)
}

func TestEnsureSkillDirRejectsExistingFileAfterEEXIST(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Production")
	require.NoError(t, writeFile(dir, []byte("not a directory"), 0644))

	err := ensureSkillDir(root, dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not a directory")
}

func TestInstallSkillDoesNotMutateRequest(t *testing.T) {
	content, err := buildZip(t, map[string]string{"SKILL.md": "# Demo Skill\n"})
	require.NoError(t, err)
	flow := NewFlow(&fakeConnector{chunks: zipChunks(content, "1.0.0", "demo.zip")})
	req := &model.SkillInstallRequest{
		Namespace: "Production",
		Name:      "demo",
		CacheDir:  t.TempDir(),
	}
	_, err = flow.Install(req)
	require.NoError(t, err)
	assert.Empty(t, req.Version)
}

func TestInstallSkillValidatesResolvedVersionBeforeCache(t *testing.T) {
	flow := NewFlow(&fakeConnector{activeVersion: "1/../../../outside"})
	_, err := flow.Install(&model.SkillInstallRequest{
		Namespace: "Production",
		Name:      "demo",
		CacheDir:  t.TempDir(),
	})
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeAPIInvalidArgument, model.GetErrorCodeFromError(err))
	assert.Contains(t, err.Error(), "not a safe path segment")
}

func TestInstallSkillRejectsNamespaceSymlink(t *testing.T) {
	content, err := buildZip(t, map[string]string{"SKILL.md": "# Demo Skill\n"})
	require.NoError(t, err)
	connector := &fakeConnector{chunks: zipChunks(content, "1.0.0", "demo.zip")}
	flow := NewFlow(connector)
	cacheRoot := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(cacheRoot, "Production")))

	_, err = flow.Install(&model.SkillInstallRequest{
		Namespace: "Production",
		Name:      "demo",
		Version:   "1.0.0",
		CacheDir:  cacheRoot,
	})
	require.Error(t, err)
	assert.EqualValues(t, model.ErrCodeDiskError, model.GetErrorCodeFromError(err))
	assert.Contains(t, err.Error(), "must not be a symlink")
	assert.Equal(t, 0, connector.count())
	entries, readErr := ioutil.ReadDir(outside)
	require.NoError(t, readErr)
	assert.Empty(t, entries)
}

func TestInstallSkillWithTopLevelDirectory(t *testing.T) {
	content, err := buildZip(t, map[string]string{
		"demo/SKILL.md":       "# Demo Skill\n",
		"demo/scripts/run.sh": "#!/bin/sh\n",
	})
	require.NoError(t, err)
	flow := NewFlow(&fakeConnector{chunks: zipChunks(content, "1.0.0", "demo.zip")})
	installed, err := flow.Install(&model.SkillInstallRequest{
		Namespace: "Production",
		Name:      "demo",
		Version:   "1.0.0",
		CacheDir:  t.TempDir(),
	})
	require.NoError(t, err)
	assert.Equal(t, "demo/SKILL.md", installed.EntryFile)
	_, statErr := os.Stat(filepath.Join(installed.Dir, "demo", model.SkillFileName))
	require.NoError(t, statErr)
}

func TestInstallSkillRejectsUnsafePackages(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		wantErr string
	}{
		{
			name:    "missing_skill_md",
			files:   map[string]string{"README.md": "missing"},
			wantErr: "does not contain SKILL.md",
		},
		{
			name:    "path_traversal",
			files:   map[string]string{"../SKILL.md": "bad"},
			wantErr: "unsafe",
		},
		{
			name:    "absolute_path",
			files:   map[string]string{"/tmp/SKILL.md": "bad"},
			wantErr: "unsafe",
		},
		{
			name:    "reserved_manifest",
			files:   map[string]string{"SKILL.md": "# Skill", skillManifestName: "{}"},
			wantErr: skillManifestName,
		},
		{
			name:    "reserved_manifest_case_variant",
			files:   map[string]string{"SKILL.md": "# Skill", ".POLARIS-SKILL.JSON": "{}"},
			wantErr: skillManifestName,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, err := buildZip(t, tt.files)
			require.NoError(t, err)
			flow := NewFlow(&fakeConnector{chunks: zipChunks(content, "1.0.0", "demo.zip")})
			_, err = flow.Install(&model.SkillInstallRequest{
				Namespace: "Production",
				Name:      "demo",
				Version:   "1.0.0",
				CacheDir:  t.TempDir(),
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Equal(t, model.ErrCodeSkillPackageInvalid, model.GetErrorCodeFromError(err))
		})
	}
}

func TestExtractZIPRejectsSymlink(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	header := &zip.FileHeader{Name: "link"}
	header.SetMode(os.ModeSymlink | 0777)
	entry, err := writer.CreateHeader(header)
	require.NoError(t, err)
	_, err = entry.Write([]byte("SKILL.md"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	target := t.TempDir()
	err = extractZIP(buffer.Bytes(), target)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symlink")
	assert.Equal(t, model.ErrCodeSkillPackageInvalid, model.GetErrorCodeFromError(err))
}

func TestExtractZIPRejectsTooManyEntries(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for index := 0; index <= maxSkillArchiveEntries; index++ {
		entry, err := writer.Create(fmt.Sprintf("files/%05d.txt", index))
		require.NoError(t, err)
		_, err = entry.Write(nil)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())

	err := extractZIP(buffer.Bytes(), t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maximum")
	assert.EqualValues(t, model.ErrCodeSkillPackageInvalid, model.GetErrorCodeFromError(err))
}

func buildZip(t *testing.T, files map[string]string) ([]byte, error) {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err = entry.Write([]byte(content)); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func zipChunks(content []byte, version, filename string) []*skillconnector.DownloadChunk {
	firstSize := len(content)
	if firstSize > maxDownloadChunkSize {
		firstSize = maxDownloadChunkSize
	}
	chunks := []*skillconnector.DownloadChunk{{
		Version:     version,
		ContentType: "application/zip",
		TotalSize:   int64(len(content)),
		Filename:    filename,
		ZipChunk:    append([]byte(nil), content[:firstSize]...),
	}}
	for offset := firstSize; offset < len(content); {
		end := offset + maxDownloadChunkSize
		if end > len(content) {
			end = len(content)
		}
		chunks = append(chunks, &skillconnector.DownloadChunk{
			Version:  version,
			ZipChunk: append([]byte(nil), content[offset:end]...),
		})
		offset = end
	}
	return chunks
}
