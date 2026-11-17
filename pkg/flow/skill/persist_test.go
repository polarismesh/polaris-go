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
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/skillconnector"
)

func persistenceOptions(t *testing.T) Options {
	t.Helper()
	return Options{
		PersistEnable:        true,
		PersistDir:           t.TempDir(),
		PersistMaxWriteRetry: 1,
		PersistMaxReadRetry:  1,
		PersistRetryInterval: 5 * time.Millisecond,
		FallbackToLocalCache: true,
	}
}

func TestGetSkillPersistsStorageAndFallsBackOnNetworkError(t *testing.T) {
	options := persistenceOptions(t)
	first := &fakeConnector{
		getSkillFunc: func(req *skillconnector.GetSkillRequest) (*skillconnector.GetSkillResponse, error) {
			return &skillconnector.GetSkillResponse{
				Resource: &skillconnector.Resource{
					Namespace: req.Namespace,
					Name:      req.Name,
					Status:    "published",
					VersionInfo: &skillconnector.VersionInfo{
						ActiveVersion: "1.2.3",
					},
				},
				Version: &skillconnector.ResourceVersion{
					Namespace: req.Namespace,
					Name:      req.Name,
					Version:   "1.2.3",
					Status:    "published",
					Storage: &skillconnector.StorageInfo{
						Provider:      "polaris_config",
						Files:         []string{"SKILL.md", "references/guide.md"},
						Scope:         "namespace",
						ContentDigest: "sha256:server",
					},
				},
				Content: "# persisted",
			}, nil
		},
	}
	flow, err := NewFlowWithOptions(first, options)
	require.NoError(t, err)
	resource, err := flow.GetSkill(&model.GetSkillRequest{Namespace: "Production", Name: "weather"})
	require.NoError(t, err)
	require.NotNil(t, resource.VersionDetail)
	require.NotNil(t, resource.VersionDetail.Storage)
	assert.Equal(t, "polaris_config", resource.VersionDetail.Storage.Provider)
	assert.Equal(t, []string{"SKILL.md", "references/guide.md"}, resource.VersionDetail.Storage.Files)
	flow.Destroy()

	cachedFlow, err := NewFlowWithOptions(&fakeConnector{
		getSkillFunc: func(*skillconnector.GetSkillRequest) (*skillconnector.GetSkillResponse, error) {
			return nil, model.NewSDKError(model.ErrCodeNetworkError, errors.New("offline"), "offline")
		},
	}, options)
	require.NoError(t, err)
	cached, err := cachedFlow.GetSkill(&model.GetSkillRequest{Namespace: "Production", Name: "weather"})
	require.NoError(t, err)
	assert.True(t, cached.FromCache)
	assert.Equal(t, "1.2.3", cached.Version)
	assert.Equal(t, "# persisted", cached.Content)
	require.NotNil(t, cached.VersionDetail)
	require.NotNil(t, cached.VersionDetail.Storage)
	assert.Equal(t, "namespace", cached.VersionDetail.Storage.Scope)
	cachedFlow.Destroy()

	active, err := ioutil.ReadFile(filepath.Join(
		options.PersistDir, persistDirActive, skillPairCacheKey("Production", "weather")))
	require.NoError(t, err)
	assert.Equal(t, "1.2.3", string(active))
}

func TestGetSkillFallsBackOnlyForNetworkErrors(t *testing.T) {
	options := persistenceOptions(t)
	flow, err := NewFlowWithOptions(&fakeConnector{}, options)
	require.NoError(t, err)
	_, err = flow.GetSkill(&model.GetSkillRequest{Namespace: "Production", Name: "weather", Version: "1.0.0"})
	require.NoError(t, err)
	flow.Destroy()

	for _, code := range []model.ErrCode{
		model.ErrCodeServerUserError,
		model.ErrCodeServerException,
		model.ErrCodeSkillNotFound,
		model.ErrCodeInvalidResponse,
		model.ErrCodeSkillPackageInvalid,
		model.ErrCodeUnauthorized,
		model.ErrCodeAPIInvalidArgument,
	} {
		t.Run(model.ErrCodeToString(code), func(t *testing.T) {
			nonNetwork, err := NewFlowWithOptions(&fakeConnector{
				getSkillFunc: func(*skillconnector.GetSkillRequest) (*skillconnector.GetSkillResponse, error) {
					return nil, model.NewSDKError(code, nil, "remote failure")
				},
			}, options)
			require.NoError(t, err)
			defer nonNetwork.Destroy()
			_, err = nonNetwork.GetSkill(&model.GetSkillRequest{
				Namespace: "Production",
				Name:      "weather",
				Version:   "1.0.0",
			})
			require.Error(t, err)
			assert.Equal(t, code, model.GetErrorCodeFromError(err))
		})
	}
}

func TestDownloadSkillPersistsZIPAndFallsBackOnNetworkError(t *testing.T) {
	options := persistenceOptions(t)
	content := []byte{0x50, 0x4b, 0x03, 0x04, 0x01, 0x02}
	first := &fakeConnector{chunks: []*skillconnector.DownloadChunk{{
		Version:     "2.0.0",
		ContentType: model.SkillContentTypeZIP,
		TotalSize:   int64(len(content)),
		Filename:    "weather.zip",
		ZipChunk:    append([]byte(nil), content...),
	}}}
	flow, err := NewFlowWithOptions(first, options)
	require.NoError(t, err)
	remote, err := flow.DownloadSkill(&model.SkillDownloadRequest{
		Namespace: "Production",
		Name:      "weather",
		Format:    model.SkillFormatZIP,
	})
	require.NoError(t, err)
	assert.False(t, remote.FromCache)
	flow.Destroy()

	zipPath := filepath.Join(options.PersistDir, persistDirDownload,
		skillCacheKey("Production", "weather", "2.0.0")+persistExtZIP)
	persisted, err := ioutil.ReadFile(zipPath)
	require.NoError(t, err)
	assert.Equal(t, content, persisted)

	cachedFlow, err := NewFlowWithOptions(&fakeConnector{
		downloadErr: model.NewSDKError(model.ErrCodeNetworkError, errors.New("offline"), "offline"),
	}, options)
	require.NoError(t, err)
	cached, err := cachedFlow.DownloadSkill(&model.SkillDownloadRequest{
		Namespace: "Production",
		Name:      "weather",
		Format:    model.SkillFormatZIP,
	})
	require.NoError(t, err)
	assert.True(t, cached.FromCache)
	assert.Equal(t, content, cached.Content)
	assert.Equal(t, remote.Digest, cached.Digest)
	assert.Equal(t, "weather.zip", cached.Filename)
	cachedFlow.Destroy()
}

func TestTaggedDownloadDoesNotReuseUnfilteredCache(t *testing.T) {
	options := persistenceOptions(t)
	content := []byte("# tagged skill")
	chunks := []*skillconnector.DownloadChunk{{
		Version:     "1.0.0",
		ContentType: model.SkillContentTypeMarkdown,
		Content:     string(content),
	}}

	untagged, err := NewFlowWithOptions(&fakeConnector{chunks: chunks}, options)
	require.NoError(t, err)
	_, err = untagged.DownloadSkill(&model.SkillDownloadRequest{
		Namespace: "Production", Name: "weather", Version: "1.0.0",
		Format: model.SkillFormatMarkdown,
	})
	require.NoError(t, err)
	untagged.Destroy()

	networkErr := model.NewSDKError(model.ErrCodeNetworkError, errors.New("offline"), "offline")
	offline, err := NewFlowWithOptions(&fakeConnector{downloadErr: networkErr}, options)
	require.NoError(t, err)
	_, err = offline.DownloadSkill(&model.SkillDownloadRequest{
		Namespace: "Production", Name: "weather", Version: "1.0.0",
		Tag: "team-only", Format: model.SkillFormatMarkdown,
	})
	require.Equal(t, networkErr, err)
	offline.Destroy()

	tagged, err := NewFlowWithOptions(&fakeConnector{chunks: chunks}, options)
	require.NoError(t, err)
	_, err = tagged.DownloadSkill(&model.SkillDownloadRequest{
		Namespace: "Production", Name: "weather", Version: "1.0.0",
		Tag: "team-only", Format: model.SkillFormatMarkdown,
	})
	require.NoError(t, err)
	tagged.Destroy()

	offlineTagged, err := NewFlowWithOptions(&fakeConnector{downloadErr: networkErr}, options)
	require.NoError(t, err)
	cached, err := offlineTagged.DownloadSkill(&model.SkillDownloadRequest{
		Namespace: "Production", Name: "weather", Version: "1.0.0",
		Tag: "team-only", Format: model.SkillFormatMarkdown,
	})
	require.NoError(t, err)
	assert.True(t, cached.FromCache)
	assert.Equal(t, content, cached.Content)
	offlineTagged.Destroy()

	taggedActive, err := NewFlowWithOptions(&fakeConnector{chunks: chunks}, options)
	require.NoError(t, err)
	_, err = taggedActive.DownloadSkill(&model.SkillDownloadRequest{
		Namespace: "Production", Name: "weather",
		Tag: "team-only", Format: model.SkillFormatMarkdown,
	})
	require.NoError(t, err)
	taggedActive.Destroy()
	_, err = os.Stat(filepath.Join(
		options.PersistDir, persistDirActive, skillPairCacheKey("Production", "weather")))
	require.True(t, os.IsNotExist(err), "tag-filtered download must not replace the active pointer")
}

func TestLegalMarkdownRemainsReadableFromPersistence(t *testing.T) {
	options := persistenceOptions(t)
	content := strings.Repeat("a", int(model.MaxSkillFileSize))

	handler, err := newPersistentHandler(options)
	require.NoError(t, err)
	require.True(t, handler.saveGet("Production", "weather", "1.0.0", &model.SkillResource{
		Namespace: "Production",
		Name:      "weather",
		Status:    "published",
		VersionInfo: &model.SkillVersionInfo{
			ActiveVersion: "1.0.0",
			LatestVersion: "1.0.0",
		},
	}, content, false))
	handler.destroy()
	getPath := filepath.Join(options.PersistDir, persistDirGet,
		skillCacheKey("Production", "weather", "1.0.0")+persistExtYAML)
	getInfo, err := os.Stat(getPath)
	require.NoError(t, err)
	require.Greater(t, getInfo.Size(), int64(model.MaxSkillFileSize))
	cachedGet, err := handler.loadGet("Production", "weather", "1.0.0")
	require.NoError(t, err)
	require.NotNil(t, cachedGet)
	assert.Len(t, cachedGet.Content, int(model.MaxSkillFileSize))
	require.NoError(t, os.Remove(getPath))

	flow, err := NewFlowWithOptions(&fakeConnector{chunks: []*skillconnector.DownloadChunk{{
		Version:     "1.0.0",
		ContentType: model.SkillContentTypeMarkdown,
		Content:     content,
		TotalSize:   int64(len(content)),
	}}}, options)
	require.NoError(t, err)
	remote, err := flow.DownloadSkill(&model.SkillDownloadRequest{
		Namespace: "Production", Name: "weather", Version: "1.0.0",
		Format: model.SkillFormatMarkdown,
	})
	require.NoError(t, err)
	flow.Destroy()

	offline, err := NewFlowWithOptions(&fakeConnector{
		downloadErr: model.NewSDKError(model.ErrCodeNetworkError, errors.New("offline"), "offline"),
	}, options)
	require.NoError(t, err)
	defer offline.Destroy()
	cached, err := offline.DownloadSkill(&model.SkillDownloadRequest{
		Namespace: "Production", Name: "weather", Version: "1.0.0",
		Format: model.SkillFormatMarkdown,
	})
	require.NoError(t, err)
	assert.True(t, cached.FromCache)
	assert.Len(t, cached.Content, int(model.MaxSkillFileSize))
	assert.Equal(t, remote.Digest, cached.Digest)
}

func TestSkillCorruptCacheIsIgnoredAndListNeverFallsBack(t *testing.T) {
	options := persistenceOptions(t)
	getPath := filepath.Join(options.PersistDir, persistDirGet, encodeSkillKey("Production", "weather", "1.0.0")+persistExtYAML)
	require.NoError(t, os.MkdirAll(filepath.Dir(getPath), 0700))
	require.NoError(t, ioutil.WriteFile(getPath, []byte("resourceVersion: ["), 0600))

	networkErr := model.NewSDKError(model.ErrCodeNetworkError, errors.New("offline"), "offline")
	listCalls := 0
	flow, err := NewFlowWithOptions(&fakeConnector{
		getSkillFunc: func(*skillconnector.GetSkillRequest) (*skillconnector.GetSkillResponse, error) {
			return nil, networkErr
		},
		listSkillsFunc: func(*skillconnector.ListSkillsRequest) (*skillconnector.ListSkillsResponse, error) {
			listCalls++
			return nil, networkErr
		},
	}, options)
	require.NoError(t, err)
	defer flow.Destroy()
	_, err = flow.GetSkill(&model.GetSkillRequest{Namespace: "Production", Name: "weather", Version: "1.0.0"})
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeNetworkError, model.GetErrorCodeFromError(err))
	_, err = flow.GetSkillList(&model.GetSkillListRequest{Namespace: "Production"})
	require.Error(t, err)
	assert.Equal(t, 1, listCalls)
}

func TestGetSkillListResultKeepsServerTotal(t *testing.T) {
	flow := NewFlow(&fakeConnector{
		listSkillsFunc: func(*skillconnector.ListSkillsRequest) (*skillconnector.ListSkillsResponse, error) {
			return &skillconnector.ListSkillsResponse{
				Total: 17,
				Resources: []*skillconnector.Resource{
					{
						Namespace: "Production",
						Name:      "published",
						Status:    "published",
						VersionInfo: &skillconnector.VersionInfo{
							ActiveVersion: "1.0.0",
						},
					},
					{Namespace: "Production", Name: "draft", Status: "draft"},
				},
			}, nil
		},
	})
	result, err := flow.GetSkillListResult(&model.GetSkillListRequest{Namespace: "Production", Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, uint32(17), result.Total)
	require.Len(t, result.Resources, 1)
}

func TestPersistentHandlerConcurrentWritesRemainReadable(t *testing.T) {
	options := persistenceOptions(t)
	handler, err := newPersistentHandler(options)
	require.NoError(t, err)
	const versions = 32
	var waitGroup sync.WaitGroup
	for index := 0; index < versions; index++ {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			version := fmt.Sprintf("1.0.%d", index)
			handler.saveGet("Production", "weather", version, &model.SkillResource{
				Namespace: "Production",
				Name:      "weather",
				Status:    "published",
				Content:   version,
			}, version, false)
		}(index)
	}
	waitGroup.Wait()
	handler.destroy()
	for index := 0; index < versions; index++ {
		version := fmt.Sprintf("1.0.%d", index)
		resource, err := handler.loadGet("Production", "weather", version)
		require.NoError(t, err)
		require.NotNil(t, resource)
		assert.Equal(t, version, resource.Content)
		assert.True(t, resource.FromCache)
	}
}

func TestPersistentHandlerRejectsIdentityAndCapacityMismatches(t *testing.T) {
	options := persistenceOptions(t)
	handler, err := newPersistentHandler(options)
	require.NoError(t, err)
	payload, err := yaml.Marshal(&persistedGetResponse{
		ResourceVersion: &persistedResourceVersion{
			Namespace: "Production",
			Name:      "other",
			Version:   "9.9.9",
		},
		Content: "wrong payload",
	})
	require.NoError(t, err)
	require.NoError(t, atomicWriteFile(
		handler.getPath("Production", "weather", "1.0.0"), payload))
	resource, err := handler.loadGet("Production", "weather", "1.0.0")
	require.NoError(t, err)
	assert.Nil(t, resource)

	block := make(chan struct{})
	accepted := handler.enqueue(persistTask{
		name: "blocked",
		size: 0,
		run: func() bool {
			<-block
			return true
		},
	})
	require.True(t, accepted)
	rejected := handler.enqueue(persistTask{
		name: "oversized",
		size: maxPersistBytes + 1,
		run:  func() bool { return true },
	})
	assert.False(t, rejected)
	factoryCalled := false
	rejected = handler.enqueueFactory(
		"oversized-factory", maxPersistBytes+1, func() persistTask {
			factoryCalled = true
			return persistTask{run: func() bool { return true }}
		})
	assert.False(t, rejected)
	assert.False(t, factoryCalled)
	close(block)
	handler.destroy()
}

func TestPersistentHandlerSupportsValidLongIdentities(t *testing.T) {
	identities := []struct {
		label    string
		name     string
		nameUTF8 int
	}{
		{label: "ascii-256", name: strings.Repeat("s", 256), nameUTF8: 256},
		{label: "utf8-255", name: strings.Repeat("技", 85), nameUTF8: 255},
	}
	for _, identity := range identities {
		t.Run(identity.label, func(t *testing.T) {
			require.LessOrEqual(t, identity.nameUTF8, 256)
			require.NoError(t, model.ValidateSkillRefParts("go-sdk-integration", identity.name, "1.0.0"))
			handler, err := newPersistentHandler(persistenceOptions(t))
			require.NoError(t, err)

			resource := &model.SkillResource{
				Namespace: "go-sdk-integration",
				Name:      identity.name,
				Status:    "published",
				Version:   "1.0.0",
			}
			require.True(t, handler.saveGet(
				"go-sdk-integration", identity.name, "1.0.0", resource, "# Skill", true))
			require.True(t, handler.saveDownload(
				"go-sdk-integration", identity.name, "",
				&model.SkillPackage{
					Version:     "1.0.0",
					Format:      model.SkillFormatMarkdown,
					ContentType: model.SkillContentTypeMarkdown,
					Filename:    "SKILL.md",
					Content:     []byte("# Skill"),
				}, true))
			handler.destroy()

			require.LessOrEqual(t, len(filepath.Base(handler.getPath(
				"go-sdk-integration", identity.name, "1.0.0"))), 128)
			cachedGet, err := handler.loadGet("go-sdk-integration", identity.name, "1.0.0")
			require.NoError(t, err)
			require.NotNil(t, cachedGet)
			assert.True(t, cachedGet.FromCache)
			assert.Equal(t, "# Skill", cachedGet.Content)

			cachedDownload, err := handler.loadDownload(
				"go-sdk-integration", identity.name, "1.0.0", model.SkillFormatMarkdown, "")
			require.NoError(t, err)
			require.NotNil(t, cachedDownload)
			assert.True(t, cachedDownload.FromCache)
			assert.Equal(t, []byte("# Skill"), cachedDownload.Content)
			assert.Equal(t, "1.0.0", handler.loadActive("go-sdk-integration", identity.name))
		})
	}
}

func TestPersistentHandlerReadsLegacyCachePath(t *testing.T) {
	handler, err := newPersistentHandler(persistenceOptions(t))
	require.NoError(t, err)
	defer handler.destroy()
	payload, err := yaml.Marshal(&persistedGetResponse{
		ResourceVersion: &persistedResourceVersion{
			Namespace: "Production",
			Name:      "weather",
			Version:   "1.0.0",
		},
		Content: "# legacy",
	})
	require.NoError(t, err)
	require.NoError(t, atomicWriteFile(handler.legacyGetPath("Production", "weather", "1.0.0"), payload))

	cached, err := handler.loadGet("Production", "weather", "1.0.0")
	require.NoError(t, err)
	require.NotNil(t, cached)
	assert.True(t, cached.FromCache)
	assert.Equal(t, "# legacy", cached.Content)
}

func TestPersistentHandlerWaitsForReservedTaskDuringClose(t *testing.T) {
	handler, err := newPersistentHandler(persistenceOptions(t))
	require.NoError(t, err)
	reserved := make(chan struct{})
	release := make(chan struct{})
	queued := make(chan bool, 1)
	ran := make(chan struct{})
	go func() {
		queued <- handler.enqueueFactory("reserved-payload", 1, func() persistTask {
			close(reserved)
			<-release
			return persistTask{run: func() bool {
				close(ran)
				return true
			}}
		})
	}()
	<-reserved

	closed := make(chan struct{})
	go func() {
		handler.destroy()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("destroy returned before the reserved payload was enqueued")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	require.True(t, <-queued, "an accepted reservation must not be discarded")
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("destroy did not finish after the reserved payload completed")
	}
	select {
	case <-ran:
	default:
		t.Fatal("reserved payload was not executed before destroy returned")
	}
}

func TestPersistentHandlerCapacityCountsQueuedReservedAndActive(t *testing.T) {
	handler := &persistentHandler{enabled: true}
	handler.tasks = make([]persistTask, maxPersistTasks-1)
	handler.reservations = 1
	assert.False(t, handler.canQueueLocked(0), "queued plus reserved work reaches the task limit")

	handler.reservations = 0
	assert.True(t, handler.canQueueLocked(0))
	handler.active = 1
	assert.False(t, handler.canQueueLocked(0), "active work must also count toward the task limit")

	handler.active = 0
	handler.tasks = nil
	handler.pendingBytes = maxPersistBytes
	assert.False(t, handler.canQueueLocked(1), "the byte limit remains independent")
	handler.pendingBytes = 0
	assert.False(t, handler.canQueueLocked(-1))
}

func TestPersistentHandlerReleasesReservationAfterFactoryPanic(t *testing.T) {
	handler, err := newPersistentHandler(persistenceOptions(t))
	require.NoError(t, err)
	defer handler.destroy()

	require.Panics(t, func() {
		handler.enqueueFactory("panic-payload", 8, func() persistTask {
			panic("factory failure")
		})
	})
	handler.mu.Lock()
	defer handler.mu.Unlock()
	assert.Zero(t, handler.reservations)
	assert.Zero(t, handler.pendingBytes)
}
