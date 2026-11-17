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

// Command summarize summarizes one AI Registry cloud verification run.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
)

type caseResult struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	Milliseconds int64  `json:"milliseconds"`
	Error        string `json:"error,omitempty"`
}

type resultFile struct {
	RunID            string       `json:"run_id"`
	Suite            string       `json:"suite"`
	Namespace        string       `json:"namespace"`
	Success          bool         `json:"success"`
	Error            string       `json:"error,omitempty"`
	CleanupRequested bool         `json:"cleanup_requested"`
	CleanupSuccess   bool         `json:"cleanup_success"`
	Results          []caseResult `json:"results"`
}

type stateFile struct {
	RunID     string   `json:"run_id"`
	Suite     string   `json:"suite,omitempty"`
	Namespace string   `json:"namespace"`
	Services  []string `json:"services"`
	Instances []struct {
		ID      string `json:"id"`
		Service string `json:"service"`
	} `json:"instances"`
	Processes []struct {
		PID     int    `json:"pid"`
		Service string `json:"service"`
	} `json:"processes"`
	Skills       []string `json:"skills"`
	ConfigGroups []string `json:"config_groups"`
	Cleaned      bool     `json:"cleaned"`
}

type analysis struct {
	RunID            string            `json:"run_id"`
	Suite            string            `json:"suite"`
	Namespace        string            `json:"namespace"`
	Success          bool              `json:"success"`
	Error            string            `json:"error,omitempty"`
	Passed           int               `json:"passed"`
	Failed           int               `json:"failed"`
	Total            int               `json:"total"`
	CleanupRequested bool              `json:"cleanup_requested"`
	CleanupSuccess   bool              `json:"cleanup_success"`
	StateCleaned     bool              `json:"state_cleaned"`
	Services         int               `json:"services"`
	Instances        int               `json:"instances"`
	Processes        int               `json:"processes"`
	Skills           int               `json:"skills"`
	ConfigGroups     int               `json:"config_groups"`
	Failures         []caseResult      `json:"failures,omitempty"`
	AnalysisErrors   []string          `json:"analysis_errors,omitempty"`
	RunEnv           map[string]string `json:"run_env,omitempty"`
	PackageEnv       map[string]string `json:"package_env,omitempty"`
}

func main() {
	var (
		outputPath = flag.String("output", "", "analysis Markdown path; default RUN/analysis.md")
		jsonPath   = flag.String("json", "", "analysis JSON path; default RUN/analysis.json")
		strict     = flag.Bool("strict", false, "return non-zero when the run failed")
	)
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: ai-registry-summarize [options] RUN_DIR")
		os.Exit(2)
	}
	runDir, err := filepath.Abs(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve run directory: %v\n", err)
		os.Exit(2)
	}
	info, err := os.Stat(runDir)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "run directory not found: %s\n", runDir)
		os.Exit(2)
	}
	if *outputPath == "" {
		*outputPath = filepath.Join(runDir, "analysis.md")
	}
	if *jsonPath == "" {
		*jsonPath = filepath.Join(runDir, "analysis.json")
	}

	summary, markdown, err := summarize(runDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "summarize run: %v\n", err)
		os.Exit(2)
	}
	if err = writeFile(*outputPath, []byte(markdown)); err != nil {
		fmt.Fprintf(os.Stderr, "write analysis: %v\n", err)
		os.Exit(2)
	}
	raw, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal analysis: %v\n", err)
		os.Exit(2)
	}
	raw = append(raw, '\n')
	if err = writeFile(*jsonPath, raw); err != nil {
		fmt.Fprintf(os.Stderr, "write analysis JSON: %v\n", err)
		os.Exit(2)
	}
	fmt.Print(markdown)
	if *strict && !summary.Success {
		os.Exit(1)
	}
}

func summarize(runDir string) (analysis, string, error) {
	result, resultErr := readResult(filepath.Join(runDir, "result.json"))
	state, stateErr := readState(filepath.Join(runDir, "state.json"))
	runEnvAll := readKeyValueFile(filepath.Join(runDir, "run.env"))
	packageEnvAll := readKeyValueFile(filepath.Join(runDir, "package.env"))

	summary := analysis{
		RunID:            result.RunID,
		Suite:            result.Suite,
		Namespace:        result.Namespace,
		Success:          result.Success,
		Error:            result.Error,
		CleanupRequested: result.CleanupRequested,
		CleanupSuccess:   result.CleanupSuccess,
		StateCleaned:     state.Cleaned,
		Services:         len(state.Services),
		Instances:        len(state.Instances),
		Processes:        len(state.Processes),
		Skills:           len(state.Skills),
		ConfigGroups:     len(state.ConfigGroups),
		RunEnv: selectKeys(runEnvAll,
			"RUN_ID", "SERVER", "SUITE", "NAMESPACE", "TIMEOUT", "RUN_TIMEOUT", "DEBUG",
			"CLEANUP", "CLEANUP_SKILLS", "CONFIG_FILE", "STARTED_AT", "FINISHED_AT",
			"STARTED_AT_UTC", "FINISHED_AT_UTC",
			"VERIFY_EXIT_CODE", "ANALYSIS_EXIT_CODE"),
		PackageEnv: selectKeys(packageEnvAll,
			"PACKAGE_NAME", "BUILD_TIME_UTC", "GIT_BRANCH", "GIT_REVISION", "TARGET_OS",
			"TARGET_ARCH", "BINARY", "BINARY_SHA256", "SUMMARIZER", "SUMMARIZER_SHA256"),
	}
	if summary.RunID == "" {
		summary.RunID = firstNonEmpty(state.RunID, runEnvAll["RUN_ID"], filepath.Base(runDir))
	}
	if summary.Namespace == "" {
		summary.Namespace = firstNonEmpty(state.Namespace, runEnvAll["NAMESPACE"])
	}
	if summary.Suite == "" {
		summary.Suite = firstNonEmpty(state.Suite, runEnvAll["SUITE"], "all")
	}
	for _, item := range result.Results {
		summary.Total++
		if strings.EqualFold(item.Status, "PASS") {
			summary.Passed++
			continue
		}
		summary.Failed++
		summary.Failures = append(summary.Failures, item)
	}
	if resultErr != nil && !errors.Is(resultErr, os.ErrNotExist) {
		summary.AnalysisErrors = append(summary.AnalysisErrors, resultErr.Error())
	}
	if stateErr != nil && !errors.Is(stateErr, os.ErrNotExist) {
		summary.AnalysisErrors = append(summary.AnalysisErrors, stateErr.Error())
	}
	if summary.Success && summary.Total == 0 {
		summary.Success = false
		summary.Error = "result.json contains no cases; inspect run-cloud.log and provider logs"
		summary.AnalysisErrors = append(summary.AnalysisErrors, summary.Error)
	}
	if summary.Success && summary.Failed > 0 {
		summary.Success = false
		summary.AnalysisErrors = append(summary.AnalysisErrors,
			fmt.Sprintf("result.json reports success=true but contains %d failed case(s)", summary.Failed))
	}
	if !summary.Success && summary.Total == 0 && errors.Is(resultErr, os.ErrNotExist) && result.Error == "" {
		summary.Error = "result.json is missing; inspect run-cloud.log and provider logs"
	}
	if !summary.Success && summary.Total == 0 && resultErr == nil && result.Error == "" {
		summary.Error = "result.json contains no cases; inspect run-cloud.log and provider logs"
	}
	return summary, renderMarkdown(runDir, summary, result), nil
}

func readResult(path string) (resultFile, error) {
	var result resultFile
	err := readJSON(path, &result)
	return result, err
}

func readState(path string) (stateFile, error) {
	var state stateFile
	err := readJSON(path, &state)
	return state, err
}

func readJSON(path string, target interface{}) error {
	raw, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func readKeyValueFile(path string) map[string]string {
	result := make(map[string]string)
	file, err := os.Open(path)
	if err != nil {
		return result
	}
	defer func() {
		_ = file.Close()
	}()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		separator := strings.IndexByte(line, '=')
		if separator < 0 {
			continue
		}
		key := strings.TrimSpace(line[:separator])
		value := strings.TrimSpace(line[separator+1:])
		result[key] = value
	}
	return result
}

func selectKeys(values map[string]string, keys ...string) map[string]string {
	selected := make(map[string]string)
	for _, key := range keys {
		if value, ok := values[key]; ok {
			selected[key] = value
		}
	}
	return selected
}

func renderMarkdown(runDir string, summary analysis, result resultFile) string {
	var builder strings.Builder
	builder.WriteString("# AI Registry Cloud E2E Result\n\n")
	fmt.Fprintf(&builder, "- Run directory: `%s`\n", runDir)
	fmt.Fprintf(&builder, "- Run ID: `%s`\n", summary.RunID)
	fmt.Fprintf(&builder, "- Suite: `%s`\n", summary.Suite)
	fmt.Fprintf(&builder, "- Namespace: `%s`\n", summary.Namespace)
	fmt.Fprintf(&builder, "- Success: `%t`\n", summary.Success)
	fmt.Fprintf(&builder, "- Cases: `%d passed / %d failed / %d total`\n",
		summary.Passed, summary.Failed, summary.Total)
	fmt.Fprintf(&builder, "- Cleanup requested: `%t`, cleanup success: `%t`, state cleaned: `%t`\n",
		summary.CleanupRequested, summary.CleanupSuccess, summary.StateCleaned)
	fmt.Fprintf(&builder, "- State resources: services=`%d`, instances=`%d`, processes=`%d`, skills=`%d`, config groups=`%d`\n",
		summary.Services, summary.Instances, summary.Processes, summary.Skills, summary.ConfigGroups)
	if summary.Error != "" {
		fmt.Fprintf(&builder, "- Final error: `%s`\n", escapeMarkdown(summary.Error))
	}
	if started := firstNonEmpty(summary.RunEnv["STARTED_AT_UTC"], summary.RunEnv["STARTED_AT"]); started != "" {
		fmt.Fprintf(&builder, "- Started: `%s`\n", started)
	}
	if finished := firstNonEmpty(summary.RunEnv["FINISHED_AT_UTC"], summary.RunEnv["FINISHED_AT"]); finished != "" {
		fmt.Fprintf(&builder, "- Finished: `%s`\n", finished)
	}
	if revision := summary.PackageEnv["GIT_REVISION"]; revision != "" {
		fmt.Fprintf(&builder, "- Package revision: `%s`\n", revision)
	}
	if target := strings.Trim(strings.Join([]string{
		summary.PackageEnv["TARGET_OS"], summary.PackageEnv["TARGET_ARCH"],
	}, "/"), "/"); target != "" {
		fmt.Fprintf(&builder, "- Target platform: `%s`\n", target)
	}
	if len(summary.AnalysisErrors) > 0 {
		builder.WriteString("\n## Analysis Errors\n\n")
		for _, item := range summary.AnalysisErrors {
			fmt.Fprintf(&builder, "- %s\n", escapeMarkdown(item))
		}
	}

	if len(result.Results) > 0 {
		builder.WriteString("\n## Cases\n\n| ID | Name | Status | Duration | Error |\n")
		builder.WriteString("| --- | --- | --- | ---: | --- |\n")
		for _, item := range result.Results {
			fmt.Fprintf(&builder, "| %s | %s | %s | %dms | %s |\n",
				escapeMarkdown(item.ID),
				escapeMarkdown(item.Name),
				escapeMarkdown(item.Status),
				item.Milliseconds,
				escapeMarkdown(item.Error),
			)
		}
	}

	if len(summary.Failures) > 0 {
		builder.WriteString("\n## Failures\n\n")
		for _, item := range summary.Failures {
			fmt.Fprintf(&builder, "- `%s` %s: %s\n",
				item.ID, escapeMarkdown(item.Name), escapeMarkdown(item.Error))
		}
	}

	builder.WriteString("\n## Inspect Order\n\n")
	builder.WriteString("1. `result.json`\n")
	builder.WriteString("2. `run-cloud.log`\n")
	builder.WriteString("3. Provider and Skill logs\n")
	builder.WriteString("4. `polaris/log/` and `state.json`\n")
	return builder.String()
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return ioutil.WriteFile(path, data, 0600)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func escapeMarkdown(value string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		"|", "\\|",
		"\r", " ",
		"\n", "<br>",
	)
	return replacer.Replace(value)
}
