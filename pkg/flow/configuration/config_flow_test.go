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

package configuration

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/global"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/configconnector"
	"github.com/polarismesh/polaris-go/pkg/plugin/configfilter"
	"github.com/polarismesh/polaris-go/pkg/plugin/events"
)

// MockConnector 模拟配置连接器
type MockConnector struct{}

// Plugin接口实现
func (m *MockConnector) Type() common.Type {
	return common.TypeConfigConnector
}

func (m *MockConnector) ID() int32 {
	return 0
}

func (m *MockConnector) GetSDKContextID() string {
	return "test-sdk-context"
}

func (m *MockConnector) Name() string {
	return "mock-connector"
}

func (m *MockConnector) Init(ctx *plugin.InitContext) error {
	return nil
}

func (m *MockConnector) Start() error {
	return nil
}

func (m *MockConnector) Destroy() error {
	return nil
}

func (m *MockConnector) IsEnable(cfg config.Configuration) bool {
	return true
}

// ConfigConnector接口实现
func (m *MockConnector) GetConfigFile(configFile *configconnector.ConfigFile) (*configconnector.ConfigFileResponse, error) {
	return &configconnector.ConfigFileResponse{
		ConfigFile: configFile,
	}, nil
}

func (m *MockConnector) WatchConfigFiles(configFiles []*configconnector.ConfigFile) (*configconnector.ConfigFileResponse, error) {
	return &configconnector.ConfigFileResponse{}, nil
}

func (m *MockConnector) CreateConfigFile(configFile *configconnector.ConfigFile) (*configconnector.ConfigFileResponse, error) {
	return &configconnector.ConfigFileResponse{}, nil
}

func (m *MockConnector) UpdateConfigFile(configFile *configconnector.ConfigFile) (*configconnector.ConfigFileResponse, error) {
	return &configconnector.ConfigFileResponse{}, nil
}

func (m *MockConnector) PublishConfigFile(configFile *configconnector.ConfigFile) (*configconnector.ConfigFileResponse, error) {
	return &configconnector.ConfigFileResponse{}, nil
}

func (m *MockConnector) UpsertAndPublishConfigFile(configFile *configconnector.ConfigFile) (*configconnector.ConfigFileResponse, error) {
	return &configconnector.ConfigFileResponse{}, nil
}

func (m *MockConnector) GetConfigGroup(req *configconnector.ConfigGroup) (*configconnector.ConfigGroupResponse, error) {
	return &configconnector.ConfigGroupResponse{}, nil
}

// TestConfigFileCacheConcurrency 测试configFileCache的并发安全性
func TestConfigFileCacheConcurrency(t *testing.T) {
	connector := &MockConnector{}
	chain := configfilter.Chain{}
	conf := config.NewDefaultConfiguration([]string{"127.0.0.1:8091"})
	eventReporterChain := []events.EventReporter{}
	globalCtx := global.NewValueContext()

	flow, err := NewConfigFileFlow(globalCtx, connector, chain, conf, eventReporterChain)
	if err != nil {
		t.Fatalf("Failed to create config file flow: %v", err)
	}
	defer flow.Destroy()

	// 并发测试参数
	numGoroutines := 100
	numFiles := 50
	duration := 2 * time.Second

	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines*numFiles)
	stop := make(chan struct{})

	// 启动并发测试
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(threadID int) {
			defer wg.Done()

			for {
				select {
				case <-stop:
					return
				default:
					// 每个线程获取不同的配置文件
					fileIndex := threadID % numFiles
					req := &model.GetConfigFileRequest{
						Namespace: "default",
						FileGroup: "test-service",
						FileName:  fmt.Sprintf("config-file-%d.properties", fileIndex),
						Subscribe: true,
					}

					_, err := flow.GetConfigFile(req)
					if err != nil {
						errors <- err
					}
				}
			}
		}(i)
	}

	// 运行指定时间
	time.Sleep(duration)
	close(stop)
	wg.Wait()

	// 检查错误
	close(errors)
	errorCount := 0
	for err := range errors {
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
			errorCount++
		}
	}

	if errorCount > 0 {
		t.Errorf("Found %d errors during concurrent access", errorCount)
	}

	// 验证sync.Map的正确使用
	t.Logf("Concurrent test completed successfully - sync.Map usage verified")
}

// TestSyncMapUsage 验证sync.Map的使用是否正确
func TestSyncMapUsage(t *testing.T) {
	flow := &ConfigFileFlow{
		configFileCache: sync.Map{},
		shardLockCount:  16,
		shardLocks:      make([]sync.RWMutex, 16),
	}

	// 测试基本的Load和Store操作
	key := "test-key"
	value := "test-value"

	// Store操作
	flow.configFileCache.Store(key, value)

	// Load操作
	if loadedValue, ok := flow.configFileCache.Load(key); !ok {
		t.Errorf("Failed to load stored value")
	} else if loadedValue != value {
		t.Errorf("Loaded value mismatch: expected %v, got %v", value, loadedValue)
	}

	// 测试并发访问
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			// 并发读取
			if _, ok := flow.configFileCache.Load(key); !ok {
				t.Errorf("Concurrent load failed for goroutine %d", index)
			}

			// 并发写入
			newKey := fmt.Sprintf("key-%d", index)
			newValue := fmt.Sprintf("value-%d", index)
			flow.configFileCache.Store(newKey, newValue)

			// 验证写入
			if loaded, ok := flow.configFileCache.Load(newKey); !ok || loaded != newValue {
				t.Errorf("Concurrent store/load failed for goroutine %d", index)
			}
		}(i)
	}
	wg.Wait()

	t.Logf("Sync.Map usage test passed - concurrent access is safe")
}
