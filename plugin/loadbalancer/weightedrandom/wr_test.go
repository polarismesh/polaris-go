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

package weightedrandom

import (
	"fmt"
	"math"
	"testing"

	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/local"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/plugin/loadbalancer"
)

// ==================== 测试辅助工具 ====================

// newClusterTestInstance 创建带 localValue 的测试实例（用于 ChooseInstance 测试，避免 nil panic）
func newClusterTestInstance(id string, weight uint32, port uint32) model.Instance {
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

// ==================== ChooseInstance 静态权重路径测试 ====================

func TestChooseInstance_基本选择(t *testing.T) {
	lb := newTestWRLoadBalancer()
	instances := []model.Instance{
		newClusterTestInstance("inst-1", 100, 8081),
		newClusterTestInstance("inst-2", 100, 8082),
		newClusterTestInstance("inst-3", 100, 8083),
	}
	svcInstances, cluster := buildTestServiceInstances(instances)

	criteria := &loadbalancer.Criteria{
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

func TestChooseInstance_静态权重路径_返回实例属于原始列表(t *testing.T) {
	lb := newTestWRLoadBalancer()
	instances := []model.Instance{
		newClusterTestInstance("inst-1", 50, 8081),
		newClusterTestInstance("inst-2", 100, 8082),
		newClusterTestInstance("inst-3", 150, 8083),
		newClusterTestInstance("inst-4", 200, 8084),
		newClusterTestInstance("inst-5", 250, 8085),
	}
	svcInstances, _ := buildTestServiceInstances(instances)

	validIds := make(map[string]bool)
	for _, inst := range instances {
		validIds[inst.GetId()] = true
	}

	for i := 0; i < 500; i++ {
		_, cluster := buildTestServiceInstances(instances)
		criteria := &loadbalancer.Criteria{
			Cluster: cluster,
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

// ==================== 静态权重分布测试 ====================

func TestChooseInstance_静态权重_等权重分布(t *testing.T) {
	lb := newTestWRLoadBalancer()
	instances := []model.Instance{
		newClusterTestInstance("inst-1", 100, 8081),
		newClusterTestInstance("inst-2", 100, 8082),
		newClusterTestInstance("inst-3", 100, 8083),
	}
	svcInstances, _ := buildTestServiceInstances(instances)

	counts := make(map[string]int)
	totalRounds := 100000
	for i := 0; i < totalRounds; i++ {
		_, cluster := buildTestServiceInstances(instances)
		criteria := &loadbalancer.Criteria{
			Cluster: cluster,
		}
		instance, err := lb.ChooseInstance(criteria, svcInstances)
		if err != nil {
			t.Fatalf("ChooseInstance 返回错误: %v", err)
		}
		counts[instance.GetId()]++
	}

	// 等权重下每个实例应该各占约 1/3
	tolerance := 0.03
	expectedRatio := 1.0 / 3.0
	for _, inst := range instances {
		actualRatio := float64(counts[inst.GetId()]) / float64(totalRounds)
		if math.Abs(actualRatio-expectedRatio) > tolerance {
			t.Errorf("实例 %s 等权重下选中比例 %.4f 偏离期望 %.4f 超过容忍度 %.2f",
				inst.GetId(), actualRatio, expectedRatio, tolerance)
		}
	}
	t.Logf("等权重分布: inst-1=%d, inst-2=%d, inst-3=%d",
		counts["inst-1"], counts["inst-2"], counts["inst-3"])
}

func TestChooseInstance_静态权重_不同权重影响分布(t *testing.T) {
	lb := newTestWRLoadBalancer()
	instances := []model.Instance{
		newClusterTestInstance("inst-1", 100, 8081),
		newClusterTestInstance("inst-2", 200, 8082),
		newClusterTestInstance("inst-3", 700, 8083),
	}
	svcInstances, _ := buildTestServiceInstances(instances)

	counts := make(map[string]int)
	totalRounds := 100000
	for i := 0; i < totalRounds; i++ {
		_, cluster := buildTestServiceInstances(instances)
		criteria := &loadbalancer.Criteria{
			Cluster: cluster,
		}
		instance, err := lb.ChooseInstance(criteria, svcInstances)
		if err != nil {
			t.Fatalf("ChooseInstance 返回错误: %v", err)
		}
		counts[instance.GetId()]++
	}

	// 权重比例 1:2:7
	tolerance := 0.03
	expectedRatios := map[string]float64{
		"inst-1": 0.1,
		"inst-2": 0.2,
		"inst-3": 0.7,
	}
	for id, expectedRatio := range expectedRatios {
		actualRatio := float64(counts[id]) / float64(totalRounds)
		if math.Abs(actualRatio-expectedRatio) > tolerance {
			t.Errorf("实例 %s 选中比例 %.4f 偏离期望比例 %.4f 超过容忍度 %.2f",
				id, actualRatio, expectedRatio, tolerance)
		}
	}
	t.Logf("权重分布: inst-1=%d, inst-2=%d, inst-3=%d",
		counts["inst-1"], counts["inst-2"], counts["inst-3"])
}

func TestChooseInstance_静态权重_极端权重分布(t *testing.T) {
	lb := newTestWRLoadBalancer()
	instances := []model.Instance{
		newClusterTestInstance("inst-1", 999, 8081),
		newClusterTestInstance("inst-2", 1, 8082),
	}
	svcInstances, _ := buildTestServiceInstances(instances)

	counts := make(map[string]int)
	totalRounds := 100000
	for i := 0; i < totalRounds; i++ {
		_, cluster := buildTestServiceInstances(instances)
		criteria := &loadbalancer.Criteria{
			Cluster: cluster,
		}
		instance, err := lb.ChooseInstance(criteria, svcInstances)
		if err != nil {
			t.Fatalf("ChooseInstance 返回错误: %v", err)
		}
		counts[instance.GetId()]++
	}

	// inst-1 应该占 99.9%
	inst1Ratio := float64(counts["inst-1"]) / float64(totalRounds)
	if inst1Ratio < 0.99 {
		t.Errorf("inst-1 权重999时选中比例 %.4f 应大于 0.99", inst1Ratio)
	}
	t.Logf("极端权重分布: inst-1=%d, inst-2=%d", counts["inst-1"], counts["inst-2"])
}

// ==================== 单实例测试 ====================

func TestChooseInstance_单个实例(t *testing.T) {
	lb := newTestWRLoadBalancer()
	instances := []model.Instance{
		newClusterTestInstance("inst-1", 100, 8081),
	}
	svcInstances, cluster := buildTestServiceInstances(instances)

	criteria := &loadbalancer.Criteria{
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
	lb := newTestWRLoadBalancer()
	instances := []model.Instance{
		newClusterTestInstance("inst-1", 100, 8081),
	}
	svcInstances, _ := buildTestServiceInstances(instances)

	for i := 0; i < 100; i++ {
		_, cluster := buildTestServiceInstances(instances)
		criteria := &loadbalancer.Criteria{
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

// ==================== 动态权重路径测试 ====================

func TestChooseInstance_动态权重路径_基本选择(t *testing.T) {
	lb := newTestWRLoadBalancer()
	instances := []model.Instance{
		newClusterTestInstance("inst-1", 100, 8081),
		newClusterTestInstance("inst-2", 100, 8082),
		newClusterTestInstance("inst-3", 100, 8083),
	}
	svcInstances, cluster := buildTestServiceInstances(instances)

	dynamicWeights := map[string]*model.InstanceWeight{
		"inst-1": {InstanceID: "inst-1", DynamicWeight: 50, BaseWeight: 100},
		"inst-2": {InstanceID: "inst-2", DynamicWeight: 50, BaseWeight: 100},
		"inst-3": {InstanceID: "inst-3", DynamicWeight: 50, BaseWeight: 100},
	}

	criteria := &loadbalancer.Criteria{
		Cluster:       cluster,
		DynamicWeight: dynamicWeights,
	}

	instance, err := lb.ChooseInstance(criteria, svcInstances)
	if err != nil {
		t.Fatalf("ChooseInstance 返回错误: %v", err)
	}
	if instance == nil {
		t.Fatal("ChooseInstance 返回 nil 实例")
	}
}

func TestChooseInstance_动态权重路径_权重分布(t *testing.T) {
	lb := newTestWRLoadBalancer()
	instances := []model.Instance{
		newClusterTestInstance("inst-1", 100, 8081),
		newClusterTestInstance("inst-2", 100, 8082),
		newClusterTestInstance("inst-3", 100, 8083),
	}
	svcInstances, _ := buildTestServiceInstances(instances)

	// 动态权重比例 1:2:7
	dynamicWeights := map[string]*model.InstanceWeight{
		"inst-1": {InstanceID: "inst-1", DynamicWeight: 10, BaseWeight: 100},
		"inst-2": {InstanceID: "inst-2", DynamicWeight: 20, BaseWeight: 100},
		"inst-3": {InstanceID: "inst-3", DynamicWeight: 70, BaseWeight: 100},
	}

	counts := make(map[string]int)
	totalRounds := 100000
	for i := 0; i < totalRounds; i++ {
		_, cluster := buildTestServiceInstances(instances)
		criteria := &loadbalancer.Criteria{
			Cluster:       cluster,
			DynamicWeight: dynamicWeights,
		}
		instance, err := lb.ChooseInstance(criteria, svcInstances)
		if err != nil {
			t.Fatalf("ChooseInstance 返回错误: %v", err)
		}
		counts[instance.GetId()]++
	}

	tolerance := 0.05
	expectedRatios := map[string]float64{
		"inst-1": 0.1,
		"inst-2": 0.2,
		"inst-3": 0.7,
	}
	for id, expectedRatio := range expectedRatios {
		actualRatio := float64(counts[id]) / float64(totalRounds)
		if math.Abs(actualRatio-expectedRatio) > tolerance {
			t.Errorf("动态权重路径: 实例 %s 选中比例 %.4f 偏离期望比例 %.4f 超过容忍度 %.2f",
				id, actualRatio, expectedRatio, tolerance)
		}
	}
	t.Logf("动态权重分布: inst-1=%d, inst-2=%d, inst-3=%d",
		counts["inst-1"], counts["inst-2"], counts["inst-3"])
}

func TestChooseInstance_动态权重路径_部分实例有动态权重(t *testing.T) {
	lb := newTestWRLoadBalancer()
	instances := []model.Instance{
		newClusterTestInstance("inst-1", 100, 8081),
		newClusterTestInstance("inst-2", 100, 8082),
		newClusterTestInstance("inst-3", 100, 8083),
	}
	svcInstances, _ := buildTestServiceInstances(instances)

	// 只有 inst-1 有动态权重（极高），其他使用静态权重
	dynamicWeights := map[string]*model.InstanceWeight{
		"inst-1": {InstanceID: "inst-1", DynamicWeight: 1000, BaseWeight: 100},
	}

	counts := make(map[string]int)
	totalRounds := 10000
	for i := 0; i < totalRounds; i++ {
		_, cluster := buildTestServiceInstances(instances)
		criteria := &loadbalancer.Criteria{
			Cluster:       cluster,
			DynamicWeight: dynamicWeights,
		}
		instance, err := lb.ChooseInstance(criteria, svcInstances)
		if err != nil {
			t.Fatalf("ChooseInstance 返回错误: %v", err)
		}
		counts[instance.GetId()]++
	}

	// inst-1 动态权重1000，inst-2/inst-3 静态权重100，总权重1200
	// inst-1 应该占约 83.3%
	inst1Ratio := float64(counts["inst-1"]) / float64(totalRounds)
	if inst1Ratio < 0.75 {
		t.Errorf("inst-1 动态权重1000时选中比例 %.4f 应大于 0.75", inst1Ratio)
	}
	t.Logf("部分动态权重分布: inst-1=%d, inst-2=%d, inst-3=%d",
		counts["inst-1"], counts["inst-2"], counts["inst-3"])
}

// ==================== 大量实例测试 ====================

func TestChooseInstance_大量实例(t *testing.T) {
	lb := newTestWRLoadBalancer()
	instances := make([]model.Instance, 100)
	for i := 0; i < 100; i++ {
		instances[i] = newClusterTestInstance(
			fmt.Sprintf("inst-%d", i),
			uint32(100+i),
			uint32(8080+i),
		)
	}
	svcInstances, cluster := buildTestServiceInstances(instances)

	criteria := &loadbalancer.Criteria{
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

func TestChooseInstance_大量实例_所有实例都能被选中(t *testing.T) {
	lb := newTestWRLoadBalancer()
	numInstances := 20
	instances := make([]model.Instance, numInstances)
	for i := 0; i < numInstances; i++ {
		instances[i] = newClusterTestInstance(
			fmt.Sprintf("inst-%d", i),
			100,
			uint32(8080+i),
		)
	}
	svcInstances, _ := buildTestServiceInstances(instances)

	counts := make(map[string]int)
	totalRounds := 100000
	for i := 0; i < totalRounds; i++ {
		_, cluster := buildTestServiceInstances(instances)
		criteria := &loadbalancer.Criteria{
			Cluster: cluster,
		}
		instance, err := lb.ChooseInstance(criteria, svcInstances)
		if err != nil {
			t.Fatalf("ChooseInstance 返回错误: %v", err)
		}
		counts[instance.GetId()]++
	}

	// 等权重下所有实例都应该被选中
	for _, inst := range instances {
		if counts[inst.GetId()] == 0 {
			t.Errorf("等权重下实例 %s 从未被选中", inst.GetId())
		}
	}
	t.Logf("20个等权重实例，选中次数范围: 期望约 %d", totalRounds/numInstances)
}

// ==================== 动态权重路径_所有权重为零 ====================

func TestChooseInstance_动态权重路径_所有权重为零返回错误(t *testing.T) {
	lb := newTestWRLoadBalancer()
	instances := []model.Instance{
		newClusterTestInstance("inst-1", 100, 8081),
		newClusterTestInstance("inst-2", 100, 8082),
	}
	svcInstances, cluster := buildTestServiceInstances(instances)

	dynamicWeights := map[string]*model.InstanceWeight{
		"inst-1": {InstanceID: "inst-1", DynamicWeight: 0, BaseWeight: 100},
		"inst-2": {InstanceID: "inst-2", DynamicWeight: 0, BaseWeight: 100},
	}

	criteria := &loadbalancer.Criteria{
		Cluster:       cluster,
		DynamicWeight: dynamicWeights,
	}

	_, err := lb.ChooseInstance(criteria, svcInstances)
	if err == nil {
		t.Error("所有动态权重为0时应返回错误")
	}
}

// ==================== 动态权重路径_单实例 ====================

func TestChooseInstance_动态权重路径_单实例(t *testing.T) {
	lb := newTestWRLoadBalancer()
	instances := []model.Instance{
		newClusterTestInstance("inst-1", 100, 8081),
	}
	svcInstances, cluster := buildTestServiceInstances(instances)

	dynamicWeights := map[string]*model.InstanceWeight{
		"inst-1": {InstanceID: "inst-1", DynamicWeight: 50, BaseWeight: 100},
	}

	criteria := &loadbalancer.Criteria{
		Cluster:       cluster,
		DynamicWeight: dynamicWeights,
	}

	for i := 0; i < 50; i++ {
		instance, err := lb.ChooseInstance(criteria, svcInstances)
		if err != nil {
			t.Fatalf("第 %d 次调用 ChooseInstance 返回错误: %v", i, err)
		}
		if instance.GetId() != "inst-1" {
			t.Errorf("单实例时第 %d 次调用应返回 inst-1, 实际返回 %s", i, instance.GetId())
		}
	}
}

// ==================== 随机性验证 ====================

func TestChooseInstance_多次调用不总是返回同一实例(t *testing.T) {
	lb := newTestWRLoadBalancer()
	instances := []model.Instance{
		newClusterTestInstance("inst-1", 100, 8081),
		newClusterTestInstance("inst-2", 100, 8082),
		newClusterTestInstance("inst-3", 100, 8083),
	}
	svcInstances, _ := buildTestServiceInstances(instances)

	selectedInstances := make(map[string]bool)
	for i := 0; i < 100; i++ {
		_, cluster := buildTestServiceInstances(instances)
		criteria := &loadbalancer.Criteria{
			Cluster: cluster,
		}
		instance, err := lb.ChooseInstance(criteria, svcInstances)
		if err != nil {
			t.Fatalf("ChooseInstance 返回错误: %v", err)
		}
		selectedInstances[instance.GetId()] = true
	}

	// 100次调用应该选中多个不同实例（随机性验证）
	if len(selectedInstances) < 2 {
		t.Errorf("100次调用只选中了 %d 个不同实例，期望至少2个（随机性不足）", len(selectedInstances))
	}
}
