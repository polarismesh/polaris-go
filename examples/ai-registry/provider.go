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
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
)

const agentBusinessVersion = "1.0.0"
const (
	agentCardSchemaVersion03 = "0.3"
	agentCardSchemaVersion10 = "1.0"
)

type providerReady struct {
	RunID            string `json:"run_id"`
	Service          string `json:"service"`
	InstanceID       string `json:"instance_id"`
	Host             string `json:"host"`
	Port             int    `json:"port"`
	RegistrationPort int    `json:"registration_port"`
	URL              string `json:"url"`
	SecondaryURL     string `json:"secondary_url,omitempty"`
	CardJSON         string `json:"card_json"`
	A2AVersion       string `json:"a2a_version"`
	BusinessVersion  string `json:"business_version"`
	PID              int    `json:"pid"`
	Stopped          bool   `json:"stopped"`
}

type processExit struct {
	done chan struct{}
	err  error
}

type providerAgentOptions struct {
	namespace         string
	service           string
	primaryEndpoint   string
	secondaryEndpoint string
	runID             string
	cardVersion       string
	businessVersion   string
	registrationPort  *int
}

type providerRegistration struct {
	Namespace  string
	Service    string
	Host       string
	Port       int
	Version    string
	Protocol   string
	Metadata   map[string]string
	A2AVersion string
}

func (r *providerRegistration) NewInstanceRegisterRequest() *model.InstanceRegisterRequest {
	if r == nil {
		return nil
	}
	protocol := r.Protocol
	version := r.Version
	metadata := make(map[string]string, len(r.Metadata))
	for key, value := range r.Metadata {
		metadata[key] = value
	}
	return &model.InstanceRegisterRequest{
		Namespace: r.Namespace,
		Service:   r.Service,
		Host:      r.Host,
		Port:      r.Port,
		Protocol:  &protocol,
		Version:   &version,
		Metadata:  metadata,
	}
}

func normalizeAgentCardSchemaVersion(value string) (string, error) {
	switch strings.TrimSpace(value) {
	case agentCardSchemaVersion03, "0.3.0":
		return agentCardSchemaVersion03, nil
	case agentCardSchemaVersion10, "1.0.0":
		return agentCardSchemaVersion10, nil
	default:
		return "", fmt.Errorf("AgentCard schema version must be 0.3 or 1.0")
	}
}

func endpointHost(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("invalid endpoint %q", rawURL)
	}
	return parsed.Hostname(), nil
}

func buildProviderRegistration(
	options providerAgentOptions,
) (string, *providerRegistration, error) {
	version, err := normalizeAgentCardSchemaVersion(options.cardVersion)
	if err != nil {
		return "", nil, err
	}
	businessVersion := options.businessVersion
	if businessVersion == "" {
		businessVersion = agentBusinessVersion
	}
	primaryURL := strings.TrimRight(options.primaryEndpoint, "/") + "/invoke"
	card := map[string]interface{}{
		"name":               options.service,
		"description":        "Polaris Go real E2E agent",
		"version":            businessVersion,
		"capabilities":       map[string]bool{"streaming": true},
		"defaultInputModes":  []string{"text/plain"},
		"defaultOutputModes": []string{"text/plain"},
		"skills": []map[string]interface{}{
			{
				"id": "echo", "name": "echo", "description": "Return an observed request",
				"tags": []string{"e2e"}, "examples": []string{"echo weather"},
				"inputModes": []string{"text/plain"}, "outputModes": []string{"text/plain"},
			},
		},
		"x-e2e-run": options.runID,
	}
	if version == agentCardSchemaVersion03 {
		card["url"] = primaryURL
		card["preferredTransport"] = "HTTP+JSON"
		card["protocolVersion"] = "0.3.0"
		if options.secondaryEndpoint != "" {
			card["additionalInterfaces"] = []map[string]string{{
				"url":       strings.TrimRight(options.secondaryEndpoint, "/") + "/invoke",
				"transport": "HTTP+JSON",
			}}
		}
	} else {
		interfaces := []map[string]string{{
			"url": primaryURL, "protocolBinding": "HTTP+JSON", "protocolVersion": "1.0",
		}}
		if options.secondaryEndpoint != "" {
			interfaces = append(interfaces, map[string]string{
				"url":             strings.TrimRight(options.secondaryEndpoint, "/") + "/invoke",
				"protocolBinding": "HTTP+JSON",
				"protocolVersion": "1.0",
			})
		}
		card["supportedInterfaces"] = interfaces
	}
	cardJSON, err := json.Marshal(card)
	if err != nil {
		return "", nil, err
	}
	host, err := endpointHost(primaryURL)
	if err != nil {
		return "", nil, err
	}
	if options.secondaryEndpoint != "" {
		secondaryHost, err := endpointHost(options.secondaryEndpoint)
		if err != nil {
			return "", nil, err
		}
		if secondaryHost != host {
			return "", nil, fmt.Errorf("primary and secondary endpoints must use the same host")
		}
	}
	registrationPort := 1
	if options.registrationPort != nil {
		if *options.registrationPort < 1 || *options.registrationPort > 65535 {
			return "", nil, fmt.Errorf("registration port must be in range 1..65535")
		}
		registrationPort = *options.registrationPort
	}
	metadata := map[string]string{
		model.MetadataKeyAIAgentName: options.service,
		model.MetadataKeyAIAgentCard: string(cardJSON),
		model.MetadataKeyA2AVersion:  version,
		ownerLabel:                   e2eOwner,
		runLabel:                     options.runID,
	}
	return string(cardJSON), &providerRegistration{
		Namespace:  options.namespace,
		Service:    options.service,
		Host:       host,
		Port:       registrationPort,
		Version:    businessVersion,
		Protocol:   "",
		Metadata:   metadata,
		A2AVersion: version,
	}, nil
}

func routeHost(address string) (string, error) {
	conn, err := net.Dial("udp", address)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	local, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || local.IP == nil {
		return "", fmt.Errorf("no route source IP")
	}
	return local.IP.String(), nil
}

func readSkillInstructions(installed *model.InstalledSkill) (string, error) {
	if installed == nil || installed.Dir == "" || installed.EntryFile == "" {
		return "", fmt.Errorf("installed Skill has no directory or entry file")
	}
	data, err := ioutil.ReadFile(filepath.Join(installed.Dir, filepath.FromSlash(installed.EntryFile)))
	return string(data), err
}

func serveProvider(ctx context.Context, o options) (result error) {
	cardVersion := o.cardVersion
	if cardVersion == "" {
		cardVersion = agentCardSchemaVersion10
	}
	cardVersion, err := normalizeAgentCardSchemaVersion(cardVersion)
	if err != nil {
		return err
	}
	cfg, err := loadConfiguration(o, o.skillName != "")
	if err != nil {
		return err
	}
	sdkCtx, err := api.InitContextByConfig(cfg)
	if err != nil {
		return err
	}
	defer sdkCtx.Destroy()
	instructions := "echo"
	if o.skillName != "" {
		installed, installErr := api.NewSkillAPIByContext(sdkCtx).Install(&api.SkillInstallRequest{SkillInstallRequest: model.SkillInstallRequest{
			Namespace: o.namespace, Name: o.skillName, Version: "1.0.0", CacheDir: filepath.Join(o.runDir, "cache"),
		}})
		if installErr != nil {
			return installErr
		}
		loaded, readErr := readSkillInstructions(installed)
		if readErr != nil {
			return readErr
		}
		instructions = loaded
	}
	host, err := routeHost(o.discover)
	if err != nil {
		return err
	}
	primaryListener, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return err
	}
	defer primaryListener.Close()
	primaryPort := primaryListener.Addr().(*net.TCPAddr).Port
	primaryEndpoint := "http://" + net.JoinHostPort(host, fmt.Sprint(primaryPort))
	var secondaryListener net.Listener
	secondaryEndpoint := ""
	secondaryPort := 0
	if o.multiBusinessPort {
		secondaryListener, err = net.Listen("tcp", net.JoinHostPort(host, "0"))
		if err != nil {
			return err
		}
		defer secondaryListener.Close()
		secondaryPort = secondaryListener.Addr().(*net.TCPAddr).Port
		secondaryEndpoint = "http://" + net.JoinHostPort(host, fmt.Sprint(secondaryPort))
	}
	var registrationPort *int
	if o.registrationPort != 0 {
		value := o.registrationPort
		registrationPort = &value
	}
	cardJSON, snapshot, err := buildProviderRegistration(providerAgentOptions{
		namespace:         o.namespace,
		service:           o.service,
		primaryEndpoint:   primaryEndpoint,
		secondaryEndpoint: secondaryEndpoint,
		runID:             o.runID,
		cardVersion:       cardVersion,
		businessVersion:   o.businessVersion,
		registrationPort:  registrationPort,
	})
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/agent-card.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(cardJSON))
	})
	mux.HandleFunc("/invoke", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"service": o.service, "reply": "Echo: " + r.URL.Query().Get("message"), "instructions": instructions})
	})
	primaryServer := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	serveDone := make(chan error, 2)
	go func() { serveDone <- primaryServer.Serve(primaryListener) }()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		result = addError(result, primaryServer.Shutdown(shutdown))
	}()
	var secondaryServer *http.Server
	if secondaryListener != nil {
		secondaryServer = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		go func() { serveDone <- secondaryServer.Serve(secondaryListener) }()
		defer func() {
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result = addError(result, secondaryServer.Shutdown(shutdown))
		}()
	}
	provider := api.NewProviderAPIByContext(sdkCtx)
	request := snapshot.NewInstanceRegisterRequest()
	request.ServiceToken = environment("POLARIS_E2E_SERVICE_TOKEN", "POLARIS_SERVICE_TOKEN")
	registration := &api.InstanceRegisterRequest{InstanceRegisterRequest: *request}
	registration.SetTTL(1)
	registered, err := provider.RegisterInstance(registration)
	if err != nil {
		return err
	}
	ready := providerReady{
		RunID: o.runID, Service: o.service, InstanceID: registered.InstanceID,
		Host: snapshot.Host, Port: primaryPort, RegistrationPort: snapshot.Port,
		URL: primaryEndpoint + "/invoke", SecondaryURL: "",
		CardJSON: cardJSON, A2AVersion: snapshot.A2AVersion,
		BusinessVersion: snapshot.Version, PID: os.Getpid(),
	}
	if secondaryEndpoint != "" {
		ready.SecondaryURL = secondaryEndpoint + "/invoke"
	}
	defer func() {
		request := &api.InstanceDeRegisterRequest{InstanceDeRegisterRequest: model.InstanceDeRegisterRequest{
			InstanceID: ready.InstanceID, Namespace: o.namespace, Service: o.service,
			Host: ready.Host, Port: ready.RegistrationPort,
			ServiceToken: registration.ServiceToken,
		}}
		request.SetTimeout(o.timeout)
		result = addError(result, provider.Deregister(request))
		ready.Stopped = result == nil
		result = addError(result, writeJSON(o.readyPath, ready))
	}()
	if err = writeJSON(o.readyPath, ready); err != nil {
		return err
	}
	log.Printf("READY service=%s instance=%s host=%s registration_port=%d business_port=%d secondary_business_port=%d",
		o.service, ready.InstanceID, ready.Host, ready.RegistrationPort, ready.Port, secondaryPort)
	select {
	case <-ctx.Done():
		return nil
	case err = <-serveDone:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func (s *suite) startProvider(ctx context.Context, label, skillName string) (providerReady, error) {
	return s.startProviderWithVersion(ctx, label, skillName, agentCardSchemaVersion10)
}

type providerStartOptions struct {
	label             string
	skillName         string
	cardVersion       string
	businessVersion   string
	registrationPort  int
	multiBusinessPort bool
}

func (s *suite) startProviderWithVersion(
	ctx context.Context, label, skillName, cardVersion string,
) (providerReady, error) {
	return s.startProviderWithOptions(ctx, providerStartOptions{
		label: label, skillName: skillName, cardVersion: cardVersion,
	})
}

func (s *suite) startProviderWithOptions(
	ctx context.Context, options providerStartOptions,
) (providerReady, error) {
	cardVersion := options.cardVersion
	if cardVersion == "" {
		cardVersion = agentCardSchemaVersion10
	}
	normalizedVersion, err := normalizeAgentCardSchemaVersion(cardVersion)
	if err != nil {
		return providerReady{}, err
	}
	cardVersion = normalizedVersion
	businessVersion := options.businessVersion
	if businessVersion == "" {
		businessVersion = agentBusinessVersion
	}
	expectedRegistrationPort := options.registrationPort
	if expectedRegistrationPort == 0 {
		expectedRegistrationPort = 1
	}
	service := s.serviceName(options.label)
	if existing, err := s.console.getService(ctx, s.opts.namespace, service); err != nil || existing != nil {
		if err != nil {
			return providerReady{}, err
		}
		return providerReady{}, fmt.Errorf("refuse to register into an existing run service")
	}
	if err := s.recordService(service); err != nil {
		return providerReady{}, err
	}
	path := filepath.Join(s.opts.runDir, options.label+"-ready.json")
	binary, err := os.Executable()
	if err != nil {
		return providerReady{}, err
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return providerReady{}, err
	}
	args := []string{"-mode=provider", "-discover=" + s.opts.discover, "-skill=" + s.opts.skill, "-config-address=" + s.opts.config,
		"-console=" + s.opts.console, "-namespace=" + s.opts.namespace, "-run-id=" + s.opts.runID,
		"-run-dir=" + filepath.Join(s.opts.runDir, options.label), "-service=" + service, "-ready=" + path,
		"-skill-name=" + options.skillName, "-timeout=" + s.opts.timeout.String(),
		"-card-version=" + cardVersion, "-business-version=" + businessVersion}
	if options.registrationPort != 0 {
		args = append(args, fmt.Sprintf("-registration-port=%d", options.registrationPort))
	}
	if options.multiBusinessPort {
		args = append(args, "-multi-business-port=true")
	}
	if s.opts.configFile != "" {
		args = append(args, "-config-file="+s.opts.configFile)
	}
	if s.opts.debug {
		args = append(args, "-debug")
	}
	command := exec.Command(binary, args...)
	command.Dir = s.opts.runDir
	logFile, err := os.OpenFile(
		filepath.Join(s.opts.runDir, options.label+"-provider.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return providerReady{}, err
	}
	command.Stdout, command.Stderr = logFile, logFile
	if err = command.Start(); err != nil {
		logFile.Close()
		return providerReady{}, err
	}
	logFile.Close()
	process := processState{PID: command.Process.Pid, Binary: binary, ReadyPath: path, Service: service}
	s.state.Processes = append(s.state.Processes, process)
	if err = s.saveState(); err != nil {
		command.Process.Signal(syscall.SIGTERM)
		command.Wait()
		return providerReady{}, err
	}
	exit := &processExit{done: make(chan struct{})}
	go func() {
		exit.err = command.Wait()
		close(exit.done)
	}()
	s.processes[command.Process.Pid] = exit
	var ready providerReady
	err = eventually(ctx, s.opts.timeout+35*time.Second, func() error {
		select {
		case <-exit.done:
			return fmt.Errorf("provider exited before ready: %v", exit.err)
		default:
		}
		data, readErr := ioutil.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if err := json.Unmarshal(data, &ready); err != nil {
			return err
		}
		if ready.RunID != s.opts.runID || ready.Service != service || ready.PID != process.PID ||
			ready.InstanceID == "" || ready.RegistrationPort != expectedRegistrationPort ||
			ready.CardJSON == "" || ready.A2AVersion != cardVersion ||
			ready.BusinessVersion != businessVersion ||
			(options.multiBusinessPort && ready.SecondaryURL == "") ||
			(!options.multiBusinessPort && ready.SecondaryURL != "") ||
			ready.Stopped {
			return fmt.Errorf("invalid provider readiness")
		}
		return nil
	})
	if err != nil {
		select {
		case <-exit.done:
		default:
			err = addError(err, command.Process.Signal(syscall.SIGTERM))
			select {
			case <-exit.done:
			case <-time.After(20 * time.Second):
				err = addError(err, command.Process.Kill())
				<-exit.done
			}
		}
		return providerReady{}, addError(err, exit.err)
	}
	s.state.Instances = append(s.state.Instances, instanceState{ID: ready.InstanceID, Service: service})
	return ready, s.saveState()
}

func stopProvider(process processState, runID string) error {
	command, err := exec.Command("ps", "-p", fmt.Sprint(process.PID), "-o", "command=").Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
			return nil
		}
		return err
	}
	actual := strings.TrimSpace(string(command))
	if !strings.HasPrefix(actual, process.Binary+" ") || !hasCommandArgument(actual, "-mode=provider") ||
		!hasCommandArgument(actual, "-run-id="+runID) || !hasCommandArgument(actual, "-service="+process.Service) ||
		!hasCommandArgument(actual, "-ready="+process.ReadyPath) {
		return fmt.Errorf("refuse to signal reused or unowned PID %d", process.PID)
	}
	p, err := os.FindProcess(process.PID)
	if err != nil {
		return err
	}
	if err = p.Signal(syscall.SIGTERM); err != nil {
		if err == os.ErrProcessDone || err == syscall.ESRCH {
			return nil
		}
		return err
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		data, readErr := ioutil.ReadFile(process.ReadyPath)
		if readErr == nil {
			var ready providerReady
			if json.Unmarshal(data, &ready) == nil && ready.Stopped && ready.RunID == runID && ready.PID == process.PID && ready.Service == process.Service {
				return nil
			}
		}
		if err = p.Signal(syscall.Signal(0)); err != nil {
			if err == os.ErrProcessDone || err == syscall.ESRCH {
				return nil
			}
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("provider PID %d did not complete shutdown", process.PID)
}

func hasCommandArgument(command, argument string) bool {
	return strings.Contains(command, " "+argument+" ") || strings.HasSuffix(command, " "+argument)
}
