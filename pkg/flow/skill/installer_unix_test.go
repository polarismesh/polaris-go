//go:build !windows
// +build !windows

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

package skill

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSkillCacheRejectsFIFOWithoutWaitingForWriter(t *testing.T) {
	root := t.TempDir()
	nestedFIFO := filepath.Join(root, "data", "pending.pipe")
	require.NoError(t, os.MkdirAll(filepath.Dir(nestedFIFO), 0755))
	require.NoError(t, syscall.Mkfifo(nestedFIFO, 0600))
	require.NoError(t, syscall.Mkfifo(filepath.Join(root, skillManifestName), 0600))

	digestDone := make(chan error, 1)
	go func() {
		_, err := digestTree(root)
		digestDone <- err
	}()
	select {
	case err := <-digestDone:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a regular file")
	case <-time.After(200 * time.Millisecond):
		t.Fatal("digestTree blocked on a FIFO")
	}

	readDone := make(chan error, 1)
	go func() {
		_, err := readFile(filepath.Join(root, skillManifestName))
		readDone <- err
	}()
	select {
	case err := <-readDone:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a regular file")
	case <-time.After(200 * time.Millisecond):
		t.Fatal("readFile blocked on a FIFO")
	}
}

func TestTreeDigestIncludesPermissionBits(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "run.sh")
	require.NoError(t, writeFile(path, []byte("#!/bin/sh\n"), 0644))

	before, err := digestTree(root)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(path, 0755))
	after, err := digestTree(root)
	require.NoError(t, err)
	assert.NotEqual(t, before, after)

	require.NoError(t, os.Chmod(path, 0644))
	restored, err := digestTree(root)
	require.NoError(t, err)
	assert.Equal(t, before, restored)
}
