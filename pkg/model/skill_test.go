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
// Unless required by applicable law or agreed to in writing, software distributed under
// the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
// either express or implied. See the License for the specific language governing permissions and
// limitations under the License.

package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSkillDownloadRequestValidateKeepsEmptyFormat(t *testing.T) {
	request := &SkillDownloadRequest{
		Namespace: "Production",
		Name:      "demo",
		Version:   "1.0.0",
	}

	require.NoError(t, request.Validate())
	assert.Empty(t, request.Format)

	request.Format = "yaml"
	err := request.Validate()
	require.Error(t, err)
	assert.Equal(t, ErrCodeAPIInvalidArgument, GetErrorCodeFromError(err))
}
