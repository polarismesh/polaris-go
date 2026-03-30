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

package maglev

import (
	"fmt"
	"testing"

	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/algorithm/hash"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/local"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/loadbalancer"
)

// ==================== 测试辅助工具 ====================

// noopLogger 空日志实现，用于测试
type noopLogger struct{}

func (n *noopLogger) Tracef(format string, args ...interface{}) {}
func (n *noopLogger) Debugf(format string, args ...interface{}) {}
func (n *noopLogger) Infof(format string, args ...interface{})  {}
func (n *noopLogger) Warnf(format string, args ...interface{})  {}
func (n *noopLogger) Errorf(format string, args ...interface{}) {}
func (n *noopLogger) Fatalf(format string, args ...interface{}) {}
func (n *noopLogger) IsLevelEnabled(l int) bool                 { return true }
func (n *noopLogger) SetLogLevel(l int) error                   { return nil }

// newTestMaglevLoadBalancer 创建用于测试的 MaglevLoadBalancer
func newTestMaglevLoadBalancer() *MaglevLoadBalancer {
	logger := &noopLogger{}
	ctxLogger := &log.ContextLogger{}
	log.SetBaseLogger(logger)
	ctxLogger.Init()
	hashFunc, _ := hash.GetHashFunc(hash.DefaultHashFuncName)
	return &MaglevLoadBalancer{
		PluginBase: &plugin.PluginBase{},
		cfg: &Config{
			TableSize:    DefaultTableSize,
			HashFunction: hash.DefaultHashFuncName,
		},
		hashFunc: hashFunc,
		logCtx:   ctxLogger,
	}
}

// newTestInstance 创建测试实例
func newTestInstance(id string, weight uint32, port uint32) model.Instance {
	inst := &apiservice.Instance{
		Id:      wrapperspb.String(id),
		Host:    wrapperspb.String("127.0.0.1"),
		Port:    wrapperspb.UInt32(port),
		Weight:  wrapperspb.UInt32(weight),
		Healthy: wrapperspb.Bool(true),
		Isolate: wrapperspb.Bool(false),
	}
	svcKey := &model.ServiceKey{
		Namespace: "test-ns",
		Service:   "test-svc",
	}
	return pb.NewInstanceInProto(inst, svcKey, local.NewInstanceLocalValue())
}

// buildTestServiceInstances 构建测试用的 ServiceInstances 和 Cluster
func buildTestServiceInstances(instances []model.Instance) (model.ServiceInstances, *model.Cluster) {
	svcInfo := model.ServiceInfo{
		Service:   "test-svc",
		Namespace: "test-ns",
	}
	svcInstances := model.NewDefaultServiceInstances(svcInfo, instances)
	clusters := svcInstances.GetServiceClusters()
	cluster := model.NewCluster(clusters, nil)
	cluster.SetReuse(false)
	return svcInstances, cluster
}

// ==================== ChooseInstance 基本功能测试 ====================

func TestChooseInstance_基本选择(t *testing.T) {
	lb := newTestMaglevLoadBalancer()
	instances := []model.Instance{
		newTestInstance("inst-1", 100, 8081),
		newTestInstance("inst-2", 100, 8082),
		newTestInstance("inst-3", 100, 8083),
	}
	svcInstances, cluster := buildTestServiceInstances(instances)

	criteria := &loadbalancer.Criteria{
		HashKey: []byte("test-key"),
		Cluster: cluster,
	}

	instance, err := lb.ChooseInstance(criteria, svcInstances)
	if err != nil {
		t.Fatalf("ChooseInstance 返回错误: %v", err)
	}
	if instance == nil {
		t.Fatal("ChooseInstance 返回 nil 实例")
	}
	// 验证返回的实例是合法的实例之一
	found := false
	for _, inst := range instances {
		if inst.GetId() == instance.GetId() {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ChooseInstance 返回了未知实例: %s", instance.GetId())
	}
}

// ==================== Hash 一致性测试 ====================

func TestChooseInstance_相同HashKey返回相同实例(t *testing.T) {
	lb := newTestMaglevLoadBalancer()
	instances := []model.Instance{
		newTestInstance("inst-1", 100, 8081),
		newTestInstance("inst-2", 100, 8082),
		newTestInstance("inst-3", 100, 8083),
	}
	svcInstances, _ := buildTestServiceInstances(instances)

	var firstInstanceId string
	for i := 0; i < 100; i++ {
		_, cluster := buildTestServiceInstances(instances)
		criteria := &loadbalancer.Criteria{
			HashKey: []byte("consistent-key"),
			Cluster: cluster,
		}
		instance, err := lb.ChooseInstance(criteria, svcInstances)
		if err != nil {
			t.Fatalf("第 %d 次调用 ChooseInstance 返回错误: %v", i, err)
		}
		if i == 0 {
			firstInstanceId = instance.GetId()
		} else if instance.GetId() != firstInstanceId {
			t.Errorf("第 %d 次调用返回不同实例: 期望 %s, 实际 %s", i, firstInstanceId, instance.GetId())
		}
	}
}

func TestChooseInstance_不同HashKey可能返回不同实例(t *testing.T) {
	lb := newTestMaglevLoadBalancer()
	instances := []model.Instance{
		newTestInstance("inst-1", 100, 8081),
		newTestInstance("inst-2", 100, 8082),
		newTestInstance("inst-3", 100, 8083),
	}
	svcInstances, _ := buildTestServiceInstances(instances)

	selectedInstances := make(map[string]int)
	for i := 0; i < 1000; i++ {
		_, cluster := buildTestServiceInstances(instances)
		criteria := &loadbalancer.Criteria{
			HashKey: []byte(fmt.Sprintf("key-%d", i)),
			Cluster: cluster,
		}
		instance, err := lb.ChooseInstance(criteria, svcInstances)
		if err != nil {
			t.Fatalf("ChooseInstance 返回错误: %v", err)
		}
		selectedInstances[instance.GetId()]++
	}

	if len(selectedInstances) < 2 {
		t.Errorf("1000个不同key只选中了 %d 个不同实例，期望至少2个", len(selectedInstances))
	}
}

// ==================== HashValue 测试 ====================

func TestChooseInstance_使用HashValue(t *testing.T) {
	lb := newTestMaglevLoadBalancer()
	instances := []model.Instance{
		newTestInstance("inst-1", 100, 8081),
		newTestInstance("inst-2", 100, 8082),
		newTestInstance("inst-3", 100, 8083),
	}
	svcInstances, cluster := buildTestServiceInstances(instances)

	criteria := &loadbalancer.Criteria{
		HashValue: 12345,
		Cluster:   cluster,
	}

	instance, err := lb.ChooseInstance(criteria, svcInstances)
	if err != nil {
		t.Fatalf("ChooseInstance 返回错误: %v", err)
	}
	if instance == nil {
		t.Fatal("ChooseInstance 返回 nil 实例")
	}
}

func TestChooseInstance_相同HashValue返回相同实例(t *testing.T) {
	lb := newTestMaglevLoadBalancer()
	instances := []model.Instance{
		newTestInstance("inst-1", 100, 8081),
		newTestInstance("inst-2", 100, 8082),
		newTestInstance("inst-3", 100, 8083),
	}
	svcInstances, _ := buildTestServiceInstances(instances)

	var firstInstanceId string
	for i := 0; i < 50; i++ {
		_, cluster := buildTestServiceInstances(instances)
		criteria := &loadbalancer.Criteria{
			HashValue: 99999,
			Cluster:   cluster,
		}
		instance, err := lb.ChooseInstance(criteria, svcInstances)
		if err != nil {
			t.Fatalf("第 %d 次调用 ChooseInstance 返回错误: %v", i, err)
		}
		if i == 0 {
			firstInstanceId = instance.GetId()
		} else if instance.GetId() != firstInstanceId {
			t.Errorf("第 %d 次调用返回不同实例: 期望 %s, 实际 %s", i, firstInstanceId, instance.GetId())
		}
	}
}

// ==================== 权重测试 ====================

func TestChooseInstance_不同权重影响分布(t *testing.T) {
	lb := newTestMaglevLoadBalancer()
	instances := []model.Instance{
		newTestInstance("inst-1", 1000, 8081),
		newTestInstance("inst-2", 1, 8082),
		newTestInstance("inst-3", 1, 8083),
	}
	svcInstances, _ := buildTestServiceInstances(instances)

	counts := make(map[string]int)
	for i := 0; i < 10000; i++ {
		_, cluster := buildTestServiceInstances(instances)
		criteria := &loadbalancer.Criteria{
			HashValue: uint64(i),
			Cluster:   cluster,
		}
		instance, err := lb.ChooseInstance(criteria, svcInstances)
		if err != nil {
			t.Fatalf("ChooseInstance 返回错误: %v", err)
		}
		counts[instance.GetId()]++
	}

	// inst-1 权重极高，应该被选中最多
	if counts["inst-1"] < counts["inst-2"] || counts["inst-1"] < counts["inst-3"] {
		t.Errorf("高权重实例 inst-1 选中次数(%d)应大于低权重实例 inst-2(%d) 和 inst-3(%d)",
			counts["inst-1"], counts["inst-2"], counts["inst-3"])
	}
	t.Logf("权重分布: inst-1=%d, inst-2=%d, inst-3=%d",
		counts["inst-1"], counts["inst-2"], counts["inst-3"])
}

// ==================== 单实例测试 ====================

func TestChooseInstance_单个实例(t *testing.T) {
	lb := newTestMaglevLoadBalancer()
	instances := []model.Instance{
		newTestInstance("inst-1", 100, 8081),
	}
	svcInstances, cluster := buildTestServiceInstances(instances)

	criteria := &loadbalancer.Criteria{
		HashKey: []byte("any-key"),
		Cluster: cluster,
	}

	instance, err := lb.ChooseInstance(criteria, svcInstances)
	if err != nil {
		t.Fatalf("ChooseInstance 返回错误: %v", err)
	}
	if instance.GetId() != "inst-1" {
		t.Errorf("单实例时应返回 inst-1, 实际返回 %s", instance.GetId())
	}
}

func TestChooseInstance_单个实例多次调用一致(t *testing.T) {
	lb := newTestMaglevLoadBalancer()
	instances := []model.Instance{
		newTestInstance("inst-1", 100, 8081),
	}
	svcInstances, _ := buildTestServiceInstances(instances)

	for i := 0; i < 50; i++ {
		_, cluster := buildTestServiceInstances(instances)
		criteria := &loadbalancer.Criteria{
			HashKey: []byte(fmt.Sprintf("key-%d", i)),
			Cluster: cluster,
		}
		instance, err := lb.ChooseInstance(criteria, svcInstances)
		if err != nil {
			t.Fatalf("第 %d 次调用 ChooseInstance 返回错误: %v", i, err)
		}
		if instance.GetId() != "inst-1" {
			t.Errorf("单实例时第 %d 次调用应返回 inst-1, 实际返回 %s", i, instance.GetId())
		}
	}
}

// ==================== 返回实例正确性测试 ====================

func TestChooseInstance_返回实例属于原始实例列表(t *testing.T) {
	lb := newTestMaglevLoadBalancer()
	instances := []model.Instance{
		newTestInstance("inst-1", 50, 8081),
		newTestInstance("inst-2", 100, 8082),
		newTestInstance("inst-3", 150, 8083),
		newTestInstance("inst-4", 200, 8084),
		newTestInstance("inst-5", 250, 8085),
	}
	svcInstances, _ := buildTestServiceInstances(instances)

	validIds := make(map[string]bool)
	for _, inst := range instances {
		validIds[inst.GetId()] = true
	}

	for i := 0; i < 500; i++ {
		_, cluster := buildTestServiceInstances(instances)
		criteria := &loadbalancer.Criteria{
			HashValue: uint64(i * 7),
			Cluster:   cluster,
		}
		instance, err := lb.ChooseInstance(criteria, svcInstances)
		if err != nil {
			t.Fatalf("ChooseInstance 返回错误: %v", err)
		}
		if !validIds[instance.GetId()] {
			t.Errorf("ChooseInstance 返回了不在原始实例列表中的实例: %s", instance.GetId())
		}
	}
}

// ==================== 大量实例测试 ====================

func TestChooseInstance_大量实例(t *testing.T) {
	lb := newTestMaglevLoadBalancer()
	instances := make([]model.Instance, 50)
	for i := 0; i < 50; i++ {
		instances[i] = newTestInstance(
			fmt.Sprintf("inst-%d", i),
			uint32(100+i),
			uint32(8080+i),
		)
	}
	svcInstances, cluster := buildTestServiceInstances(instances)

	criteria := &loadbalancer.Criteria{
		HashKey: []byte("large-scale-test"),
		Cluster: cluster,
	}

	instance, err := lb.ChooseInstance(criteria, svcInstances)
	if err != nil {
		t.Fatalf("ChooseInstance 返回错误: %v", err)
	}
	if instance == nil {
		t.Fatal("ChooseInstance 返回 nil 实例")
	}
}

// ==================== 等权重分布测试 ====================

func TestChooseInstance_等权重分布(t *testing.T) {
	lb := newTestMaglevLoadBalancer()
	instances := []model.Instance{
		newTestInstance("inst-1", 100, 8081),
		newTestInstance("inst-2", 100, 8082),
		newTestInstance("inst-3", 100, 8083),
	}
	svcInstances, _ := buildTestServiceInstances(instances)

	counts := make(map[string]int)
	for i := 0; i < 10000; i++ {
		_, cluster := buildTestServiceInstances(instances)
		criteria := &loadbalancer.Criteria{
			HashValue: uint64(i),
			Cluster:   cluster,
		}
		instance, err := lb.ChooseInstance(criteria, svcInstances)
		if err != nil {
			t.Fatalf("ChooseInstance 返回错误: %v", err)
		}
		counts[instance.GetId()]++
	}

	for _, inst := range instances {
		if counts[inst.GetId()] == 0 {
			t.Errorf("等权重下实例 %s 从未被选中", inst.GetId())
		}
	}
	t.Logf("等权重分布: inst-1=%d, inst-2=%d, inst-3=%d",
		counts["inst-1"], counts["inst-2"], counts["inst-3"])
}

// ==================== Maglev 均匀性测试 ====================

func TestChooseInstance_Maglev均匀性(t *testing.T) {
	lb := newTestMaglevLoadBalancer()
	instances := []model.Instance{
		newTestInstance("inst-1", 100, 8081),
		newTestInstance("inst-2", 100, 8082),
		newTestInstance("inst-3", 100, 8083),
	}
	svcInstances, _ := buildTestServiceInstances(instances)

	counts := make(map[string]int)
	total := 30000
	for i := 0; i < total; i++ {
		_, cluster := buildTestServiceInstances(instances)
		criteria := &loadbalancer.Criteria{
			HashValue: uint64(i),
			Cluster:   cluster,
		}
		instance, err := lb.ChooseInstance(criteria, svcInstances)
		if err != nil {
			t.Fatalf("ChooseInstance 返回错误: %v", err)
		}
		counts[instance.GetId()]++
	}

	// Maglev 算法在等权重下应该有较好的均匀性
	expected := float64(total) / float64(len(instances))
	for id, count := range counts {
		deviation := float64(count) / expected
		// 允许 30% 的偏差
		if deviation < 0.7 || deviation > 1.3 {
			t.Errorf("Maglev 均匀性不佳: 实例 %s 选中 %d 次, 期望约 %.0f 次 (偏差 %.2f)",
				id, count, expected, deviation)
		}
	}
	t.Logf("Maglev 均匀性分布: inst-1=%d, inst-2=%d, inst-3=%d (期望各约 %d)",
		counts["inst-1"], counts["inst-2"], counts["inst-3"], total/len(instances))
}
