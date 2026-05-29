/**
 * Tencent is pleased to support the open source community by making polaris-go available.
 *
 * Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
 *
 * Licensed under the BSD 3-Clause License (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * https://opensource.org/licenses/BSD-3-Clause
 *
 * Unless required by applicable law or agreed to in writing, software distributed
 * under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
 * CONDITIONS OF ANY KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations under the License.
 */

// Package main is an E2E test for the ConfigGroupFlow concurrent first-fetch fix.
//
// It concurrently fetches N config groups via the SDK and asserts they all
// complete within a reasonable time threshold. The config groups must already
// exist on the server (created by verify.sh or manually).
//
// Configure via environment variables:
//
//	POLARIS_SERVER  - server IP/hostname (required)
//	POLARIS_TOKEN   - optional auth token
//
// Or via command-line flags.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/pkg/config"
)

var (
	serverAddr   string
	namespace    string
	groupNames   string
	thresholdSec int
)

func getEnvOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func initFlags() {
	defaultServer := getEnvOrDefault("POLARIS_SERVER", "")

	flag.StringVar(&serverAddr, "server", defaultServer,
		"Polaris server IP/hostname (env: POLARIS_SERVER)")
	flag.StringVar(&namespace, "namespace", "default", "namespace for config groups")
	flag.StringVar(&groupNames, "groups", "",
		"comma-separated list of config group names to fetch concurrently (required)")
	flag.IntVar(&thresholdSec, "threshold", 10,
		"max allowed seconds for all concurrent fetches (FAIL if exceeded)")
}

func main() {
	initFlags()
	flag.Parse()
	log.SetFlags(log.Ldate | log.Lmicroseconds | log.Lshortfile)

	// Validate
	if serverAddr == "" {
		log.Fatalf("POLARIS_SERVER is required. Set env or use -server flag.\n" +
			"Example: POLARIS_SERVER=114.132.192.60 go run . -groups grp1,grp2,grp3")
	}
	if groupNames == "" {
		log.Fatalf("-groups is required. Provide comma-separated group names.\n" +
			"Example: go run . -groups e2e-grp-000,e2e-grp-001,e2e-grp-002")
	}

	groups := strings.Split(groupNames, ",")
	for i := range groups {
		groups[i] = strings.TrimSpace(groups[i])
	}
	groupCount := len(groups)

	log.Printf("=== E2E: ConfigGroupFlow Concurrent Fetch Test ===")
	log.Printf("server=%s, namespace=%s, groups=%d, threshold=%ds",
		serverAddr, namespace, groupCount, thresholdSec)

	// Init SDK
	cfg := config.NewDefaultConfiguration([]string{serverAddr + ":8091"})
	cfg.GetConfigFile().GetConfigConnectorConfig().SetAddresses([]string{serverAddr + ":8093"})
	token := os.Getenv("POLARIS_TOKEN")
	if token != "" {
		cfg.GetConfigFile().GetConfigConnectorConfig().SetToken(token)
	}

	sdkCtx, err := polaris.NewSDKContextByConfig(cfg)
	if err != nil {
		log.Fatalf("SDK init failed: %v", err)
	}
	defer sdkCtx.Destroy()

	groupAPI := polaris.NewConfigGroupAPIByContext(sdkCtx)

	// Concurrent fetch
	type result struct {
		idx      int
		name     string
		started  time.Time
		finished time.Time
		files    int
		err      error
	}

	results := make([]*result, groupCount)
	var wg sync.WaitGroup
	fetchStart := time.Now()

	for i, name := range groups {
		i, name := i, name
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := &result{idx: i, name: name, started: time.Now()}
			group, err := groupAPI.GetConfigGroup(namespace, name)
			r.finished = time.Now()
			r.err = err
			if err == nil && group != nil {
				files, _, ok := group.GetFiles()
				if ok {
					r.files = len(files)
				}
			}
			results[i] = r
		}()
	}
	wg.Wait()
	fetchTotal := time.Since(fetchStart)

	// Sort by completion time
	sort.Slice(results, func(i, j int) bool {
		return results[i].finished.Before(results[j].finished)
	})

	var maxWait, sumWait time.Duration
	var maxGap time.Duration
	var errCount int
	for i, r := range results {
		w := r.finished.Sub(r.started)
		sumWait += w
		if w > maxWait {
			maxWait = w
		}
		if i > 0 {
			gap := r.finished.Sub(results[i-1].finished)
			if gap > maxGap {
				maxGap = gap
			}
		}
		if r.err != nil {
			errCount++
			log.Printf("  [#%02d] %s ERROR after %s: %v", r.idx, r.name, w, r.err)
		} else {
			log.Printf("  [#%02d] %s wait=%s files=%d finished=%s",
				r.idx, r.name, w, r.files, r.finished.Format("15:04:05.000"))
		}
	}

	avgWait := sumWait / time.Duration(groupCount)
	threshold := time.Duration(thresholdSec) * time.Second

	fmt.Println()
	fmt.Println("=== E2E Test Results ===")
	fmt.Printf("  groups fetched             : %d\n", groupCount)
	fmt.Printf("  total wall-clock           : %s\n", fetchTotal)
	fmt.Printf("  per-call wait avg          : %s\n", avgWait)
	fmt.Printf("  per-call wait max          : %s\n", maxWait)
	fmt.Printf("  max gap between completions: %s\n", maxGap)
	fmt.Printf("  errors                     : %d\n", errCount)
	fmt.Printf("  threshold                  : %s\n", threshold)
	fmt.Println()

	// Verdict
	if errCount > 0 {
		log.Fatalf("FAIL: %d fetch errors occurred", errCount)
	}
	if fetchTotal > threshold {
		log.Fatalf("FAIL: total %s > threshold %s — concurrent fetch bottleneck detected", fetchTotal, threshold)
	}
	fmt.Printf("PASS: total %s <= threshold %s — concurrent fetch is healthy\n", fetchTotal, threshold)
}
