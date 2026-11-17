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
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/polarismesh/polaris-go/pkg/model"
)

const (
	skillManifestName         = ".polaris-skill.json"
	selectionFileSuffix       = ".current.json"
	selectionFileVersion      = 1
	treeDigestVersion         = 3
	installGeneration         = "generation-"
	legacyGenerationSelection = "."
	maxSkillArchiveEntries    = 10000
)

type installManifest struct {
	Namespace         string    `json:"namespace"`
	Name              string    `json:"name"`
	Version           string    `json:"version"`
	Digest            string    `json:"digest"`
	TreeDigest        string    `json:"tree_digest"`
	TreeDigestVersion int       `json:"tree_digest_version"`
	Downloaded        time.Time `json:"download_time"`
	PackageSize       int64     `json:"package_size"`
}

type installSelection struct {
	Version    int    `json:"version"`
	Generation string `json:"generation"`
}

// Installer 负责 Skill 包安全解压和原子安装。
type Installer struct {
	flow  *Flow
	locks sync.Map
}

// Install 安装 published Skill。
func (i *Installer) Install(req *model.SkillInstallRequest) (*model.InstalledSkill, error) {
	if req == nil {
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil,
			"SkillInstallRequest can not be nil")
	}
	installReq := *req
	if err := installReq.Validate(); err != nil {
		return nil, err
	}
	if installReq.Version == "" {
		resource, err := i.flow.GetSkill(&model.GetSkillRequest{
			Namespace: installReq.Namespace,
			Name:      installReq.Name,
		})
		if err != nil {
			return nil, err
		}
		installReq.Version = resource.VersionInfo.ActiveVersion
		if installReq.Version == "" {
			return nil, model.NewSDKError(model.ErrCodeSkillNotFound, nil,
				"skill %s/%s has no active version", installReq.Namespace, installReq.Name)
		}
		if err = installReq.Validate(); err != nil {
			return nil, err
		}
	}
	cacheDir, err := filepath.Abs(installReq.CacheDir)
	if err != nil {
		return nil, model.NewSDKError(model.ErrCodeDiskError, err,
			"fail to resolve skill cache dir %s", installReq.CacheDir)
	}
	if err = model.EnsureAndVerifyDir(cacheDir); err != nil {
		return nil, model.NewSDKError(model.ErrCodeDiskError, err,
			"fail to create skill cache dir %s", cacheDir)
	}
	cacheRoot, err := filepath.EvalSymlinks(cacheDir)
	if err != nil {
		return nil, model.NewSDKError(model.ErrCodeDiskError, err,
			"fail to resolve physical skill cache dir %s", cacheDir)
	}
	namespaceDir := filepath.Join(cacheRoot, installReq.Namespace)
	installParent := namespaceDir
	cacheIdentity := skillCacheIdentity(installReq.Namespace, installReq.Name, installReq.Version)
	target := filepath.Join(installParent, cacheIdentity)
	key := cacheIdentity
	lockValue, _ := i.locks.LoadOrStore(key, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	if err = ensureSkillDir(cacheRoot, namespaceDir); err != nil {
		return nil, err
	}
	if err = ensureSkillDir(cacheRoot, target); err != nil {
		return nil, err
	}

	if !installReq.Force {
		installed, cacheErr := findInstalledSkill(target, &installReq)
		if cacheErr != nil {
			return nil, cacheErr
		}
		if installed != nil {
			return installed, nil
		}
	}

	pkg, err := i.flow.DownloadSkill(&model.SkillDownloadRequest{
		Namespace: installReq.Namespace,
		Name:      installReq.Name,
		Version:   installReq.Version,
		Format:    model.SkillFormatZIP,
	})
	if err != nil {
		return nil, err
	}
	if pkg.Version != installReq.Version {
		return nil, model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
			"downloaded skill version %q does not match requested version %q",
			pkg.Version, installReq.Version)
	}
	tempDir := filepath.Join(target, ".install-"+uuid.New().String())
	if err = os.Mkdir(tempDir, 0700); err != nil {
		return nil, model.NewSDKError(model.ErrCodeDiskError, err, "fail to create temporary skill dir")
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()
	if err = extractZIP(pkg.Content, tempDir); err != nil {
		return nil, err
	}
	if _, err = findSkillEntry(tempDir); err != nil {
		return nil, err
	}
	treeDigest, err := digestTree(tempDir)
	if err != nil {
		return nil, err
	}
	manifest := installManifest{
		Namespace:         installReq.Namespace,
		Name:              installReq.Name,
		Version:           installReq.Version,
		Digest:            pkg.Digest,
		TreeDigest:        treeDigest,
		TreeDigestVersion: treeDigestVersion,
		Downloaded:        time.Now().UTC(),
		PackageSize:       int64(len(pkg.Content)),
	}
	if err = writeManifest(tempDir, manifest); err != nil {
		return nil, err
	}
	installed, err := publishGeneration(target, pkg.Digest, tempDir, &installReq)
	if err != nil {
		return nil, model.NewSDKError(model.ErrCodeDiskError, err,
			"fail to publish skill generation to %s", target)
	}
	return installed, nil
}

func ensureSkillDir(root, dir string) error {
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		if err = os.Mkdir(dir, 0744); err != nil && !os.IsExist(err) {
			return model.NewSDKError(model.ErrCodeDiskError, err,
				"fail to create skill cache dir %s", dir)
		}
		if info, err = os.Lstat(dir); err != nil {
			return model.NewSDKError(model.ErrCodeDiskError, err,
				"fail to inspect created skill cache dir %s", dir)
		}
	} else if err != nil {
		return model.NewSDKError(model.ErrCodeDiskError, err,
			"fail to inspect skill cache dir %s", dir)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return model.NewSDKError(model.ErrCodeDiskError, nil,
			"skill cache dir %s must not be a symlink", dir)
	}
	if !info.IsDir() {
		return model.NewSDKError(model.ErrCodeDiskError, nil,
			"skill cache path %s is not a directory", dir)
	}
	if !pathWithin(root, dir) {
		return model.NewSDKError(model.ErrCodeDiskError, nil,
			"skill cache dir %s escapes physical cache root %s", dir, root)
	}
	return nil
}

func verifyInstall(target string, req *model.SkillInstallRequest) (*model.InstalledSkill, bool, error) {
	data, err := readFile(filepath.Join(target, skillManifestName))
	if err != nil {
		return nil, false, err
	}
	var manifest installManifest
	if err = json.Unmarshal(data, &manifest); err != nil {
		return nil, false, err
	}
	if manifest.Namespace != req.Namespace || manifest.Name != req.Name || manifest.Version != req.Version {
		return nil, false, fmt.Errorf("skill cache manifest identity mismatch")
	}
	if manifest.TreeDigestVersion != treeDigestVersion {
		return nil, false, fmt.Errorf("skill cache tree digest version mismatch")
	}
	treeDigest, err := digestTree(target)
	if err != nil {
		return nil, false, err
	}
	if treeDigest != manifest.TreeDigest {
		return nil, false, fmt.Errorf("skill cache tree digest mismatch")
	}
	entryFile, err := findSkillEntry(target)
	if err != nil {
		return nil, false, err
	}
	return &model.InstalledSkill{
		Namespace:   manifest.Namespace,
		Name:        manifest.Name,
		Version:     manifest.Version,
		Dir:         target,
		Digest:      manifest.Digest,
		TreeDigest:  manifest.TreeDigest,
		Downloaded:  manifest.Downloaded,
		CacheHit:    true,
		EntryFile:   entryFile,
		PackageSize: manifest.PackageSize,
	}, true, nil
}

func findInstalledSkill(bucket string, req *model.SkillInstallRequest) (*model.InstalledSkill, error) {
	selection, found, err := readCurrentSelection(bucket)
	if err != nil {
		return nil, err
	}
	if found {
		target, targetErr := selectedGenerationPath(bucket, selection.Generation)
		if targetErr != nil {
			return nil, targetErr
		}
		installed, hit, verifyErr := verifyInstall(target, req)
		if verifyErr == nil && hit {
			return installed, nil
		}
		return nil, nil
	}

	entries, err := ioutil.ReadDir(bucket)
	if err != nil {
		return nil, model.NewSDKError(model.ErrCodeDiskError, err,
			"fail to scan skill cache index %s", bucket)
	}
	var latest *model.InstalledSkill
	hasGenerationIndex := false
	hasInvalidGeneration := false
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), installGeneration) {
			continue
		}
		hasGenerationIndex = true
		if !entry.IsDir() {
			hasInvalidGeneration = true
			continue
		}
		installed, hit, verifyErr := verifyInstall(filepath.Join(bucket, entry.Name()), req)
		if verifyErr != nil || !hit {
			hasInvalidGeneration = true
			continue
		}
		if latest == nil || installed.Downloaded.After(latest.Downloaded) {
			latest = installed
		}
	}
	if hasInvalidGeneration {
		// Without a selection file the latest generation is unknowable. Repairing is safer
		// than presenting an older valid generation as the current cache hit.
		return nil, nil
	}
	if latest != nil {
		if err = writeCurrentSelection(bucket, filepath.Base(latest.Dir)); err != nil {
			return nil, err
		}
		return latest, nil
	}
	// Keep caches created before the generation layout readable.
	if !hasGenerationIndex {
		installed, hit, verifyErr := verifyInstall(bucket, req)
		if verifyErr != nil || !hit {
			return nil, nil
		}
		if err = writeCurrentSelection(bucket, legacyGenerationSelection); err != nil {
			return nil, err
		}
		return installed, nil
	}
	return nil, nil
}

func selectionFilePath(bucket string) string {
	return filepath.Join(filepath.Dir(bucket), filepath.Base(bucket)+selectionFileSuffix)
}

func readCurrentSelection(bucket string) (*installSelection, bool, error) {
	path := selectionFilePath(bucket)
	data, err := readFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, model.NewSDKError(model.ErrCodeDiskError, err,
			"fail to read current skill selection %s", path)
	}
	var selection installSelection
	if err = json.Unmarshal(data, &selection); err != nil {
		return nil, false, model.NewSDKError(model.ErrCodeDiskError, err,
			"fail to decode current skill selection %s", path)
	}
	if selection.Version != selectionFileVersion {
		return nil, false, model.NewSDKError(model.ErrCodeDiskError, nil,
			"unsupported current skill selection version %d in %s", selection.Version, path)
	}
	if err = validateSelectionGeneration(selection.Generation); err != nil {
		return nil, false, err
	}
	return &selection, true, nil
}

func writeCurrentSelection(bucket, generation string) error {
	if err := validateSelectionGeneration(generation); err != nil {
		return err
	}
	data, err := json.Marshal(installSelection{
		Version:    selectionFileVersion,
		Generation: generation,
	})
	if err != nil {
		return model.NewSDKError(model.ErrCodeDiskError, err,
			"fail to encode current skill selection")
	}
	path := selectionFilePath(bucket)
	tempPath := filepath.Join(filepath.Dir(path),
		"."+filepath.Base(path)+"-"+uuid.New().String()+".tmp")
	temp, err := os.OpenFile(tempPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return model.NewSDKError(model.ErrCodeDiskError, err,
			"fail to create current skill selection in %s", filepath.Dir(path))
	}
	defer func() {
		_ = os.Remove(tempPath)
	}()
	written, writeErr := temp.Write(data)
	if writeErr == nil && written != len(data) {
		writeErr = fmt.Errorf("short write: wrote %d of %d bytes", written, len(data))
	}
	if writeErr != nil {
		_ = temp.Close()
		return model.NewSDKError(model.ErrCodeDiskError, writeErr,
			"fail to write current skill selection %s", tempPath)
	}
	if err = temp.Sync(); err != nil {
		_ = temp.Close()
		return model.NewSDKError(model.ErrCodeDiskError, err,
			"fail to sync current skill selection %s", tempPath)
	}
	if err = temp.Close(); err != nil {
		return model.NewSDKError(model.ErrCodeDiskError, err,
			"fail to close current skill selection %s", tempPath)
	}
	if err = os.Rename(tempPath, path); err != nil {
		return model.NewSDKError(model.ErrCodeDiskError, err,
			"fail to publish current skill selection %s", path)
	}
	return nil
}

func validateSelectionGeneration(generation string) error {
	if generation == legacyGenerationSelection {
		return nil
	}
	if generation == "" || strings.ContainsAny(generation, `/\`) ||
		!strings.HasPrefix(generation, installGeneration) {
		return model.NewSDKError(model.ErrCodeDiskError, nil,
			"invalid current skill generation %q", generation)
	}
	return nil
}

func selectedGenerationPath(bucket, generation string) (string, error) {
	if err := validateSelectionGeneration(generation); err != nil {
		return "", err
	}
	target := filepath.Join(bucket, generation)
	if !pathWithin(bucket, target) {
		return "", model.NewSDKError(model.ErrCodeDiskError, nil,
			"current skill generation %q escapes cache bucket %s", generation, bucket)
	}
	return target, nil
}

func publishGeneration(
	bucket, digest, temp string, req *model.SkillInstallRequest,
) (*model.InstalledSkill, error) {
	base := filepath.Join(bucket, installGeneration+digest)
	for attempts := 0; attempts < 2; attempts++ {
		target := base
		if attempts > 0 {
			target += "-" + uuid.New().String()
		}
		info, err := os.Lstat(target)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				continue
			}
			installed, reused, reuseErr := reusePublishedGeneration(bucket, target, req)
			if reuseErr != nil {
				return nil, reuseErr
			}
			if reused {
				return installed, nil
			}
			continue
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
		if err = os.Rename(temp, target); err != nil {
			installed, reused, reuseErr := reusePublishedGeneration(bucket, target, req)
			if reuseErr != nil {
				return nil, reuseErr
			}
			if reused {
				return installed, nil
			}
			if os.IsExist(err) {
				continue
			}
			return nil, err
		}
		installed, hit, err := verifyInstall(target, req)
		if err != nil || !hit {
			return nil, firstError(err, fmt.Errorf("published skill generation failed verification"))
		}
		installed.CacheHit = false
		if err = writeCurrentSelection(bucket, filepath.Base(installed.Dir)); err != nil {
			return nil, err
		}
		return installed, nil
	}
	return nil, fmt.Errorf("no available immutable generation path for digest %s", digest)
}

func reusePublishedGeneration(
	bucket, target string, req *model.SkillInstallRequest,
) (*model.InstalledSkill, bool, error) {
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, false, nil
	}
	installed, hit, err := verifyInstall(target, req)
	if err != nil || !hit {
		return nil, false, nil
	}
	installed.CacheHit = false
	if err = writeCurrentSelection(bucket, filepath.Base(installed.Dir)); err != nil {
		return nil, false, err
	}
	return installed, true, nil
}

func extractZIP(content []byte, target string) error {
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return model.NewSDKError(model.ErrCodeSkillPackageInvalid, err, "invalid skill ZIP package")
	}
	if len(reader.File) > maxSkillArchiveEntries {
		return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
			"skill ZIP has %d entries, maximum is %d", len(reader.File), maxSkillArchiveEntries)
	}
	seen := make(map[string]struct{}, len(reader.File))
	var totalDeclared, totalExtracted int64
	for _, file := range reader.File {
		name := strings.ReplaceAll(file.Name, "\\", "/")
		if name == "" || strings.ContainsRune(name, '\x00') {
			return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil, "skill ZIP contains an empty path")
		}
		clean := filepath.ToSlash(filepath.Clean(name))
		if filepath.IsAbs(name) || strings.HasPrefix(clean, "../") || clean == ".." ||
			(filepath.VolumeName(name) != "") || strings.Contains(strings.Split(name, "/")[0], ":") {
			return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
				"skill ZIP path %q is unsafe", file.Name)
		}
		if _, ok := seen[clean]; ok {
			return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
				"skill ZIP path %q is duplicated", file.Name)
		}
		seen[clean] = struct{}{}
		if strings.EqualFold(clean, skillManifestName) {
			return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
				"skill ZIP must not contain %s", skillManifestName)
		}
		mode := file.Mode()
		if mode&os.ModeSymlink != 0 {
			return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
				"skill ZIP symlink %q is not allowed", file.Name)
		}
		output := filepath.Join(target, filepath.FromSlash(clean))
		if !pathWithin(target, output) {
			return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
				"skill ZIP path %q escapes install directory", file.Name)
		}
		if file.FileInfo().IsDir() {
			if err = os.MkdirAll(output, 0755); err != nil {
				return model.NewSDKError(model.ErrCodeDiskError, err,
					"fail to create skill dir %s", output)
			}
			continue
		}
		if !mode.IsRegular() && mode.Type() != 0 {
			return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
				"skill ZIP entry %q is not a regular file", file.Name)
		}
		if file.UncompressedSize64 > uint64(model.MaxSkillFileSize) {
			return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
				"skill ZIP file %q size %d exceeds maximum %d",
				file.Name, file.UncompressedSize64, model.MaxSkillFileSize)
		}
		if file.UncompressedSize64 > uint64(model.MaxSkillPackageSize-totalDeclared) {
			return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
				"skill ZIP uncompressed size exceeds maximum %d", model.MaxSkillPackageSize)
		}
		totalDeclared += int64(file.UncompressedSize64)
		if err = os.MkdirAll(filepath.Dir(output), 0755); err != nil {
			return model.NewSDKError(model.ErrCodeDiskError, err,
				"fail to create skill file parent dir")
		}
		permissions := os.FileMode(0644)
		if mode&0111 != 0 {
			permissions = 0755
		}
		source, openErr := file.Open()
		if openErr != nil {
			return model.NewSDKError(model.ErrCodeSkillPackageInvalid, openErr,
				"fail to open skill ZIP entry %q", file.Name)
		}
		destination, createErr := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, permissions)
		if createErr != nil {
			_ = source.Close()
			return model.NewSDKError(model.ErrCodeDiskError, createErr,
				"fail to create skill file %s", output)
		}
		remaining := model.MaxSkillPackageSize - totalExtracted
		fileLimit := model.MaxSkillFileSize
		if remaining < fileLimit {
			fileLimit = remaining
		}
		written, copyErr := io.Copy(destination, io.LimitReader(source, fileLimit+1))
		sourceErr := source.Close()
		destinationErr := destination.Close()
		if copyErr != nil {
			return model.NewSDKError(model.ErrCodeDiskError, copyErr,
				"fail to extract skill file %s", output)
		}
		if sourceErr != nil || destinationErr != nil {
			return model.NewSDKError(model.ErrCodeDiskError, firstError(sourceErr, destinationErr),
				"fail to close skill file %s", output)
		}
		if written > fileLimit || written > model.MaxSkillFileSize {
			return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
				"skill ZIP file %q extracted size exceeds package limits", file.Name)
		}
		if written != int64(file.UncompressedSize64) {
			return model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
				"skill ZIP file %q extracted size %d, expected %d",
				file.Name, written, file.UncompressedSize64)
		}
		totalExtracted += written
	}
	return nil
}

func findSkillEntry(root string) (string, error) {
	var entries []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		if info.Name() == model.SkillFileName {
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			entries = append(entries, filepath.ToSlash(relative))
		}
		return nil
	})
	if err != nil {
		return "", model.NewSDKError(model.ErrCodeDiskError, err, "fail to scan skill package")
	}
	if len(entries) == 0 {
		return "", model.NewSDKError(model.ErrCodeSkillPackageInvalid, nil,
			"skill package does not contain %s", model.SkillFileName)
	}
	sort.Strings(entries)
	return entries[0], nil
}

func digestTree(root string) (string, error) {
	type digestFile struct {
		path string
		mode os.FileMode
	}
	var files []digestFile
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if relative == skillManifestName {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("skill cache file %s is not a regular file", path)
		}
		files = append(files, digestFile{path: path, mode: info.Mode().Perm()})
		return nil
	})
	if err != nil {
		return "", model.NewSDKError(model.ErrCodeDiskError, err, "fail to scan skill cache tree")
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].path < files[j].path
	})
	hash := sha256.New()
	_, _ = io.WriteString(hash, "polaris-skill-tree-v3\x00")
	for _, file := range files {
		relative, relErr := filepath.Rel(root, file.path)
		if relErr != nil {
			return "", model.NewSDKError(model.ErrCodeDiskError, relErr, "fail to resolve skill file path")
		}
		data, readErr := readFile(file.path)
		if readErr != nil {
			return "", model.NewSDKError(model.ErrCodeDiskError, readErr,
				"fail to read skill file %s", file.path)
		}
		relativeBytes := []byte(filepath.ToSlash(relative))
		writeDigestLength(hash, uint64(len(relativeBytes)))
		_, _ = hash.Write(relativeBytes)
		writeDigestLength(hash, uint64(file.mode))
		writeDigestLength(hash, uint64(len(data)))
		contentDigest := sha256.Sum256(data)
		_, _ = hash.Write(contentDigest[:])
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func skillCacheIdentity(namespace, name, version string) string {
	hash := sha256.New()
	_, _ = io.WriteString(hash, "polaris-skill-cache-v1\x00")
	for _, part := range [][]byte{[]byte(namespace), []byte(name), []byte(version)} {
		writeDigestLength(hash, uint64(len(part)))
		_, _ = hash.Write(part)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func writeDigestLength(hash io.Writer, length uint64) {
	var buffer [8]byte
	binary.BigEndian.PutUint64(buffer[:], length)
	_, _ = hash.Write(buffer[:])
}

func writeManifest(root string, manifest installManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return model.NewSDKError(model.ErrCodeDiskError, err, "fail to encode skill manifest")
	}
	file, err := os.OpenFile(filepath.Join(root, skillManifestName), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return model.NewSDKError(model.ErrCodeDiskError, err, "fail to create skill manifest")
	}
	_, writeErr := file.Write(data)
	if err = firstError(writeErr, file.Close()); err != nil {
		return model.NewSDKError(model.ErrCodeDiskError, err, "fail to write skill manifest")
	}
	return nil
}

func readFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("skill cache file %s is not a regular file", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	stat, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !stat.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("skill cache file %s is not a regular file", path)
	}
	var buffer bytes.Buffer
	_, readErr := buffer.ReadFrom(io.LimitReader(file, model.MaxSkillFileSize+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if int64(buffer.Len()) > model.MaxSkillFileSize {
		return nil, fmt.Errorf("skill cache file %s exceeds maximum size %d",
			path, model.MaxSkillFileSize)
	}
	return buffer.Bytes(), nil
}

func writeFile(path string, data []byte, permissions os.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, permissions)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	return firstError(writeErr, closeErr)
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func firstError(first, second error) error {
	if first != nil {
		return first
	}
	return second
}
