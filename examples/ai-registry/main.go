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

// Package main verifies AI Registry workflows against an explicitly configured Polaris deployment.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
)

const (
	e2eOwner            = "polaris-go-ai-registry"
	runLabel            = "e2e.run.id"
	ownerLabel          = "e2e.owner"
	defaultNamespace    = "go-sdk-integration"
	servicePrefix       = "go-sdk-e2e-"
	legacyServicePrefix = "polaris-go-ai-e2e-"
	weatherSkill        = "polaris-go-ai-e2e-a-weather"
	currencySkill       = "polaris-go-ai-e2e-z-currency"
	draftSkill          = "polaris-go-ai-e2e-draft"
	fixtureTag          = "polaris-go-ai-e2e"
)

type options struct {
	mode, server, discover, skill, config, console              string
	namespace, runID, runDir, statePath, configFile, suite      string
	service, readyPath, skillName, cardVersion, businessVersion string
	timeout, runTimeout                                         time.Duration
	cleanServices, cleanProcesses, cleanSkills, apply, debug    bool
	dryRun                                                      bool
	registrationPort                                            int
	multiBusinessPort                                           bool
}

func environment(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func validSuite(suite string) bool {
	switch suite {
	case "all", "agent", "mcp", "skill":
		return true
	default:
		return false
	}
}

func suiteIncludes(selected, group string) bool {
	return selected == "all" || selected == group
}

func serviceBelongsToRun(service, runID string) bool {
	return strings.HasPrefix(service, servicePrefix+runID+"-") ||
		strings.HasPrefix(service, legacyServicePrefix+runID+"-")
}

func newRunID(now time.Time) string {
	return strings.ReplaceAll(now.In(time.Local).Format("20060102150405.000"), ".", "")
}

func readOptions() options {
	o := options{
		mode: "verify", server: environment("POLARIS_SERVER"),
		discover: environment("POLARIS_SERVER_ADDRESS", "POLARIS_SERVER_ADDRESSES"),
		skill:    environment("POLARIS_SKILL_ADDRESS"), config: environment("POLARIS_CONFIG_ADDRESS", "POLARIS_CONFIG_ADDR"),
		console:   environment("POLARIS_CONSOLE_ADDRESS", "POLARIS_OPENAPI"),
		namespace: environment("POLARIS_E2E_NAMESPACE"), configFile: environment("POLARIS_E2E_CONFIG"),
		suite:   environment("POLARIS_E2E_SUITE"),
		timeout: 10 * time.Second, runTimeout: 12 * time.Minute,
	}
	if o.namespace == "" {
		o.namespace = defaultNamespace
	}
	if o.suite == "" {
		o.suite = "all"
	}
	flag.StringVar(&o.mode, "mode", o.mode, "verify, provider, or cleanup")
	flag.StringVar(&o.server, "server", o.server, "Polaris host; conventional ports are derived when not specified")
	flag.StringVar(&o.discover, "discover", o.discover, "Discovery gRPC host:port")
	flag.StringVar(&o.skill, "skill", o.skill, "Skill gRPC host:port")
	flag.StringVar(&o.config, "config-address", o.config, "Configuration gRPC host:port")
	flag.StringVar(&o.console, "console", o.console, "Console HTTP(S) origin")
	flag.StringVar(&o.configFile, "config-file", o.configFile, "Optional SDK configuration; tokens are never saved to state")
	flag.StringVar(&o.suite, "suite", o.suite, "Verification suite: all, agent, mcp, or skill")
	flag.StringVar(&o.namespace, "namespace", o.namespace, "Isolated non-default E2E namespace")
	flag.StringVar(&o.runID, "run-id", "", "Run ownership identifier")
	flag.StringVar(&o.runDir, "run-dir", "", "Run logs and state directory")
	flag.StringVar(&o.statePath, "state", "", "State file for explicit cleanup")
	flag.StringVar(&o.service, "service", "", "Provider service name")
	flag.StringVar(&o.readyPath, "ready", "", "Provider readiness file")
	flag.StringVar(&o.skillName, "skill-name", "", "Published Skill loaded by the provider")
	flag.StringVar(&o.cardVersion, "card-version", o.cardVersion, "Provider AgentCard schema version: 0.3 or 1.0")
	flag.StringVar(&o.businessVersion, "business-version", o.businessVersion,
		"Provider AgentCard business version")
	flag.IntVar(&o.registrationPort, "registration-port", o.registrationPort,
		"Explicit instance registration port; 0 uses the default")
	flag.BoolVar(&o.multiBusinessPort, "multi-business-port", o.multiBusinessPort,
		"Expose a second AgentCard interface on another business port")
	flag.DurationVar(&o.timeout, "timeout", o.timeout, "Per-operation timeout")
	flag.DurationVar(&o.runTimeout, "run-timeout", o.runTimeout, "Complete verification deadline")
	flag.BoolVar(&o.cleanServices, "services", false, "Explicitly clean this run's services and instances")
	flag.BoolVar(&o.cleanProcesses, "processes", false, "Explicitly stop this run's providers")
	flag.BoolVar(&o.cleanSkills, "skills", false, "Explicitly delete owned reusable Skill fixtures")
	flag.BoolVar(&o.apply, "apply", false, "Apply cleanup; otherwise show a dry run")
	flag.BoolVar(&o.apply, "force", false, "Apply cleanup without bypassing ownership checks")
	flag.BoolVar(&o.apply, "f", false, "Alias for -apply")
	flag.BoolVar(&o.dryRun, "dry-run", false, "Show selected resources without changing them")
	flag.BoolVar(&o.debug, "debug", false, "Enable SDK debug logging")
	flag.Parse()
	if o.dryRun {
		o.apply = false
	}
	return o
}

func (o *options) validate() error {
	if o.dryRun && o.mode != "cleanup" {
		return fmt.Errorf("dry-run is only supported for cleanup")
	}
	if o.dryRun {
		o.apply = false
	}
	if o.mode == "cleanup" {
		if o.statePath == "" {
			return fmt.Errorf("cleanup requires -state")
		}
		return nil
	}
	if o.mode != "verify" && o.mode != "provider" {
		return fmt.Errorf("unknown mode %q", o.mode)
	}
	o.suite = strings.ToLower(strings.TrimSpace(o.suite))
	if o.suite == "" {
		o.suite = "all"
	}
	if !validSuite(o.suite) {
		return fmt.Errorf("unknown suite %q, want all, agent, mcp, or skill", o.suite)
	}
	if o.server == "" && o.discover != "" {
		o.discover = strings.Split(o.discover, ",")[0]
		host, _, err := net.SplitHostPort(o.discover)
		if err != nil {
			return fmt.Errorf("invalid discovery address: %w", err)
		}
		o.server = host
	}
	if o.server == "" || strings.ContainsAny(o.server, "/?#@") {
		return fmt.Errorf("provide -server or POLARIS_SERVER_ADDRESS")
	}
	if o.discover == "" {
		o.discover = net.JoinHostPort(o.server, "8091")
	}
	if o.skill == "" {
		o.skill = net.JoinHostPort(o.server, "8094")
	}
	if o.config == "" {
		o.config = net.JoinHostPort(o.server, "8093")
	}
	if o.console == "" {
		o.console = "http://" + net.JoinHostPort(o.server, "8090")
	}
	for _, address := range []string{o.discover, o.skill, o.config} {
		host, port, err := net.SplitHostPort(address)
		if err != nil || host == "" || port == "" {
			return fmt.Errorf("invalid gRPC address %q", address)
		}
	}
	if err := validateConsole(o.console); err != nil {
		return err
	}
	if o.namespace == "" || strings.EqualFold(o.namespace, "default") || strings.ContainsAny(o.namespace, "/\\:*@") {
		return fmt.Errorf("use an isolated non-default namespace")
	}
	if o.timeout <= 0 || o.runTimeout <= o.timeout {
		return fmt.Errorf("run-timeout must exceed timeout, and both must be positive")
	}
	if o.registrationPort < 0 || o.registrationPort > 65535 {
		return fmt.Errorf("registration-port must be empty or in range 1..65535")
	}
	if o.businessVersion == "" {
		o.businessVersion = agentBusinessVersion
	}
	if o.businessVersion != strings.TrimSpace(o.businessVersion) {
		return fmt.Errorf("business-version must not contain surrounding whitespace")
	}
	if o.runID == "" {
		o.runID = newRunID(time.Now())
	}
	if strings.ContainsAny(o.runID, " /\\:@") {
		return fmt.Errorf("run-id contains reserved characters")
	}
	if o.runDir == "" {
		o.runDir = filepath.Join(".logs", o.runID)
	}
	abs, err := filepath.Abs(o.runDir)
	if err != nil {
		return err
	}
	o.runDir = abs
	if o.mode == "provider" && (o.readyPath == "" || o.service == "") {
		return fmt.Errorf("provider requires -ready and -service")
	}
	return nil
}

func validateConsole(address string) error {
	u, err := url.Parse(address)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("console must be an HTTP(S) origin without credentials, query, or path")
	}
	return nil
}

func loadConfiguration(o options, enableSkill bool) (config.Configuration, error) {
	var cfg config.Configuration = config.NewDefaultConfiguration([]string{o.discover})
	if o.configFile != "" {
		loaded, err := config.LoadConfigurationByFile(o.configFile)
		if err != nil {
			return nil, err
		}
		cfg = loaded
		cfg.SetDefault()
	}
	cfg.GetGlobal().GetServerConnector().SetAddresses([]string{o.discover})
	if token := environment("POLARIS_TOKEN"); token != "" {
		cfg.GetGlobal().GetServerConnector().SetToken(token)
	}
	cfg.GetGlobal().GetAPI().SetTimeout(o.timeout)
	cfg.GetGlobal().GetAPI().SetMaxRetryTimes(0)
	cfg.GetGlobal().GetAdmin().SetPort(0)
	cfg.GetGlobal().GetStatReporter().SetEnable(false)
	client, ok := cfg.GetGlobal().GetClient().(*config.ClientConfigImpl)
	if !ok {
		return nil, fmt.Errorf("unsupported client configuration")
	}
	client.AddLabels(map[string]string{"CLIENT_ID": e2eOwner + "-" + o.runID})
	cfg.GetConsumer().GetLocalCache().SetPersistEnable(false)
	cfg.GetConsumer().GetLocalCache().SetServiceRefreshInterval(500 * time.Millisecond)
	cfg.GetConfigFile().GetLocalCache().SetPersistEnable(false)
	configConnector := cfg.GetConfigFile().GetConfigConnectorConfig()
	configConnector.SetAddresses([]string{o.config})
	if token := environment("POLARIS_CONFIG_TOKEN", "POLARIS_TOKEN"); token != "" {
		configConnector.SetToken(token)
	}
	configConnector.SetConnectTimeout(o.timeout)
	configConnector.SetMessageTimeout(o.timeout)
	cfg.GetSkill().SetEnable(enableSkill)
	cfg.GetProvider().SetReportServiceContractEnable(true)
	cfg.GetSkill().SetAddresses([]string{o.skill})
	if token := environment("POLARIS_SKILL_TOKEN", "POLARIS_TOKEN"); token != "" {
		cfg.GetSkill().SetToken(token)
	}
	cfg.GetSkill().SetConnectTimeout(o.timeout)
	cfg.GetSkill().SetMessageTimeout(o.timeout)
	cfg.GetSkill().SetDownloadTimeout(o.timeout)
	cfg.GetSkill().SetPersistEnable(true)
	cfg.GetSkill().SetPersistDir(filepath.Join(o.runDir, "skill-persistence"))
	cfg.GetSkill().SetFallbackToLocalCache(true)
	return cfg, nil
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime)
	o := readOptions()
	if err := o.validate(); err != nil {
		log.Print(err)
		os.Exit(2)
	}
	if o.debug {
		if err := api.SetLoggersLevel(api.DebugLog); err != nil {
			log.Printf("debug logging: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-interrupts:
			cancel()
		case <-ctx.Done():
		}
	}()
	var err error
	switch o.mode {
	case "verify":
		err = verify(ctx, o)
	case "provider":
		err = serveProvider(ctx, o)
	case "cleanup":
		err = cleanupState(ctx, o)
	}
	cancel()
	signal.Stop(interrupts)
	if err != nil {
		log.Printf("FAIL: %v", err)
		os.Exit(1)
	}
}
