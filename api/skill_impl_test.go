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
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/sdk"
)

type skillAPITestEngine struct {
	sdk.Engine

	lastGetReq        *model.GetSkillRequest
	lastListReq       *model.GetSkillListRequest
	lastListResultReq *model.GetSkillListRequest
	lastDownloadReq   *model.SkillDownloadRequest
	lastInstallReq    *model.SkillInstallRequest
	getResult         *model.SkillResource
	listResult        []*model.SkillResource
	listResultValue   *model.SkillListResult
	downloadResult    *model.SkillPackage
	installResult     *model.InstalledSkill
	getErr            error
	listErr           error
	listResultErr     error
	downloadErr       error
	installErr        error
}

func (e *skillAPITestEngine) SyncGetSkill(req *model.GetSkillRequest) (*model.SkillResource, error) {
	e.lastGetReq = req
	return e.getResult, e.getErr
}

func (e *skillAPITestEngine) SyncGetSkillList(req *model.GetSkillListRequest) ([]*model.SkillResource, error) {
	e.lastListReq = req
	return e.listResult, e.listErr
}

func (e *skillAPITestEngine) SyncGetSkillListResult(req *model.GetSkillListRequest) (*model.SkillListResult, error) {
	e.lastListResultReq = req
	return e.listResultValue, e.listResultErr
}

func (e *skillAPITestEngine) SyncDownloadSkill(req *model.SkillDownloadRequest) (*model.SkillPackage, error) {
	e.lastDownloadReq = req
	return e.downloadResult, e.downloadErr
}

func (e *skillAPITestEngine) SyncInstallSkill(req *model.SkillInstallRequest) (*model.InstalledSkill, error) {
	e.lastInstallReq = req
	return e.installResult, e.installErr
}

type skillAPITestContext struct {
	SDKContext
	engine    sdk.Engine
	destroyed bool
}

func (c *skillAPITestContext) Destroy()              { c.destroyed = true }
func (c *skillAPITestContext) IsDestroyed() bool     { return c.destroyed }
func (c *skillAPITestContext) GetEngine() sdk.Engine { return c.engine }

func newSkillAPITestAPI(engine sdk.Engine) (*skillAPI, *skillAPITestContext) {
	context := &skillAPITestContext{engine: engine}
	return NewSkillAPIByContext(context).(*skillAPI), context
}

func requireSDKErrorCode(t *testing.T, err error, expected model.ErrCode) {
	t.Helper()
	require.Error(t, err)
	assert.Equal(t, expected, model.GetErrorCodeFromError(err))
}

func TestSkillAPIRejectsInvalidAndClosedRequests(t *testing.T) {
	engine := &skillAPITestEngine{}
	skill, context := newSkillAPITestAPI(engine)

	requireSDKErrorCode(t, func() error {
		_, err := skill.GetSkill(nil)
		return err
	}(), model.ErrCodeAPIInvalidArgument)
	requireSDKErrorCode(t, func() error {
		_, err := skill.GetSkillList(nil)
		return err
	}(), model.ErrCodeAPIInvalidArgument)
	requireSDKErrorCode(t, func() error {
		_, err := skill.GetSkillListResult(nil)
		return err
	}(), model.ErrCodeAPIInvalidArgument)
	requireSDKErrorCode(t, func() error {
		_, err := skill.DownloadSkill(nil)
		return err
	}(), model.ErrCodeAPIInvalidArgument)
	requireSDKErrorCode(t, func() error {
		_, err := skill.Install(nil)
		return err
	}(), model.ErrCodeAPIInvalidArgument)

	requireSDKErrorCode(t, func() error {
		_, err := skill.GetSkill(&GetSkillRequest{})
		return err
	}(), model.ErrCodeAPIInvalidArgument)
	requireSDKErrorCode(t, func() error {
		_, err := skill.GetSkillList(&GetSkillListRequest{})
		return err
	}(), model.ErrCodeAPIInvalidArgument)
	requireSDKErrorCode(t, func() error {
		_, err := skill.GetSkillListResult(&GetSkillListRequest{})
		return err
	}(), model.ErrCodeAPIInvalidArgument)
	requireSDKErrorCode(t, func() error {
		_, err := skill.DownloadSkill(&SkillDownloadRequest{})
		return err
	}(), model.ErrCodeAPIInvalidArgument)
	requireSDKErrorCode(t, func() error {
		_, err := skill.Install(&SkillInstallRequest{})
		return err
	}(), model.ErrCodeAPIInvalidArgument)

	skill.Close()
	assert.False(t, context.destroyed)
	requireSDKErrorCode(t, func() error {
		_, err := skill.GetSkill(validSkillGetRequest())
		return err
	}(), model.ErrCodeInvalidStateError)
	requireSDKErrorCode(t, func() error {
		_, err := skill.GetSkillList(validSkillListRequest())
		return err
	}(), model.ErrCodeInvalidStateError)
	requireSDKErrorCode(t, func() error {
		_, err := skill.GetSkillListResult(validSkillListRequest())
		return err
	}(), model.ErrCodeInvalidStateError)
	requireSDKErrorCode(t, func() error {
		_, err := skill.DownloadSkill(validSkillDownloadRequest(""))
		return err
	}(), model.ErrCodeInvalidStateError)
	requireSDKErrorCode(t, func() error {
		_, err := skill.Install(validSkillInstallRequest(t))
		return err
	}(), model.ErrCodeInvalidStateError)

	var nilSkill *skillAPI
	assert.Nil(t, nilSkill.SDKContext())
	requireSDKErrorCode(t, func() error {
		_, err := nilSkill.GetSkill(validSkillGetRequest())
		return err
	}(), model.ErrCodeAPIInvalidArgument)
}

func TestSkillAPIDelegatesRequests(t *testing.T) {
	engine := &skillAPITestEngine{
		getResult:       &model.SkillResource{Name: "weather"},
		listResult:      []*model.SkillResource{{Name: "weather"}},
		listResultValue: &model.SkillListResult{Total: 2},
		downloadResult:  &model.SkillPackage{Format: model.SkillFormatMarkdown},
		installResult:   &model.InstalledSkill{Version: "1.0.0"},
	}
	skill, context := newSkillAPITestAPI(engine)

	gotSkill, err := skill.GetSkill(validSkillGetRequest())
	require.NoError(t, err)
	assert.Equal(t, "weather", gotSkill.Name)
	assert.Equal(t, "weather", engine.lastGetReq.Name)

	gotList, err := skill.GetSkillList(validSkillListRequest())
	require.NoError(t, err)
	require.Len(t, gotList, 1)
	assert.Equal(t, "Production", engine.lastListReq.Namespace)

	gotPage, err := skill.GetSkillListResult(validSkillListRequest())
	require.NoError(t, err)
	assert.Equal(t, uint32(2), gotPage.Total)
	assert.Equal(t, "Production", engine.lastListResultReq.Namespace)

	downloadReq := validSkillDownloadRequest("")
	gotDownload, err := skill.DownloadSkill(downloadReq)
	require.NoError(t, err)
	assert.Equal(t, model.SkillFormatMarkdown, gotDownload.Format)
	assert.Equal(t, model.SkillFormatMarkdown, engine.lastDownloadReq.Format)
	assert.Empty(t, downloadReq.Format, "DownloadSkill must not mutate caller input")

	installReq := validSkillInstallRequest(t)
	gotInstall, err := skill.Install(installReq)
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", gotInstall.Version)
	assert.Equal(t, "weather", engine.lastInstallReq.Name)

	assert.Equal(t, context, skill.SDKContext())
	skill.Destroy()
	assert.False(t, context.destroyed, "borrowed context must not be destroyed")
}

func TestSkillAPIListResultErrors(t *testing.T) {
	t.Run("engine error is returned", func(t *testing.T) {
		sentinel := errors.New("list result failed")
		engine := &skillAPITestEngine{listResultErr: sentinel}
		skill, _ := newSkillAPITestAPI(engine)
		_, err := skill.GetSkillListResult(validSkillListRequest())
		require.Error(t, err)
		assert.ErrorIs(t, err, sentinel)
	})

	t.Run("nil result is invalid response", func(t *testing.T) {
		engine := &skillAPITestEngine{}
		skill, _ := newSkillAPITestAPI(engine)
		_, err := skill.GetSkillListResult(validSkillListRequest())
		requireSDKErrorCode(t, err, model.ErrCodeInvalidResponse)
	})
}

func TestSkillAPIDelegationErrors(t *testing.T) {
	sentinel := errors.New("skill operation failed")
	engine := &skillAPITestEngine{
		getErr:      sentinel,
		listErr:     sentinel,
		downloadErr: sentinel,
		installErr:  sentinel,
	}
	skill, _ := newSkillAPITestAPI(engine)

	_, err := skill.GetSkill(validSkillGetRequest())
	assert.ErrorIs(t, err, sentinel)
	_, err = skill.GetSkillList(validSkillListRequest())
	assert.ErrorIs(t, err, sentinel)
	_, err = skill.DownloadSkill(validSkillDownloadRequest(""))
	assert.ErrorIs(t, err, sentinel)
	_, err = skill.Install(validSkillInstallRequest(t))
	assert.ErrorIs(t, err, sentinel)
}

func TestSkillAPIConstructorRejectsMissingFile(t *testing.T) {
	_, err := NewSkillAPIByFile(t.TempDir() + "/missing-polaris.yaml")
	requireSDKErrorCode(t, err, model.ErrCodeAPIInvalidArgument)
}

func validSkillGetRequest() *GetSkillRequest {
	return &GetSkillRequest{GetSkillRequest: model.GetSkillRequest{
		Namespace: "Production", Name: "weather",
	}}
}

func validSkillListRequest() *GetSkillListRequest {
	return &GetSkillListRequest{GetSkillListRequest: model.GetSkillListRequest{
		Namespace: "Production", Limit: 10,
	}}
}

func validSkillDownloadRequest(format string) *SkillDownloadRequest {
	return &SkillDownloadRequest{SkillDownloadRequest: model.SkillDownloadRequest{
		Namespace: "Production", Name: "weather", Format: format,
	}}
}

func validSkillInstallRequest(t *testing.T) *SkillInstallRequest {
	t.Helper()
	return &SkillInstallRequest{SkillInstallRequest: model.SkillInstallRequest{
		Namespace: "Production", Name: "weather", CacheDir: t.TempDir(),
	}}
}
