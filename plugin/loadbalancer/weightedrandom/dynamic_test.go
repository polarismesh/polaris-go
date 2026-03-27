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

	"github.com/polarismesh/polaris-go/pkg/algorithm/rand"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
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

// newTestWRLoadBalancer 创建用于测试的 WRLoadBalancer
func newTestWRLoadBalancer() *WRLoadBalancer {
	logger := &noopLogger{}
	ctxLogger := &log.ContextLogger{}
	// 通过全局设置让 ContextLogger 获取到 noopLogger
	log.SetBaseLogger(logger)
	ctxLogger.Init()
	return &WRLoadBalancer{
		scalableRand: rand.NewScalableRand(),
		log:          ctxLogger,
	}
}

// newTestInstance 创建测试实例
func newTestInstance(id string, weight uint32) model.Instance {
	inst := &apiservice.Instance{
		Id:      wrapperspb.String(id),
		Host:    wrapperspb.String("127.0.0.1"),
		Port:    wrapperspb.UInt32(8080),
		Weight:  wrapperspb.UInt32(weight),
		Healthy: wrapperspb.Bool(true),
		Isolate: wrapperspb.Bool(false),
	}
	svcKey := &model.ServiceKey{
		Namespace: "test-ns",
		Service:   "test-svc",
	}
	return pb.NewInstanceInProto(inst, svcKey, nil)
}

// newTestInstanceWithPort 创建带端口的测试实例
func newTestInstanceWithPort(id string, weight uint32, port uint32) model.Instance {
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
	return pb.NewInstanceInProto(inst, svcKey, nil)
}

// ==================== dynamicWeightSlice 测试 ====================

func TestDynamicWeightSlice_GetValue(t *testing.T) {
	slice := &dynamicWeightSlice{
		accumulateWeight: []uint64{10, 30, 60, 100},
	}
	tests := []struct {
		idx      int
		expected uint64
	}{
		{0, 10},
		{1, 30},
		{2, 60},
		{3, 100},
	}
	for _, tt := range tests {
		if got := slice.GetValue(tt.idx); got != tt.expected {
			t.Errorf("GetValue(%d) = %d, 期望 %d", tt.idx, got, tt.expected)
		}
	}
}

func TestDynamicWeightSlice_Count(t *testing.T) {
	instances := []model.Instance{
		newTestInstance("inst-1", 100),
		newTestInstance("inst-2", 200),
		newTestInstance("inst-3", 300),
	}
	slice := &dynamicWeightSlice{
		instances:        instances,
		accumulateWeight: []uint64{100, 300, 600},
	}
	if got := slice.Count(); got != 3 {
		t.Errorf("Count() = %d, 期望 3", got)
	}
}

func TestDynamicWeightSlice_Count_空数组(t *testing.T) {
	slice := &dynamicWeightSlice{
		instances:        []model.Instance{},
		accumulateWeight: []uint64{},
	}
	if got := slice.Count(); got != 0 {
		t.Errorf("Count() = %d, 期望 0", got)
	}
}

func TestDynamicWeightSlice_TotalWeight(t *testing.T) {
	slice := &dynamicWeightSlice{
		total: 600,
	}
	if got := slice.TotalWeight(); got != 600 {
		t.Errorf("TotalWeight() = %d, 期望 600", got)
	}
}

func TestDynamicWeightSlice_TotalWeight_零权重(t *testing.T) {
	slice := &dynamicWeightSlice{
		total: 0,
	}
	if got := slice.TotalWeight(); got != 0 {
		t.Errorf("TotalWeight() = %d, 期望 0", got)
	}
}

// ==================== getWeight 测试 ====================

func TestGetWeight_无动态权重使用静态权重(t *testing.T) {
	lb := newTestWRLoadBalancer()
	inst := newTestInstance("inst-1", 100)

	weight := lb.getWeight(nil, inst)
	if weight != 100 {
		t.Errorf("无动态权重时期望使用静态权重 100, 实际=%d", weight)
	}
}

func TestGetWeight_空动态权重Map使用静态权重(t *testing.T) {
	lb := newTestWRLoadBalancer()
	inst := newTestInstance("inst-1", 100)
	dynamicWeights := map[string]*model.InstanceWeight{}

	weight := lb.getWeight(dynamicWeights, inst)
	if weight != 100 {
		t.Errorf("空动态权重map时期望使用静态权重 100, 实际=%d", weight)
	}
}

func TestGetWeight_有动态权重使用动态权重(t *testing.T) {
	lb := newTestWRLoadBalancer()
	inst := newTestInstance("inst-1", 100)
	dynamicWeights := map[string]*model.InstanceWeight{
		"inst-1": {
			InstanceID:    "inst-1",
			DynamicWeight: 50,
			BaseWeight:    100,
		},
	}

	weight := lb.getWeight(dynamicWeights, inst)
	if weight != 50 {
		t.Errorf("有动态权重时期望使用动态权重 50, 实际=%d", weight)
	}
}

func TestGetWeight_动态权重Map中无匹配实例使用静态权重(t *testing.T) {
	lb := newTestWRLoadBalancer()
	inst := newTestInstance("inst-1", 100)
	dynamicWeights := map[string]*model.InstanceWeight{
		"inst-2": {
			InstanceID:    "inst-2",
			DynamicWeight: 80,
			BaseWeight:    200,
		},
	}

	weight := lb.getWeight(dynamicWeights, inst)
	if weight != 100 {
		t.Errorf("动态权重map中无匹配实例时期望使用静态权重 100, 实际=%d", weight)
	}
}

func TestGetWeight_动态权重为零(t *testing.T) {
	lb := newTestWRLoadBalancer()
	inst := newTestInstance("inst-1", 100)
	dynamicWeights := map[string]*model.InstanceWeight{
		"inst-1": {
			InstanceID:    "inst-1",
			DynamicWeight: 0,
			BaseWeight:    100,
		},
	}

	weight := lb.getWeight(dynamicWeights, inst)
	if weight != 0 {
		t.Errorf("动态权重为0时期望返回 0, 实际=%d", weight)
	}
}

// ==================== buildDynamicWeightSlice 测试 ====================

func TestBuildDynamicWeightSlice_使用动态权重构建累积数组(t *testing.T) {
	lb := newTestWRLoadBalancer()
	instances := []model.Instance{
		newTestInstance("inst-1", 100),
		newTestInstance("inst-2", 200),
		newTestInstance("inst-3", 300),
	}
	dynamicWeights := map[string]*model.InstanceWeight{
		"inst-1": {InstanceID: "inst-1", DynamicWeight: 10, BaseWeight: 100},
		"inst-2": {InstanceID: "inst-2", DynamicWeight: 20, BaseWeight: 200},
		"inst-3": {InstanceID: "inst-3", DynamicWeight: 30, BaseWeight: 300},
	}

	slice := lb.buildDynamicWeightSlice(dynamicWeights, instances)

	// 验证累积权重: 10, 30, 60
	expectedAccumulate := []uint64{10, 30, 60}
	for i, expected := range expectedAccumulate {
		if slice.GetValue(i) != expected {
			t.Errorf("累积权重[%d] = %d, 期望 %d", i, slice.GetValue(i), expected)
		}
	}
	if slice.TotalWeight() != 60 {
		t.Errorf("总权重 = %d, 期望 60", slice.TotalWeight())
	}
	if slice.Count() != 3 {
		t.Errorf("实例数 = %d, 期望 3", slice.Count())
	}
}

func TestBuildDynamicWeightSlice_无动态权重使用静态权重构建(t *testing.T) {
	lb := newTestWRLoadBalancer()
	instances := []model.Instance{
		newTestInstance("inst-1", 100),
		newTestInstance("inst-2", 200),
	}

	slice := lb.buildDynamicWeightSlice(nil, instances)

	// 使用静态权重: 100, 300(累积)
	if slice.GetValue(0) != 100 {
		t.Errorf("累积权重[0] = %d, 期望 100", slice.GetValue(0))
	}
	if slice.GetValue(1) != 300 {
		t.Errorf("累积权重[1] = %d, 期望 300", slice.GetValue(1))
	}
	if slice.TotalWeight() != 300 {
		t.Errorf("总权重 = %d, 期望 300", slice.TotalWeight())
	}
}

func TestBuildDynamicWeightSlice_部分实例有动态权重(t *testing.T) {
	lb := newTestWRLoadBalancer()
	instances := []model.Instance{
		newTestInstance("inst-1", 100),
		newTestInstance("inst-2", 200),
		newTestInstance("inst-3", 300),
	}
	// 只有 inst-1 和 inst-3 有动态权重
	dynamicWeights := map[string]*model.InstanceWeight{
		"inst-1": {InstanceID: "inst-1", DynamicWeight: 50, BaseWeight: 100},
		"inst-3": {InstanceID: "inst-3", DynamicWeight: 150, BaseWeight: 300},
	}

	slice := lb.buildDynamicWeightSlice(dynamicWeights, instances)

	// inst-1 动态权重50, inst-2 静态权重200, inst-3 动态权重150
	// 累积: 50, 250, 400
	expectedAccumulate := []uint64{50, 250, 400}
	for i, expected := range expectedAccumulate {
		if slice.GetValue(i) != expected {
			t.Errorf("累积权重[%d] = %d, 期望 %d", i, slice.GetValue(i), expected)
		}
	}
	if slice.TotalWeight() != 400 {
		t.Errorf("总权重 = %d, 期望 400", slice.TotalWeight())
	}
}

func TestBuildDynamicWeightSlice_所有动态权重为零(t *testing.T) {
	lb := newTestWRLoadBalancer()
	instances := []model.Instance{
		newTestInstance("inst-1", 100),
		newTestInstance("inst-2", 200),
	}
	dynamicWeights := map[string]*model.InstanceWeight{
		"inst-1": {InstanceID: "inst-1", DynamicWeight: 0, BaseWeight: 100},
		"inst-2": {InstanceID: "inst-2", DynamicWeight: 0, BaseWeight: 200},
	}

	slice := lb.buildDynamicWeightSlice(dynamicWeights, instances)

	if slice.TotalWeight() != 0 {
		t.Errorf("所有动态权重为0时总权重应为 0, 实际=%d", slice.TotalWeight())
	}
}

func TestBuildDynamicWeightSlice_单个实例(t *testing.T) {
	lb := newTestWRLoadBalancer()
	instances := []model.Instance{
		newTestInstance("inst-1", 100),
	}
	dynamicWeights := map[string]*model.InstanceWeight{
		"inst-1": {InstanceID: "inst-1", DynamicWeight: 80, BaseWeight: 100},
	}

	slice := lb.buildDynamicWeightSlice(dynamicWeights, instances)

	if slice.GetValue(0) != 80 {
		t.Errorf("累积权重[0] = %d, 期望 80", slice.GetValue(0))
	}
	if slice.TotalWeight() != 80 {
		t.Errorf("总权重 = %d, 期望 80", slice.TotalWeight())
	}
	if slice.Count() != 1 {
		t.Errorf("实例数 = %d, 期望 1", slice.Count())
	}
}

// ==================== 权重分布正确性测试 ====================

func TestDynamicWeight_权重分布验证(t *testing.T) {
	lb := newTestWRLoadBalancer()
	instances := []model.Instance{
		newTestInstanceWithPort("inst-1", 100, 8081),
		newTestInstanceWithPort("inst-2", 100, 8082),
		newTestInstanceWithPort("inst-3", 100, 8083),
	}
	// 动态权重比例 1:2:7
	dynamicWeights := map[string]*model.InstanceWeight{
		"inst-1": {InstanceID: "inst-1", DynamicWeight: 10, BaseWeight: 100},
		"inst-2": {InstanceID: "inst-2", DynamicWeight: 20, BaseWeight: 100},
		"inst-3": {InstanceID: "inst-3", DynamicWeight: 70, BaseWeight: 100},
	}

	weightSlice := lb.buildDynamicWeightSlice(dynamicWeights, instances)

	// 模拟大量随机选择，验证权重分布
	counts := make(map[string]int)
	totalRounds := 100000
	for i := 0; i < totalRounds; i++ {
		index := rand.SelectWeightedRandItem(lb.scalableRand, weightSlice)
		if index >= 0 && index < len(instances) {
			counts[instances[index].GetId()]++
		}
	}

	// 验证分布比例，允许一定误差（5%）
	tolerance := 0.05
	expectedRatios := map[string]float64{
		"inst-1": 0.1,
		"inst-2": 0.2,
		"inst-3": 0.7,
	}

	for id, expectedRatio := range expectedRatios {
		actualRatio := float64(counts[id]) / float64(totalRounds)
		if math.Abs(actualRatio-expectedRatio) > tolerance {
			t.Errorf("实例 %s 的选中比例 %.4f 偏离期望比例 %.4f 超过容忍度 %.2f",
				id, actualRatio, expectedRatio, tolerance)
		}
	}
}

func TestDynamicWeight_等权重分布验证(t *testing.T) {
	lb := newTestWRLoadBalancer()
	instances := []model.Instance{
		newTestInstanceWithPort("inst-1", 100, 8081),
		newTestInstanceWithPort("inst-2", 100, 8082),
		newTestInstanceWithPort("inst-3", 100, 8083),
	}
	// 等动态权重
	dynamicWeights := map[string]*model.InstanceWeight{
		"inst-1": {InstanceID: "inst-1", DynamicWeight: 50, BaseWeight: 100},
		"inst-2": {InstanceID: "inst-2", DynamicWeight: 50, BaseWeight: 100},
		"inst-3": {InstanceID: "inst-3", DynamicWeight: 50, BaseWeight: 100},
	}

	weightSlice := lb.buildDynamicWeightSlice(dynamicWeights, instances)

	counts := make(map[string]int)
	totalRounds := 100000
	for i := 0; i < totalRounds; i++ {
		index := rand.SelectWeightedRandItem(lb.scalableRand, weightSlice)
		if index >= 0 && index < len(instances) {
			counts[instances[index].GetId()]++
		}
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
}

func TestDynamicWeight_极端权重分布_一个实例权重远大于其他(t *testing.T) {
	lb := newTestWRLoadBalancer()
	instances := []model.Instance{
		newTestInstanceWithPort("inst-1", 100, 8081),
		newTestInstanceWithPort("inst-2", 100, 8082),
	}
	// inst-1 权重极高，inst-2 权重极低
	dynamicWeights := map[string]*model.InstanceWeight{
		"inst-1": {InstanceID: "inst-1", DynamicWeight: 999, BaseWeight: 100},
		"inst-2": {InstanceID: "inst-2", DynamicWeight: 1, BaseWeight: 100},
	}

	weightSlice := lb.buildDynamicWeightSlice(dynamicWeights, instances)

	counts := make(map[string]int)
	totalRounds := 100000
	for i := 0; i < totalRounds; i++ {
		index := rand.SelectWeightedRandItem(lb.scalableRand, weightSlice)
		if index >= 0 && index < len(instances) {
			counts[instances[index].GetId()]++
		}
	}

	// inst-1 应该占 99.9%，inst-2 占 0.1%
	inst1Ratio := float64(counts["inst-1"]) / float64(totalRounds)
	if inst1Ratio < 0.99 {
		t.Errorf("inst-1 权重999时选中比例 %.4f 应大于 0.99", inst1Ratio)
	}
	inst2Ratio := float64(counts["inst-2"]) / float64(totalRounds)
	if inst2Ratio > 0.01 {
		// 允许较宽松的误差
		if inst2Ratio > 0.02 {
			t.Errorf("inst-2 权重1时选中比例 %.4f 应小于 0.02", inst2Ratio)
		}
	}
}

func TestDynamicWeight_单个实例选择(t *testing.T) {
	lb := newTestWRLoadBalancer()
	instances := []model.Instance{
		newTestInstance("inst-1", 100),
	}
	dynamicWeights := map[string]*model.InstanceWeight{
		"inst-1": {InstanceID: "inst-1", DynamicWeight: 50, BaseWeight: 100},
	}

	weightSlice := lb.buildDynamicWeightSlice(dynamicWeights, instances)

	// 只有一个实例时，每次都应该选中它
	for i := 0; i < 100; i++ {
		index := rand.SelectWeightedRandItem(lb.scalableRand, weightSlice)
		if index != 0 {
			t.Errorf("单个实例时index应为 0, 实际=%d", index)
		}
	}
}

// ==================== 多实例权重大数值测试 ====================

func TestBuildDynamicWeightSlice_大权重值不溢出(t *testing.T) {
	lb := newTestWRLoadBalancer()
	instances := make([]model.Instance, 100)
	dynamicWeights := make(map[string]*model.InstanceWeight, 100)
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("inst-%d", i)
		instances[i] = newTestInstanceWithPort(id, 10000, uint32(8080+i))
		dynamicWeights[id] = &model.InstanceWeight{
			InstanceID:    id,
			DynamicWeight: 10000,
			BaseWeight:    10000,
		}
	}

	slice := lb.buildDynamicWeightSlice(dynamicWeights, instances)

	expectedTotal := 100 * 10000
	if slice.TotalWeight() != expectedTotal {
		t.Errorf("100个实例各10000权重，总权重应为 %d, 实际=%d", expectedTotal, slice.TotalWeight())
	}
	// 验证最后一个累积权重等于总权重
	if slice.GetValue(99) != uint64(expectedTotal) {
		t.Errorf("最后一个累积权重应为 %d, 实际=%d", expectedTotal, slice.GetValue(99))
	}
}

// ==================== 累积权重数组单调递增验证 ====================

func TestBuildDynamicWeightSlice_累积权重单调递增(t *testing.T) {
	lb := newTestWRLoadBalancer()
	instances := []model.Instance{
		newTestInstanceWithPort("inst-1", 100, 8081),
		newTestInstanceWithPort("inst-2", 50, 8082),
		newTestInstanceWithPort("inst-3", 200, 8083),
		newTestInstanceWithPort("inst-4", 10, 8084),
		newTestInstanceWithPort("inst-5", 300, 8085),
	}
	dynamicWeights := map[string]*model.InstanceWeight{
		"inst-1": {InstanceID: "inst-1", DynamicWeight: 30, BaseWeight: 100},
		"inst-2": {InstanceID: "inst-2", DynamicWeight: 10, BaseWeight: 50},
		"inst-3": {InstanceID: "inst-3", DynamicWeight: 80, BaseWeight: 200},
		"inst-4": {InstanceID: "inst-4", DynamicWeight: 5, BaseWeight: 10},
		"inst-5": {InstanceID: "inst-5", DynamicWeight: 100, BaseWeight: 300},
	}

	slice := lb.buildDynamicWeightSlice(dynamicWeights, instances)

	// 验证累积权重严格单调递增
	for i := 1; i < slice.Count(); i++ {
		if slice.GetValue(i) <= slice.GetValue(i-1) {
			t.Errorf("累积权重非单调递增: accumulateWeight[%d]=%d <= accumulateWeight[%d]=%d",
				i, slice.GetValue(i), i-1, slice.GetValue(i-1))
		}
	}

	// 验证具体累积值: 30, 40, 120, 125, 225
	expected := []uint64{30, 40, 120, 125, 225}
	for i, exp := range expected {
		if slice.GetValue(i) != exp {
			t.Errorf("累积权重[%d] = %d, 期望 %d", i, slice.GetValue(i), exp)
		}
	}
}

// ==================== 混合权重来源测试 ====================

func TestGetWeight_多实例混合权重来源(t *testing.T) {
	lb := newTestWRLoadBalancer()

	instances := []model.Instance{
		newTestInstance("inst-1", 100), // 有动态权重
		newTestInstance("inst-2", 200), // 无动态权重，使用静态
		newTestInstance("inst-3", 300), // 有动态权重
	}

	dynamicWeights := map[string]*model.InstanceWeight{
		"inst-1": {InstanceID: "inst-1", DynamicWeight: 10, BaseWeight: 100},
		"inst-3": {InstanceID: "inst-3", DynamicWeight: 30, BaseWeight: 300},
	}

	expectedWeights := []int{10, 200, 30}
	for i, inst := range instances {
		w := lb.getWeight(dynamicWeights, inst)
		if w != expectedWeights[i] {
			t.Errorf("实例 %s 的权重 = %d, 期望 %d", inst.GetId(), w, expectedWeights[i])
		}
	}
}
