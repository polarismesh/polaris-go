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
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
)

func TestFixtureValidationAcceptsMetadataRewriteButRejectsBodyChanges(t *testing.T) {
	name := weatherSkill
	content := []byte(fmt.Sprintf("---\nname: %s\ndescription: %s\nexamples:\n  - %s\n---\n\n# Weather and Currency\n\nFixture %s version 1.0.0. Use the supplied reference data to answer the task.\n", name, fixtureDescription, fixtureExample, name))
	if err := validateFixtureFile(name, "1.0.0", "SKILL.md", content); err != nil {
		t.Fatal(err)
	}
	for _, changed := range [][]byte{
		bytes.ReplaceAll(content, []byte("version 1.0.0"), []byte("version 2.0.0")),
		bytes.ReplaceAll(content, []byte(fixtureDescription), []byte("wrong description")),
		bytes.ReplaceAll(content, []byte(fixtureExample), []byte("wrong example")),
		[]byte("---\nname: [\n---\nbody"),
	} {
		if err := validateFixtureFile(name, "1.0.0", "SKILL.md", changed); err == nil {
			t.Fatal("changed body or invalid metadata passed fixture validation")
		}
	}
}

func TestSuiteSelection(t *testing.T) {
	for _, suite := range []string{"all", "agent", "mcp", "skill"} {
		if !validSuite(suite) {
			t.Fatalf("suite %q should be valid", suite)
		}
	}
	if validSuite("unknown") {
		t.Fatal("unknown suite was accepted")
	}
	if !suiteIncludes("all", "agent") || !suiteIncludes("all", "mcp") || !suiteIncludes("all", "skill") {
		t.Fatal("all suite must include every group")
	}
	if !suiteIncludes("mcp", "mcp") || suiteIncludes("mcp", "agent") || suiteIncludes("mcp", "skill") {
		t.Fatal("mcp suite filtering differs")
	}
}

func TestRunIDAndServiceNameFormat(t *testing.T) {
	now := time.Date(2026, time.November, 6, 16, 5, 0, 123456789, time.Local)
	runID := newRunID(now)
	if runID != "20261106160500123" {
		t.Fatalf("run ID=%q, want millisecond timestamp", runID)
	}
	s := &suite{opts: options{runID: runID}}
	if actual := s.serviceName("concurrent-writer"); actual != "go-sdk-e2e-20261106160500123-concurrent-writer" {
		t.Fatalf("service name=%q", actual)
	}
}

func TestServiceBelongsToRunAcceptsLegacyAndCurrentPrefixes(t *testing.T) {
	runID := "20261106160500123"
	for _, service := range []string{
		"go-sdk-e2e-" + runID + "-agent",
		"polaris-go-ai-e2e-" + runID + "-agent",
	} {
		if !serviceBelongsToRun(service, runID) {
			t.Fatalf("service %q should belong to run", service)
		}
	}
	if serviceBelongsToRun("go-sdk-e2e-other-agent", runID) {
		t.Fatal("unowned service was accepted")
	}
}

func TestMCPMetadata(t *testing.T) {
	metadata, err := model.BuildMCPServerMetadata(&model.MCPServerMetadataRequest{
		ServerName: "mcp-service", ProtocolVersion: "2025-06-18",
		Transport: model.MCPTransportStreamableHTTP, EndpointPath: "/mcp",
	})
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{
		model.MCPMetadataKeyServerName:      "mcp-service",
		model.MCPMetadataKeyProtocolVersion: "2025-06-18",
		model.MCPMetadataKeyEndpointPath:    "/mcp",
	}
	if len(metadata) != len(expected) {
		t.Fatalf("metadata length=%d, want %d", len(metadata), len(expected))
	}
	for key, value := range expected {
		if metadata[key] != value {
			t.Fatalf("metadata[%s]=%q, want %q", key, metadata[key], value)
		}
	}
}

func TestProviderMetadataUsesStandardAgentKeys(t *testing.T) {
	card, snapshot, err := buildProviderRegistration(providerAgentOptions{
		namespace:       defaultNamespace,
		service:         "agent",
		primaryEndpoint: "http://127.0.0.1:1234",
		runID:           "test",
		cardVersion:     agentCardSchemaVersion10,
		businessVersion: agentBusinessVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata := snapshot.Metadata
	if metadata[model.MetadataKeyAIAgentCard] != card ||
		metadata[model.MetadataKeyA2AVersion] != agentCardSchemaVersion10 ||
		metadata[runLabel] != "test" ||
		!strings.Contains(card, "http://127.0.0.1:1234/invoke") {
		t.Fatalf("metadata and endpoint differ: %+v", metadata)
	}
	for _, legacy := range []string{"a2a.agent.card", model.MetadataKeyA2ATransport, model.MetadataKeyA2APath} {
		if _, exists := metadata[legacy]; exists {
			t.Fatalf("legacy Agent metadata key %q must not be written", legacy)
		}
	}
}

func TestSkillFixtureRespectsServerFileLimitAndRequiresMultiframeZIP(t *testing.T) {
	var payload bytes.Buffer
	writer := zip.NewWriter(&payload)
	for name, content := range skillFiles(weatherSkill, "1.0.0") {
		if len(content) > 1024*1024 {
			t.Fatalf("server rejects fixture file %s with %d bytes", name, len(content))
		}
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if payload.Len() <= 1024*1024 {
		t.Fatalf("ZIP %d bytes cannot prove multi-frame download", payload.Len())
	}
}

func TestProviderRegistrationV03MultiBusinessPort(t *testing.T) {
	cardJSON, snapshot, err := buildProviderRegistration(providerAgentOptions{
		namespace:         defaultNamespace,
		service:           "legacy-agent",
		primaryEndpoint:   "http://127.0.0.1:1234",
		secondaryEndpoint: "http://127.0.0.1:5678",
		runID:             "test",
		cardVersion:       agentCardSchemaVersion03,
		businessVersion:   "2.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata := snapshot.Metadata
	var card struct {
		Version              string `json:"version"`
		URL                  string `json:"url"`
		AdditionalInterfaces []struct {
			URL       string `json:"url"`
			Transport string `json:"transport"`
		} `json:"additionalInterfaces"`
	}
	if err = json.Unmarshal([]byte(cardJSON), &card); err != nil {
		t.Fatal(err)
	}
	if len(card.AdditionalInterfaces) != 1 ||
		card.URL != "http://127.0.0.1:1234/invoke" ||
		card.AdditionalInterfaces[0].URL != "http://127.0.0.1:5678/invoke" ||
		card.AdditionalInterfaces[0].Transport != "HTTP+JSON" {
		t.Fatalf("unexpected 0.3 card: %s", cardJSON)
	}
	if card.Version != "2.0.0" ||
		metadata[model.MetadataKeyAIAgentCard] != cardJSON ||
		metadata[model.MetadataKeyA2AVersion] != agentCardSchemaVersion03 ||
		metadata[runLabel] != "test" {
		t.Fatalf("unexpected 0.3 metadata: %+v", metadata)
	}
}

func TestProviderRegistrationUsesPublicSnapshotForExplicitPortAndVersion(t *testing.T) {
	registrationPort := 18081
	cardJSON, snapshot, err := buildProviderRegistration(providerAgentOptions{
		namespace:        defaultNamespace,
		service:          "agent",
		primaryEndpoint:  "http://127.0.0.1:1234",
		runID:            "test",
		cardVersion:      agentCardSchemaVersion10,
		businessVersion:  "2026.11-explicit",
		registrationPort: &registrationPort,
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Host != "127.0.0.1" || snapshot.Port != registrationPort ||
		snapshot.Version != "2026.11-explicit" || snapshot.Protocol != "" ||
		snapshot.A2AVersion != agentCardSchemaVersion10 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	request := snapshot.NewInstanceRegisterRequest()
	if request == nil || request.Port != registrationPort || request.Version == nil ||
		*request.Version != "2026.11-explicit" || request.Protocol == nil || *request.Protocol != "" ||
		request.Metadata[model.MetadataKeyAIAgentCard] != cardJSON ||
		request.Metadata[runLabel] != "test" {
		t.Fatalf("unexpected instance request: %+v", request)
	}
}

func TestCleanupValidatesServiceBeforeDeletingInstances(t *testing.T) {
	deletes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/naming/v1/services" {
			fmt.Fprintf(w, `{"code":200000,"services":[{"name":"go-sdk-e2e-test-agent","namespace":%q,"metadata":{"e2e.owner":"business"}}]}`, defaultNamespace)
			return
		}
		if r.Method != http.MethodGet {
			deletes++
		}
		fmt.Fprint(w, `{"code":200000}`)
	}))
	defer server.Close()
	state := runState{Schema: 1, Owner: e2eOwner, RunID: "test", Namespace: defaultNamespace, Console: server.URL,
		Services: []string{"go-sdk-e2e-test-agent"}, Instances: []instanceState{{ID: "instance", Service: "go-sdk-e2e-test-agent"}}}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := writeJSON(path, state); err != nil {
		t.Fatal(err)
	}
	err := cleanupState(context.Background(), options{statePath: path, apply: true, cleanServices: true})
	if err == nil || deletes != 0 {
		t.Fatalf("cleanup must refuse before any delete: err=%v deletes=%d", err, deletes)
	}
}

func TestCleanupServicesWithoutProcessesMarksCleaned(t *testing.T) {
	service := "go-sdk-e2e-test-mcp"
	deleted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/naming/v1/services":
			if deleted {
				fmt.Fprint(w, `{"code":200000,"services":[]}`)
				return
			}
			fmt.Fprintf(w, `{"code":200000,"services":[{"name":%q,"namespace":%q,"metadata":{"e2e.owner":%q,"e2e.run.id":"test"}}]}`, service, defaultNamespace, e2eOwner)
		case "/naming/v1/instances":
			fmt.Fprint(w, `{"code":200000,"amount":0,"instances":[]}`)
		case "/naming/v1/services/delete":
			deleted = true
			fmt.Fprint(w, `{"code":200000,"responses":[{"code":200000}]}`)
		default:
			fmt.Fprint(w, `{"code":200000,"responses":[{"code":200000}]}`)
		}
	}))
	defer server.Close()
	state := runState{
		Schema: 1, Suite: "mcp", Owner: e2eOwner, RunID: "test",
		Namespace: defaultNamespace, Console: server.URL, Services: []string{service},
	}
	path := filepath.Join(t.TempDir(), "state.json")
	requireNoError := writeJSON(path, state)
	if requireNoError != nil {
		t.Fatal(requireNoError)
	}
	if err := cleanupState(context.Background(), options{statePath: path, apply: true, cleanServices: true}); err != nil {
		t.Fatal(err)
	}
	data, err := ioutil.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if !state.Cleaned {
		t.Fatal("service-only cleanup with no processes must mark state cleaned")
	}
}

func TestCleanupDoesNotIgnoreInstanceDeleteFailure(t *testing.T) {
	service := "go-sdk-e2e-test-agent"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/naming/v1/services":
			fmt.Fprintf(w, `{"code":200000,"services":[{"name":%q,"namespace":%q,"metadata":{"e2e.owner":%q,"e2e.run.id":"test"}}]}`, service, defaultNamespace, e2eOwner)
		case "/naming/v1/instances":
			fmt.Fprintf(w, `{"code":200000,"amount":1,"instances":[{"id":"instance","service":%q,"namespace":%q,"metadata":{"e2e.owner":%q,"e2e.run.id":"test"}}]}`, service, defaultNamespace, e2eOwner)
		case "/naming/v1/instances/delete":
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"code":500000}`)
		default:
			fmt.Fprint(w, `{"code":200000}`)
		}
	}))
	defer server.Close()
	state := runState{Schema: 1, Owner: e2eOwner, RunID: "test", Namespace: defaultNamespace, Console: server.URL,
		Services: []string{service}, Instances: []instanceState{{ID: "instance", Service: service}}}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := writeJSON(path, state); err != nil {
		t.Fatal(err)
	}
	err := cleanupState(context.Background(), options{statePath: path, apply: true, cleanServices: true})
	if err == nil || !strings.Contains(err.Error(), "500000") {
		t.Fatalf("instance deletion failure must fail cleanup: %v", err)
	}
}

func TestSkillFixtureAlreadyPublishedDoesNotRepublish(t *testing.T) {
	publishes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprintf(w, `{"code":200000,"resource":{"name":%q,"namespace":%q,"ext":{"e2e.owner":%q},"versionInfo":{"activeVersion":"1.0.0"}},"version":{"version":"1.0.0","status":"published"}}`, weatherSkill, defaultNamespace, e2eOwner)
			return
		}
		publishes++
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"code":400000}`)
	}))
	defer server.Close()
	c := newConsole(options{console: server.URL, timeout: time.Second}, nil)
	err := c.ensureSkillVersion(context.Background(), defaultNamespace, weatherSkill, "1.0.0", true)
	if err != nil || publishes != 0 {
		t.Fatalf("published fixture must be reusable: err=%v publish_count=%d", err, publishes)
	}
}

func TestCleanupDefaultIsDryRun(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		fmt.Fprint(w, `{"code":200000}`)
	}))
	defer server.Close()
	state := runState{Schema: 1, Owner: e2eOwner, RunID: "test", Namespace: defaultNamespace, Console: server.URL}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := writeJSON(path, state); err != nil {
		t.Fatal(err)
	}
	if err := cleanupState(context.Background(), options{statePath: path}); err != nil || requests != 0 {
		t.Fatalf("default cleanup must have no remote effects: err=%v requests=%d", err, requests)
	}
}

func TestCleanupDryRunRetainsSelectionAndOverridesApply(t *testing.T) {
	previousFlags, previousArgs := flag.CommandLine, os.Args
	defer func() { flag.CommandLine, os.Args = previousFlags, previousArgs }()
	flag.CommandLine = flag.NewFlagSet("cleanup-test", flag.ContinueOnError)
	os.Args = []string{"cleanup-test", "-mode=cleanup", "-state=state.json", "-services", "-processes", "-skills", "-apply", "-dry-run"}
	o := readOptions()
	if o.apply || !o.cleanServices || !o.cleanProcesses || !o.cleanSkills {
		t.Fatalf("dry-run lost selected resources or applied cleanup: %+v", o)
	}
}

func TestVerificationRejectsCleanupDryRun(t *testing.T) {
	o := options{mode: "verify", server: "127.0.0.1", namespace: defaultNamespace, runID: "test", runDir: t.TempDir(), timeout: time.Second, runTimeout: time.Minute, dryRun: true}
	if err := o.validate(); err == nil {
		t.Fatal("verify accepted dry-run while its scenarios still write remote resources")
	}
}

func TestProviderReadsSkillEntryWithinInstalledDirectory(t *testing.T) {
	for _, entry := range []string{"SKILL.md", "package/SKILL.md"} {
		t.Run(entry, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, filepath.FromSlash(entry))
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := ioutil.WriteFile(path, []byte("installed instructions"), 0600); err != nil {
				t.Fatal(err)
			}
			content, err := readSkillInstructions(&model.InstalledSkill{Dir: root, EntryFile: entry})
			if err != nil || content != "installed instructions" {
				t.Fatalf("provider did not read installed entry: %q %v", content, err)
			}
		})
	}
}

func TestConfigCallbackRequiresExactPublication(t *testing.T) {
	expected := model.SimpleConfigFile{Namespace: defaultNamespace, FileGroup: "group", FileName: "agent.yaml", Version: 2, Md5: "generation-1"}
	for _, test := range []struct {
		name  string
		event *model.ConfigGroupChangeEvent
		match bool
	}{
		{name: "nil event"},
		{name: "empty event", event: &model.ConfigGroupChangeEvent{}},
		{name: "initial version", event: &model.ConfigGroupChangeEvent{After: []*model.SimpleConfigFile{{Namespace: defaultNamespace, FileGroup: "group", FileName: "agent.yaml", Version: 1, Md5: "generation-0"}}}},
		{name: "unrelated file", event: &model.ConfigGroupChangeEvent{After: []*model.SimpleConfigFile{{Namespace: defaultNamespace, FileGroup: "group", FileName: "other.yaml", Version: 2, Md5: "generation-1"}}}},
		{name: "wrong namespace", event: &model.ConfigGroupChangeEvent{After: []*model.SimpleConfigFile{{Namespace: "another", FileGroup: "group", FileName: "agent.yaml", Version: 2, Md5: "generation-1"}}}},
		{name: "wrong group", event: &model.ConfigGroupChangeEvent{After: []*model.SimpleConfigFile{{Namespace: defaultNamespace, FileGroup: "other", FileName: "agent.yaml", Version: 2, Md5: "generation-1"}}}},
		{name: "wrong digest", event: &model.ConfigGroupChangeEvent{After: []*model.SimpleConfigFile{{Namespace: defaultNamespace, FileGroup: "group", FileName: "agent.yaml", Version: 2, Md5: "wrong"}}}},
		{name: "optional digest omitted", event: &model.ConfigGroupChangeEvent{After: []*model.SimpleConfigFile{{Namespace: defaultNamespace, FileGroup: "group", FileName: "agent.yaml", Version: 2}}}, match: true},
		{name: "correct update", event: &model.ConfigGroupChangeEvent{After: []*model.SimpleConfigFile{nil, &expected}}, match: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if matched := configPublicationMatches(test.event, expected); matched != test.match {
				t.Fatalf("matched=%t expected=%t", matched, test.match)
			}
		})
	}
}
