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
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

type suite struct {
	opts                                        options
	console                                     *consoleClient
	sdk                                         api.SDKContext
	skills                                      api.SkillAPI
	consumer                                    api.ConsumerAPI
	provider                                    api.ProviderAPI
	state                                       runState
	processes                                   map[int]*processExit
	secrets                                     []string
	cardProvider, skillProvider, legacyProvider providerReady
	installed                                   *model.InstalledSkill
	results                                     []caseResult
}

type caseResult struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	Milliseconds int64  `json:"milliseconds"`
	Error        string `json:"error,omitempty"`
}

func eventually(ctx context.Context, timeout time.Duration, condition func() error) error {
	step, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		if err := step.Err(); err != nil {
			return fmt.Errorf("condition deadline: %v; last: %w", err, lastErr)
		}
		if lastErr = condition(); lastErr == nil {
			return nil
		}
		select {
		case <-step.Done():
			return fmt.Errorf("condition deadline: %v; last: %w", step.Err(), lastErr)
		case <-ticker.C:
		}
	}
}

func verify(parent context.Context, o options) (failure error) {
	ctx, cancel := context.WithTimeout(parent, o.runTimeout)
	defer cancel()
	if err := os.MkdirAll(o.runDir, 0700); err != nil {
		return err
	}
	statePath := filepath.Join(o.runDir, "state.json")
	if _, err := os.Lstat(statePath); !os.IsNotExist(err) {
		return fmt.Errorf("run directory already has a state file")
	}
	needsSkill := suiteIncludes(o.suite, "agent") || suiteIncludes(o.suite, "skill")
	cfg, err := loadConfiguration(o, needsSkill)
	if err != nil {
		return err
	}
	s := &suite{opts: o, console: newConsole(o, cfg), processes: make(map[int]*processExit), state: runState{
		Schema: 1, Suite: o.suite, Owner: e2eOwner, RunID: o.runID, Namespace: o.namespace, Console: o.console,
	}}
	s.secrets = []string{s.console.token, cfg.GetGlobal().GetServerConnector().GetToken(), cfg.GetSkill().GetToken(), cfg.GetConfigFile().GetConfigConnectorConfig().GetToken()}
	if err = s.saveState(); err != nil {
		return err
	}
	defer func() {
		var cleanupErr error
		if o.cleanServices || o.cleanProcesses || o.cleanSkills {
			cleanup := o
			cleanup.statePath, cleanup.apply = statePath, true
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
			cleanupErr = cleanupState(cleanupCtx, cleanup)
			cleanupCancel()
		}
		if o.cleanProcesses {
			for _, exit := range s.processes {
				select {
				case <-exit.done:
					cleanupErr = addError(cleanupErr, exit.err)
				case <-time.After(15 * time.Second):
					cleanupErr = addError(cleanupErr, fmt.Errorf("provider exit was not observed"))
				}
			}
		}
		failure = addError(failure, cleanupErr)
		if failure != nil {
			failure = fmt.Errorf("%s", redactError(failure, s.secrets...))
		}
		summary := map[string]interface{}{"run_id": o.runID, "suite": o.suite, "namespace": o.namespace, "results": s.results, "success": failure == nil,
			"cleanup_requested": o.cleanServices || o.cleanProcesses || o.cleanSkills, "cleanup_success": cleanupErr == nil}
		if failure != nil {
			summary["error"] = failure.Error()
		}
		failure = addError(failure, writeJSON(filepath.Join(o.runDir, "result.json"), summary))
		log.Printf("SUMMARY cases=%d success=%t state=%s", len(s.results), failure == nil, statePath)
	}()
	if err = s.console.ensureNamespace(ctx, o.namespace); err != nil {
		return err
	}
	if needsSkill {
		setupFailed := false
		for _, fixture := range []struct {
			name, version string
			publish       bool
		}{
			{weatherSkill, "1.0.0", true}, {weatherSkill, "2.0.0", true}, {currencySkill, "1.0.0", true}, {draftSkill, "1.0.0", false},
		} {
			if err = s.console.ensureSkillVersion(ctx, o.namespace, fixture.name, fixture.version, fixture.publish); err != nil {
				failure = addError(failure, err)
				setupFailed = true
				log.Printf("FIXTURE FAIL name=%s version=%s: %s", fixture.name, fixture.version, redactError(err, s.secrets...))
			} else {
				if err = s.recordSkill(fixture.name); err != nil {
					return addError(failure, err)
				}
			}
		}
		if setupFailed {
			return failure
		}
		if err = s.console.publishSkill(ctx, o.namespace, weatherSkill, "1.0.0"); err != nil {
			return err
		}
	}
	s.sdk, err = api.InitContextByConfig(cfg)
	if err != nil {
		return err
	}
	defer s.sdk.Destroy()
	s.skills = api.NewSkillAPIByContext(s.sdk)
	s.consumer = api.NewConsumerAPIByContext(s.sdk)
	s.provider = api.NewProviderAPIByContext(s.sdk)
	if suiteIncludes(o.suite, "agent") {
		if err = s.prepareAgentFixture(ctx); err != nil {
			return err
		}
	}
	cases := []struct {
		id, name, group string
		run             func(context.Context) error
	}{
		{"1.1", "shared-sdk-context", "agent", s.sharedContext},
		{"1.2", "console-fixture-consumer-readback", "agent", s.agentFixture},
		{"1.3", "ordinary-service-discovery-type", "agent", s.ordinaryService},
		{"1.4", "agentcard-only-auto-registration", "agent", s.agentCardOnly},
		{"1.5", "discovered-agentcard-real-request", "agent", s.invokeAgent},
		{"1.6", "automatic-heartbeat-remains-healthy", "agent", s.heartbeat},
		{"1.7", "agentcard-v03-multi-business-port-single-instance", "agent", s.legacyAgentCard},
		{"1.8", "explicit-registration-port-and-business-version", "agent", s.explicitRegistrationPort},
		{"2.3", "discover-mounted-skills-and-clone-snapshot", "agent", s.discoverMounted},
		{"2.4", "normal-services-and-agent-concurrent-query", "agent", s.concurrentDiscover},
		{"3.1", "published-explicit-and-active-version", "skill", s.getPublished},
		{"3.2", "draft-and-missing-skill-errors", "skill", s.missingSkills},
		{"3.3", "published-filter-and-pagination", "skill", s.skillPagination},
		{"3.4", "markdown-download-and-shared-input", "skill", s.downloadMarkdown},
		{"3.5", "multiframe-zip-and-file-integrity", "skill", s.downloadZIP},
		{"4.1", "install-and-cache-hit", "skill", s.installCache},
		{"4.2", "tampered-entry-and-nested-file-recovery", "skill", s.repairCache},
		{"4.3", "forced-and-concurrent-install", "skill", s.concurrentInstall},
		{"4.4", "active-version-change-and-pinned-cache", "skill", s.activeVersion},
		{"4.5", "provider-loads-published-skill-and-invokes", "skill", s.skillBackedProvider},
		{"5.1", "healthy-and-isolated-instance-filtering", "agent", s.filterInstances},
		{"5.2", "configuration-read-watch-and-cancel", "agent", s.configRegression},
		{"5.3", "provider-deregister-and-stop-heartbeat", "skill", s.deregisterProvider},
		{"5.4", "sdk-destroy-rejects-consumer-and-skill", "agent", s.destroyedAPI},
		{"6.1", "mcp-streamable-http-metadata-and-discovery", "mcp", s.mcpStreamableHTTP},
		{"6.2", "mcp-sse-endpoint-and-metadata", "mcp", s.mcpSSE},
		{"6.3", "mcp-heartbeat-and-deregister", "mcp", s.mcpLifecycle},
	}
	for _, test := range cases {
		if !suiteIncludes(o.suite, test.group) {
			continue
		}
		start := time.Now()
		log.Printf("RUN [\u7528\u4f8b %s %s]", test.id, test.name)
		err := test.run(ctx)
		result := caseResult{ID: test.id, Name: test.name, Status: "PASS", Milliseconds: time.Since(start).Milliseconds()}
		if err != nil {
			result.Status, result.Error = "FAIL", redactError(err, s.secrets...)
			failure = addError(failure, fmt.Errorf("[%s] %s: %s", test.id, test.name, result.Error))
		}
		s.results = append(s.results, result)
		log.Printf("[\u7528\u4f8b %s %s] %s (%dms) %s", test.id, test.name, result.Status, result.Milliseconds, result.Error)
		if ctx.Err() != nil {
			break
		}
	}
	return failure
}

func redactError(err error, secrets ...string) string {
	text := err.Error()
	for _, key := range []string{"POLARIS_TOKEN", "POLARIS_CONSOLE_TOKEN", "POLARIS_SKILL_TOKEN", "POLARIS_CONFIG_TOKEN", "POLARIS_SERVICE_TOKEN", "POLARIS_E2E_SERVICE_TOKEN"} {
		if value := os.Getenv(key); value != "" {
			text = strings.ReplaceAll(text, value, "[redacted]")
		}
	}
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[redacted]")
		}
	}
	return text
}

func (s *suite) saveState() error {
	return writeJSON(filepath.Join(s.opts.runDir, "state.json"), s.state)
}
func (s *suite) serviceName(label string) string { return servicePrefix + s.opts.runID + "-" + label }
func (s *suite) recordSkill(name string) error {
	for _, existing := range s.state.Skills {
		if name == existing {
			return nil
		}
	}
	s.state.Skills = append(s.state.Skills, name)
	return s.saveState()
}
func (s *suite) recordService(name string) error {
	for _, existing := range s.state.Services {
		if existing == name {
			return nil
		}
	}
	s.state.Services = append(s.state.Services, name)
	return s.saveState()
}
func (s *suite) serviceMetadata() map[string]string {
	return map[string]string{ownerLabel: e2eOwner, runLabel: s.opts.runID, "business": "customer-ai"}
}
func (s *suite) getAgent(name string) (*model.AgentService, error) {
	r := &api.GetAllInstancesRequest{GetAllInstancesRequest: model.GetAllInstancesRequest{Namespace: s.opts.namespace, Service: name}}
	r.SetTimeout(2 * time.Second)
	r.SetRetryCount(0)
	response, err := s.consumer.GetAllInstances(r)
	if err != nil {
		return nil, err
	}
	if response == nil || response.NotExists {
		return nil, model.NewSDKError(model.ErrCodeServiceNotFound, nil, "service %s/%s not found", s.opts.namespace, name)
	}
	return &model.AgentService{Namespace: response.Namespace, Name: response.Service,
		Type: response.ServiceType, Metadata: response.Metadata, Revision: response.ServiceRevision,
		ExtendedMetadata: response.ExtendedMetadata}, nil
}
func skillValues(namespace, name, version, description string) model.AgentSkill {
	return model.AgentSkill{ID: namespace + ":" + name + "@" + version, Name: name, Version: version, Description: description,
		Tags: []string{"customer-ai", "fixture"}, Examples: []string{"Use the published reference"}, InputModes: []string{"text"}, OutputModes: []string{"text"}}
}
func mountedSkills(service *model.AgentService) []*model.AgentSkill {
	var result []*model.AgentSkill
	if service != nil {
		for _, ext := range service.ExtendedMetadata {
			if ext != nil && ext.Type == model.ExtendedMetadataSkill && ext.AgentSkill != nil {
				result = append(result, ext.AgentSkill)
			}
		}
	}
	return result
}
func requireIDs(service *model.AgentService, expected ...string) error {
	var actual []string
	for _, skill := range mountedSkills(service) {
		actual = append(actual, skill.ID)
	}
	sort.Strings(actual)
	sort.Strings(expected)
	if strings.Join(actual, "\x00") != strings.Join(expected, "\x00") {
		return fmt.Errorf("mounted IDs differ: actual=%v expected=%v", actual, expected)
	}
	return nil
}

func (s *suite) sharedContext(_ context.Context) error {
	for _, owner := range []api.SDKOwner{s.skills, s.consumer, s.provider} {
		if owner.SDKContext() != s.sdk {
			return fmt.Errorf("API did not reuse SDKContext")
		}
	}
	return nil
}
func (s *suite) agentCardOnly(ctx context.Context) error {
	ready, err := s.startProvider(ctx, "card-only", "")
	if err != nil {
		return err
	}
	s.cardProvider = ready
	if err = s.markProviderService(ctx, ready); err != nil {
		return err
	}
	return eventually(ctx, 20*time.Second, func() error {
		service, err := s.getAgent(ready.Service)
		if err != nil {
			return err
		}
		if service.Type != model.ServiceTypeAIAgent || len(mountedSkills(service)) != 0 {
			return fmt.Errorf("auto-created service has wrong type or mounted data")
		}
		return nil
	})
}
func (s *suite) markProviderService(ctx context.Context, ready providerReady) error {
	return s.markOwnedService(ctx, ready.Service)
}

func (s *suite) markOwnedService(ctx context.Context, service string) error {
	if err := eventually(ctx, 20*time.Second, func() error {
		record, err := s.console.getService(ctx, s.opts.namespace, service)
		if err != nil {
			return err
		}
		if record == nil {
			return fmt.Errorf("auto-created service not visible")
		}
		if record.Metadata[ownerLabel] != "" &&
			(record.Metadata[ownerLabel] != e2eOwner || record.Metadata[runLabel] != s.opts.runID) {
			return fmt.Errorf("refuse to mark an unowned auto-created service")
		}
		return nil
	}); err != nil {
		return err
	}
	_, err := s.console.request(ctx, http.MethodPut, "/naming/v1/services", []map[string]interface{}{{
		"namespace": s.opts.namespace, "name": service, "metadata": s.serviceMetadata(),
	}})
	if err != nil {
		return err
	}
	return eventually(ctx, 15*time.Second, func() error {
		record, err := s.console.getService(ctx, s.opts.namespace, service)
		if err != nil {
			return err
		}
		if record == nil || record.Metadata[ownerLabel] != e2eOwner || record.Metadata[runLabel] != s.opts.runID {
			return fmt.Errorf("service ownership labels not yet visible")
		}
		return nil
	})
}
func (s *suite) waitInstance(ctx context.Context, ready providerReady, present bool) error {
	return eventually(ctx, 25*time.Second, func() error {
		request := &api.GetAllInstancesRequest{GetAllInstancesRequest: model.GetAllInstancesRequest{Namespace: s.opts.namespace, Service: ready.Service}}
		request.SetTimeout(2 * time.Second)
		request.SetRetryCount(0)
		response, err := s.consumer.GetAllInstances(request)
		if err != nil {
			return err
		}
		for _, instance := range response.GetInstances() {
			if instance.GetId() == ready.InstanceID {
				if !present {
					return fmt.Errorf("deregistered instance remains visible")
				}
				metadata := instance.GetMetadata()
				if instance.GetHost() != ready.Host || instance.GetPort() != uint32(ready.RegistrationPort) ||
					instance.GetVersion() != ready.BusinessVersion || instance.GetProtocol() != "" ||
					metadata[runLabel] != s.opts.runID ||
					metadata[model.MetadataKeyAIAgentName] != ready.Service ||
					metadata[model.MetadataKeyAIAgentCard] != ready.CardJSON ||
					metadata[model.MetadataKeyA2AVersion] != ready.A2AVersion ||
					!instance.IsHealthy() || instance.IsIsolated() {
					return fmt.Errorf("instance metadata/health differs")
				}
				return nil
			}
		}
		if present {
			return fmt.Errorf("registered instance is not visible")
		}
		return nil
	})
}
func requestBusiness(ctx context.Context, address string) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("business HTTP %d", resp.StatusCode)
	}
	var payload map[string]string
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}
func (s *suite) invokeReady(ctx context.Context, ready providerReady, withSkill bool) error {
	if ready.InstanceID == "" {
		return fmt.Errorf("provider was not started")
	}
	if err := s.waitInstance(ctx, ready, true); err != nil {
		return err
	}
	r := &api.GetInstancesRequest{GetInstancesRequest: model.GetInstancesRequest{Namespace: s.opts.namespace, Service: ready.Service}}
	response, err := s.consumer.GetInstances(r)
	if err != nil {
		return err
	}
	for _, instance := range response.GetInstances() {
		if instance.GetId() != ready.InstanceID {
			continue
		}
		metadata := instance.GetMetadata()
		if metadata[model.MetadataKeyAIAgentName] != ready.Service ||
			metadata[model.MetadataKeyA2AVersion] != ready.A2AVersion ||
			metadata[model.MetadataKeyAIAgentCard] != ready.CardJSON {
			return fmt.Errorf("discovered AgentCard differs")
		}
		message := "weather and currency request"
		payload, err := requestBusiness(ctx, ready.URL+"?message="+url.QueryEscape(message))
		if err != nil {
			return err
		}
		if payload["reply"] != "Echo: "+message || payload["service"] != ready.Service {
			return fmt.Errorf("wrong business reply")
		}
		if withSkill && !strings.Contains(payload["instructions"], "version 1.0.0") {
			return fmt.Errorf("provider did not load the published Skill")
		}
		return nil
	}
	return fmt.Errorf("healthy instance missing")
}
func (s *suite) invokeAgent(ctx context.Context) error {
	return s.invokeReady(ctx, s.cardProvider, false)
}
func (s *suite) heartbeat(ctx context.Context) error {
	if s.cardProvider.InstanceID == "" {
		return fmt.Errorf("provider was not started")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
	}
	return s.waitInstance(ctx, s.cardProvider, true)
}

func (s *suite) legacyAgentCard(ctx context.Context) error {
	ready, err := s.startProviderWithOptions(ctx, providerStartOptions{
		label: "legacy-card", cardVersion: agentCardSchemaVersion03, multiBusinessPort: true,
	})
	if err != nil {
		return err
	}
	s.legacyProvider = ready
	if err = s.markProviderService(ctx, ready); err != nil {
		return err
	}
	if err = s.waitInstance(ctx, ready, true); err != nil {
		return err
	}
	request := &api.GetAllInstancesRequest{GetAllInstancesRequest: model.GetAllInstancesRequest{
		Namespace: s.opts.namespace, Service: ready.Service,
	}}
	request.SetTimeout(2 * time.Second)
	request.SetRetryCount(0)
	response, err := s.consumer.GetAllInstances(request)
	if err != nil {
		return err
	}
	if len(response.GetInstances()) != 1 {
		return fmt.Errorf("one AgentCard must produce one instance, got %d", len(response.GetInstances()))
	}
	primaryURL, err := url.Parse(ready.URL)
	if err != nil {
		return err
	}
	secondaryURL, err := url.Parse(ready.SecondaryURL)
	if err != nil {
		return err
	}
	if primaryURL.Hostname() != secondaryURL.Hostname() || primaryURL.Port() == secondaryURL.Port() {
		return fmt.Errorf("AgentCard interfaces must share one IP and use different business ports")
	}
	message := "secondary business port"
	payload, err := requestBusiness(ctx, ready.SecondaryURL+"?message="+url.QueryEscape(message))
	if err != nil {
		return err
	}
	if payload["reply"] != "Echo: "+message || payload["service"] != ready.Service {
		return fmt.Errorf("wrong secondary business reply")
	}
	return s.invokeReady(ctx, ready, false)
}

func (s *suite) explicitRegistrationPort(ctx context.Context) error {
	const (
		registrationPort = 18881
		businessVersion  = "2026.11-explicit"
	)
	ready, err := s.startProviderWithOptions(ctx, providerStartOptions{
		label: "explicit-port", cardVersion: agentCardSchemaVersion10,
		businessVersion: businessVersion, registrationPort: registrationPort,
	})
	if err != nil {
		return err
	}
	if ready.RegistrationPort != registrationPort || ready.BusinessVersion != businessVersion {
		return fmt.Errorf("provider readiness does not preserve explicit port or business version")
	}
	if err = s.markProviderService(ctx, ready); err != nil {
		return err
	}
	if err = s.waitInstance(ctx, ready, true); err != nil {
		return err
	}
	return s.invokeReady(ctx, ready, false)
}

func mcpEndpoint(instance model.Instance, scheme string) (string, error) {
	if instance == nil {
		return "", fmt.Errorf("MCP instance is nil")
	}
	protocol := instance.GetProtocol()
	if protocol != "mcp-streamable-http" && protocol != "mcp-sse" {
		return "", fmt.Errorf("unsupported MCP protocol %q", protocol)
	}
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("unsupported MCP endpoint scheme %q", scheme)
	}
	path := instance.GetMetadata()[model.MCPMetadataKeyEndpointPath]
	if path == "" {
		if protocol == "mcp-sse" {
			path = "/sse"
		} else {
			path = "/mcp"
		}
	}
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") ||
		strings.ContainsAny(path, "?#") {
		return "", fmt.Errorf("invalid MCP endpoint path %q", path)
	}
	return scheme + "://" + net.JoinHostPort(instance.GetHost(), fmt.Sprint(instance.GetPort())) + path, nil
}

func (s *suite) registerMCP(
	ctx context.Context, label, protocol, protocolVersion, path string,
) (*model.InstanceRegisterResponse, *api.InstanceRegisterRequest, error) {
	service := s.serviceName(label)
	if err := s.recordService(service); err != nil {
		return nil, nil, err
	}
	host, err := routeHost(s.opts.discover)
	if err != nil {
		return nil, nil, err
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return nil, nil, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err = listener.Close(); err != nil {
		return nil, nil, err
	}
	serviceVersion := "v1"
	metadata, err := model.BuildMCPServerMetadata(&model.MCPServerMetadataRequest{
		ServerName: service, ProtocolVersion: protocolVersion,
		Transport: model.MCPTransport(protocol), EndpointPath: path,
	})
	if err != nil {
		return nil, nil, err
	}
	request := &api.InstanceRegisterRequest{InstanceRegisterRequest: model.InstanceRegisterRequest{
		Namespace: s.opts.namespace,
		Service:   service,
		Host:      host,
		Port:      port,
		Protocol:  &protocol,
		Version:   &serviceVersion,
		Metadata:  metadata,
		ServiceToken: environment(
			"POLARIS_E2E_SERVICE_TOKEN", "POLARIS_SERVICE_TOKEN",
		),
	}}
	if err = model.ValidateMCPServerMetadata(&model.MCPServerValidationRequest{
		Namespace: s.opts.namespace, Service: service, ServerName: service,
		ProtocolVersion: protocolVersion, Transport: model.MCPTransport(protocol),
		EndpointPath: path, Metadata: metadata,
	}); err != nil {
		return nil, nil, err
	}
	request.SetTTL(1)
	registered, err := s.provider.RegisterInstance(request)
	if err != nil {
		return nil, nil, err
	}
	if err = s.markOwnedService(ctx, service); err != nil {
		_ = s.provider.Deregister(&api.InstanceDeRegisterRequest{InstanceDeRegisterRequest: model.InstanceDeRegisterRequest{
			InstanceID: registered.InstanceID, Namespace: s.opts.namespace, Service: service,
			Host: host, Port: port, ServiceToken: request.ServiceToken,
		}})
		return nil, nil, err
	}
	s.state.Instances = append(s.state.Instances, instanceState{
		ID: registered.InstanceID, Service: service,
	})
	if err = s.saveState(); err != nil {
		_ = s.provider.Deregister(&api.InstanceDeRegisterRequest{InstanceDeRegisterRequest: model.InstanceDeRegisterRequest{
			InstanceID: registered.InstanceID, Namespace: s.opts.namespace, Service: service,
			Host: host, Port: port, ServiceToken: request.ServiceToken,
		}})
		return nil, nil, err
	}
	return registered, request, nil
}

func (s *suite) deregisterMCP(
	registered *model.InstanceRegisterResponse, request *api.InstanceRegisterRequest,
) error {
	if registered == nil || request == nil {
		return nil
	}
	deregister := &api.InstanceDeRegisterRequest{InstanceDeRegisterRequest: model.InstanceDeRegisterRequest{
		InstanceID: registered.InstanceID, Namespace: request.Namespace, Service: request.Service,
		Host: request.Host, Port: request.Port, ServiceToken: request.ServiceToken,
	}}
	deregister.SetTimeout(s.opts.timeout)
	return s.provider.Deregister(deregister)
}

func (s *suite) waitMCPInstance(
	ctx context.Context, request *api.InstanceRegisterRequest, instanceID string, present bool,
) error {
	return eventually(ctx, 20*time.Second, func() error {
		getAll := &api.GetAllInstancesRequest{GetAllInstancesRequest: model.GetAllInstancesRequest{
			Namespace: request.Namespace, Service: request.Service,
		}}
		getAll.SetTimeout(2 * time.Second)
		response, err := s.consumer.GetAllInstances(getAll)
		if err != nil {
			return err
		}
		var found model.Instance
		for _, instance := range response.GetInstances() {
			if instance.GetId() == instanceID {
				found = instance
				break
			}
		}
		if !present {
			if found != nil {
				return fmt.Errorf("MCP instance remains visible after deregister")
			}
			return nil
		}
		if found == nil {
			return fmt.Errorf("MCP instance is not visible")
		}
		if !found.IsHealthy() || found.IsIsolated() || found.GetWeight() <= 0 {
			return fmt.Errorf("MCP instance is not selectable")
		}
		if found.GetProtocol() != *request.Protocol || found.GetVersion() != *request.Version {
			return fmt.Errorf("MCP protocol/version differs")
		}
		for _, key := range []string{
			model.MCPMetadataKeyServerName,
			model.MCPMetadataKeyProtocolVersion,
			model.MCPMetadataKeyEndpointPath,
		} {
			if found.GetMetadata()[key] != request.Metadata[key] {
				return fmt.Errorf("MCP metadata %s differs", key)
			}
		}
		return nil
	})
}

func (s *suite) mcpStreamableHTTP(ctx context.Context) (result error) {
	registered, request, err := s.registerMCP(
		ctx, "mcp-http", "mcp-streamable-http", "2025-06-18,2025-03-26", "/mcp")
	if err != nil {
		return err
	}
	defer func() {
		result = addError(result, s.deregisterMCP(registered, request))
	}()
	if err = s.waitMCPInstance(ctx, request, registered.InstanceID, true); err != nil {
		return err
	}
	selected, err := s.consumer.GetOneInstance(&api.GetOneInstanceRequest{GetOneInstanceRequest: model.GetOneInstanceRequest{
		Namespace: request.Namespace, Service: request.Service,
	}})
	if err != nil {
		return err
	}
	instance := selected.GetInstance()
	if instance == nil || instance.GetId() != registered.InstanceID {
		return fmt.Errorf("MCP GetOneInstance did not select the registered instance")
	}
	endpoint, err := mcpEndpoint(instance, "http")
	if err != nil || !strings.HasSuffix(endpoint, "/mcp") {
		return fmt.Errorf("MCP endpoint differs: %q, %v", endpoint, err)
	}
	contract := &model.ServiceContract{
		GetServiceContractRequest: model.GetServiceContractRequest{
			Name:      request.Service,
			Namespace: request.Namespace,
			Service:   request.Service,
			Protocol:  *request.Protocol,
			Version:   *request.Version,
		},
		ServiceFeatures: []*model.ServiceFeature{
			{
				Type: model.ServiceFeatureTypeMCPTool, Name: "echo_text", Description: "Echo text",
				Status: model.ServiceFeatureEnabled, Content: `{"name":"echo_text","description":"Echo text"}`,
			},
			{
				Type: model.ServiceFeatureTypeMCPResource, Name: "fixture", Description: "Fixture resource",
				Status: model.ServiceFeatureEnabled, Content: `{"name":"fixture","uri":"fixture://mcp"}`,
			},
			{
				Type: model.ServiceFeatureTypeMCPPrompt, Name: "summarize", Description: "Summarize text",
				Status: model.ServiceFeatureDisabled, Content: `{"name":"summarize"}`,
			},
		},
		Interfaces: []*model.InterfaceDescriptor{
			{Method: "tools/list", Path: "/mcp"},
			{Method: "notifications/initialized", Path: "/mcp"},
		},
	}
	_, err = s.provider.ReportServiceContract(&api.ReportServiceContractRequest{ServiceContract: contract})
	if err != nil {
		return err
	}
	return eventually(ctx, 30*time.Second, func() error {
		response, getErr := s.consumer.GetServiceContract(&api.GetServiceContractRequest{
			GetServiceContractRequest: model.GetServiceContractRequest{
				Namespace: request.Namespace, Service: request.Service, Name: request.Service,
				Protocol: *request.Protocol, Version: *request.Version,
			},
		})
		if getErr != nil {
			return getErr
		}
		got, ok := response.GetValue().(*model.ServiceContract)
		if !ok || got == nil {
			return fmt.Errorf("Service Contract response has no contract")
		}
		if got.Namespace != request.Namespace || got.Service != request.Service ||
			got.Protocol != *request.Protocol || got.Version != *request.Version || got.Revision == "" {
			return fmt.Errorf("MCP contract identity differs: %s", model.JSONString(got))
		}
		query := url.Values{
			"namespace": {request.Namespace}, "service": {request.Service},
			"protocol": {*request.Protocol}, "version": {*request.Version},
		}
		features, queryErr := s.console.request(ctx, http.MethodGet,
			"/naming/v1/service/contract/features?"+query.Encode(), nil)
		if queryErr != nil {
			return queryErr
		}
		featureNames := make(map[string]bool)
		for _, item := range features.Data {
			featureNames[item.Name] = true
		}
		for _, name := range []string{"echo_text", "fixture", "summarize"} {
			if !featureNames[name] {
				return fmt.Errorf("stored MCP feature %s is missing: %v", name, featureNames)
			}
		}
		for _, method := range []string{"tools/list", "notifications/initialized"} {
			methodQuery := url.Values{
				"namespace": {request.Namespace}, "service": {request.Service},
				"protocol": {*request.Protocol}, "version": {*request.Version},
				"method": {method},
			}
			methods, methodErr := s.console.request(ctx, http.MethodGet,
				"/naming/v1/service/contract/methods?"+methodQuery.Encode(), nil)
			if methodErr != nil {
				return methodErr
			}
			if len(methods.Data) != 1 || methods.Data[0].Path != "/mcp" {
				return fmt.Errorf("stored MCP method %s differs: %+v", method, methods.Data)
			}
		}
		return nil
	})
}

func (s *suite) mcpSSE(ctx context.Context) (result error) {
	registered, request, err := s.registerMCP(ctx, "mcp-sse", "mcp-sse", "2025-03-26", "/sse")
	if err != nil {
		return err
	}
	defer func() {
		result = addError(result, s.deregisterMCP(registered, request))
	}()
	if err = s.waitMCPInstance(ctx, request, registered.InstanceID, true); err != nil {
		return err
	}
	instances, err := s.consumer.GetInstances(&api.GetInstancesRequest{GetInstancesRequest: model.GetInstancesRequest{
		Namespace: request.Namespace, Service: request.Service,
	}})
	if err != nil {
		return err
	}
	var endpoint string
	for _, instance := range instances.GetInstances() {
		if instance.GetId() == registered.InstanceID {
			endpoint, err = mcpEndpoint(instance, "http")
			break
		}
	}
	if err != nil || !strings.HasSuffix(endpoint, "/sse") {
		return fmt.Errorf("MCP SSE endpoint differs: %q, %v", endpoint, err)
	}
	return nil
}

func (s *suite) mcpLifecycle(ctx context.Context) (result error) {
	registered, request, err := s.registerMCP(
		ctx, "mcp-lifecycle", "mcp-streamable-http", "2025-06-18", "/mcp")
	if err != nil {
		return err
	}
	if err = s.waitMCPInstance(ctx, request, registered.InstanceID, true); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(3 * time.Second):
	}
	if err = s.waitMCPInstance(ctx, request, registered.InstanceID, true); err != nil {
		return err
	}
	if err = s.deregisterMCP(registered, request); err != nil {
		return err
	}
	return s.waitMCPInstance(ctx, request, registered.InstanceID, false)
}

func (s *suite) discoverMounted(ctx context.Context) error {
	return eventually(ctx, 25*time.Second, func() error {
		service, err := s.getAgent(s.serviceName("agent"))
		if err != nil {
			return err
		}
		if err = requireIDs(service, s.opts.namespace+":"+currencySkill+"@1.0.0", s.opts.namespace+":"+weatherSkill+"@1.0.0"); err != nil {
			return err
		}
		service.Metadata[runLabel] = "caller-mutation"
		mountedSkills(service)[0].Description = "caller-mutation"
		again, err := s.getAgent(s.serviceName("agent"))
		if err != nil {
			return err
		}
		if again.Metadata[runLabel] != s.opts.runID || mountedSkills(again)[0].Description == "caller-mutation" {
			return fmt.Errorf("caller mutation leaked into cached snapshot")
		}
		return nil
	})
}
func (s *suite) concurrentDiscover(ctx context.Context) error {
	var wait sync.WaitGroup
	failures := make(chan error, 24)
	for i := 0; i < 12; i++ {
		wait.Add(2)
		go func() {
			defer wait.Done()
			failures <- eventually(ctx, 15*time.Second, func() error {
				_, err := s.getAgent(s.serviceName("agent"))
				return err
			})
		}()
		go func() {
			defer wait.Done()
			r := &api.GetServicesRequest{GetServicesRequest: model.GetServicesRequest{Namespace: s.opts.namespace, Business: s.serviceName("agent")}}
			r.SetTimeout(3 * time.Second)
			r.SetRetryCount(0)
			_, err := s.consumer.GetServices(r)
			failures <- err
		}()
	}
	wait.Wait()
	close(failures)
	var result error
	for err := range failures {
		result = addError(result, err)
	}
	return result
}
func (s *suite) getPublished(ctx context.Context) error {
	return eventually(ctx, 20*time.Second, func() error {
		for _, version := range []string{"1.0.0", "2.0.0", ""} {
			resource, err := s.skills.GetSkill(&api.GetSkillRequest{GetSkillRequest: model.GetSkillRequest{Namespace: s.opts.namespace, Name: weatherSkill, Version: version}})
			if err != nil {
				return err
			}
			expected := version
			if expected == "" {
				expected = "1.0.0"
			}
			if resource.Namespace != s.opts.namespace || resource.Name != weatherSkill || resource.Version != expected {
				return fmt.Errorf("published Skill content/version differs")
			}
			if resource.VersionDetail == nil || resource.VersionDetail.Storage == nil ||
				resource.VersionDetail.Storage.Provider == "" {
				return fmt.Errorf("published Skill did not expose storage information")
			}
			if err := validateFixtureFile(weatherSkill, expected, "SKILL.md", []byte(resource.Content)); err != nil {
				return err
			}
		}
		persistDir := s.sdk.GetConfig().GetSkill().GetPersistDir()
		activePath := filepath.Join(persistDir, "active", url.QueryEscape(s.opts.namespace)+"#"+url.QueryEscape(weatherSkill))
		if err := eventually(ctx, 10*time.Second, func() error {
			data, readErr := ioutil.ReadFile(activePath)
			if readErr != nil {
				return readErr
			}
			if strings.TrimSpace(string(data)) != "1.0.0" {
				return fmt.Errorf("persisted active version is %q", data)
			}
			return nil
		}); err != nil {
			return err
		}
		offlineCfg := config.NewDefaultConfiguration([]string{s.opts.discover})
		offlineCfg.GetGlobal().GetAPI().SetTimeout(2 * time.Second)
		offlineCfg.GetGlobal().GetAPI().SetMaxRetryTimes(0)
		offlineCfg.GetGlobal().GetAdmin().SetPort(0)
		offlineCfg.GetGlobal().GetStatReporter().SetEnable(false)
		offlineCfg.GetSkill().SetEnable(true)
		offlineCfg.GetSkill().SetAddresses([]string{"127.0.0.1:1"})
		offlineCfg.GetSkill().SetConnectTimeout(200 * time.Millisecond)
		offlineCfg.GetSkill().SetMessageTimeout(200 * time.Millisecond)
		offlineCfg.GetSkill().SetPersistEnable(true)
		offlineCfg.GetSkill().SetPersistDir(persistDir)
		offlineCfg.GetSkill().SetFallbackToLocalCache(true)
		offline, openErr := api.NewSkillAPIByConfig(offlineCfg)
		if openErr != nil {
			return openErr
		}
		cached, getErr := offline.GetSkill(&api.GetSkillRequest{GetSkillRequest: model.GetSkillRequest{
			Namespace: s.opts.namespace, Name: weatherSkill, Version: "1.0.0",
		}})
		offline.Close()
		if getErr != nil {
			return fmt.Errorf("network fallback failed: %w", getErr)
		}
		if !cached.FromCache {
			return fmt.Errorf("fallback did not return the persisted Skill payload")
		}
		if err := validateFixtureFile(weatherSkill, "1.0.0", "SKILL.md", []byte(cached.Content)); err != nil {
			return fmt.Errorf("persisted Skill payload differs: %w", err)
		}
		return nil
	})
}
func (s *suite) missingSkills(_ context.Context) error {
	for _, name := range []string{draftSkill, "polaris-go-ai-e2e-missing-" + s.opts.runID} {
		_, err := s.skills.GetSkill(&api.GetSkillRequest{GetSkillRequest: model.GetSkillRequest{Namespace: s.opts.namespace, Name: name, Version: "1.0.0"}})
		if model.GetErrorCodeFromError(err) != model.ErrCodeSkillNotFound {
			return fmt.Errorf("expected SkillNotFound for %s, got %v", name, err)
		}
	}
	return nil
}
func (s *suite) skillPagination(ctx context.Context) error {
	timeout := s.opts.timeout
	if timeout < 120*time.Second {
		timeout = 120 * time.Second
	}
	return eventually(ctx, timeout, func() error {
		all, err := s.skills.GetSkillListResult(&api.GetSkillListRequest{GetSkillListRequest: model.GetSkillListRequest{
			Namespace: s.opts.namespace, Limit: 10, OrderBy: "ctime", OrderType: "asc",
		}})
		if err != nil {
			return err
		}
		if all.Total != 2 || len(all.Resources) != 2 {
			return fmt.Errorf("published fixture list total=%d size=%d", all.Total, len(all.Resources))
		}
		var names []string
		for offset := uint32(0); offset < 3; offset++ {
			list, listErr := s.skills.GetSkillList(&api.GetSkillListRequest{GetSkillListRequest: model.GetSkillListRequest{
				Namespace: s.opts.namespace, Offset: offset, Limit: 1,
				OrderBy: "ctime", OrderType: "asc",
			}})
			if listErr != nil {
				return listErr
			}
			if offset < 2 {
				if len(list) != 1 {
					return fmt.Errorf("pagination offset %d returned %d", offset, len(list))
				}
				if !containsString(list[0].Tags, fixtureTag) {
					return fmt.Errorf("published fixture %s has no fixture tag", list[0].Name)
				}
				names = append(names, list[0].Name)
			} else if len(list) != 0 {
				return fmt.Errorf("published list included a draft or extra fixture")
			}
		}
		sort.Strings(names)
		expected := []string{weatherSkill, currencySkill}
		sort.Strings(expected)
		if strings.Join(names, ",") != strings.Join(expected, ",") {
			return fmt.Errorf("published pagination skipped or duplicated fixtures: %v", names)
		}
		result, err := s.skills.GetSkillListResult(&api.GetSkillListRequest{GetSkillListRequest: model.GetSkillListRequest{
			Namespace: s.opts.namespace, Limit: 1,
		}})
		if err != nil {
			return err
		}
		list, err := s.skills.GetSkillList(&api.GetSkillListRequest{GetSkillListRequest: model.GetSkillListRequest{
			Namespace: s.opts.namespace, Name: weatherSkill, Limit: 10,
		}})
		if err != nil {
			return err
		}
		if len(list) != 1 || list[0].Name != weatherSkill || !containsString(list[0].Tags, fixtureTag) {
			return fmt.Errorf("name filter or fixture tag differs")
		}
		if result.Total < uint32(len(result.Resources)) || result.Total != 2 {
			return fmt.Errorf("Skill list total %d does not preserve the server pagination total", result.Total)
		}
		return nil
	})
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
func (s *suite) downloadMarkdown(_ context.Context) error {
	r := &api.SkillDownloadRequest{SkillDownloadRequest: model.SkillDownloadRequest{Namespace: s.opts.namespace, Name: weatherSkill, Version: "1.0.0"}}
	var wait sync.WaitGroup
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			pkg, err := s.skills.DownloadSkill(r)
			if err == nil && (pkg.Format != model.SkillFormatMarkdown || pkg.Version != "1.0.0") {
				err = fmt.Errorf("Markdown content or default format differs")
			}
			if err == nil {
				err = validateFixtureFile(weatherSkill, "1.0.0", "SKILL.md", pkg.Content)
			}
			failures <- err
		}()
	}
	wait.Wait()
	close(failures)
	var result error
	for err := range failures {
		result = addError(result, err)
	}
	if r.Format != "" {
		return fmt.Errorf("Download mutated caller input")
	}
	return result
}
func (s *suite) downloadZIP(_ context.Context) error {
	pkg, err := s.skills.DownloadSkill(&api.SkillDownloadRequest{SkillDownloadRequest: model.SkillDownloadRequest{Namespace: s.opts.namespace, Name: weatherSkill, Version: "1.0.0", Format: model.SkillFormatZIP}})
	if err != nil {
		return err
	}
	if len(pkg.Content) <= 1024*1024 {
		return fmt.Errorf("ZIP fixture is too small to prove multiframe reassembly")
	}
	reader, err := zip.NewReader(bytes.NewReader(pkg.Content), int64(len(pkg.Content)))
	if err != nil {
		return err
	}
	expected := skillFiles(weatherSkill, "1.0.0")
	seen := map[string]bool{}
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		stream, err := file.Open()
		if err != nil {
			return err
		}
		content, err := ioutil.ReadAll(io.LimitReader(stream, 4<<20))
		stream.Close()
		if err != nil {
			return err
		}
		if err := validateFixtureFile(weatherSkill, "1.0.0", file.Name, content); err != nil {
			return err
		}
		seen[file.Name] = true
	}
	if len(seen) != len(expected) {
		return fmt.Errorf("ZIP lost files: seen=%d expected=%d", len(seen), len(expected))
	}
	return nil
}
func (s *suite) installRequest(version string, force bool) *api.SkillInstallRequest {
	return &api.SkillInstallRequest{SkillInstallRequest: model.SkillInstallRequest{Namespace: s.opts.namespace, Name: weatherSkill, Version: version, Force: force, CacheDir: filepath.Join(s.opts.runDir, "cache")}}
}
func (s *suite) installCache(_ context.Context) error {
	first, err := s.skills.Install(s.installRequest("1.0.0", false))
	if err != nil {
		return err
	}
	if first.CacheHit || first.Version != "1.0.0" {
		return fmt.Errorf("first install has wrong state")
	}
	s.installed = first
	second, err := s.skills.Install(s.installRequest("1.0.0", false))
	if err != nil {
		return err
	}
	if !second.CacheHit || second.Dir != first.Dir || second.Digest != first.Digest {
		return fmt.Errorf("second install is not a validated cache hit")
	}
	return s.verifyInstalled(second)
}
func (s *suite) verifyInstalled(installed *model.InstalledSkill) error {
	for path := range skillFiles(weatherSkill, installed.Version) {
		data, err := ioutil.ReadFile(filepath.Join(installed.Dir, filepath.FromSlash(path)))
		if err != nil {
			return err
		}
		if err := validateFixtureFile(weatherSkill, installed.Version, path, data); err != nil {
			return err
		}
	}
	return nil
}
func (s *suite) repairCache(_ context.Context) error {
	if s.installed == nil {
		return fmt.Errorf("initial install failed")
	}
	for _, path := range []string{"SKILL.md", "references/.polaris-skill.json"} {
		if err := ioutil.WriteFile(filepath.Join(s.installed.Dir, filepath.FromSlash(path)), []byte("local tamper"), 0600); err != nil {
			return err
		}
		installed, err := s.skills.Install(s.installRequest("1.0.0", false))
		if err != nil {
			return err
		}
		if installed.CacheHit {
			return fmt.Errorf("tampered file %s still returned cache hit", path)
		}
		if err = s.verifyInstalled(installed); err != nil {
			return err
		}
		s.installed = installed
	}
	return nil
}
func (s *suite) concurrentInstall(_ context.Context) error {
	installed, err := s.skills.Install(s.installRequest("1.0.0", true))
	if err != nil {
		return err
	}
	if installed.CacheHit {
		return fmt.Errorf("Force returned a cache hit")
	}
	var wait sync.WaitGroup
	failures := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			installed, err := s.skills.Install(s.installRequest("1.0.0", false))
			if err == nil {
				err = s.verifyInstalled(installed)
			}
			failures <- err
		}()
	}
	wait.Wait()
	close(failures)
	var result error
	for err := range failures {
		result = addError(result, err)
	}
	return result
}
func (s *suite) activeVersion(ctx context.Context) error {
	if err := s.console.publishSkill(ctx, s.opts.namespace, weatherSkill, "2.0.0"); err != nil {
		return err
	}
	var active *model.InstalledSkill
	if err := eventually(ctx, 25*time.Second, func() error {
		installed, err := s.skills.Install(s.installRequest("", false))
		if err != nil {
			return err
		}
		if installed.Version != "2.0.0" {
			return fmt.Errorf("active version remains %s", installed.Version)
		}
		active = installed
		return s.verifyInstalled(installed)
	}); err != nil {
		return err
	}
	pinned, err := s.skills.Install(s.installRequest("1.0.0", false))
	if err != nil {
		return err
	}
	if !pinned.CacheHit || pinned.Version != "1.0.0" || pinned.Dir == active.Dir {
		return fmt.Errorf("active transition replaced pinned cache")
	}
	return s.verifyInstalled(pinned)
}
func (s *suite) skillBackedProvider(ctx context.Context) error {
	ready, err := s.startProvider(ctx, "skill-provider", weatherSkill)
	if err != nil {
		return err
	}
	s.skillProvider = ready
	if err = s.markProviderService(ctx, ready); err != nil {
		return err
	}
	return s.invokeReady(ctx, ready, true)
}
func (s *suite) filterInstances(ctx context.Context) (result error) {
	if s.cardProvider.InstanceID == "" {
		return fmt.Errorf("provider was not started")
	}
	r := &api.InstanceRegisterRequest{InstanceRegisterRequest: model.InstanceRegisterRequest{Namespace: s.opts.namespace, Service: s.cardProvider.Service, Host: s.cardProvider.Host, Port: s.cardProvider.Port + 1,
		Metadata: map[string]string{ownerLabel: e2eOwner, runLabel: s.opts.runID}, ServiceToken: environment("POLARIS_E2E_SERVICE_TOKEN", "POLARIS_SERVICE_TOKEN")}}
	if r.Port > 65535 {
		r.Port = s.cardProvider.Port - 1
	}
	r.SetIsolate(true)
	registered, err := s.provider.Register(r)
	if err != nil {
		return err
	}
	s.state.Instances = append(s.state.Instances, instanceState{ID: registered.InstanceID, Service: r.Service})
	if err = s.saveState(); err != nil {
		return err
	}
	defer func() {
		result = addError(result, s.provider.Deregister(&api.InstanceDeRegisterRequest{InstanceDeRegisterRequest: model.InstanceDeRegisterRequest{InstanceID: registered.InstanceID, Namespace: r.Namespace, Service: r.Service, Host: r.Host, Port: r.Port, ServiceToken: r.ServiceToken}}))
	}()
	return eventually(ctx, 20*time.Second, func() error {
		all, err := s.consumer.GetAllInstances(&api.GetAllInstancesRequest{GetAllInstancesRequest: model.GetAllInstancesRequest{Namespace: r.Namespace, Service: r.Service}})
		if err != nil {
			return err
		}
		found := false
		for _, instance := range all.GetInstances() {
			if instance.GetId() == registered.InstanceID && instance.IsIsolated() {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("isolated fixture not yet visible")
		}
		filtered, err := s.consumer.GetInstances(&api.GetInstancesRequest{GetInstancesRequest: model.GetInstancesRequest{Namespace: r.Namespace, Service: r.Service}})
		if err != nil {
			return err
		}
		for _, instance := range filtered.GetInstances() {
			if instance.GetId() == registered.InstanceID {
				return fmt.Errorf("isolated instance escaped filtering")
			}
		}
		return nil
	})
}
func configPublicationMatches(event *model.ConfigGroupChangeEvent, expected model.SimpleConfigFile) bool {
	if event == nil || expected.Version == 0 || expected.Md5 == "" {
		return false
	}
	for _, file := range event.After {
		if file != nil && file.Namespace == expected.Namespace && file.FileGroup == expected.FileGroup &&
			file.FileName == expected.FileName && file.Version == expected.Version &&
			(file.Md5 == "" || file.Md5 == expected.Md5) {
			return true
		}
	}
	return false
}

func (s *suite) configRegression(ctx context.Context) error {
	group := s.serviceName("config")
	existing, err := s.console.configGroup(ctx, s.opts.namespace, group)
	if err != nil {
		return err
	}
	if existing != nil {
		return fmt.Errorf("refuse to reuse an existing run configuration group")
	}
	s.state.ConfigGroups = append(s.state.ConfigGroups, group)
	if err := s.saveState(); err != nil {
		return err
	}
	if _, err := s.console.request(ctx, http.MethodPost, "/config/v1/configfilegroups", map[string]string{"namespace": s.opts.namespace, "name": group, "comment": configComment(s.opts.runID)}); err != nil {
		return err
	}
	if _, err := s.console.request(ctx, http.MethodPost, "/config/v1/configfiles", map[string]string{"namespace": s.opts.namespace, "group": group, "name": "agent.yaml", "content": "generation: 0\n", "format": "yaml"}); err != nil {
		return err
	}
	files := api.NewConfigFileAPIBySDKContext(s.sdk)
	groups := api.NewConfigGroupAPIBySDKContext(s.sdk)
	if err := files.PublishConfigFile(s.opts.namespace, group, "agent.yaml"); err != nil {
		return err
	}
	var initialVersion uint64
	if err := eventually(ctx, 20*time.Second, func() error {
		file, err := files.FetchConfigFile(&api.GetConfigFileRequest{GetConfigFileRequest: &model.GetConfigFileRequest{Namespace: s.opts.namespace, FileGroup: group, FileName: "agent.yaml"}})
		if err != nil {
			return err
		}
		if file.GetContent() != "generation: 0\n" {
			return fmt.Errorf("initial config not visible")
		}
		initialVersion = file.GetVersion()
		return nil
	}); err != nil {
		return err
	}
	view, err := groups.FetchConfigGroup(&api.GetConfigGroupRequest{GetConfigGroupRequest: &model.GetConfigGroupRequest{Namespace: s.opts.namespace, FileGroup: group, Subscribe: true}})
	if err != nil {
		return err
	}
	cancelable, ok := view.(model.CancelableConfigFileGroup)
	if !ok {
		return fmt.Errorf("config group has no cancelable listener")
	}
	events := make(chan *model.ConfigGroupChangeEvent, 16)
	cancel := cancelable.AddChangeListenerWithCancel(func(event *model.ConfigGroupChangeEvent) {
		select {
		case events <- event:
		default:
		}
	})
	defer cancel()
	if err = files.UpdateConfigFile(s.opts.namespace, group, "agent.yaml", "generation: 1\n"); err != nil {
		return err
	}
	if err = files.PublishConfigFile(s.opts.namespace, group, "agent.yaml"); err != nil {
		return err
	}
	expected := model.SimpleConfigFile{Namespace: s.opts.namespace, FileGroup: group, FileName: "agent.yaml"}
	if err = eventually(ctx, 20*time.Second, func() error {
		file, err := files.FetchConfigFile(&api.GetConfigFileRequest{GetConfigFileRequest: &model.GetConfigFileRequest{Namespace: s.opts.namespace, FileGroup: group, FileName: "agent.yaml"}})
		if err != nil {
			return err
		}
		if file.GetContent() != "generation: 1\n" || file.GetVersion() <= initialVersion || file.GetMd5() == "" {
			return fmt.Errorf("first config update content and version not visible")
		}
		expected.Version, expected.Md5 = file.GetVersion(), file.GetMd5()
		return nil
	}); err != nil {
		return err
	}
	if err = eventually(ctx, 20*time.Second, func() error {
		select {
		case event := <-events:
			if configPublicationMatches(event, expected) {
				log.Printf("CONFIG publication callback matched file=%s/%s/%s version=%d", expected.Namespace, expected.FileGroup, expected.FileName, expected.Version)
				return nil
			}
			return fmt.Errorf("config callback does not match publication")
		default:
			return fmt.Errorf("config update callback not received")
		}
	}); err != nil {
		return err
	}
	cancel()
	cancel()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Second):
	}
	for len(events) > 0 {
		<-events
	}
	if err = files.UpdateConfigFile(s.opts.namespace, group, "agent.yaml", "generation: 2\n"); err != nil {
		return err
	}
	if err = files.PublishConfigFile(s.opts.namespace, group, "agent.yaml"); err != nil {
		return err
	}
	if err = eventually(ctx, 20*time.Second, func() error {
		file, err := files.FetchConfigFile(&api.GetConfigFileRequest{GetConfigFileRequest: &model.GetConfigFileRequest{Namespace: s.opts.namespace, FileGroup: group, FileName: "agent.yaml"}})
		if err != nil {
			return err
		}
		if file.GetContent() != "generation: 2\n" || file.GetVersion() <= expected.Version {
			return fmt.Errorf("second config publication not visible")
		}
		return nil
	}); err != nil {
		return err
	}
	select {
	case <-events:
		return fmt.Errorf("callback fired after cancellation and settled publication")
	case <-time.After(time.Second):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *suite) deregisterProvider(ctx context.Context) error {
	if s.skillProvider.InstanceID == "" {
		return fmt.Errorf("skill provider was not started")
	}
	for _, process := range s.state.Processes {
		if process.PID == s.skillProvider.PID {
			if err := stopProvider(process, s.opts.runID); err != nil {
				return err
			}
		}
	}
	if err := s.waitInstance(ctx, s.skillProvider, false); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(3 * time.Second):
	}
	return s.waitInstance(ctx, s.skillProvider, false)
}
func (s *suite) destroyedAPI(_ context.Context) error {
	cfg, err := loadConfiguration(s.opts, true)
	if err != nil {
		return err
	}
	sdk, err := api.InitContextByConfig(cfg)
	if err != nil {
		return err
	}
	skills := api.NewSkillAPIByContext(sdk)
	consumer := api.NewConsumerAPIByContext(sdk)
	if _, err = skills.GetSkill(&api.GetSkillRequest{GetSkillRequest: model.GetSkillRequest{Namespace: s.opts.namespace, Name: weatherSkill}}); err != nil {
		sdk.Destroy()
		return err
	}
	sdk.Destroy()
	_, err = skills.GetSkill(&api.GetSkillRequest{GetSkillRequest: model.GetSkillRequest{Namespace: s.opts.namespace, Name: weatherSkill}})
	if model.GetErrorCodeFromError(err) != model.ErrCodeInvalidStateError {
		return fmt.Errorf("destroyed SkillAPI did not reject call")
	}
	_, err = consumer.GetAllInstances(&api.GetAllInstancesRequest{GetAllInstancesRequest: model.GetAllInstancesRequest{Namespace: s.opts.namespace, Service: s.serviceName("agent")}})
	if model.GetErrorCodeFromError(err) != model.ErrCodeInvalidStateError {
		return fmt.Errorf("destroyed ConsumerAPI did not reject call")
	}
	return nil
}
