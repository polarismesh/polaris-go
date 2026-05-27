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

// Package main reproduces the ConfigGroupFlow concurrent first-fetch
// bottleneck described in `slow-concurrent-fetching-of-config-group.md`.
//
// The reproduction uses an in-process mock ConfigConnector with controlled
// gRPC latency, so it does NOT need a real Polaris server. It demonstrates:
//
//  1. Pre-populating the SDK with many groups (so doSync has work).
//  2. Concurrently fetching N NEW groups simultaneously.
//  3. Measuring per-call wait time and the gap between consecutive completions.
//
// Without the fix:
//   - per-call wait scales with `pre × latency` (one doSync pass duration)
//   - gaps between completions ≈ doSync pass duration (~17s in production)
//
// With the fix:
//   - all N completions land within a few hundred ms total
package main

import (
	"flag"
	"fmt"
	"log"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/configconnector"
)

// -----------------------------------------------------------------------------
// Mock ConfigConnector — emulates Polaris server with controlled latency.
// -----------------------------------------------------------------------------

const mockConnectorName = "reproMock"

// mockLatency is consulted on every mock RPC. We use an atomic so the test
// can adjust latency without restarting the SDK.
var mockLatency atomic.Int64 // milliseconds

// callCounters track total mock RPCs for diagnostics.
var (
	mockGetConfigGroupCalls atomic.Int64
	mockGetConfigFileCalls  atomic.Int64
)

// mockConnector implements configconnector.ConfigConnector. The struct embeds
// plugin.PluginBase so it inherits Init/Destroy/ID/Start defaults.
type mockConnector struct {
	*plugin.PluginBase
}

func (c *mockConnector) Type() common.Type { return common.TypeConfigConnector }
func (c *mockConnector) Name() string      { return mockConnectorName }

func (c *mockConnector) Init(ctx *plugin.InitContext) error {
	c.PluginBase = plugin.NewPluginBase(ctx)
	return nil
}

// Always enabled; no agent mode.
func (c *mockConnector) IsEnable(cfg config.Configuration) bool { return true }

func (c *mockConnector) Destroy() error { return nil }

// sleep simulates a gRPC round trip.
func (c *mockConnector) sleep() {
	if d := mockLatency.Load(); d > 0 {
		time.Sleep(time.Duration(d) * time.Millisecond)
	}
}

// GetConfigGroup returns 1 fake config file inside the requested group.
// We always return ExecuteSuccess with a fixed revision so the SDK's group
// repo treats the group as valid and stays in the doSync loop.
func (c *mockConnector) GetConfigGroup(req *configconnector.ConfigGroup) (*configconnector.ConfigGroupResponse, error) {
	mockGetConfigGroupCalls.Add(1)
	c.sleep()
	// If revision matches the previous fake revision, return DataNoChange.
	if req.Revision == fakeGroupRevision(req.Namespace, req.Group) {
		return &configconnector.ConfigGroupResponse{
			Code:      uint32(apimodel.Code_DataNoChange),
			Namespace: req.Namespace,
			Group:     req.Group,
			Revision:  req.Revision,
		}, nil
	}
	return &configconnector.ConfigGroupResponse{
		Code:      uint32(apimodel.Code_ExecuteSuccess),
		Namespace: req.Namespace,
		Group:     req.Group,
		Revision:  fakeGroupRevision(req.Namespace, req.Group),
		ReleaseFiles: []*model.SimpleConfigFile{
			{
				Namespace:   req.Namespace,
				FileGroup:   req.Group,
				FileName:    "data",
				Version:     1,
				Md5:         "deadbeefdeadbeefdeadbeefdeadbeef",
				ReleaseTime: time.Now(),
			},
		},
	}, nil
}

// GetConfigFile returns a small fake content.
func (c *mockConnector) GetConfigFile(file *configconnector.ConfigFile) (*configconnector.ConfigFileResponse, error) {
	mockGetConfigFileCalls.Add(1)
	c.sleep()
	cf := &configconnector.ConfigFile{
		Namespace:     file.Namespace,
		FileGroup:     file.FileGroup,
		FileName:      file.FileName,
		SourceContent: "key: value\n",
		Version:       1,
		Md5:           "deadbeefdeadbeefdeadbeefdeadbeef",
	}
	cf.SetContent("key: value\n")
	return &configconnector.ConfigFileResponse{
		Code:       uint32(apimodel.Code_ExecuteSuccess),
		ConfigFile: cf,
	}, nil
}

func (c *mockConnector) WatchConfigFiles(_ []*configconnector.ConfigFile) (*configconnector.ConfigFileResponse, error) {
	// Block long enough that the watcher loop doesn't spam.
	time.Sleep(60 * time.Second)
	return &configconnector.ConfigFileResponse{Code: uint32(apimodel.Code_DataNoChange)}, nil
}

func (c *mockConnector) CreateConfigFile(_ *configconnector.ConfigFile) (*configconnector.ConfigFileResponse, error) {
	return nil, fmt.Errorf("mock connector does not support CreateConfigFile")
}

func (c *mockConnector) UpdateConfigFile(_ *configconnector.ConfigFile) (*configconnector.ConfigFileResponse, error) {
	return nil, fmt.Errorf("mock connector does not support UpdateConfigFile")
}

func (c *mockConnector) PublishConfigFile(_ *configconnector.ConfigFile) (*configconnector.ConfigFileResponse, error) {
	return nil, fmt.Errorf("mock connector does not support PublishConfigFile")
}

func (c *mockConnector) UpsertAndPublishConfigFile(_ *configconnector.ConfigFile) (*configconnector.ConfigFileResponse, error) {
	return nil, fmt.Errorf("mock connector does not support UpsertAndPublishConfigFile")
}

func fakeGroupRevision(ns, group string) string {
	return "rev-" + ns + "-" + group
}

func init() {
	plugin.RegisterPlugin(&mockConnector{})
}

// -----------------------------------------------------------------------------
// Reproduction scenario
// -----------------------------------------------------------------------------

var (
	preGroups       int
	concurrentNew   int
	populateLatency int
	bugLatency      int
	bugThresholdSec int
	debugLog        bool
)

func initFlags() {
	flag.IntVar(&preGroups, "pre", 100,
		"number of groups pre-populated to make doSync busy")
	flag.IntVar(&concurrentNew, "new", 20,
		"number of NEW groups fetched concurrently to trigger the bottleneck")
	flag.IntVar(&populateLatency, "popLatency", 1,
		"mock latency during Phase 1 / pre-population (ms). Kept low so "+
			"populate completes quickly and doSync passes are short.")
	flag.IntVar(&bugLatency, "bugLatency", 30,
		"mock latency during Phase 2 / concurrent fetch (ms). Picked so "+
			"`pre × bugLatency` exceeds the 2s doSync ticker, mimicking "+
			"production where each doSync pass took 17s.")
	flag.IntVar(&bugThresholdSec, "threshold", 5,
		"if total concurrent fetch wall-clock exceeds this many seconds, "+
			"the bug is considered reproduced (and the test exits non-zero)")
	flag.BoolVar(&debugLog, "debug", false, "enable SDK debug logging")
}

func main() {
	initFlags()
	flag.Parse()

	log.SetFlags(log.Ldate | log.Lmicroseconds | log.Lshortfile)
	log.Printf("=== ConfigGroupFlow concurrent first-fetch repro ===")
	log.Printf("preGroups=%d, concurrentNew=%d, populateLatency=%dms, bugLatency=%dms, threshold=%ds",
		preGroups, concurrentNew, populateLatency, bugLatency, bugThresholdSec)

	// Phase 1 uses fast latency so populate is quick.
	mockLatency.Store(int64(populateLatency))

	cfg := config.NewDefaultConfiguration([]string{"127.0.0.1:1"}) // discovery; unused
	// Ensure the SDK uses our mock connector and doesn't try to reach a server.
	cfg.GetConfigFile().GetConfigConnectorConfig().SetProtocol(mockConnectorName)
	cfg.GetConfigFile().GetConfigConnectorConfig().SetAddresses([]string{"127.0.0.1:1"})
	cfg.GetConfigFile().GetLocalCache().SetPersistDir("./polaris/backup_repro")

	sdkCtx, err := polaris.NewSDKContextByConfig(cfg)
	if err != nil {
		log.Fatalf("create SDK context: %v", err)
	}
	defer sdkCtx.Destroy()

	if debugLog {
		_ = api.SetLoggersLevel(api.DebugLog)
	}

	groupAPI := polaris.NewConfigGroupAPIByContext(sdkCtx)

	// Phase 1: populate `repos` map by sequential fetches. This makes doSync
	// loop expensive (preGroups × latencyMs per pass).
	log.Printf("[Phase 1] sequentially populating %d groups …", preGroups)
	phase1Start := time.Now()
	for i := 0; i < preGroups; i++ {
		name := fmt.Sprintf("repro-pre-%05d", i)
		if _, err := groupAPI.GetConfigGroup("repro", name); err != nil {
			log.Fatalf("populate %s: %v", name, err)
		}
	}
	log.Printf("[Phase 1] done in %s (single-thread sequential fetch)", time.Since(phase1Start))

	// Wait for at least one full doSync pass so it is actively running when
	// Phase 2 starts. doSync ticks every 2s; one full pass over N groups
	// takes N×latency. We need to ensure doSync is MID-PASS when Phase 2 fires.
	passDuration := time.Duration(preGroups*bugLatency) * time.Millisecond
	wait := passDuration + 2*time.Second
	// Switch to high latency BEFORE the wait so doSync picks it up.
	mockLatency.Store(int64(bugLatency))
	log.Printf("[Phase 1.5] set latency to %dms; sleeping %s so doSync runs a full pass …",
		bugLatency, wait)
	time.Sleep(wait)

	// Phase 2: concurrently fetch concurrentNew BRAND NEW groups. This is the
	// scenario that takes ~5min in production due to doSync holding the read
	// lock for an entire pass.
	log.Printf("[Phase 2] concurrently fetching %d NEW groups …", concurrentNew)
	type result struct {
		idx      int
		name     string
		started  time.Time
		finished time.Time
		err      error
	}
	results := make([]*result, concurrentNew)
	var wg sync.WaitGroup
	phase2Start := time.Now()
	for i := 0; i < concurrentNew; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := &result{
				idx:     i,
				name:    fmt.Sprintf("repro-new-%05d", i),
				started: time.Now(),
			}
			_, err := groupAPI.GetConfigGroup("repro", r.name)
			r.finished = time.Now()
			r.err = err
			results[i] = r
		}()
	}
	wg.Wait()
	phase2Total := time.Since(phase2Start)

	// Sort by completion time so we can compute completion-gap statistics.
	sort.Slice(results, func(i, j int) bool {
		return results[i].finished.Before(results[j].finished)
	})

	var maxWait, sumWait time.Duration
	var maxGap time.Duration
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
			log.Printf("  [#%02d] %s ERROR after %s: %v", r.idx, r.name, w, r.err)
		} else {
			log.Printf("  [#%02d] %s wait=%s, finished=%s",
				r.idx, r.name, w, r.finished.Format("15:04:05.000"))
		}
	}

	avgWait := sumWait / time.Duration(concurrentNew)

	fmt.Println()
	fmt.Println("=== Phase 2 summary ===")
	fmt.Printf("  total wall-clock           : %s\n", phase2Total)
	fmt.Printf("  per-call wait avg          : %s\n", avgWait)
	fmt.Printf("  per-call wait max          : %s\n", maxWait)
	fmt.Printf("  max gap between completions: %s\n", maxGap)
	fmt.Printf("  mock GetConfigGroup calls  : %d (incl. doSync)\n", mockGetConfigGroupCalls.Load())
	fmt.Printf("  mock GetConfigFile  calls  : %d\n", mockGetConfigFileCalls.Load())

	threshold := time.Duration(bugThresholdSec) * time.Second
	fmt.Println()
	if phase2Total > threshold {
		fmt.Printf("BUG REPRODUCED: total %s > threshold %s\n", phase2Total, threshold)
		fmt.Printf("Concurrent first-fetch is being serialized by doSync's RLock.\n")
		fmt.Printf("Expected fixed total: ≈ %d × %dms ≈ %s.\n",
			concurrentNew, bugLatency, time.Duration(concurrentNew*bugLatency)*time.Millisecond)
		// Non-zero exit so CI / scripts can detect the bug.
		log.Fatalf("repro confirmed bug")
	}
	fmt.Printf("PASS: total %s <= threshold %s\n", phase2Total, threshold)
	fmt.Printf("Concurrent first-fetch completed quickly — bug is fixed.\n")
}
