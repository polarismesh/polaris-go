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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v2"

	"github.com/polarismesh/polaris-go/pkg/config"
)

const fixtureDescription = "Reusable Polaris Go AI Registry E2E fixture"
const fixtureExample = "What is the fixture exchange rate?"

func validateFixtureFile(name, version, path string, content []byte) error {
	expected, found := skillFiles(name, version)[path]
	if !found {
		return fmt.Errorf("unexpected fixture file %s", path)
	}
	if path != "SKILL.md" {
		if !bytes.Equal(content, expected) {
			return fmt.Errorf("fixture file %s differs", path)
		}
		return nil
	}
	parts := bytes.SplitN(content, []byte("\n---\n"), 2)
	if len(parts) != 2 || !bytes.HasPrefix(parts[0], []byte("---\n")) {
		return fmt.Errorf("client SKILL.md has no complete YAML front matter")
	}
	var metadata struct {
		Name        string   `yaml:"name"`
		Description string   `yaml:"description"`
		Examples    []string `yaml:"examples"`
	}
	if err := yaml.Unmarshal(parts[0][4:], &metadata); err != nil {
		return fmt.Errorf("invalid client SKILL.md front matter: %w", err)
	}
	if metadata.Name != name || metadata.Description != fixtureDescription || len(metadata.Examples) != 1 || metadata.Examples[0] != fixtureExample {
		return fmt.Errorf("client SKILL.md front matter does not match published resource metadata")
	}
	expectedParts := bytes.SplitN(expected, []byte("\n---\n"), 2)
	if len(expectedParts) != 2 || !bytes.Equal(parts[1], expectedParts[1]) {
		return fmt.Errorf("client SKILL.md body differs from the published fixture")
	}
	return nil
}

type consoleClient struct {
	address, token string
	http           *http.Client
}

type consoleEnvelope struct {
	Code      uint32 `json:"code"`
	Info      string `json:"info"`
	Responses []struct {
		Code uint32 `json:"code"`
	} `json:"responses"`
	Services   []serviceView                  `json:"services"`
	Namespaces []struct{ Name, Token string } `json:"namespaces"`
	Resource   *struct {
		Name, Namespace string
		Ext             map[string]string `json:"ext"`
	} `json:"resource"`
	Version          *struct{ Version, Status, Content string } `json:"version"`
	Content          string                                     `json:"content"`
	Instances        []instanceView                             `json:"instances"`
	Amount           uint32                                     `json:"amount"`
	ConfigFileGroups []configGroupView                          `json:"configFileGroups"`
	Data             []struct {
		Type   string `json:"@type"`
		Name   string `json:"name"`
		Method string `json:"method"`
		Path   string `json:"path"`
	} `json:"data"`
}

type instanceView struct {
	ID, Service, Namespace string
	Metadata               map[string]string `json:"metadata"`
}

type configGroupView struct {
	Name, Namespace, Comment string
}

type serviceView struct {
	Name, Namespace      string
	Metadata             map[string]string `json:"metadata"`
	ExportTo             []string          `json:"export_to"`
	SyncToGlobalRegistry bool              `json:"sync_to_global_registry"`
}

func newConsole(o options, cfg config.Configuration) *consoleClient {
	token := environment("POLARIS_CONSOLE_TOKEN", "POLARIS_TOKEN", "POLARIS_E2E_SERVICE_TOKEN", "POLARIS_SERVICE_TOKEN")
	if token == "" && cfg != nil {
		token = cfg.GetGlobal().GetServerConnector().GetToken()
	}
	return &consoleClient{address: strings.TrimRight(o.console, "/"), token: token,
		http: &http.Client{Timeout: o.timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c *consoleClient) request(ctx context.Context, method, path string, payload interface{}) (*consoleEnvelope, error) {
	var body []byte
	var err error
	if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.address+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("X-Polaris-Token", c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Console %s %s: %w", method, strings.Split(path, "?")[0], err)
	}
	defer resp.Body.Close()
	var envelope consoleEnvelope
	err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&envelope)
	if err != nil {
		return nil, fmt.Errorf("Console %s %s: HTTP %d, malformed response", method, strings.Split(path, "?")[0], resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || envelope.Code != 200000 {
		return &envelope, fmt.Errorf("Console %s %s: HTTP %d, code=%d", method, strings.Split(path, "?")[0], resp.StatusCode, envelope.Code)
	}
	for _, item := range envelope.Responses {
		if item.Code != 200000 && item.Code != 200002 {
			return &envelope, fmt.Errorf("Console %s %s: item code=%d", method, strings.Split(path, "?")[0], item.Code)
		}
	}
	return &envelope, nil
}

func (c *consoleClient) ensureNamespace(ctx context.Context, namespace string) error {
	query := url.Values{"name": {namespace}}
	result, err := c.request(ctx, http.MethodGet, "/naming/v1/namespaces?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	for _, item := range result.Namespaces {
		if item.Name == namespace {
			return nil
		}
	}
	_, err = c.request(ctx, http.MethodPost, "/naming/v1/namespaces", []map[string]string{{"name": namespace, "comment": "polaris-go isolated AI Registry real E2E"}})
	return err
}

func skillFiles(name, version string) map[string][]byte {
	content := fmt.Sprintf("---\nname: %s\ndescription: Polaris Go AI Registry fixture\n---\n\n# Weather and Currency\n\nFixture %s version %s. Use the supplied reference data to answer the task.\n", name, name, version)
	files := map[string][]byte{
		"SKILL.md":                       []byte(content),
		"references/rates.json":          []byte(`{"USD":1,"CNY":7,"source":"fixture"}`),
		"references/.polaris-skill.json": []byte("package reference, not an SDK manifest\n"),
	}
	if name == weatherSkill {
		data := make([]byte, 1280*1024)
		for offset := 0; offset < len(data); offset += sha256.Size {
			digest := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d", name, version, offset)))
			copy(data[offset:], digest[:])
		}
		files["assets/data-1.bin"] = data[:len(data)/2]
		files["assets/data-2.bin"] = data[len(data)/2:]
	}
	return files
}

func (c *consoleClient) ensureSkillVersion(ctx context.Context, namespace, name, version string, publish bool) error {
	resourceQuery := url.Values{"namespace": {namespace}, "name": {name}}
	resource, resourceErr := c.request(ctx, http.MethodGet, "/skill/v1/skills?"+resourceQuery.Encode(), nil)
	if resourceErr != nil && (resource == nil || resource.Code != 400202) {
		return resourceErr
	}
	if resourceErr == nil && (resource.Resource == nil || resource.Resource.Name != name || resource.Resource.Namespace != namespace || resource.Resource.Ext[ownerLabel] != e2eOwner) {
		return fmt.Errorf("refuse to reuse unowned Skill %s/%s", namespace, name)
	}
	query := url.Values{"namespace": {namespace}, "name": {name}, "version": {version}, "withContent": {"true"}}
	result, err := c.request(ctx, http.MethodGet, "/skill/v1/skills?"+query.Encode(), nil)
	if err == nil {
		if result.Resource == nil || result.Resource.Name != name || result.Resource.Namespace != namespace || result.Resource.Ext[ownerLabel] != e2eOwner {
			return fmt.Errorf("refuse to reuse unowned Skill %s/%s", namespace, name)
		}
		if result.Version == nil || result.Version.Version != version {
			return fmt.Errorf("Skill fixture %s/%s has no requested version %s", namespace, name, version)
		}
		if result.Content != "" && result.Content != string(skillFiles(name, version)["SKILL.md"]) {
			return fmt.Errorf("owned Skill fixture %s/%s@%s differs; use a separate namespace", namespace, name, version)
		}
		if publish && result.Version.Status != "published" {
			return c.publishSkill(ctx, namespace, name, version)
		}
		if !publish && result.Version.Status != "draft" {
			return fmt.Errorf("draft fixture has unexpected status %q", result.Version.Status)
		}
		return nil
	}
	if result == nil || result.Code != 400202 {
		return err
	}
	files := skillFiles(name, version)
	type skillFile struct {
		Path        string `json:"path"`
		Content     []byte `json:"content"`
		ContentType string `json:"contentType"`
	}
	entries := make([]skillFile, 0, len(files))
	for path, content := range files {
		contentType := "text"
		if strings.HasSuffix(path, ".bin") {
			contentType = "binary_base64"
		}
		entries = append(entries, skillFile{Path: path, Content: content, ContentType: contentType})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	_, err = c.request(ctx, http.MethodPost, "/skill/v1/skills", map[string]interface{}{
		"namespace": namespace, "name": name, "version": version,
		"description": fixtureDescription, "files": entries,
		"tags": []string{fixtureTag, name}, "examples": []string{fixtureExample},
		"scope": "PRIVATE", "author": e2eOwner, "ext": map[string]string{ownerLabel: e2eOwner}, "publish": publish,
	})
	return err
}

func (c *consoleClient) publishSkill(ctx context.Context, namespace, name, version string) error {
	query := url.Values{"namespace": {namespace}, "name": {name}, "version": {version}}
	result, err := c.request(ctx, http.MethodGet, "/skill/v1/skills?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	if result.Resource == nil || result.Resource.Name != name || result.Resource.Namespace != namespace || result.Resource.Ext[ownerLabel] != e2eOwner || result.Version == nil || result.Version.Version != version {
		return fmt.Errorf("refuse to publish an unowned or incomplete Skill fixture")
	}
	path := "/skill/v1/skills/publish"
	payload := map[string]string{"namespace": namespace, "name": name, "version": version, "comment": "AI Registry E2E active-version transition"}
	if result.Version.Status == "published" {
		path = "/skill/v1/skills/rollback"
		delete(payload, "version")
		payload["targetVersion"] = version
	}
	_, err = c.request(ctx, http.MethodPut, path, payload)
	return err
}

type instanceState struct {
	ID      string `json:"id"`
	Service string `json:"service"`
}

type processState struct {
	PID       int    `json:"pid"`
	Binary    string `json:"binary"`
	ReadyPath string `json:"ready_path"`
	Service   string `json:"service"`
}

type runState struct {
	Schema       int             `json:"schema"`
	Suite        string          `json:"suite,omitempty"`
	Owner        string          `json:"owner"`
	RunID        string          `json:"run_id"`
	Namespace    string          `json:"namespace"`
	Console      string          `json:"console"`
	Services     []string        `json:"services"`
	Instances    []instanceState `json:"instances"`
	Processes    []processState  `json:"processes"`
	Skills       []string        `json:"skills"`
	ConfigGroups []string        `json:"config_groups"`
	Cleaned      bool            `json:"cleaned"`
}

func writeJSON(path string, value interface{}) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := ioutil.TempFile(filepath.Dir(path), ".state-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func validateState(state *runState) error {
	if state.Schema != 1 || state.Owner != e2eOwner || state.RunID == "" || strings.ContainsAny(state.RunID, " /\\:@") ||
		state.Namespace == "" || strings.EqualFold(state.Namespace, "default") || strings.ContainsAny(state.Namespace, "/\\:*@") {
		return fmt.Errorf("invalid or unowned cleanup state")
	}
	if state.Suite != "" && !validSuite(state.Suite) {
		return fmt.Errorf("cleanup state has invalid suite %q", state.Suite)
	}
	if err := validateConsole(state.Console); err != nil {
		return err
	}
	for _, service := range state.Services {
		if !serviceBelongsToRun(service, state.RunID) {
			return fmt.Errorf("service %q does not belong to run", service)
		}
	}
	for _, instance := range state.Instances {
		ownedService := false
		for _, service := range state.Services {
			ownedService = ownedService || instance.Service == service
		}
		if !ownedService || !serviceBelongsToRun(instance.Service, state.RunID) || instance.ID == "" {
			return fmt.Errorf("invalid instance ownership")
		}
	}
	for _, process := range state.Processes {
		if process.PID <= 1 || !serviceBelongsToRun(process.Service, state.RunID) ||
			!filepath.IsAbs(process.Binary) || !filepath.IsAbs(process.ReadyPath) {
			return fmt.Errorf("invalid provider ownership")
		}
	}
	for _, name := range state.Skills {
		if name != weatherSkill && name != currencySkill && name != draftSkill {
			return fmt.Errorf("unknown reusable fixture %q", name)
		}
	}
	for _, group := range state.ConfigGroups {
		if group != servicePrefix+state.RunID+"-config" &&
			group != legacyServicePrefix+state.RunID+"-config" {
			return fmt.Errorf("unowned configuration group")
		}
	}
	return nil
}

func (c *consoleClient) listInstances(ctx context.Context, namespace, name string) ([]instanceView, error) {
	query := url.Values{"namespace": {namespace}, "service": {name}, "limit": {"100"}}
	result, err := c.request(ctx, http.MethodGet, "/naming/v1/instances?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if result.Amount > uint32(len(result.Instances)) {
		return nil, fmt.Errorf("refuse cleanup of a service with truncated instance results")
	}
	return result.Instances, nil
}

func (c *consoleClient) cleanupService(ctx context.Context, state *runState, name string) error {
	service, err := c.getService(ctx, state.Namespace, name)
	if err != nil || service == nil {
		return err
	}
	if service.Metadata[ownerLabel] != e2eOwner || service.Metadata[runLabel] != state.RunID {
		return fmt.Errorf("refuse to delete unowned service %s", name)
	}
	instances, err := c.listInstances(ctx, state.Namespace, name)
	if err != nil {
		return err
	}
	for _, instance := range instances {
		if instance.Namespace != state.Namespace || instance.Service != name || instance.ID == "" || instance.Metadata[ownerLabel] != e2eOwner || instance.Metadata[runLabel] != state.RunID {
			return fmt.Errorf("refuse cleanup of %s containing an unowned instance", name)
		}
	}
	for _, instance := range instances {
		if _, err = c.request(ctx, http.MethodPost, "/naming/v1/instances/delete", []map[string]string{{"id": instance.ID, "namespace": state.Namespace, "service": name}}); err != nil {
			return err
		}
	}
	if _, err = c.request(ctx, http.MethodPost, "/naming/v1/services/delete", []map[string]string{{"namespace": state.Namespace, "name": name}}); err != nil {
		return err
	}
	if err = eventually(ctx, 20*time.Second, func() error {
		remaining, err := c.getService(ctx, state.Namespace, name)
		if err != nil {
			return err
		}
		if remaining != nil {
			return fmt.Errorf("deleted service %s remains visible", name)
		}
		return nil
	}); err != nil {
		return err
	}
	log.Printf("CLEANUP service %s/%s", state.Namespace, name)
	return nil
}

func configComment(runID string) string { return e2eOwner + ":" + runID }

func (c *consoleClient) configGroup(ctx context.Context, namespace, name string) (*configGroupView, error) {
	query := url.Values{"namespace": {namespace}, "group": {name}, "limit": {"100"}, "offset": {"0"}}
	result, err := c.request(ctx, http.MethodGet, "/config/v1/configfilegroups?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	for index := range result.ConfigFileGroups {
		group := &result.ConfigFileGroups[index]
		if group.Namespace == namespace && group.Name == name {
			return group, nil
		}
	}
	return nil, nil
}

func (c *consoleClient) cleanupConfig(ctx context.Context, state *runState, name string) error {
	group, err := c.configGroup(ctx, state.Namespace, name)
	if err != nil || group == nil {
		return err
	}
	if group.Comment != configComment(state.RunID) {
		return fmt.Errorf("refuse to delete unowned configuration group %s", name)
	}
	query := url.Values{"namespace": {state.Namespace}, "group": {name}, "name": {"agent.yaml"}}
	result, err := c.request(ctx, http.MethodGet, "/config/v1/configfiles?"+query.Encode(), nil)
	if err == nil {
		_, err = c.request(ctx, http.MethodDelete, "/config/v1/configfiles?"+query.Encode(), nil)
	}
	if err != nil && (result == nil || result.Code != 400201) {
		return err
	}
	query.Del("name")
	if _, err = c.request(ctx, http.MethodDelete, "/config/v1/configfilegroups?"+query.Encode(), nil); err != nil {
		return err
	}
	return eventually(ctx, 15*time.Second, func() error {
		group, err := c.configGroup(ctx, state.Namespace, name)
		if err != nil {
			return err
		}
		if group != nil {
			return fmt.Errorf("deleted configuration group remains visible")
		}
		return nil
	})
}

func (c *consoleClient) getService(ctx context.Context, namespace, name string) (*serviceView, error) {
	query := url.Values{"namespace": {namespace}, "name": {name}}
	result, err := c.request(ctx, http.MethodGet, "/naming/v1/services?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	for index := range result.Services {
		item := &result.Services[index]
		if item.Namespace == namespace && item.Name == name {
			return item, nil
		}
	}
	return nil, nil
}

func cleanupState(ctx context.Context, o options) error {
	data, err := ioutil.ReadFile(o.statePath)
	if err != nil {
		return err
	}
	var state runState
	if err = json.Unmarshal(data, &state); err != nil {
		return err
	}
	if err = validateState(&state); err != nil {
		return err
	}
	if !o.apply {
		log.Printf("DRY RUN run=%s services=%t (%d) processes=%t (%d) skills=%t (%d)", state.RunID, o.cleanServices, len(state.Services), o.cleanProcesses, len(state.Processes), o.cleanSkills, len(state.Skills))
		return nil
	}
	if o.cleanServices && !o.cleanProcesses && len(state.Processes) != 0 {
		return fmt.Errorf("service cleanup requires -processes so providers cannot re-register")
	}
	o.console = state.Console
	o.timeout = 15 * time.Second
	var cfg config.Configuration
	if o.configFile != "" {
		cfg, err = config.LoadConfigurationByFile(o.configFile)
		if err != nil {
			return err
		}
		cfg.SetDefault()
	}
	c := newConsole(o, cfg)
	var failure error
	if o.cleanProcesses {
		for _, process := range state.Processes {
			failure = addError(failure, stopProvider(process, state.RunID))
		}
		if failure != nil {
			return failure
		}
	}
	if o.cleanServices {
		for _, name := range state.Services {
			failure = addError(failure, c.cleanupService(ctx, &state, name))
		}
		for _, group := range state.ConfigGroups {
			failure = addError(failure, c.cleanupConfig(ctx, &state, group))
		}
	}
	if o.cleanSkills {
		for _, name := range state.Skills {
			query := url.Values{"namespace": {state.Namespace}, "name": {name}}
			result, getErr := c.request(ctx, http.MethodGet, "/skill/v1/skills?"+query.Encode(), nil)
			if getErr != nil {
				if result != nil && result.Code == 400202 {
					continue
				}
				failure = addError(failure, getErr)
				continue
			}
			if result.Resource == nil || result.Resource.Ext[ownerLabel] != e2eOwner {
				failure = addError(failure, fmt.Errorf("refuse to delete unowned Skill %s", name))
				continue
			}
			_, err = c.request(ctx, http.MethodDelete, "/skill/v1/skills?"+query.Encode(), nil)
			failure = addError(failure, err)
		}
	}
	if failure == nil && o.cleanServices && (o.cleanProcesses || len(state.Processes) == 0) {
		state.Cleaned = true
		return writeJSON(o.statePath, state)
	}
	return failure
}

func addError(previous, next error) error {
	if next == nil {
		return previous
	}
	if previous == nil {
		return next
	}
	return fmt.Errorf("%v; %w", previous, next)
}
