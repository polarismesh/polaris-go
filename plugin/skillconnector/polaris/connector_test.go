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
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	skillmanage "github.com/polarismesh/specification/source/go/api/v1/skill_manage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/network"
	"github.com/polarismesh/polaris-go/pkg/plugin/skillconnector"
	"github.com/polarismesh/polaris-go/pkg/sdk"
	"github.com/polarismesh/polaris-go/test/mock"
)

func TestConnectorSkillOperations(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("local gRPC listener is unavailable in this environment: %v", err)
	}
	server := grpc.NewServer()
	skillServer := mock.NewSkillServer()
	zipContent := bytes.Repeat([]byte{0x5a}, 1024*1024+17)
	skillServer.AddSkill(
		&skillmanage.SkillResource{
			Namespace: "Production",
			Name:      "demo",
			Status:    "published",
			VersionInfo: &skillmanage.VersionInfo{
				ActiveVersion: "1.0.0",
				LatestVersion: "1.0.0",
				TotalVersions: 1,
			},
		},
		&skillmanage.SkillResourceVersion{
			Namespace: "Production",
			Name:      "demo",
			Version:   "1.0.0",
			Status:    "published",
			Storage: &skillmanage.StorageInfo{
				Provider:      "polaris_config",
				Files:         []string{"SKILL.md", "scripts/run.sh"},
				Scope:         "namespace",
				ContentDigest: "sha256:demo",
			},
		},
		zipContent,
	)
	skillmanage.RegisterPolarisSkillServer(server, skillServer)
	go func() {
		_ = server.Serve(listener)
	}()
	defer server.Stop()

	cfg := config.NewDefaultConfiguration([]string{"127.0.0.1:8091"})
	cfg.GetSkill().SetEnable(true)
	cfg.GetSkill().SetAddresses([]string{listener.Addr().String()})
	installNoopLoggers()
	valueCtx := sdk.NewValueContext()
	manager, err := network.NewSkillConnectionManager(cfg, valueCtx)
	require.NoError(t, err)
	connector := &Connector{
		cfg:             &networkConfig{MaxCallRecvMsgSize: defaultMaxCallRecvMsgSize},
		connManager:     manager,
		requestTimeout:  2 * time.Second,
		downloadTimeout: 2 * time.Second,
		logCtx:          valueCtx.GetContextLogger(),
	}
	manager.SetConnCreator(connector)
	defer connector.Destroy()

	resource, err := connector.GetSkill(&skillconnector.GetSkillRequest{
		Namespace: "Production",
		Name:      "demo",
	})
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", resource.Version.Version)
	assert.Equal(t, string(zipContent), resource.Content)
	require.NotNil(t, resource.Version.Storage)
	assert.Equal(t, "polaris_config", resource.Version.Storage.Provider)
	assert.Equal(t, []string{"SKILL.md", "scripts/run.sh"}, resource.Version.Storage.Files)
	assert.Equal(t, "sha256:demo", resource.Version.Storage.ContentDigest)

	list, err := connector.GetSkillList(&skillconnector.ListSkillsRequest{Namespace: "Production"})
	require.NoError(t, err)
	assert.Equal(t, uint32(1), list.Total)

	var downloaded []byte
	err = connector.DownloadSkill(&skillconnector.DownloadRequest{
		Namespace: "Production",
		Name:      "demo",
		Version:   "1.0.0",
		Format:    "zip",
	}, func(chunk *skillconnector.DownloadChunk) error {
		downloaded = append(downloaded, chunk.ZipChunk...)
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, zipContent, downloaded)
}

type permissionDeniedSkillServer struct {
	skillmanage.UnimplementedPolarisSkillServer
}

func (*permissionDeniedSkillServer) GetSkill(
	context.Context, *skillmanage.GetSkillRequest,
) (*skillmanage.GetSkillResponse, error) {
	return nil, status.Error(codes.PermissionDenied, "skill token denied")
}

func (*permissionDeniedSkillServer) DownloadSkill(
	_ *skillmanage.DownloadSkillRequest,
	stream skillmanage.PolarisSkill_DownloadSkillServer,
) error {
	if err := stream.Send(&skillmanage.DownloadSkillResponse{
		Code:        uint32(apimodel.Code_ExecuteSuccess),
		Info:        "success",
		Version:     "1.0.0",
		ContentType: "text/markdown",
		Content:     "cached",
	}); err != nil {
		return err
	}
	return status.Error(codes.PermissionDenied, "skill token denied")
}

func TestConnectorPermissionDeniedIsNotNetworkError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("local gRPC listener is unavailable in this environment: %v", err)
	}
	server := grpc.NewServer()
	skillmanage.RegisterPolarisSkillServer(server, &permissionDeniedSkillServer{})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	cfg := config.NewDefaultConfiguration([]string{"127.0.0.1:8091"})
	cfg.GetSkill().SetEnable(true)
	cfg.GetSkill().SetAddresses([]string{listener.Addr().String()})
	installNoopLoggers()
	valueCtx := sdk.NewValueContext()
	manager, err := network.NewSkillConnectionManager(cfg, valueCtx)
	require.NoError(t, err)
	connector := &Connector{
		cfg:             &networkConfig{MaxCallRecvMsgSize: defaultMaxCallRecvMsgSize},
		connManager:     manager,
		requestTimeout:  2 * time.Second,
		downloadTimeout: 2 * time.Second,
		logCtx:          valueCtx.GetContextLogger(),
	}
	manager.SetConnCreator(connector)
	defer connector.Destroy()

	_, err = connector.GetSkill(&skillconnector.GetSkillRequest{
		Namespace: "Production", Name: "demo",
	})
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeUnauthorized, model.GetErrorCodeFromError(err))

	err = connector.DownloadSkill(&skillconnector.DownloadRequest{
		Namespace: "Production", Name: "demo", Version: "1.0.0", Format: "markdown",
	}, func(*skillconnector.DownloadChunk) error { return nil })
	require.Error(t, err)
	assert.Equal(t, model.ErrCodeUnauthorized, model.GetErrorCodeFromError(err))
	assert.NotEqual(t, model.ErrCodeNetworkError, model.GetErrorCodeFromError(err))
}

func TestDownloadRequestTimeoutUsesArchiveBudgetForZIP(t *testing.T) {
	connector := &Connector{
		requestTimeout:  5 * time.Second,
		downloadTimeout: 30 * time.Second,
	}
	assert.Equal(t, 30*time.Second, connector.downloadRequestTimeout("zip"))
	assert.Equal(t, 30*time.Second, connector.downloadRequestTimeout("ZIP"))
	assert.Equal(t, 5*time.Second, connector.downloadRequestTimeout("markdown"))

	connector.downloadTimeout = 0
	assert.Equal(t, 5*time.Second, connector.downloadRequestTimeout("zip"))
}

type noopLogger struct{}

func (noopLogger) Tracef(string, ...interface{}) {}
func (noopLogger) Debugf(string, ...interface{}) {}
func (noopLogger) Infof(string, ...interface{})  {}
func (noopLogger) Warnf(string, ...interface{})  {}
func (noopLogger) Errorf(string, ...interface{}) {}
func (noopLogger) Fatalf(string, ...interface{}) {}
func (noopLogger) IsLevelEnabled(int) bool       { return false }
func (noopLogger) SetLogLevel(int) error         { return nil }

func installNoopLoggers() {
	logger := noopLogger{}
	log.SetBaseLogger(logger)
	log.SetStatLogger(logger)
	log.SetStatReportLogger(logger)
	log.SetDetectLogger(logger)
	log.SetCacheLogger(logger)
	log.SetNetworkLogger(logger)
	log.SetEventLogger(logger)
	log.SetLosslessLogger(logger)
	log.SetRouteLogger(logger)
	log.SetAuthLogger(logger)
	log.SetRateLimitLogger(logger)
	log.SetCircuitBreakerLogger(logger)
	log.SetAuditLogger(logger)
}
