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

package main

import (
	"io/ioutil"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummarizeRun(t *testing.T) {
	runDir := t.TempDir()
	writeTestFile(t, filepath.Join(runDir, "result.json"), `{
  "run_id": "run-1",
  "suite": "all",
  "namespace": "go-sdk-integration",
  "success": false,
  "error": "one case failed",
  "cleanup_requested": true,
  "cleanup_success": true,
  "results": [
    {"id":"1.1","name":"pass","status":"PASS","milliseconds":10},
    {"id":"1.2","name":"fail","status":"FAIL","milliseconds":20,"error":"boom | bad"}
  ]
}`)
	writeTestFile(t, filepath.Join(runDir, "state.json"), `{
  "run_id":"run-1",
  "namespace":"go-sdk-integration",
  "services":["a","b"],
  "instances":[{"id":"i-1","service":"a"}],
  "processes":[{"pid":10,"service":"a"}],
  "skills":["skill-a"],
  "config_groups":["group-a"],
  "cleaned":true
}`)
	writeTestFile(t, filepath.Join(runDir, "run.env"), "SUITE=all\nNAMESPACE=go-sdk-integration\n")
	writeTestFile(t, filepath.Join(runDir, "package.env"), "GIT_REVISION=abc-dirty\n")

	summary, markdown, err := summarize(runDir)
	require.NoError(t, err)
	assert.Equal(t, "run-1", summary.RunID)
	assert.Equal(t, 1, summary.Passed)
	assert.Equal(t, 1, summary.Failed)
	assert.Equal(t, 2, summary.Services)
	assert.True(t, summary.StateCleaned)
	assert.Contains(t, markdown, "1 passed / 1 failed / 2 total")
	assert.Contains(t, markdown, `boom \| bad`)
	assert.Contains(t, markdown, "abc-dirty")
}

func TestSummarizeMissingResult(t *testing.T) {
	summary, markdown, err := summarize(t.TempDir())
	require.NoError(t, err)
	assert.False(t, summary.Success)
	assert.Contains(t, summary.Error, "result.json is missing")
	assert.Contains(t, markdown, "AI Registry Cloud E2E Result")
}

func TestSummarizeMalformedResult(t *testing.T) {
	runDir := t.TempDir()
	writeTestFile(t, filepath.Join(runDir, "result.json"), `{"success":`)
	writeTestFile(t, filepath.Join(runDir, "state.json"), `{
  "run_id":"run-2",
  "namespace":"go-sdk-integration",
  "cleaned":false
}`)

	summary, markdown, err := summarize(runDir)
	require.NoError(t, err)
	assert.False(t, summary.Success)
	require.Len(t, summary.AnalysisErrors, 1)
	assert.Contains(t, summary.AnalysisErrors[0], "decode")
	assert.Contains(t, markdown, "## Analysis Errors")
	assert.Contains(t, markdown, "run-2")
}

func TestSummarizeSelectedMetadata(t *testing.T) {
	runDir := t.TempDir()
	writeTestFile(t, filepath.Join(runDir, "result.json"), `{
  "run_id":"run-3",
  "suite":"mcp",
  "namespace":"go-sdk-integration",
  "success":true,
  "results":[{"id":"6.1","name":"mcp","status":"PASS","milliseconds":1}]
}`)
	writeTestFile(t, filepath.Join(runDir, "run.env"), "SERVER=example\nPOLARIS_TOKEN=must-not-leak\n")
	writeTestFile(t, filepath.Join(runDir, "package.env"),
		"TARGET_OS=linux\nTARGET_ARCH=arm64\nGIT_REVISION=abc\nPOLARIS_TOKEN=must-not-leak\n")

	summary, markdown, err := summarize(runDir)
	require.NoError(t, err)
	assert.Equal(t, "example", summary.RunEnv["SERVER"])
	assert.NotContains(t, summary.RunEnv, "POLARIS_TOKEN")
	assert.NotContains(t, summary.PackageEnv, "POLARIS_TOKEN")
	assert.Contains(t, markdown, "`linux/arm64`")
}

func TestSummarizeInconsistentSuccess(t *testing.T) {
	runDir := t.TempDir()
	writeTestFile(t, filepath.Join(runDir, "result.json"), `{
  "run_id":"run-4",
  "suite":"all",
  "namespace":"go-sdk-integration",
  "success":true,
  "results":[
    {"id":"1.1","name":"pass","status":"PASS","milliseconds":1},
    {"id":"1.2","name":"fail","status":"FAIL","milliseconds":1,"error":"boom"}
  ]
}`)

	summary, _, err := summarize(runDir)
	require.NoError(t, err)
	assert.False(t, summary.Success)
	assert.Contains(t, summary.AnalysisErrors[0], "success=true")
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, ioutil.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0600))
}
