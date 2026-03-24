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

package warmup

import (
	"fmt"
	"math"
	"testing"
	"time"

	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	_ "github.com/polarismesh/polaris-go/pkg/plugin/weightadjuster"
)

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

// newTestAdjuster 创建用于测试的 Adjuster
func newTestAdjuster() *Adjuster {
	return &Adjuster{
		log: &noopLogger{},
	}
}

// newTestInstance 创建测试实例
// ctime 为秒级时间戳字符串，weight 为实例权重
func newTestInstance(id string, weight uint32, ctime string, metadata map[string]string) *pb.InstanceInProto {
	inst := &apiservice.Instance{
		Id:       wrapperspb.String(id),
		Host:     wrapperspb.String("127.0.0.1"),
		Port:     wrapperspb.UInt32(8080),
		Weight:   wrapperspb.UInt32(weight),
		Healthy:  wrapperspb.Bool(true),
		Isolate:  wrapperspb.Bool(false),
		Metadata: metadata,
	}
	if ctime != "" {
		inst.Ctime = wrapperspb.String(ctime)
	}
	svcKey := &model.ServiceKey{
		Namespace: "test-ns",
		Service:   "test-svc",
	}
	return pb.NewInstanceInProto(inst, svcKey, nil)
}

// newTestWarmup 创建测试 Warmup 配置
func newTestWarmup(enable bool, intervalSecond int32, curvature int32,
	enableOverloadProtection bool, overloadProtectionThreshold int32) *apitraffic.Warmup {
	return &apitraffic.Warmup{
		Enable:                      enable,
		IntervalSecond:              intervalSecond,
		Curvature:                   curvature,
		EnableOverloadProtection:    enableOverloadProtection,
		OverloadProtectionThreshold: overloadProtectionThreshold,
	}
}

// newTestLosslessRule 创建测试 LosslessRule
func newTestLosslessRule(warmup *apitraffic.Warmup, metadata map[string]string) *apitraffic.LosslessRule {
	return &apitraffic.LosslessRule{
		LosslessOnline: &apitraffic.LosslessOnline{
			Warmup: warmup,
		},
		Metadata: metadata,
	}
}

// newTestServiceInstances 创建测试 ServiceInstances
func newTestServiceInstances(instances []model.Instance) model.ServiceInstances {
	svcInfo := model.ServiceInfo{
		Namespace: "test-ns",
		Service:   "test-svc",
	}
	return model.NewDefaultServiceInstances(svcInfo, instances)
}

// ==================== getInstanceCreateTime 测试 ====================

func TestGetInstanceCreateTime_正常时间戳(t *testing.T) {
	adj := newTestAdjuster()
	now := time.Now().Unix()
	inst := newTestInstance("inst-1", 100, fmt.Sprintf("%d", now), nil)

	ts := adj.getInstanceCreateTime(inst)
	if ts != now {
		t.Errorf("期望 createTime=%d, 实际=%d", now, ts)
	}
}

func TestGetInstanceCreateTime_空Ctime(t *testing.T) {
	adj := newTestAdjuster()
	inst := newTestInstance("inst-1", 100, "", nil)

	ts := adj.getInstanceCreateTime(inst)
	if ts != 0 {
		t.Errorf("期望 createTime=0, 实际=%d", ts)
	}
}

func TestGetInstanceCreateTime_无Ctime字段(t *testing.T) {
	adj := newTestAdjuster()
	// 不设置 Ctime
	protoInst := &apiservice.Instance{
		Id:     wrapperspb.String("inst-1"),
		Host:   wrapperspb.String("127.0.0.1"),
		Port:   wrapperspb.UInt32(8080),
		Weight: wrapperspb.UInt32(100),
	}
	svcKey := &model.ServiceKey{Namespace: "test-ns", Service: "test-svc"}
	inst := pb.NewInstanceInProto(protoInst, svcKey, nil)

	ts := adj.getInstanceCreateTime(inst)
	if ts != 0 {
		t.Errorf("期望 createTime=0, 实际=%d", ts)
	}
}

func TestGetInstanceCreateTime_无效时间戳字符串(t *testing.T) {
	adj := newTestAdjuster()
	inst := newTestInstance("inst-1", 100, "not-a-number", nil)

	ts := adj.getInstanceCreateTime(inst)
	if ts != 0 {
		t.Errorf("期望 createTime=0 (无效字符串), 实际=%d", ts)
	}
}

// ==================== getInstanceWeight 测试 ====================

func TestGetInstanceWeight_预热中权重减小(t *testing.T) {
	adj := newTestAdjuster()
	warmup := newTestWarmup(true, 120, 2, false, 0) // 120秒预热, 曲线系数2

	// 实例30秒前创建
	now := time.Now()
	createTime := now.Unix() - 30
	inst := newTestInstance("inst-1", 100, fmt.Sprintf("%d", createTime), nil)

	weight := adj.getInstanceWeight(nil, inst, warmup, now)

	// ratio = 30/120 = 0.25, calcWeight = ceil(0.25^2 * 100) = ceil(6.25) = 7
	expectedWeight := uint32(math.Ceil(math.Pow(30.0/120.0, 2) * 100))
	if weight.DynamicWeight != expectedWeight {
		t.Errorf("期望 DynamicWeight=%d, 实际=%d", expectedWeight, weight.DynamicWeight)
	}
	if weight.BaseWeight != 100 {
		t.Errorf("期望 BaseWeight=100, 实际=%d", weight.BaseWeight)
	}
	if weight.InstanceID != "inst-1" {
		t.Errorf("期望 InstanceID=inst-1, 实际=%s", weight.InstanceID)
	}
}

func TestGetInstanceWeight_预热完成权重等于基础权重(t *testing.T) {
	adj := newTestAdjuster()
	warmup := newTestWarmup(true, 60, 2, false, 0) // 60秒预热

	// 实例120秒前创建，已超过预热时间
	now := time.Now()
	createTime := now.Unix() - 120
	inst := newTestInstance("inst-1", 100, fmt.Sprintf("%d", createTime), nil)

	weight := adj.getInstanceWeight(nil, inst, warmup, now)

	if weight.DynamicWeight != 100 {
		t.Errorf("预热完成后期望 DynamicWeight=100, 实际=%d", weight.DynamicWeight)
	}
	if weight.BaseWeight != 100 {
		t.Errorf("预热完成后期望 BaseWeight=100, 实际=%d", weight.BaseWeight)
	}
}

func TestGetInstanceWeight_无创建时间返回基础权重(t *testing.T) {
	adj := newTestAdjuster()
	warmup := newTestWarmup(true, 60, 2, false, 0)

	// 没有设置 Ctime
	inst := newTestInstance("inst-1", 100, "", nil)

	weight := adj.getInstanceWeight(nil, inst, warmup, time.Now())

	if weight.DynamicWeight != 100 {
		t.Errorf("无创建时间期望 DynamicWeight=100, 实际=%d", weight.DynamicWeight)
	}
}

func TestGetInstanceWeight_默认曲线系数(t *testing.T) {
	adj := newTestAdjuster()
	// curvature 设为 0，应该使用默认值 2
	warmup := newTestWarmup(true, 100, 0, false, 0)

	now := time.Now()
	createTime := now.Unix() - 50
	inst := newTestInstance("inst-1", 100, fmt.Sprintf("%d", createTime), nil)

	weight := adj.getInstanceWeight(nil, inst, warmup, now)

	// ratio = 50/100 = 0.5, 默认 curvature=2, calcWeight = ceil(0.5^2 * 100) = ceil(25) = 25
	expectedWeight := uint32(math.Ceil(math.Pow(0.5, float64(_defaultCurvature)) * 100))
	if weight.DynamicWeight != expectedWeight {
		t.Errorf("默认曲线系数期望 DynamicWeight=%d, 实际=%d", expectedWeight, weight.DynamicWeight)
	}
}

func TestGetInstanceWeight_使用前序adjuster动态权重作为基础(t *testing.T) {
	adj := newTestAdjuster()
	warmup := newTestWarmup(true, 120, 2, false, 0)

	now := time.Now()
	createTime := now.Unix() - 60
	inst := newTestInstance("inst-1", 100, fmt.Sprintf("%d", createTime), nil)

	// 前序 adjuster 设置了动态权重为 80
	dynamicWeight := map[string]*model.InstanceWeight{
		"inst-1": {
			InstanceID:    "inst-1",
			DynamicWeight: 80,
			BaseWeight:    100,
		},
	}

	weight := adj.getInstanceWeight(dynamicWeight, inst, warmup, now)

	// baseWeight 应该使用前序动态权重 80
	// ratio = 60/120 = 0.5, calcWeight = ceil(0.5^2 * 80) = ceil(20) = 20
	expectedWeight := uint32(math.Ceil(math.Pow(0.5, 2) * 80))
	if weight.DynamicWeight != expectedWeight {
		t.Errorf("使用前序动态权重期望 DynamicWeight=%d, 实际=%d", expectedWeight, weight.DynamicWeight)
	}
	if weight.BaseWeight != 80 {
		t.Errorf("使用前序动态权重期望 BaseWeight=80, 实际=%d", weight.BaseWeight)
	}
}

func TestGetInstanceWeight_刚创建uptime为0(t *testing.T) {
	adj := newTestAdjuster()
	warmup := newTestWarmup(true, 60, 2, false, 0)

	now := time.Now()
	createTime := now.Unix() // uptime = 0
	inst := newTestInstance("inst-1", 100, fmt.Sprintf("%d", createTime), nil)

	weight := adj.getInstanceWeight(nil, inst, warmup, now)

	// ratio = 0/60 = 0, calcWeight = ceil(0^2 * 100) = ceil(0) = 0
	if weight.DynamicWeight != 0 {
		t.Errorf("刚创建期望 DynamicWeight=0, 实际=%d", weight.DynamicWeight)
	}
}

func TestGetInstanceWeight_曲线系数为1线性增长(t *testing.T) {
	adj := newTestAdjuster()
	warmup := newTestWarmup(true, 100, 1, false, 0)

	now := time.Now()
	createTime := now.Unix() - 50
	inst := newTestInstance("inst-1", 200, fmt.Sprintf("%d", createTime), nil)

	weight := adj.getInstanceWeight(nil, inst, warmup, now)

	// ratio = 50/100 = 0.5, curvature=1, calcWeight = ceil(0.5 * 200) = 100
	expectedWeight := uint32(math.Ceil(0.5 * 200))
	if weight.DynamicWeight != expectedWeight {
		t.Errorf("线性增长期望 DynamicWeight=%d, 实际=%d", expectedWeight, weight.DynamicWeight)
	}
}

func TestGetInstanceWeight_曲线系数为3(t *testing.T) {
	adj := newTestAdjuster()
	warmup := newTestWarmup(true, 100, 3, false, 0)

	now := time.Now()
	createTime := now.Unix() - 50
	inst := newTestInstance("inst-1", 100, fmt.Sprintf("%d", createTime), nil)

	weight := adj.getInstanceWeight(nil, inst, warmup, now)

	// ratio = 50/100 = 0.5, curvature=3, calcWeight = ceil(0.5^3 * 100) = ceil(12.5) = 13
	expectedWeight := uint32(math.Ceil(math.Pow(0.5, 3) * 100))
	if weight.DynamicWeight != expectedWeight {
		t.Errorf("曲线系数3期望 DynamicWeight=%d, 实际=%d", expectedWeight, weight.DynamicWeight)
	}
}

// ==================== parseMetadataLosslessRules 测试 ====================

func TestParseMetadataLosslessRules_有元数据规则(t *testing.T) {
	adj := newTestAdjuster()

	warmup := newTestWarmup(true, 60, 2, false, 0)
	rule1 := newTestLosslessRule(warmup, map[string]string{"env": "prod"})
	rule2 := newTestLosslessRule(warmup, map[string]string{"env": "gray"})

	rules := []*apitraffic.LosslessRule{rule1, rule2}
	result := adj.parseMetadataLosslessRules(rules)

	if len(result) != 1 {
		t.Fatalf("期望 1 个元数据 key, 实际=%d", len(result))
	}
	envRules, ok := result["env"]
	if !ok {
		t.Fatal("期望存在 env key")
	}
	if len(envRules) != 2 {
		t.Errorf("期望 env 下有 2 个值, 实际=%d", len(envRules))
	}
	if envRules["prod"] != rule1 {
		t.Error("期望 env=prod 匹配 rule1")
	}
	if envRules["gray"] != rule2 {
		t.Error("期望 env=gray 匹配 rule2")
	}
}

func TestParseMetadataLosslessRules_无元数据规则(t *testing.T) {
	adj := newTestAdjuster()

	warmup := newTestWarmup(true, 60, 2, false, 0)
	rule := newTestLosslessRule(warmup, nil) // 没有元数据

	rules := []*apitraffic.LosslessRule{rule}
	result := adj.parseMetadataLosslessRules(rules)

	if len(result) != 0 {
		t.Errorf("无元数据规则期望返回空 map, 实际大小=%d", len(result))
	}
}

func TestParseMetadataLosslessRules_多个元数据Key(t *testing.T) {
	adj := newTestAdjuster()

	warmup := newTestWarmup(true, 60, 2, false, 0)
	rule := newTestLosslessRule(warmup, map[string]string{"env": "prod", "version": "v1"})

	rules := []*apitraffic.LosslessRule{rule}
	result := adj.parseMetadataLosslessRules(rules)

	if len(result) != 2 {
		t.Errorf("期望 2 个元数据 key, 实际=%d", len(result))
	}
}

// ==================== getMatchMetadataLosslessRule 测试 ====================

func TestGetMatchMetadataLosslessRule_匹配成功(t *testing.T) {
	adj := newTestAdjuster()

	warmup := newTestWarmup(true, 60, 2, false, 0)
	rule := newTestLosslessRule(warmup, map[string]string{"env": "prod"})

	metadataRules := adj.parseMetadataLosslessRules([]*apitraffic.LosslessRule{rule})

	inst := newTestInstance("inst-1", 100, "", map[string]string{"env": "prod"})
	matched := adj.getMatchMetadataLosslessRule(inst, metadataRules)
	if matched != rule {
		t.Error("期望匹配到规则，实际未匹配")
	}
}

func TestGetMatchMetadataLosslessRule_不匹配(t *testing.T) {
	adj := newTestAdjuster()

	warmup := newTestWarmup(true, 60, 2, false, 0)
	rule := newTestLosslessRule(warmup, map[string]string{"env": "prod"})

	metadataRules := adj.parseMetadataLosslessRules([]*apitraffic.LosslessRule{rule})

	// 实例的 env=gray，不匹配
	inst := newTestInstance("inst-1", 100, "", map[string]string{"env": "gray"})
	matched := adj.getMatchMetadataLosslessRule(inst, metadataRules)
	if matched != nil {
		t.Error("期望不匹配，实际匹配了")
	}
}

func TestGetMatchMetadataLosslessRule_实例无元数据(t *testing.T) {
	adj := newTestAdjuster()

	warmup := newTestWarmup(true, 60, 2, false, 0)
	rule := newTestLosslessRule(warmup, map[string]string{"env": "prod"})

	metadataRules := adj.parseMetadataLosslessRules([]*apitraffic.LosslessRule{rule})

	inst := newTestInstance("inst-1", 100, "", nil)
	matched := adj.getMatchMetadataLosslessRule(inst, metadataRules)
	if matched != nil {
		t.Error("实例无元数据期望不匹配")
	}
}

// ==================== countNeedWarmupInstances 测试 ====================

func TestCountNeedWarmupInstances_部分需要预热(t *testing.T) {
	adj := newTestAdjuster()
	warmup := newTestWarmup(true, 60, 2, false, 0) // 60秒预热

	now := time.Now()
	// 第一个实例30秒前创建（需要预热）
	inst1 := newTestInstance("inst-1", 100, fmt.Sprintf("%d", now.Unix()-30), nil)
	// 第二个实例120秒前创建（不需要预热）
	inst2 := newTestInstance("inst-2", 100, fmt.Sprintf("%d", now.Unix()-120), nil)
	// 第三个实例10秒前创建（需要预热）
	inst3 := newTestInstance("inst-3", 100, fmt.Sprintf("%d", now.Unix()-10), nil)

	instances := []model.Instance{inst1, inst2, inst3}

	count := adj.countNeedWarmupInstances(instances, warmup, now)
	if count != 2 {
		t.Errorf("期望需要预热实例数=2, 实际=%d", count)
	}
}

func TestCountNeedWarmupInstances_全部不需要预热(t *testing.T) {
	adj := newTestAdjuster()
	warmup := newTestWarmup(true, 60, 2, false, 0)

	now := time.Now()
	inst1 := newTestInstance("inst-1", 100, fmt.Sprintf("%d", now.Unix()-120), nil)
	inst2 := newTestInstance("inst-2", 100, fmt.Sprintf("%d", now.Unix()-200), nil)

	instances := []model.Instance{inst1, inst2}

	count := adj.countNeedWarmupInstances(instances, warmup, now)
	if count != 0 {
		t.Errorf("全部不需要预热期望 count=0, 实际=%d", count)
	}
}

func TestCountNeedWarmupInstances_无创建时间跳过(t *testing.T) {
	adj := newTestAdjuster()
	warmup := newTestWarmup(true, 60, 2, false, 0)

	inst := newTestInstance("inst-1", 100, "", nil)
	instances := []model.Instance{inst}

	count := adj.countNeedWarmupInstances(instances, warmup, time.Now())
	if count != 0 {
		t.Errorf("无创建时间期望 count=0, 实际=%d", count)
	}
}

// ==================== getInstanceWeightFromLosslessRule 测试 ====================

func TestGetInstanceWeightFromLosslessRule_正常预热(t *testing.T) {
	adj := newTestAdjuster()

	warmup := newTestWarmup(true, 120, 2, false, 0)
	losslessRule := newTestLosslessRule(warmup, nil)

	now := time.Now()
	// 30秒前创建的实例（正在预热）
	inst1 := newTestInstance("inst-1", 100, fmt.Sprintf("%d", now.Unix()-30), nil)
	// 200秒前创建的实例（预热完成）
	inst2 := newTestInstance("inst-2", 100, fmt.Sprintf("%d", now.Unix()-200), nil)

	instances := []model.Instance{inst1, inst2}
	svcInstances := newTestServiceInstances(instances)

	result, err := adj.getInstanceWeightFromLosslessRule(nil, svcInstances, losslessRule)
	if err != nil {
		t.Fatalf("预期无错误, 实际=%v", err)
	}
	if result == nil {
		t.Fatal("预期有结果, 实际返回 nil")
	}
	if len(result) != 2 {
		t.Fatalf("预期 2 个权重结果, 实际=%d", len(result))
	}

	// inst-1 应在预热中
	if !result[0].IsDynamicWeightValid() {
		t.Error("inst-1 应该在预热中 (DynamicWeight != BaseWeight)")
	}
	// inst-2 预热完成
	if result[1].IsDynamicWeightValid() {
		t.Error("inst-2 应该预热完成 (DynamicWeight == BaseWeight)")
	}
}

func TestGetInstanceWeightFromLosslessRule_Warmup未启用(t *testing.T) {
	adj := newTestAdjuster()

	warmup := newTestWarmup(false, 60, 2, false, 0) // enable=false
	losslessRule := newTestLosslessRule(warmup, nil)

	inst := newTestInstance("inst-1", 100, fmt.Sprintf("%d", time.Now().Unix()-30), nil)
	svcInstances := newTestServiceInstances([]model.Instance{inst})

	result, err := adj.getInstanceWeightFromLosslessRule(nil, svcInstances, losslessRule)
	if err != nil {
		t.Fatalf("预期无错误, 实际=%v", err)
	}
	if result != nil {
		t.Error("Warmup 未启用期望返回 nil")
	}
}

func TestGetInstanceWeightFromLosslessRule_规则为nil(t *testing.T) {
	adj := newTestAdjuster()

	inst := newTestInstance("inst-1", 100, "", nil)
	svcInstances := newTestServiceInstances([]model.Instance{inst})

	result, err := adj.getInstanceWeightFromLosslessRule(nil, svcInstances, nil)
	if err != nil {
		t.Fatalf("预期无错误, 实际=%v", err)
	}
	if result != nil {
		t.Error("规则为 nil 期望返回 nil")
	}
}

func TestGetInstanceWeightFromLosslessRule_全部预热完成返回nil(t *testing.T) {
	adj := newTestAdjuster()

	warmup := newTestWarmup(true, 60, 2, false, 0)
	losslessRule := newTestLosslessRule(warmup, nil)

	now := time.Now()
	// 都已超过预热时间
	inst1 := newTestInstance("inst-1", 100, fmt.Sprintf("%d", now.Unix()-200), nil)
	inst2 := newTestInstance("inst-2", 100, fmt.Sprintf("%d", now.Unix()-300), nil)

	svcInstances := newTestServiceInstances([]model.Instance{inst1, inst2})

	result, err := adj.getInstanceWeightFromLosslessRule(nil, svcInstances, losslessRule)
	if err != nil {
		t.Fatalf("预期无错误, 实际=%v", err)
	}
	if result != nil {
		t.Error("全部预热完成期望返回 nil")
	}
}

func TestGetInstanceWeightFromLosslessRule_过载保护触发(t *testing.T) {
	adj := newTestAdjuster()

	// 开启过载保护，阈值 50%
	warmup := newTestWarmup(true, 120, 2, true, 50)
	losslessRule := newTestLosslessRule(warmup, nil)

	now := time.Now()
	// 3个实例中2个在预热中 = 66%, 超过 50% 阈值
	inst1 := newTestInstance("inst-1", 100, fmt.Sprintf("%d", now.Unix()-30), nil)
	inst2 := newTestInstance("inst-2", 100, fmt.Sprintf("%d", now.Unix()-30), nil)
	inst3 := newTestInstance("inst-3", 100, fmt.Sprintf("%d", now.Unix()-200), nil)

	svcInstances := newTestServiceInstances([]model.Instance{inst1, inst2, inst3})

	result, err := adj.getInstanceWeightFromLosslessRule(nil, svcInstances, losslessRule)
	if err != nil {
		t.Fatalf("预期无错误, 实际=%v", err)
	}
	if result != nil {
		t.Error("过载保护触发期望返回 nil")
	}
}

func TestGetInstanceWeightFromLosslessRule_过载保护未触发(t *testing.T) {
	adj := newTestAdjuster()

	// 开启过载保护，阈值 80%
	warmup := newTestWarmup(true, 120, 2, true, 80)
	losslessRule := newTestLosslessRule(warmup, nil)

	now := time.Now()
	// 5个实例中1个在预热中 = 20%, 未超过 80% 阈值
	inst1 := newTestInstance("inst-1", 100, fmt.Sprintf("%d", now.Unix()-30), nil)
	inst2 := newTestInstance("inst-2", 100, fmt.Sprintf("%d", now.Unix()-200), nil)
	inst3 := newTestInstance("inst-3", 100, fmt.Sprintf("%d", now.Unix()-200), nil)
	inst4 := newTestInstance("inst-4", 100, fmt.Sprintf("%d", now.Unix()-200), nil)
	inst5 := newTestInstance("inst-5", 100, fmt.Sprintf("%d", now.Unix()-200), nil)

	svcInstances := newTestServiceInstances([]model.Instance{inst1, inst2, inst3, inst4, inst5})

	result, err := adj.getInstanceWeightFromLosslessRule(nil, svcInstances, losslessRule)
	if err != nil {
		t.Fatalf("预期无错误, 实际=%v", err)
	}
	if result == nil {
		t.Fatal("过载保护未触发期望有结果")
	}
	// 应该有 5 个实例权重
	if len(result) != 5 {
		t.Errorf("期望 5 个权重结果, 实际=%d", len(result))
	}
}

// ==================== getInstanceWeightFromMetadataRule 测试 ====================

func TestGetInstanceWeightFromMetadataRule_按元数据匹配(t *testing.T) {
	adj := newTestAdjuster()

	warmup1 := newTestWarmup(true, 120, 2, false, 0)
	warmup2 := newTestWarmup(true, 60, 3, false, 0)

	rule1 := newTestLosslessRule(warmup1, map[string]string{"env": "prod"})
	rule2 := newTestLosslessRule(warmup2, map[string]string{"env": "gray"})

	metadataRules := adj.parseMetadataLosslessRules([]*apitraffic.LosslessRule{rule1, rule2})

	now := time.Now()
	// prod 实例30秒前创建
	inst1 := newTestInstance("inst-1", 100, fmt.Sprintf("%d", now.Unix()-30), map[string]string{"env": "prod"})
	// gray 实例30秒前创建
	inst2 := newTestInstance("inst-2", 100, fmt.Sprintf("%d", now.Unix()-30), map[string]string{"env": "gray"})

	svcInstances := newTestServiceInstances([]model.Instance{inst1, inst2})

	result, err := adj.getInstanceWeightFromMetadataRule(nil, svcInstances, metadataRules)
	if err != nil {
		t.Fatalf("预期无错误, 实际=%v", err)
	}
	if result == nil {
		t.Fatal("期望有结果")
	}
	if len(result) != 2 {
		t.Fatalf("期望 2 个结果, 实际=%d", len(result))
	}

	// inst-1: warmup1, interval=120, curvature=2, ratio=30/120=0.25
	// calcWeight = ceil(0.25^2 * 100) = ceil(6.25) = 7
	expected1 := uint32(math.Ceil(math.Pow(30.0/120.0, 2) * 100))
	if result[0].DynamicWeight != expected1 {
		t.Errorf("inst-1 期望 DynamicWeight=%d, 实际=%d", expected1, result[0].DynamicWeight)
	}

	// inst-2: warmup2, interval=60, curvature=3, ratio=30/60=0.5
	// calcWeight = ceil(0.5^3 * 100) = ceil(12.5) = 13
	expected2 := uint32(math.Ceil(math.Pow(30.0/60.0, 3) * 100))
	if result[1].DynamicWeight != expected2 {
		t.Errorf("inst-2 期望 DynamicWeight=%d, 实际=%d", expected2, result[1].DynamicWeight)
	}
}

func TestGetInstanceWeightFromMetadataRule_无匹配规则的实例被跳过(t *testing.T) {
	adj := newTestAdjuster()

	warmup := newTestWarmup(true, 120, 2, false, 0)
	rule := newTestLosslessRule(warmup, map[string]string{"env": "prod"})

	metadataRules := adj.parseMetadataLosslessRules([]*apitraffic.LosslessRule{rule})

	now := time.Now()
	// prod 实例匹配
	inst1 := newTestInstance("inst-1", 100, fmt.Sprintf("%d", now.Unix()-30), map[string]string{"env": "prod"})
	// 没有 env 标签，不匹配
	inst2 := newTestInstance("inst-2", 100, fmt.Sprintf("%d", now.Unix()-30), nil)

	svcInstances := newTestServiceInstances([]model.Instance{inst1, inst2})

	result, err := adj.getInstanceWeightFromMetadataRule(nil, svcInstances, metadataRules)
	if err != nil {
		t.Fatalf("预期无错误, 实际=%v", err)
	}
	if result == nil {
		t.Fatal("期望有结果")
	}
	// 只有 inst-1 匹配
	if len(result) != 1 {
		t.Errorf("期望 1 个结果 (只有匹配的实例), 实际=%d", len(result))
	}
}

func TestGetInstanceWeightFromMetadataRule_过载保护触发(t *testing.T) {
	adj := newTestAdjuster()

	// 两条规则都开启过载保护
	warmup := newTestWarmup(true, 120, 2, true, 30) // 阈值 30%
	rule := newTestLosslessRule(warmup, map[string]string{"env": "prod"})

	metadataRules := adj.parseMetadataLosslessRules([]*apitraffic.LosslessRule{rule})

	now := time.Now()
	// 3个实例中2个在预热 = 66% > 30%
	inst1 := newTestInstance("inst-1", 100, fmt.Sprintf("%d", now.Unix()-30), map[string]string{"env": "prod"})
	inst2 := newTestInstance("inst-2", 100, fmt.Sprintf("%d", now.Unix()-30), map[string]string{"env": "prod"})
	inst3 := newTestInstance("inst-3", 100, fmt.Sprintf("%d", now.Unix()-200), map[string]string{"env": "prod"})

	svcInstances := newTestServiceInstances([]model.Instance{inst1, inst2, inst3})

	result, err := adj.getInstanceWeightFromMetadataRule(nil, svcInstances, metadataRules)
	if err != nil {
		t.Fatalf("预期无错误, 实际=%v", err)
	}
	if result != nil {
		t.Error("过载保护触发期望返回 nil")
	}
}

func TestGetInstanceWeightFromMetadataRule_关闭过载保护(t *testing.T) {
	adj := newTestAdjuster()

	// 关闭过载保护
	warmup := newTestWarmup(true, 120, 2, false, 0)
	rule := newTestLosslessRule(warmup, map[string]string{"env": "prod"})

	metadataRules := adj.parseMetadataLosslessRules([]*apitraffic.LosslessRule{rule})

	now := time.Now()
	// 所有实例都在预热中
	inst1 := newTestInstance("inst-1", 100, fmt.Sprintf("%d", now.Unix()-30), map[string]string{"env": "prod"})
	inst2 := newTestInstance("inst-2", 100, fmt.Sprintf("%d", now.Unix()-30), map[string]string{"env": "prod"})

	svcInstances := newTestServiceInstances([]model.Instance{inst1, inst2})

	result, err := adj.getInstanceWeightFromMetadataRule(nil, svcInstances, metadataRules)
	if err != nil {
		t.Fatalf("预期无错误, 实际=%v", err)
	}
	if result == nil {
		t.Fatal("关闭过载保护期望有结果")
	}
	if len(result) != 2 {
		t.Errorf("期望 2 个结果, 实际=%d", len(result))
	}
}

func TestGetInstanceWeightFromMetadataRule_全部预热完成返回nil(t *testing.T) {
	adj := newTestAdjuster()

	warmup := newTestWarmup(true, 60, 2, false, 0)
	rule := newTestLosslessRule(warmup, map[string]string{"env": "prod"})

	metadataRules := adj.parseMetadataLosslessRules([]*apitraffic.LosslessRule{rule})

	now := time.Now()
	inst1 := newTestInstance("inst-1", 100, fmt.Sprintf("%d", now.Unix()-200), map[string]string{"env": "prod"})
	inst2 := newTestInstance("inst-2", 100, fmt.Sprintf("%d", now.Unix()-300), map[string]string{"env": "prod"})

	svcInstances := newTestServiceInstances([]model.Instance{inst1, inst2})

	result, err := adj.getInstanceWeightFromMetadataRule(nil, svcInstances, metadataRules)
	if err != nil {
		t.Fatalf("预期无错误, 实际=%v", err)
	}
	if result != nil {
		t.Error("全部预热完成期望返回 nil")
	}
}

// ==================== doTimingAdjustDynamicWeight 测试 ====================

func TestDoTimingAdjustDynamicWeight_无规则返回nil(t *testing.T) {
	adj := newTestAdjuster()

	inst := newTestInstance("inst-1", 100, "", nil)
	svcInstances := newTestServiceInstances([]model.Instance{inst})

	result, err := adj.doTimingAdjustDynamicWeight(nil, svcInstances, nil)
	if err != nil {
		t.Fatalf("预期无错误, 实际=%v", err)
	}
	if result != nil {
		t.Error("无规则期望返回 nil")
	}
}

func TestDoTimingAdjustDynamicWeight_无元数据使用第一条规则(t *testing.T) {
	adj := newTestAdjuster()

	warmup := newTestWarmup(true, 120, 2, false, 0)
	rule := newTestLosslessRule(warmup, nil) // 无元数据

	now := time.Now()
	inst := newTestInstance("inst-1", 100, fmt.Sprintf("%d", now.Unix()-30), nil)
	svcInstances := newTestServiceInstances([]model.Instance{inst})

	result, err := adj.doTimingAdjustDynamicWeight(nil, svcInstances, []*apitraffic.LosslessRule{rule})
	if err != nil {
		t.Fatalf("预期无错误, 实际=%v", err)
	}
	if result == nil {
		t.Fatal("期望有结果")
	}
	if len(result) != 1 {
		t.Errorf("期望 1 个结果, 实际=%d", len(result))
	}
}

func TestDoTimingAdjustDynamicWeight_有元数据使用元数据匹配(t *testing.T) {
	adj := newTestAdjuster()

	warmup := newTestWarmup(true, 120, 2, false, 0)
	rule := newTestLosslessRule(warmup, map[string]string{"env": "prod"})

	now := time.Now()
	inst := newTestInstance("inst-1", 100, fmt.Sprintf("%d", now.Unix()-30), map[string]string{"env": "prod"})
	svcInstances := newTestServiceInstances([]model.Instance{inst})

	result, err := adj.doTimingAdjustDynamicWeight(nil, svcInstances, []*apitraffic.LosslessRule{rule})
	if err != nil {
		t.Fatalf("预期无错误, 实际=%v", err)
	}
	if result == nil {
		t.Fatal("期望有结果")
	}
}

// ==================== IsDynamicWeightValid 测试 ====================

func TestIsDynamicWeightValid(t *testing.T) {
	// 正在预热：DynamicWeight != BaseWeight
	w1 := &model.InstanceWeight{
		InstanceID:    "inst-1",
		DynamicWeight: 50,
		BaseWeight:    100,
	}
	if !w1.IsDynamicWeightValid() {
		t.Error("DynamicWeight=50 != BaseWeight=100, 期望 IsDynamicWeightValid=true")
	}

	// 预热完成：DynamicWeight == BaseWeight
	w2 := &model.InstanceWeight{
		InstanceID:    "inst-2",
		DynamicWeight: 100,
		BaseWeight:    100,
	}
	if w2.IsDynamicWeightValid() {
		t.Error("DynamicWeight=100 == BaseWeight=100, 期望 IsDynamicWeightValid=false")
	}
}

// ==================== debugf 测试 ====================

func TestDebugf_日志级别不满足不输出(t *testing.T) {
	// 使用一个不启用 debug 的 logger
	disabledLogger := &levelDisabledLogger{}
	adj := &Adjuster{
		log: disabledLogger,
	}
	// 应该不 panic
	adj.debugf("test %s", "message")
	if disabledLogger.debugCalled {
		t.Error("日志级别未启用时不应调用 Debugf")
	}
}

// levelDisabledLogger 模拟 debug 级别未启用的 logger
type levelDisabledLogger struct {
	noopLogger
	debugCalled bool
}

func (l *levelDisabledLogger) IsLevelEnabled(level int) bool {
	return level > log.DebugLog
}

func (l *levelDisabledLogger) Debugf(format string, args ...interface{}) {
	l.debugCalled = true
}
