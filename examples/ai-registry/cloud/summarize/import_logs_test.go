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
	"archive/tar"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func createRunArchive(t *testing.T, path, resultJSON string) {
	t.Helper()
	file, err := os.Create(path)
	require.NoError(t, err)
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	keep := ""
	require.NoError(t, tarWriter.WriteHeader(&tar.Header{
		Name: "run/.keep",
		Mode: 0600,
		Size: int64(len(keep)),
	}))
	_, err = tarWriter.Write([]byte(keep))
	require.NoError(t, err)
	if resultJSON != "" {
		require.NoError(t, tarWriter.WriteHeader(&tar.Header{
			Name: "run/result.json",
			Mode: 0600,
			Size: int64(len(resultJSON)),
		}))
		_, err = tarWriter.Write([]byte(resultJSON))
		require.NoError(t, err)
	}
	require.NoError(t, tarWriter.Close())
	require.NoError(t, gzipWriter.Close())
	require.NoError(t, file.Close())
}

func runImportWithoutGo(t *testing.T, arguments ...string) (string, error) {
	t.Helper()
	command := exec.Command("/bin/bash", append([]string{"../import-logs.sh"}, arguments...)...)
	command.Env = []string{"PATH=/usr/bin:/bin"}
	output, err := command.CombinedOutput()
	return string(output), err
}

func TestImportStrictFailsWithoutSummarizer(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "logs.tar.gz")
	createRunArchive(t, archive,
		`{"run_id":"failed","success":false,"results":[{"id":"1.1","status":"FAIL"}]}`)

	output, err := runImportWithoutGo(t,
		"--strict", "--output-dir", filepath.Join(dir, "imported"), archive)
	require.Error(t, err, "strict import must fail when analysis is unavailable:\n%s", output)
}

func TestImportWithoutStrictCanSkipAnalysis(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "logs.tar.gz")
	createRunArchive(t, archive,
		`{"run_id":"failed","success":false,"results":[{"id":"1.1","status":"FAIL"}]}`)

	output, err := runImportWithoutGo(t,
		"--output-dir", filepath.Join(dir, "imported"), archive)
	require.NoError(t, err, "non-strict import should allow import-only mode:\n%s", output)
}

func TestImportStrictFailsWithoutResult(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "logs.tar.gz")
	createRunArchive(t, archive, "")

	output, err := runImportWithoutGo(t,
		"--strict", "--output-dir", filepath.Join(dir, "imported"), archive)
	require.Error(t, err, "strict import must fail when no result.json exists:\n%s", output)
}
